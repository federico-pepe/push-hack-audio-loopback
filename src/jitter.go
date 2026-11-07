package main

// jitter.go — the playout rule for a Link source.
//
// Link delivers buffers when it can. A hack needs one steady stream. The ring
// of a source holds the difference. This rule says, once for each period,
// whether to play a period now, wait, or throw old audio away.
//
//   - Play starts when the ring holds `target` frames.
//   - If the ring holds more than `max` frames, the oldest audio goes until
//     `target` frames are left. Latency does not grow without end.
//   - If the ring holds less than one period, play silence, count an underrun,
//     and fill up to `target` again before the next period.
//
// The clock of this service and the clock of Live drift apart slowly. This
// costs one drop or one silence every few minutes. A later step can resample.

// jitter holds the state of one route.
type jitter struct {
	target, max int // frames
	primed      bool

	Underruns uint64 // periods played as silence
	Drops     uint64 // frames thrown away
}

// newJitter makes a rule. target and extra are in frames. The ring is full of
// old audio above target+extra.
func newJitter(target, extra int) *jitter {
	return &jitter{target: target, max: target + extra}
}

// plan says what to do for the next period, given the frames in the ring.
// skip is the number of oldest frames to discard first. play says if a period
// can be read after that. When play is false, the caller plays silence.
func (j *jitter) plan(fill, period int) (skip int, play bool) {
	if !j.primed {
		if fill < j.target {
			return 0, false
		}
		j.primed = true
	}
	if fill > j.max {
		skip = fill - j.target
		j.Drops += uint64(skip)
		fill -= skip
	}
	if fill < period {
		j.primed = false
		j.Underruns++
		return skip, false
	}
	return skip, true
}
