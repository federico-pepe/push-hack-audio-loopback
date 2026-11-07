package main

// link.go — the output mode switch and the Link Audio output.
//
// The service has two ways to hand the audio of the hacks to Live:
//   "card": the mixer writes the loopback card (mixer.go). Live reads it.
//   "link": each hack gets its own Link Audio channel (linkshim.cpp). Live
//           lists them under the peer "PushHack". The card is not touched.
// The web page switches between them. The choice is saved next to hack.json.
// Link is the default.
//
// In Link mode the audiobus server still does the work for the hacks: they
// connect, write into rings, and read the format. Here the format is fixed and
// each client is read on its own pair, so nothing is mixed across hacks.
// Design: plans/2026-11-05-link-audio-for-hacks.md in ableton-push-hack.

import (
	"encoding/json"
	"errors"
	"log"
	"math"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/federico-pepe/ableton-push-hack/core/audiobus"
)

const (
	modeCard = "card"
	modeLink = "link"

	modeFile = "output-mode.json"

	// Format the hacks see in Link mode. Pairs: 8 hacks of one stereo channel.
	linkRate     = 44100
	linkPeriod   = 128
	linkChannels = 16
	linkPeerName = "PushHack"
	// linkLeadMs is how far ahead of "now" the stamp counter starts. Live
	// ignored buffers that were stamped with a smaller lead or with its own
	// timestamps; 40 ms played clean at a Link Audio latency of 20 ms.
	linkLeadMs = 40
)

// outputCtl holds the chosen mode and what the Link output reports.
type outputCtl struct {
	path    string
	mode    atomic.Value // string
	changed chan struct{}

	linkOn    atomic.Bool
	linkPeers atomic.Int32
	tempoBits atomic.Uint64
	linkErr   atomic.Value // string

	mu    sync.Mutex
	sinks []sinkInfo
}

// sinkInfo is one Link channel, for the status page.
type sinkInfo struct {
	Name      string `json:"name"`
	Listening bool   `json:"listening"`
	Reanchors uint64 `json:"reanchors"`
}

type modeDisk struct {
	Mode string `json:"mode"`
}

func validMode(m string) bool { return m == modeCard || m == modeLink }

// newOutputCtl loads the saved mode. A missing or bad file means "link".
func newOutputCtl(path string) *outputCtl {
	c := &outputCtl{path: path, changed: make(chan struct{}, 1)}
	c.mode.Store(modeLink)
	if b, err := os.ReadFile(path); err == nil {
		var d modeDisk
		if json.Unmarshal(b, &d) == nil && validMode(d.Mode) {
			c.mode.Store(d.Mode)
		}
	}
	return c
}

func (c *outputCtl) Mode() string { m, _ := c.mode.Load().(string); return m }

// SetMode saves the mode and wakes the supervisor. Same mode: no change.
func (c *outputCtl) SetMode(m string) error {
	if !validMode(m) {
		return errors.New("mode must be \"card\" or \"link\"")
	}
	if m == c.Mode() {
		return nil
	}
	b, _ := json.Marshal(modeDisk{Mode: m})
	if err := os.WriteFile(c.path, b, 0o644); err != nil {
		return err
	}
	c.mode.Store(m)
	select {
	case c.changed <- struct{}{}:
	default:
	}
	return nil
}

func (c *outputCtl) setSinks(s []sinkInfo) {
	c.mu.Lock()
	c.sinks = s
	c.mu.Unlock()
}

func (c *outputCtl) Sinks() []sinkInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]sinkInfo, len(c.sinks))
	copy(out, c.sinks)
	return out
}

// runOutput runs the chosen output until quit closes, and swaps it when the
// mode changes. Swapping drops every client; the hacks reconnect by themselves.
func runOutput(c *outputCtl, srv *audiobus.Server, st *mixStats, quit <-chan struct{}) {
	for {
		mode := c.Mode()
		stop := make(chan struct{})
		done := make(chan struct{})
		go func() {
			defer close(done)
			if mode == modeLink {
				runLink(c, srv, st, stop)
			} else {
				c.linkOn.Store(false)
				runMixer(srv, st, stop)
			}
		}()
		select {
		case <-quit:
			close(stop)
			<-done
			return
		case <-c.changed:
			log.Printf("output: switching from %s to %s", mode, c.Mode())
			close(stop)
			<-done
			st.rate.Store(0)
			st.period.Store(0)
			st.channels.Store(0)
		}
	}
}

