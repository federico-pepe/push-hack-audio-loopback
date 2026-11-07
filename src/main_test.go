package main

import (
	"path/filepath"
	"testing"

	"github.com/federico-pepe/ableton-push-hack/core/audiobus"
)

func TestExtractVermagic(t *testing.T) {
	data := []byte("junk\x00vermagic=5.15.48-push3 SMP mod_unload \x00more junk")
	got, ok := extractVermagic(data)
	if !ok {
		t.Fatal("expected a vermagic match")
	}
	want := "5.15.48-push3 SMP mod_unload "
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExtractVermagicMissing(t *testing.T) {
	if _, ok := extractVermagic([]byte("nothing here")); ok {
		t.Error("expected no match")
	}
}

func TestCString(t *testing.T) {
	got := cString([]byte("5.15.48-push3\x00\x00\x00"))
	if got != "5.15.48-push3" {
		t.Errorf("got %q", got)
	}
}

func TestToS16ClipsAndSurvivesNaN(t *testing.T) {
	nan := float32(0)
	nan = nan / nan
	for _, c := range []struct {
		in   float32
		want int16
	}{{0, 0}, {0.5, 16383}, {2, 32767}, {-2, -32768}, {nan, 0}} {
		if got := toS16(c.in); got != c.want {
			t.Errorf("toS16(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestStatusNeverHasNilClients(t *testing.T) {
	st := &mixStats{}
	st.setState("waiting for the card")
	srv := audiobus.NewServer(t.TempDir())
	ctl := newOutputCtl(filepath.Join(t.TempDir(), modeFile))
	s := status(srv, st, ctl)
	if s.Clients == nil || s.Running || s.State != "waiting for the card" {
		t.Errorf("status = %+v", s)
	}
	if s.Mode != modeCard || s.Link.Enabled || s.Link.Sinks == nil {
		t.Errorf("link status = %+v mode=%q", s.Link, s.Mode)
	}
}
