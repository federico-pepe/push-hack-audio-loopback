package main

/*
#cgo LDFLAGS: -lasound
#include "bridge.h"
#include <stdlib.h>
*/
import "C"

import (
	"errors"
	"unsafe"
)

// pcmOut is an open ALSA playback stream.
type pcmOut struct{ p *C.mixer_pcm_t }

func openPCM(device string, channels, rate, period, buffer int) (*pcmOut, error) {
	cd := C.CString(device)
	defer C.free(unsafe.Pointer(cd))
	p := C.mixer_pcm_open(cd, C.uint(channels), C.uint(rate), C.uint(period), C.uint(buffer))
	if p == nil {
		return nil, errors.New(C.GoString(C.mixer_last_error()))
	}
	return &pcmOut{p: p}, nil
}

func (o *pcmOut) Period() int   { return int(C.mixer_pcm_period(o.p)) }
func (o *pcmOut) Channels() int { return int(C.mixer_pcm_channels(o.p)) }

// Write sends frames of interleaved S16 samples; it returns the frames written
// or a negative ALSA code.
func (o *pcmOut) Write(buf []int16, frames int) int {
	if len(buf) == 0 || frames <= 0 {
		return 0
	}
	return int(C.mixer_pcm_writei(o.p, (*C.int16_t)(unsafe.Pointer(&buf[0])), C.uint(frames)))
}

func (o *pcmOut) Close() { C.mixer_pcm_close(o.p) }

func setRealtime(prio int) bool { return C.mixer_set_realtime(C.int(prio)) == 0 }
