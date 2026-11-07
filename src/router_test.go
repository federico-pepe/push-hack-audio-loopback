package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ---- fakes ----

type fakeSrc struct {
	fill      int
	reads     int
	skipped   int
	closed    bool
	stats     linkSourceStats
	lastValue float32
}

func (s *fakeSrc) Fill() int              { return s.fill }
func (s *fakeSrc) Skip(n int)             { s.skipped += n; s.fill -= n }
func (s *fakeSrc) Stats() linkSourceStats { return s.stats }
func (s *fakeSrc) Close()                 { s.closed = true }
func (s *fakeSrc) Read(out []float32, frames int) int {
	n := frames
	if n > s.fill {
		n = s.fill
	}
	for i := 0; i < n*2; i++ {
		out[i] = s.lastValue
	}
	s.fill -= n
	s.reads++
	return n
}

type fakeNet struct {
	chans   []linkChannel
	made    []*fakeSrc
	madeFor []string
}

func (n *fakeNet) Channels() ([]linkChannel, error) { return n.chans, nil }
func (n *fakeNet) NewSource(id string, rate, ring int) (srcAPI, error) {
	s := &fakeSrc{lastValue: 0.5}
	n.made = append(n.made, s)
	n.madeFor = append(n.madeFor, id)
	return s, nil
}

type fakeIn struct {
	wrote   int
	refused uint64
	peak    float32
}

func (f *fakeIn) Write(buf []float32) bool { f.wrote++; return true }
func (f *fakeIn) Peak() float32            { return f.peak }
func (f *fakeIn) Refused() uint64          { return f.refused }

var (
	chPiano = linkChannel{ID: "aaaaaaaaaaaaaaaa", Name: "1-Grand Piano", PeerID: "1111111111111111", Peer: "Push"}
	chMain  = linkChannel{ID: "bbbbbbbbbbbbbbbb", Name: "Main", PeerID: "1111111111111111", Peer: "Push"}
)

func newTestRouter(t *testing.T, chans ...linkChannel) (*router, *fakeNet, *routeStore) {
	t.Helper()
	st := newRouteStore(filepath.Join(t.TempDir(), routesFile))
	n := &fakeNet{chans: chans}
	return newRouter(st, n), n, st
}

func routeToPiano() route {
	return route{Hack: "Sampler", Input: "in", ChannelID: chPiano.ID, Peer: "Push", Channel: "1-Grand Piano"}
}

// ---- the store ----

func TestRouteStoreSavesAndReloads(t *testing.T) {
	p := filepath.Join(t.TempDir(), routesFile)
	s := newRouteStore(p)
	if err := s.set(routeToPiano()); err != nil {
		t.Fatal(err)
	}
	other := routeToPiano()
	other.ChannelID, other.Channel = chMain.ID, "Main"
	if err := s.set(other); err != nil { // same input: replaces
		t.Fatal(err)
	}
	got := newRouteStore(p).all()
	if len(got) != 1 || got[0].ChannelID != chMain.ID {
		t.Fatalf("routes = %+v", got)
	}
	if err := s.remove("Sampler", "in"); err != nil {
		t.Fatal(err)
	}
	if len(newRouteStore(p).all()) != 0 {
		t.Error("route not removed from the file")
	}
	if err := s.remove("Sampler", "in"); err != nil {
		t.Errorf("removing a missing route: %v", err)
	}
}

func TestRouteStoreRejectsBadRoutesAndFiles(t *testing.T) {
	p := filepath.Join(t.TempDir(), routesFile)
	s := newRouteStore(p)
	if err := s.set(route{Hack: "x"}); err == nil {
		t.Error("route without input and channel accepted")
	}
	for _, body := range []string{"not json", `[{"hack":"a"}]`, `{"a":1}`} {
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := newRouteStore(p).all(); len(got) != 0 {
			t.Errorf("file %q gave routes %+v", body, got)
		}
	}
}

// ---- the router ----

func TestNoRoutesMeansNoSources(t *testing.T) {
	r, n, _ := newTestRouter(t, chPiano, chMain)
	in := &fakeIn{}
	r.tick([]inputRef{{"Sampler", "in", in}}, time.Now())
	if len(n.made) != 0 || in.wrote != 0 {
		t.Errorf("sources=%d writes=%d, want none", len(n.made), in.wrote)
	}
	m := r.Matrix()
	if !m.Available || len(m.Channels) != 2 || len(m.Inputs) != 1 || m.Inputs[0].Route != nil || !m.Inputs[0].Connected {
		t.Errorf("matrix = %+v", m)
	}
}

func TestRouteWaitsForTargetThenPlays(t *testing.T) {
	r, n, st := newTestRouter(t, chPiano)
	st.set(routeToPiano())
	in := &fakeIn{}
	now := time.Now()
	r.tick([]inputRef{{"Sampler", "in", in}}, now)
	if len(n.made) != 1 || n.madeFor[0] != chPiano.ID {
		t.Fatalf("sources = %v", n.madeFor)
	}
	src := n.made[0]
	// Below the target: silence, no write.
	src.fill = routeTarget - 1
	r.tick([]inputRef{{"Sampler", "in", in}}, now)
	if in.wrote != 0 {
		t.Fatal("wrote before the ring reached the target")
	}
	src.fill = routeTarget
	r.tick([]inputRef{{"Sampler", "in", in}}, now)
	if in.wrote != 1 || src.fill != routeTarget-linkPeriod {
		t.Fatalf("wrote=%d fill=%d", in.wrote, src.fill)
	}
	r.tick([]inputRef{{"Sampler", "in", in}}, now.Add(2*time.Second)) // reconcile
	row := r.Matrix().Inputs[0].Route
	if row == nil || row.State != "ok" {
		t.Errorf("route = %+v, want ok", row)
	}
}

