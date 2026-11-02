// mixer: prototype of the audio bus mixer for push-audio-loopback. It owns the
// write side of the loopback card, mixes every audiobus client into the pair
// that client asked for, and writes one block per period.
//
// Prototype only: a separate binary so the released service stays pure Go.
// See docs/audio-bus.md in ableton-push-hack.
package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/federico-pepe/ableton-push-hack/core/alsapcm"
	"github.com/federico-pepe/ableton-push-hack/core/audiobus"
)

const (
	realtimePrio   = 50
	maxWriteErrors = 100
	reportEvery    = 5 * time.Second
	watchEvery     = 2 * time.Second
)

func main() {
	card := flag.String("card", "PHVAudio", "loopback card id")
	device := flag.String("device", "hw:PHVAudio,1,0", "ALSA playback device to write")
	dir := flag.String("dir", audiobus.DefaultDir, "bus folder (socket and rings)")
	pairs := flag.String("pairs", "", "file that remembers each client's pair (default: in the bus folder)")
	flag.Parse()

	srv := audiobus.NewServer(*dir)
	srv.PairsPath = *pairs
	if err := srv.Listen(); err != nil {
		log.Fatalf("listen: %v", err)
	}
	defer srv.Close()
	log.Printf("audio bus on %s", audiobus.SocketPath(*dir))

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	quit := make(chan struct{})
	go func() { <-sig; close(quit) }()

	for {
		hp, ok, err := alsapcm.ReadHWParams(*card)
		if err != nil || !ok {
			srv.SetFormat(audiobus.Format{})
			if !sleep(time.Second, quit) {
				return
			}
			continue
		}
		if !runSession(srv, *device, hp, *card, quit) {
			return
		}
		if !sleep(time.Second, quit) {
			return
		}
	}
}

func sleep(d time.Duration, quit <-chan struct{}) bool {
	select {
	case <-time.After(d):
		return true
	case <-quit:
		return false
	}
}

// runSession writes the card until Live changes the format, the device fails,
// or quit closes. It returns false only on quit.
func runSession(srv *audiobus.Server, device string, hp alsapcm.HWParams, card string, quit <-chan struct{}) bool {
	pcm, err := openPCM(device, hp.Channels, hp.Rate, hp.Period, hp.Buffer)
	if err != nil {
		log.Printf("open %s: %v", device, err)
		return true
	}
	defer pcm.Close()
	period, ch := pcm.Period(), pcm.Channels()
	srv.SetFormat(audiobus.Format{Rate: hp.Rate, Period: period, Channels: ch})
	defer srv.SetFormat(audiobus.Format{})
	log.Printf("card open: %s channels=%d rate=%d period=%d", device, ch, hp.Rate, period)

	changed := make(chan struct{})
	stopWatch := make(chan struct{})
	defer close(stopWatch)
	go func() {
		t := time.NewTicker(watchEvery)
		defer t.Stop()
		for {
			select {
			case <-stopWatch:
				return
			case <-t.C:
				if now, ok, err := alsapcm.ReadHWParams(card); err != nil || !ok || now != hp {
					close(changed)
					return
				}
			}
		}
	}()

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if !setRealtime(realtimePrio) {
		log.Printf("warning: SCHED_FIFO not available")
	}
	mix := make([]float32, period*ch)
	wide := make([]int16, period*ch)
	var blocks, errs, streak int
	var worst time.Duration
	last := time.Now()
	for {
		select {
		case <-quit:
			return false
		case <-changed:
			log.Printf("card format changed; reopening")
			return true
		default:
		}
		t0 := time.Now()
		srv.Mix(mix)
		for i, v := range mix {
			wide[i] = toS16(v)
		}
		if d := time.Since(t0); d > worst {
			worst = d
		}
		if pcm.Write(wide, period) < 0 {
			errs++
			if streak++; streak >= maxWriteErrors {
				log.Printf("%d write errors in a row; reopening", streak)
				return true
			}
			time.Sleep(time.Duration(period) * time.Second / time.Duration(hp.Rate))
			continue
		}
		streak = 0
		blocks++
		if time.Since(last) > reportEvery {
			last = time.Now()
			log.Printf("blocks=%d write_errors=%d worst_mix=%v clients=%+v", blocks, errs, worst, srv.Clients())
			worst = 0
		}
	}
}

func toS16(v float32) int16 {
	v *= 32767
	switch {
	case v > 32767:
		return 32767
	case v < -32768:
		return -32768
	case v != v:
		return 0
	}
	return int16(v)
}
