//go:build !(linux && cgo)

package main

import "errors"

// No Link here: this build only runs the portable parts (tests on a dev
// machine). The real peer is in link_cgo.go.

var errNoLink = errors.New("Link Audio needs the cgo build")

type linkPeer struct{}

func newLinkPeer(name string) (*linkPeer, error) { return nil, errNoLink }
func (l *linkPeer) Peers() int                   { return 0 }
func (l *linkPeer) Tempo() float64               { return 0 }
func (l *linkPeer) Close()                       {}

type linkSink struct{}

func (l *linkPeer) NewSink(name string, maxFrames int) (*linkSink, error) {
	return nil, errNoLink
}
func (k *linkSink) Write(samples []int16, frames, rate, leadMs int) (bool, error) {
	return false, errNoLink
}
func (k *linkSink) Reanchors() uint64 { return 0 }
func (k *linkSink) Close()            {}

type linkChannel struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	PeerID string `json:"peer_id"`
	Peer   string `json:"peer"`
}

func (l *linkPeer) Channels() ([]linkChannel, error) { return nil, errNoLink }

type linkSource struct{}

type linkSourceStats struct {
	Buffers, Frames           uint64
	BadRate, BadFormat, Overs uint64
	RateSeen, ChannelsSeen    int
	Peak                      float32
}

func (l *linkPeer) NewSource(id string, rate, ringFrames int) (*linkSource, error) {
	return nil, errNoLink
}
func (k *linkSource) Fill() int                          { return 0 }
func (k *linkSource) Skip(frames int)                    {}
func (k *linkSource) Close()                             {}
func (k *linkSource) Read(out []float32, frames int) int { return 0 }
func (k *linkSource) Stats() linkSourceStats             { return linkSourceStats{} }
