package main

import "testing"

const testPeriod = 128

func TestJitterWaitsForTarget(t *testing.T) {
	j := newJitter(512, 256)
	for _, fill := range []int{0, 128, 384, 511} {
		if skip, play := j.plan(fill, testPeriod); play || skip != 0 {
			t.Errorf("fill %d: play=%v skip=%d, want wait", fill, play, skip)
		}
	}
	if j.Underruns != 0 {
		t.Errorf("waiting at the start is not an underrun: %d", j.Underruns)
	}
	if _, play := j.plan(512, testPeriod); !play {
		t.Error("fill at target must start play")
	}
}

func TestJitterSteadyFlowNeverDrops(t *testing.T) {
	j := newJitter(512, 256)
	fill := 512
	for i := 0; i < 1000; i++ {
		fill += testPeriod // one period arrives
		skip, play := j.plan(fill, testPeriod)
		if !play || skip != 0 {
			t.Fatalf("period %d: play=%v skip=%d", i, play, skip)
		}
		fill -= testPeriod // and one is played
	}
	if j.Underruns != 0 || j.Drops != 0 {
		t.Errorf("underruns=%d drops=%d, want 0", j.Underruns, j.Drops)
	}
}

func TestJitterBurstWithinMaxIsKept(t *testing.T) {
	j := newJitter(512, 256)
	j.plan(512, testPeriod)
	if skip, play := j.plan(512+256, testPeriod); skip != 0 || !play {
		t.Errorf("fill at max: skip=%d play=%v", skip, play)
	}
}

func TestJitterBurstAboveMaxDropsOldest(t *testing.T) {
	j := newJitter(512, 256)
	j.plan(512, testPeriod)
	skip, play := j.plan(1024, testPeriod)
	if skip != 1024-512 || !play {
		t.Errorf("skip=%d play=%v, want skip=512 play", skip, play)
	}
	if j.Drops != 512 {
		t.Errorf("drops = %d, want 512", j.Drops)
	}
}

func TestJitterGapGivesSilenceAndPrimesAgain(t *testing.T) {
	j := newJitter(512, 256)
	j.plan(512, testPeriod)
	if _, play := j.plan(64, testPeriod); play {
		t.Error("less than a period must not play")
	}
	if j.Underruns != 1 {
		t.Errorf("underruns = %d, want 1", j.Underruns)
	}
	// Not primed any more: even 256 frames wait until target is back.
	if _, play := j.plan(256, testPeriod); play {
		t.Error("must wait for target after an underrun")
	}
	if j.Underruns != 1 {
		t.Errorf("waiting again is not a second underrun: %d", j.Underruns)
	}
	if _, play := j.plan(512, testPeriod); !play {
		t.Error("must play again at target")
	}
}

func TestJitterFastClockDropsNowAndThen(t *testing.T) {
	// The sender runs 1% fast: 1.01 periods arrive for each period played.
	j := newJitter(512, 256)
	fill := 512.0
	for i := 0; i < 2000; i++ {
		fill += 1.01 * testPeriod
		skip, play := j.plan(int(fill), testPeriod)
		fill -= float64(skip)
		if play {
			fill -= testPeriod
		}
	}
	if j.Underruns != 0 {
		t.Errorf("underruns = %d, want 0 for a fast sender", j.Underruns)
	}
	if j.Drops == 0 || j.Drops > 2000*testPeriod/50 {
		t.Errorf("drops = %d, want some but not many", j.Drops)
	}
}

func TestJitterSlowClockGoesSilentNowAndThen(t *testing.T) {
	// The sender runs 1% slow.
	j := newJitter(512, 256)
	fill := 512.0
	for i := 0; i < 2000; i++ {
		fill += 0.99 * testPeriod
		skip, play := j.plan(int(fill), testPeriod)
		fill -= float64(skip)
		if play {
			fill -= testPeriod
		}
	}
	if j.Drops != 0 {
		t.Errorf("drops = %d, want 0 for a slow sender", j.Drops)
	}
	if j.Underruns == 0 || j.Underruns > 2000/20 {
		t.Errorf("underruns = %d, want some but not many", j.Underruns)
	}
}
