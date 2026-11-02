//go:build !(linux && cgo)

package main

import "errors"

// No ALSA here: this build only runs the portable parts (tests on a dev
// machine). The real PCM is in pcm_cgo.go.

type pcmOut struct{}

func openPCM(device string, channels, rate, period, buffer int) (*pcmOut, error) {
	return nil, errors.New("ALSA playback needs linux and cgo")
}

func (o *pcmOut) Period() int                       { return 0 }
func (o *pcmOut) Channels() int                     { return 0 }
func (o *pcmOut) Write(buf []int16, frames int) int { return -1 }
func (o *pcmOut) Close()                            {}

func setRealtime(prio int) bool { return false }
