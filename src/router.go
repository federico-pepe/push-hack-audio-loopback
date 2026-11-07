package main

// router.go — routes from Link channels to the inputs of the hacks.
//
// A route connects one Link channel (a track of Live, Main, the channel of
// another hack) to one input of one hack. The hack declares the input on the
// audio bus (core/audiobus, OpenWithInputs). This file subscribes to the
// channel, steadies the stream with the rule in jitter.go, and writes one
// period for each tick into the input ring of the hack.
//
// Routes are saved in link-routes.json next to hack.json. A route stays in the
// file when its hack or its channel is gone, and connects again by itself when
// they come back. There is no route by default: any device on the Wi-Fi can
// offer a Link channel, so nothing connects until the user chooses it.
// Plan: plans/2026-11-07-link-inputs-matrix.md in ableton-push-hack.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"
)

const (
	routesFile = "link-routes.json"

	// The ring of a source holds 8192 frames (186 ms). The playout rule starts
	// at 4 periods and throws old audio away above 6.
	routeRing   = 8192
	routeTarget = 4 * linkPeriod
	routeExtra  = 2 * linkPeriod

	reconcileEvery = time.Second
)

// route is one saved connection. Peer and Channel are the names the channel
// had when the user chose it: the ID is tried first, the names second.
type route struct {
	Hack      string `json:"hack"`
	Input     string `json:"input"`
	ChannelID string `json:"channel_id"`
	Peer      string `json:"peer"`
	Channel   string `json:"channel"`
}

func (r route) key() string { return r.Hack + "\x00" + r.Input }

// routeStore holds the saved routes. Safe for concurrent use.
type routeStore struct {
	path string
	mu   sync.Mutex
	list []route
}

// newRouteStore loads the file. A missing or bad file means no routes.
func newRouteStore(path string) *routeStore {
	s := &routeStore{path: path}
	if b, err := os.ReadFile(path); err == nil {
		var l []route
		if json.Unmarshal(b, &l) == nil {
			for _, r := range l {
				if r.Hack != "" && r.Input != "" && r.ChannelID != "" {
					s.list = append(s.list, r)
				}
			}
		}
	}
	return s
}

func (s *routeStore) save() error {
	b, _ := json.MarshalIndent(s.list, "", "  ")
	return os.WriteFile(s.path, b, 0o644)
}

func (s *routeStore) all() []route {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]route(nil), s.list...)
}

func (s *routeStore) get(hack, input string) (route, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.list {
		if r.Hack == hack && r.Input == input {
			return r, true
		}
	}
	return route{}, false
}

// set replaces the route of an input.
func (s *routeStore) set(r route) error {
	if r.Hack == "" || r.Input == "" || r.ChannelID == "" {
		return errors.New("hack, input and channel_id are needed")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.list[:0:0]
	for _, x := range s.list {
		if x.key() != r.key() {
			out = append(out, x)
		}
	}
	s.list = append(out, r)
	return s.save()
}

// remove deletes the route of an input. No route: no error.
func (s *routeStore) remove(hack, input string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.list[:0:0]
	for _, x := range s.list {
		if x.Hack != hack || x.Input != input {
			out = append(out, x)
		}
	}
	if len(out) == len(s.list) {
		return nil
	}
	s.list = out
	return s.save()
}

// update changes the saved names and ID of a route after the channel was found
// by name under a new ID. It does nothing when the route is gone.
func (s *routeStore) update(r route) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, x := range s.list {
		if x.key() == r.key() {
			s.list[i] = r
			_ = s.save()
			return
		}
	}
}

// ---- what the router needs from Link and from the bus ----

// srcAPI is a Link source, as the router uses it. *linkSource has these methods.
type srcAPI interface {
	Fill() int
	Skip(frames int)
	Read(out []float32, frames int) int
	Stats() linkSourceStats
	Close()
}

