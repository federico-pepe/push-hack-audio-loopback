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