// stereoPair copies one hack's pair out of the interleaved mix into a stereo block.
func stereoPair(mix []float32, channels, pair, frames int, out []int16) {
	for i := 0; i < frames; i++ {
		out[2*i] = toS16(mix[i*channels+pair])
		out[2*i+1] = toS16(mix[i*channels+pair+1])
	}
}

// runLink sends every client to its own Link sink until quit closes.
func runLink(c *outputCtl, srv *audiobus.Server, st *mixStats, quit <-chan struct{}) {
	c.linkOn.Store(true)
	c.linkErr.Store("")
	c.linkPeers.Store(0)
	peer, err := newLinkPeer(linkPeerName)
	if err != nil {
		log.Printf("link: %v", err)
		c.linkErr.Store(err.Error())
		st.setState("Link: " + err.Error())
		<-quit
		return
	}
	defer peer.Close()

	srv.SetFormat(audiobus.Format{Rate: linkRate, Period: linkPeriod, Channels: linkChannels})
	defer srv.SetFormat(audiobus.Format{})
	st.rate.Store(linkRate)
	st.period.Store(linkPeriod)
	st.channels.Store(linkChannels)
	st.setState("running")
	log.Printf("link: peer %q open, rate=%d period=%d lead=%dms", linkPeerName, linkRate, linkPeriod, linkLeadMs)

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if !setRealtime(realtimePrio) {
		log.Printf("link: warning: SCHED_FIFO not available")
	}

	sinks := map[string]*linkSink{}
	listening := map[string]bool{}
	defer func() {
		for _, k := range sinks {
			k.Close()
		}
	}()
	mix := make([]float32, linkPeriod*linkChannels)
	block := make([]int16, linkPeriod*2)
	blockDur := time.Duration(linkPeriod) * time.Second / linkRate
	next := time.Now()
	lastInfo := time.Time{}
	lastLog := time.Now()

	for {
		select {
		case <-quit:
			return
		default:
		}
		next = next.Add(blockDur)
		if d := time.Until(next); d > 0 {
			time.Sleep(d)
		} else if d < -50*time.Millisecond {
			next = time.Now() // far behind: do not burst to catch up
		}

		t0 := time.Now()
		srv.Mix(mix)
		clients := srv.Clients()
		seen := make(map[string]bool, len(clients))
		for _, cl := range clients {
			seen[cl.Name] = true
			k := sinks[cl.Name]
			if k == nil {
				if k, err = peer.NewSink(cl.Name, linkPeriod); err != nil {
					log.Printf("link: %v", err)
					continue
				}
				sinks[cl.Name] = k
				log.Printf("link: channel %q opened", cl.Name)
			}
			stereoPair(mix, linkChannels, cl.Pair, linkPeriod, block)
			ok, werr := k.Write(block, linkPeriod, linkRate, linkLeadMs)
			if werr != nil {
				st.errors.Add(1)
			}
			listening[cl.Name] = ok
		}
		for name, k := range sinks {
			if !seen[name] {
				k.Close()
				delete(sinks, name)
				delete(listening, name)
				log.Printf("link: channel %q closed", name)
			}
		}
		if d := int64(time.Since(t0)); d > st.worstNs.Load() {
			st.worstNs.Store(d)
		}
		st.blocks.Add(1)

		if time.Since(lastInfo) > time.Second {
			lastInfo = time.Now()
			c.linkPeers.Store(int32(peer.Peers()))
			c.tempoBits.Store(math.Float64bits(peer.Tempo()))
			info := make([]sinkInfo, 0, len(sinks))
			for _, cl := range clients {
				if k := sinks[cl.Name]; k != nil {
					info = append(info, sinkInfo{Name: cl.Name, Listening: listening[cl.Name], Reanchors: k.Reanchors()})
				}
			}
			c.setSinks(info)
		}
		if time.Since(lastLog) > reportEvery {
			lastLog = time.Now()
			log.Printf("link: peers=%d blocks=%d worst=%v sinks=%+v", c.linkPeers.Load(), st.blocks.Load(),
				time.Duration(st.worstNs.Swap(0)), c.Sinks())
		}
	}
}