// netAPI is the Link session, as the router uses it.
type netAPI interface {
	Channels() ([]linkChannel, error)
	NewSource(id string, rate, ringFrames int) (srcAPI, error)
}

// peerNet adapts a *linkPeer to netAPI.
type peerNet struct{ p *linkPeer }

func (n peerNet) Channels() ([]linkChannel, error) { return n.p.Channels() }
func (n peerNet) NewSource(id string, rate, ring int) (srcAPI, error) {
	s, err := n.p.NewSource(id, rate, ring)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// busInput is an input ring of a hack. *audiobus.Input has these methods.
type busInput interface {
	Write(buf []float32) bool
	Peak() float32
	Refused() uint64
}

// inputRef names one connected input.
type inputRef struct {
	Hack, Name string
	In         busInput
}

// ---- the router ----

// active is a route with a live source.
type active struct {
	rt    route
	chID  string
	src   srcAPI
	jit   *jitter
	in    busInput // refreshed on every tick: it is only valid for a short time
	state string
	buf   []float32
}

// routeStatus is what the page shows for one route.
type routeStatus struct {
	ChannelID string  `json:"channel_id"`
	Peer      string  `json:"peer"`
	Channel   string  `json:"channel"`
	State     string  `json:"state"` // ok, waiting, missing, hack not connected, rate not supported
	Fill      int     `json:"fill"`  // frames in the ring of the source
	Peak      float32 `json:"peak"`
	Underruns uint64  `json:"underruns"`
	Dropped   uint64  `json:"dropped_frames"`
	Refused   uint64  `json:"refused"` // periods the hack did not take
	BadRate   uint64  `json:"bad_rate"`
	RateSeen  int     `json:"rate_seen"`
}

// inputStatus is one column of the matrix.
type inputStatus struct {
	Hack      string       `json:"hack"`
	Input     string       `json:"input"`
	Connected bool         `json:"connected"` // the hack is on the bus now
	Route     *routeStatus `json:"route"`
}

// matrixReply is GET /api/matrix.
type matrixReply struct {
	Available bool          `json:"available"` // false when the output is not Link
	Channels  []linkChannel `json:"channels"`
	Inputs    []inputStatus `json:"inputs"`
}

type router struct {
	store  *routeStore
	net    netAPI
	period int
	rate   int

	mu     sync.Mutex // guards the snapshot only
	snap   matrixReply
	active map[string]*active
	chans  []linkChannel
	ins    []inputRef
	last   time.Time
}

func newRouter(store *routeStore, n netAPI) *router {
	return &router{store: store, net: n, period: linkPeriod, rate: linkRate,
		active: map[string]*active{}, snap: matrixReply{Available: true, Channels: []linkChannel{}, Inputs: []inputStatus{}}}
}

// Close drops every source.
func (r *router) Close() {
	for k, a := range r.active {
		a.src.Close()
		delete(r.active, k)
	}
}

// Matrix is the last snapshot. Safe from any goroutine.
func (r *router) Matrix() matrixReply {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.snap
}

// Channels is the last list of channels, for the route API.
func (r *router) Channels() []linkChannel {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]linkChannel(nil), r.snap.Channels...)
}

// tick runs once for each period. It moves one period from each source to the
// input of its hack, and once a second it reconciles routes with the session.
func (r *router) tick(ins []inputRef, now time.Time) {
	r.ins = ins
	if now.Sub(r.last) >= reconcileEvery {
		r.last = now
		r.reconcile(now)
	}
	for _, in := range ins {
		a := r.active[in.Hack+"\x00"+in.Name]
		if a == nil {
			continue
		}
		a.in = in.In
		skip, play := a.jit.plan(a.src.Fill(), r.period)
		if skip > 0 {
			a.src.Skip(skip)
		}
		if !play {
			continue // the hack reads silence from an empty ring
		}
		if a.src.Read(a.buf, r.period) == r.period {
			a.in.Write(a.buf)
		}
	}
}