func TestRouteMissingChannelClosesSourceAndComesBack(t *testing.T) {
	r, n, st := newTestRouter(t, chPiano)
	st.set(routeToPiano())
	in := &fakeIn{}
	ins := []inputRef{{"Sampler", "in", in}}
	now := time.Now()
	r.tick(ins, now)
	first := n.made[0]

	n.chans = nil // the track is gone
	r.tick(ins, now.Add(2*time.Second))
	if !first.closed {
		t.Error("source of a missing channel was not closed")
	}
	if row := r.Matrix().Inputs[0].Route; row == nil || row.State != "missing" {
		t.Errorf("route = %+v, want missing", row)
	}

	n.chans = []linkChannel{chPiano}
	r.tick(ins, now.Add(4*time.Second))
	if len(n.made) != 2 || n.made[1].closed {
		t.Errorf("source not made again: %d", len(n.made))
	}
}

func TestRouteFindsChannelByNameWhenIDChanges(t *testing.T) {
	r, n, st := newTestRouter(t)
	st.set(routeToPiano())
	moved := chPiano
	moved.ID = "cccccccccccccccc"
	n.chans = []linkChannel{moved}
	r.tick([]inputRef{{"Sampler", "in", &fakeIn{}}}, time.Now())
	if len(n.made) != 1 || n.madeFor[0] != moved.ID {
		t.Fatalf("sources for %v", n.madeFor)
	}
	if got, _ := st.get("Sampler", "in"); got.ChannelID != moved.ID {
		t.Errorf("saved ID = %q, want the new one", got.ChannelID)
	}
}

func TestRouteOfAHackThatIsAwayShowsAndWaits(t *testing.T) {
	r, n, st := newTestRouter(t, chPiano)
	st.set(routeToPiano())
	now := time.Now()
	r.tick(nil, now) // the hack is not on the bus
	if len(n.made) != 0 {
		t.Error("source made for a hack that is away")
	}
	m := r.Matrix()
	if len(m.Inputs) != 1 || m.Inputs[0].Connected || m.Inputs[0].Route.State != "hack not connected" {
		t.Errorf("matrix = %+v", m.Inputs)
	}
	r.tick([]inputRef{{"Sampler", "in", &fakeIn{}}}, now.Add(2*time.Second)) // it comes back
	if len(n.made) != 1 {
		t.Error("route did not connect when the hack came back")
	}
}

func TestRemovedRouteClosesItsSource(t *testing.T) {
	r, n, st := newTestRouter(t, chPiano)
	st.set(routeToPiano())
	ins := []inputRef{{"Sampler", "in", &fakeIn{}}}
	now := time.Now()
	r.tick(ins, now)
	st.remove("Sampler", "in")
	r.tick(ins, now.Add(2*time.Second))
	if !n.made[0].closed {
		t.Error("source of a removed route still open")
	}
	if row := r.Matrix().Inputs[0].Route; row != nil {
		t.Errorf("route = %+v, want none", row)
	}
}

func TestChangedChannelReplacesTheSource(t *testing.T) {
	r, n, st := newTestRouter(t, chPiano, chMain)
	st.set(routeToPiano())
	ins := []inputRef{{"Sampler", "in", &fakeIn{}}}
	now := time.Now()
	r.tick(ins, now)
	to := routeToPiano()
	to.ChannelID, to.Channel = chMain.ID, "Main"
	st.set(to)
	r.tick(ins, now.Add(2*time.Second))
	if !n.made[0].closed || len(n.made) != 2 || n.madeFor[1] != chMain.ID {
		t.Errorf("made for %v, first closed=%v", n.madeFor, n.made[0].closed)
	}
}

func TestUnsupportedRateIsShown(t *testing.T) {
	r, n, st := newTestRouter(t, chPiano)
	st.set(routeToPiano())
	ins := []inputRef{{"Sampler", "in", &fakeIn{}}}
	now := time.Now()
	r.tick(ins, now)
	n.made[0].stats = linkSourceStats{BadRate: 40, RateSeen: 48000}
	r.tick(ins, now.Add(2*time.Second))
	row := r.Matrix().Inputs[0].Route
	if row == nil || row.State != "rate 48000 Hz not supported" {
		t.Errorf("route = %+v", row)
	}
}

func TestUnderrunSilencesAndPrimesAgain(t *testing.T) {
	r, n, st := newTestRouter(t, chPiano)
	st.set(routeToPiano())
	in := &fakeIn{}
	ins := []inputRef{{"Sampler", "in", in}}
	now := time.Now()
	r.tick(ins, now)
	src := n.made[0]
	src.fill = routeTarget
	r.tick(ins, now) // plays one period
	src.fill = 10    // the sender stops
	before := in.wrote
	r.tick(ins, now)
	if in.wrote != before {
		t.Error("wrote a short period")
	}
	r.tick(ins, now.Add(2*time.Second))
	if row := r.Matrix().Inputs[0].Route; row == nil || row.Underruns != 1 || row.State != "waiting" {
		t.Errorf("route = %+v, want 1 underrun and waiting", row)
	}
}

func TestSnapshotNeverHasNilLists(t *testing.T) {
	r, _, _ := newTestRouter(t)
	m := r.Matrix()
	if m.Channels == nil || m.Inputs == nil {
		t.Errorf("matrix = %+v", m)
	}
	r.tick(nil, time.Now())
	m = r.Matrix()
	if m.Channels == nil || m.Inputs == nil {
		t.Errorf("matrix after tick = %+v", m)
	}
}
