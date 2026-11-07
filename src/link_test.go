package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOutputCtlDefaultsToLink(t *testing.T) {
	c := newOutputCtl(filepath.Join(t.TempDir(), modeFile))
	if c.Mode() != modeLink {
		t.Errorf("mode = %q, want link", c.Mode())
	}
}

func TestOutputCtlSavesAndLoads(t *testing.T) {
	p := filepath.Join(t.TempDir(), modeFile)
	c := newOutputCtl(p)
	if err := c.SetMode(modeCard); err != nil {
		t.Fatal(err)
	}
	if c.Mode() != modeCard {
		t.Errorf("mode = %q", c.Mode())
	}
	select {
	case <-c.changed:
	default:
		t.Error("SetMode did not wake the supervisor")
	}
	if got := newOutputCtl(p).Mode(); got != modeCard {
		t.Errorf("reloaded mode = %q, want card", got)
	}
}

func TestOutputCtlSameModeDoesNotWake(t *testing.T) {
	c := newOutputCtl(filepath.Join(t.TempDir(), modeFile))
	if err := c.SetMode(modeLink); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.changed:
		t.Error("same mode woke the supervisor")
	default:
	}
}

func TestOutputCtlRejectsBadMode(t *testing.T) {
	c := newOutputCtl(filepath.Join(t.TempDir(), modeFile))
	if err := c.SetMode("jack"); err == nil {
		t.Error("bad mode accepted")
	}
	if c.Mode() != modeLink {
		t.Errorf("mode changed to %q", c.Mode())
	}
}

func TestOutputCtlIgnoresBadFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), modeFile)
	if err := os.WriteFile(p, []byte(`{"mode":"nonsense"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := newOutputCtl(p).Mode(); got != modeLink {
		t.Errorf("mode = %q, want link", got)
	}
	if err := os.WriteFile(p, []byte(`not json`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := newOutputCtl(p).Mode(); got != modeLink {
		t.Errorf("mode = %q, want link", got)
	}
}

func TestStereoPairPicksItsOwnChannels(t *testing.T) {
	const ch, frames = 4, 3
	mix := make([]float32, ch*frames)
	for i := 0; i < frames; i++ {
		mix[i*ch+0] = 0.1 // pair 0, must not appear
		mix[i*ch+1] = 0.1
		mix[i*ch+2] = 0.5 // pair 2
		mix[i*ch+3] = -0.5
	}
	out := make([]int16, frames*2)
	stereoPair(mix, ch, 2, frames, out)
	for i := 0; i < frames; i++ {
		if out[2*i] != toS16(0.5) || out[2*i+1] != toS16(-0.5) {
			t.Fatalf("frame %d = %d,%d", i, out[2*i], out[2*i+1])
		}
	}
}