// resolve finds the channel of a route: by ID first, then by names.
func resolve(rt route, chans []linkChannel) (linkChannel, bool) {
	for _, c := range chans {
		if c.ID == rt.ChannelID {
			return c, true
		}
	}
	for _, c := range chans {
		if c.Peer == rt.Peer && c.Name == rt.Channel {
			return c, true
		}
	}
	return linkChannel{}, false
}

// reconcile makes the sources match the routes and the session, then
// takes the snapshot for the page.
func (r *router) reconcile(now time.Time) {
	chans, err := r.net.Channels()
	if err == nil {
		r.chans = chans
	}
	connected := map[string]inputRef{}
	for _, in := range r.ins {
		connected[in.Hack+"\x00"+in.Name] = in
	}
	routes := r.store.all()
	want := map[string]bool{}
	status := map[string]*routeStatus{}

	for _, rt := range routes {
		k := rt.key()
		st := &routeStatus{ChannelID: rt.ChannelID, Peer: rt.Peer, Channel: rt.Channel}
		status[k] = st
		if _, ok := connected[k]; !ok {
			st.State = "hack not connected"
			continue
		}
		ch, found := resolve(rt, r.chans)
		if !found {
			st.State = "missing"
			continue
		}
		if ch.ID != rt.ChannelID || ch.Name != rt.Channel || ch.Peer != rt.Peer {
			rt.ChannelID, rt.Channel, rt.Peer = ch.ID, ch.Name, ch.Peer
			r.store.update(rt)
			st.ChannelID, st.Peer, st.Channel = ch.ID, ch.Peer, ch.Name
		}
		want[k] = true
		a := r.active[k]
		if a != nil && a.chID != ch.ID {
			a.src.Close()
			delete(r.active, k)
			a = nil
		}
		if a == nil {
			src, err := r.net.NewSource(ch.ID, r.rate, routeRing)
			if err != nil {
				st.State = "cannot subscribe: " + err.Error()
				continue
			}
			a = &active{rt: rt, chID: ch.ID, src: src, jit: newJitter(routeTarget, routeExtra),
				buf: make([]float32, r.period*2)}
			r.active[k] = a
		}
		ss := a.src.Stats()
		st.Fill, st.Peak = a.src.Fill(), ss.Peak
		st.Underruns, st.Dropped = a.jit.Underruns, a.jit.Drops
		st.BadRate, st.RateSeen = ss.BadRate, ss.RateSeen
		if a.in != nil {
			st.Refused = a.in.Refused()
		}
		switch {
		case ss.BadRate > 0 && ss.Buffers == 0:
			st.State = fmt.Sprintf("rate %d Hz not supported", ss.RateSeen)
		case a.jit.primed:
			st.State = "ok"
		default:
			st.State = "waiting"
		}
	}
	for k, a := range r.active {
		if !want[k] {
			a.src.Close()
			delete(r.active, k)
		}
	}

	// The columns: every connected input, then saved routes of hacks that are away.
	var inputs []inputStatus
	seen := map[string]bool{}
	for _, in := range r.ins {
		k := in.Hack + "\x00" + in.Name
		seen[k] = true
		inputs = append(inputs, inputStatus{Hack: in.Hack, Input: in.Name, Connected: true, Route: status[k]})
	}
	for _, rt := range routes {
		if !seen[rt.key()] {
			inputs = append(inputs, inputStatus{Hack: rt.Hack, Input: rt.Input, Route: status[rt.key()]})
		}
	}
	sort.SliceStable(inputs, func(i, j int) bool {
		if inputs[i].Hack != inputs[j].Hack {
			return inputs[i].Hack < inputs[j].Hack
		}
		return inputs[i].Input < inputs[j].Input
	})
	if inputs == nil {
		inputs = []inputStatus{}
	}
	chOut := append([]linkChannel{}, r.chans...)

	r.mu.Lock()
	r.snap = matrixReply{Available: true, Channels: chOut, Inputs: inputs}
	r.mu.Unlock()
}
