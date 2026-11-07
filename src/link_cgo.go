//go:build linux && cgo

package main

/*
#cgo CXXFLAGS: -std=c++17 -O2 -Wno-multichar -DLINK_PLATFORM_UNIX=1 -DLINK_PLATFORM_LINUX=1 -I${SRCDIR}/third_party/link/include -I${SRCDIR}/third_party/link/modules/asio-standalone/asio/include
#cgo LDFLAGS: -lpthread -static-libstdc++ -static-libgcc
#include "linkshim.h"
#include <stdlib.h>
*/
import "C"

import (
	"encoding/json"
	"errors"
	"unsafe"
)

// linkPeer is the process-wide Link Audio peer.
type linkPeer struct{ p *C.pl_link }

func newLinkPeer(name string) (*linkPeer, error) {
	cn := C.CString(name)
	defer C.free(unsafe.Pointer(cn))
	p := C.pl_link_new(cn)
	if p == nil {
		return nil, errors.New("cannot start the Link peer")
	}
	return &linkPeer{p: p}, nil
}

func (l *linkPeer) Peers() int     { return int(C.pl_link_peers(l.p)) }
func (l *linkPeer) Tempo() float64 { return float64(C.pl_link_tempo(l.p)) }
func (l *linkPeer) Close()         { C.pl_link_free(l.p); l.p = nil }

// linkSink is one channel of the peer, for one audio hack.
type linkSink struct{ s *C.pl_sink }

func (l *linkPeer) NewSink(name string, maxFrames int) (*linkSink, error) {
	cn := C.CString(name)
	defer C.free(unsafe.Pointer(cn))
	s := C.pl_sink_new(l.p, cn, C.int(maxFrames))
	if s == nil {
		return nil, errors.New("cannot make the Link sink " + name)
	}
	return &linkSink{s: s}, nil
}

// Write sends interleaved stereo. It reports whether a peer listens.
func (k *linkSink) Write(samples []int16, frames, rate, leadMs int) (listening bool, err error) {
	if frames <= 0 || len(samples) < frames*2 {
		return false, errors.New("bad block")
	}
	r := C.pl_sink_write(k.s, (*C.int16_t)(unsafe.Pointer(&samples[0])), C.int(frames), C.int(rate), C.int(leadMs))
	switch r {
	case 1:
		return true, nil
	case 0:
		return false, nil
	}
	return false, errors.New("Link refused the block")
}

func (k *linkSink) Reanchors() uint64 { return uint64(C.pl_sink_reanchors(k.s)) }
func (k *linkSink) Close()            { C.pl_sink_free(k.s); k.s = nil }

// linkChannel is one channel offered by a peer in the session.
type linkChannel struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	PeerID string `json:"peer_id"`
	Peer   string `json:"peer"`
}

// Channels lists every channel in the session, ours included.
func (l *linkPeer) Channels() ([]linkChannel, error) {
	buf := make([]byte, 4096)
	for {
		n := int(C.pl_link_channels(l.p, (*C.char)(unsafe.Pointer(&buf[0])), C.int(len(buf))))
		if n < len(buf) {
			var out []linkChannel
			if err := json.Unmarshal(buf[:n], &out); err != nil {
				return nil, err
			}
			return out, nil
		}
		buf = make([]byte, n+1)
	}
}

// linkSource receives one channel. The Link thread fills a ring; the owner
// reads it one period at a time.
type linkSource struct{ s *C.pl_source }

// linkSourceStats is what the source counted.
type linkSourceStats struct {
	Buffers, Frames           uint64
	BadRate, BadFormat, Overs uint64
	RateSeen, ChannelsSeen    int
	Peak                      float32
}

func (l *linkPeer) NewSource(id string, rate, ringFrames int) (*linkSource, error) {
	cid := C.CString(id)
	defer C.free(unsafe.Pointer(cid))
	s := C.pl_source_new(l.p, cid, C.int(rate), C.int(ringFrames))
	if s == nil {
		return nil, errors.New("cannot subscribe to channel " + id)
	}
	return &linkSource{s: s}, nil
}

func (k *linkSource) Fill() int       { return int(C.pl_source_fill(k.s)) }
func (k *linkSource) Skip(frames int) { C.pl_source_skip(k.s, C.int(frames)) }
func (k *linkSource) Close()          { C.pl_source_free(k.s); k.s = nil }

// Read copies up to frames stereo frames (interleaved) into out and returns how many.
func (k *linkSource) Read(out []float32, frames int) int {
	if frames <= 0 || len(out) < frames*2 {
		return 0
	}
	return int(C.pl_source_read(k.s, (*C.float)(unsafe.Pointer(&out[0])), C.int(frames)))
}

func (k *linkSource) Stats() linkSourceStats {
	var st C.pl_source_stats
	C.pl_source_get_stats(k.s, &st)
	return linkSourceStats{
		Buffers: uint64(st.buffers), Frames: uint64(st.frames),
		BadRate: uint64(st.bad_rate), BadFormat: uint64(st.bad_format), Overs: uint64(st.overruns),
		RateSeen: int(st.rate_seen), ChannelsSeen: int(st.channels_seen), Peak: float32(st.peak),
	}
}
