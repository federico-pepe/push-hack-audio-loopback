package main

// mixer.go — the audio bus mixer. push-audio-loopback owns the write side of
// the loopback card: it mixes every audiobus client into the channel pair the
// client asked for and writes one block per period. Hacks do not open the card.
// Design: docs/audio-bus.md in ableton-push-hack.

import (
	"log"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/federico-pepe/ableton-push-hack/core/alsapcm"
	"github.com/federico-pepe/ableton-push-hack/core/audiobus"
)

const (
	realtimePrio   = 50
	maxWriteErrors = 100
	reportEvery    = 30 * time.Second
	watchEvery     = 2 * time.Second
	retryEvery     = time.Second
	mixDevice      = "hw:" + cardID + ",1,0"
)

// mixStats is what the status page shows. Written by the mixer, read by HTTP.
type mixStats struct {
	state    atomic.Value // string: why audio is or is not flowing
	blocks   atomic.Uint64
	errors   atomic.Uint64
	worstNs  atomic.Int64 // slowest Mix of the current report window
	rate     atomic.Int32
	period   atomic.Int32
	channels atomic.Int32
}

func (m *mixStats) setState(s string) { m.state.Store(s) }

func (m *mixStats) getState() string {
	s, _ := m.state.Load().(string)
	return s
}

// runMixer follows the card until quit closes. It waits for the card, waits for
// Live to open its side, then writes the card with the format Live negotiated
// and reopens when that changes.
func runMixer(srv *audiobus.Server, st *mixStats, quit <-chan struct{}) {
	for {
		hp, ok, err := alsapcm.ReadHWParams(cardID)
		switch {
		case err != nil:
			st.setState("cannot read hw_params: " + err.Error())
		case !cardPresent(cardID):
			st.setState("waiting for the card")
		case !ok:
			st.setState("waiting for Live to open the card")
		}
		if err != nil || !ok {
			srv.SetFormat(audiobus.Format{})
			if !sleepOrQuit(retryEvery, quit) {
				return
			}
			continue
		}
		if !mixSession(srv, st, hp, quit) {
			return
		}
		if !sleepOrQuit(retryEvery, quit) {
			return
		}
	}
}

func sleepOrQuit(d time.Duration, quit <-chan struct{}) bool {
	select {
	case <-time.After(d):
		return true
	case <-quit:
		return false
	}
}

// mixSession writes the card until Live changes the format, the device fails,
// or quit closes. It returns false only on quit.
func mixSession(srv *audiobus.Server, st *mixStats, hp alsapcm.HWParams, quit <-chan struct{}) bool {
	pcm, err := openPCM(mixDevice, hp.Channels, hp.Rate, hp.Period, hp.Buffer)
	if err != nil {
		log.Printf("mixer: open %s: %v", mixDevice, err)
		st.setState("cannot open " + mixDevice + ": " + err.Error())
		return true
	}
	defer pcm.Close()
	period, ch := pcm.Period(), pcm.Channels()
	srv.SetFormat(audiobus.Format{Rate: hp.Rate, Period: period, Channels: ch})
	defer srv.SetFormat(audiobus.Format{})
	st.rate.Store(int32(hp.Rate))
	st.period.Store(int32(period))
	st.channels.Store(int32(ch))
	st.setState("running")
	log.Printf("mixer: card open: %s channels=%d rate=%d period=%d", mixDevice, ch, hp.Rate, period)

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
				if now, ok, err := alsapcm.ReadHWParams(cardID); err != nil || !ok || now != hp {
					close(changed)
					return
				}
			}
		}
	}()

	// SCHED_FIFO belongs to the thread, so this goroutine keeps its own.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if !setRealtime(realtimePrio) {
		log.Printf("mixer: warning: SCHED_FIFO not available")
	}
	mix := make([]float32, period*ch)
	wide := make([]int16, period*ch)
	blockDur := time.Duration(period) * time.Second / time.Duration(hp.Rate)
	var streak int
	last := time.Now()
	for {
		select {
		case <-quit:
			return false
		case <-changed:
			log.Printf("mixer: card format changed; reopening")
			return true
		default:
		}
		t0 := time.Now()
		srv.Mix(mix)
		for i, v := range mix {
			wide[i] = toS16(v)
		}
		if d := int64(time.Since(t0)); d > st.worstNs.Load() {
			st.worstNs.Store(d)
		}
		if pcm.Write(wide, period) < 0 {
			st.errors.Add(1)
			if streak++; streak >= maxWriteErrors {
				log.Printf("mixer: %d write errors in a row; reopening", streak)
				return true
			}
			time.Sleep(blockDur) // a failed write returns at once; do not spin
			continue
		}
		streak = 0
		st.blocks.Add(1)
		if time.Since(last) > reportEvery {
			last = time.Now()
			log.Printf("mixer: blocks=%d write_errors=%d worst_mix=%v clients=%+v",
				st.blocks.Load(), st.errors.Load(), time.Duration(st.worstNs.Swap(0)), srv.Clients())
		}
	}
}

// toS16 clips once, at the end: the mix itself is float and cannot clip.
func toS16(v float32) int16 {
	v *= 32767
	switch {
	case v > 32767:
		return 32767
	case v < -32768:
		return -32768
	case v != v: // NaN
		return 0
	}
	return int16(v)
}
