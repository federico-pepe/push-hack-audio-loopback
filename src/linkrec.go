package main

// linkrec.go — a test tool, not part of the service. It records one Link
// channel (for example a track of Live) to a WAV file, with the same source
// and playout rule that a hack input will use. Run it by hand on the Push:
//
//	push-audio-loopback -linkrec "Sine" -secs 20 -out /tmp/rec.wav
//
// It joins Link as "PushHack rec", so it can run beside the service.

import (
	"encoding/binary"
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	recRate   = linkRate
	recPeriod = linkPeriod
	// Ring of 8192 frames (186 ms). Target 4 periods, drop above 6.
	recRing   = 8192
	recTarget = 4 * recPeriod
	recExtra  = 2 * recPeriod
)

func runLinkRec(match string, secs int, out string) error {
	peer, err := newLinkPeer("PushHack rec")
	if err != nil {
		return err
	}
	defer peer.Close()

	// Wait for the channel to show up.
	var ch *linkChannel
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(500 * time.Millisecond) {
		list, _ := peer.Channels()
		for i := range list {
			if list[i].Peer != "PushHack rec" && strings.Contains(strings.ToLower(list[i].Name), strings.ToLower(match)) {
				ch = &list[i]
				break
			}
		}
		if ch != nil {
			break
		}
	}
	if ch == nil {
		list, _ := peer.Channels()
		fmt.Printf("no channel matches %q. The session has:\n", match)
		for _, c := range list {
			fmt.Printf("  %s | %s (id %s)\n", c.Peer, c.Name, c.ID)
		}
		return fmt.Errorf("channel not found")
	}
	fmt.Printf("recording %s | %s (id %s) for %d s\n", ch.Peer, ch.Name, ch.ID, secs)

	src, err := peer.NewSource(ch.ID, recRate, recRing)
	if err != nil {
		return err
	}
	defer src.Close()

	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := writeWAVHeader(f, 0); err != nil {
		return err
	}
	// WriteAt does not move the position. Start the audio after the header.
	if _, err := f.Seek(44, 0); err != nil {
		return err
	}

	j := newJitter(recTarget, recExtra)
	buf := make([]float32, recPeriod*2)
	pcm := make([]byte, recPeriod*4)
	blockDur := time.Duration(recPeriod) * time.Second / recRate
	start := time.Now()
	next := start
	var frames int
	var lastUnder, lastDrops uint64
	for time.Since(start) < time.Duration(secs)*time.Second {
		next = next.Add(blockDur)
		time.Sleep(time.Until(next))

		skip, play := j.plan(src.Fill(), recPeriod)
		if skip > 0 {
			src.Skip(skip)
		}
		for i := range buf {
			buf[i] = 0
		}
		if play {
			src.Read(buf, recPeriod)
		}
		for i, v := range buf {
			binary.LittleEndian.PutUint16(pcm[2*i:], uint16(toS16(v)))
		}
		if _, err := f.Write(pcm); err != nil {
			return err
		}
		frames += recPeriod
		if j.Underruns != lastUnder || j.Drops != lastDrops {
			fmt.Printf("  %6.2fs  underruns=%d drops=%d frames  fill=%d\n",
				time.Since(start).Seconds(), j.Underruns, j.Drops, src.Fill())
			lastUnder, lastDrops = j.Underruns, j.Drops
		}
	}
	if err := writeWAVHeader(f, frames); err != nil {
		return err
	}
	st := src.Stats()
	fmt.Printf("done: wrote %d frames to %s\n", frames, out)
	fmt.Printf("source: buffers=%d frames=%d peak=%.3f rate_seen=%d channels_seen=%d bad_rate=%d bad_format=%d ring_overruns=%d\n",
		st.Buffers, st.Frames, st.Peak, st.RateSeen, st.ChannelsSeen, st.BadRate, st.BadFormat, st.Overs)
	fmt.Printf("playout: underruns=%d dropped_frames=%d\n", j.Underruns, j.Drops)
	return nil
}

// writeWAVHeader writes (or rewrites) a 16-bit stereo PCM header at the start of f.
func writeWAVHeader(f *os.File, frames int) error {
	data := uint32(frames * 4)
	h := make([]byte, 44)
	copy(h[0:], "RIFF")
	binary.LittleEndian.PutUint32(h[4:], 36+data)
	copy(h[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(h[16:], 16)
	binary.LittleEndian.PutUint16(h[20:], 1) // PCM
	binary.LittleEndian.PutUint16(h[22:], 2) // channels
	binary.LittleEndian.PutUint32(h[24:], recRate)
	binary.LittleEndian.PutUint32(h[28:], recRate*4)
	binary.LittleEndian.PutUint16(h[32:], 4)
	binary.LittleEndian.PutUint16(h[34:], 16)
	copy(h[36:], "data")
	binary.LittleEndian.PutUint32(h[40:], data)
	_, err := f.WriteAt(h, 0)
	return err
}
