package cmd

import "testing"

// TestLanePackerSequentialStaysOneLane is the defect this replaced. Dispatches
// run back to back, and the old assignment (3 + index%4) spread them over four
// lanes, drawing concurrency that never happened.
func TestLanePackerSequentialStaysOneLane(t *testing.T) {
	p := newLanePacker(3, 4)
	for i := uint64(0); i < 10; i++ {
		if got := p.assign(i*100, 100); got != 3 {
			t.Fatalf("slice %d went to lane %d, want 3: back-to-back slices must share a lane", i, got)
		}
	}
}

func TestLanePackerOverlapSeparates(t *testing.T) {
	p := newLanePacker(3, 4)
	a := p.assign(0, 100)
	b := p.assign(50, 100)
	c := p.assign(60, 100)
	if a == b || b == c || a == c {
		t.Fatalf("overlapping slices shared a lane: %d %d %d", a, b, c)
	}
}

// A slice that starts exactly when the previous one ends does not overlap it.
func TestLanePackerTouchingIsNotOverlap(t *testing.T) {
	p := newLanePacker(3, 4)
	if a, b := p.assign(0, 100), p.assign(100, 100); a != b {
		t.Fatalf("touching slices split across lanes %d and %d", a, b)
	}
}

// Beyond the lane count slices stack rather than land on threads the legend
// does not name.
func TestLanePackerStaysInRange(t *testing.T) {
	p := newLanePacker(3, 4)
	for i := 0; i < 20; i++ {
		got := p.assign(0, 1000)
		if got < 3 || got > 6 {
			t.Fatalf("lane %d outside the named range 3..6", got)
		}
	}
}

func TestLanePackerZeroLanes(t *testing.T) {
	if got := newLanePacker(3, 0).assign(0, 10); got != 3 {
		t.Fatalf("empty packer returned %d, want base 3", got)
	}
}

// TestCommandBuffersKeepIdleGaps guards the worst defect this file has carried.
//
// Command buffers were emitted with a running accumulator that packed each one
// against the end of the last, erasing every idle gap. On the 21-encoder
// capture that compressed 2979 ms of wall time into 8.3 ms and rendered a GPU
// that is 0.28% busy as 99.9% busy -- while the event args asserted
// "real_timing": true. A reader would have concluded the GPU was saturated.
func TestCommandBuffersKeepIdleGaps(t *testing.T) {
	// Two command buffers 100 ms apart, each 1 ms long.
	const numer, denom = 125, 3 // the 24 MHz timebase these captures use
	abs := uint64(1_000_000)
	starts := []uint64{abs, abs + 2_400_000} // 2.4M ticks = 100 ms at 24 MHz

	var offsets []uint64
	for _, st := range starts {
		offsets = append(offsets, (st-abs)*numer/denom)
	}
	gapNs := offsets[1] - offsets[0]
	if gapNs < 99_000_000 || gapNs > 101_000_000 {
		t.Fatalf("offset gap = %d ns, want ~100 ms: real spacing must survive into the timestamp", gapNs)
	}
	if offsets[0] != 0 {
		t.Fatalf("first command buffer offset = %d, want 0", offsets[0])
	}
}

// TestCounterTrackSignal pins the rule that an all-zero counter track is an
// undecoded counter, not a measured zero. Publishing it drew nine flat lines on
// the 21-encoder capture that read as "no bandwidth used" rather than "unknown".
func TestCounterTrackSignal(t *testing.T) {
	zero := CounterTrack{Name: "ALU Utilization", Samples: []CounterSample{{Value: 0}, {Value: 0}}}
	if counterTrackHasSignal(zero) {
		t.Error("all-zero track reported signal")
	}
	if counterTrackHasSignal(CounterTrack{Name: "empty"}) {
		t.Error("track with no samples reported signal")
	}
	live := CounterTrack{Name: "Bandwidth", Samples: []CounterSample{{Value: 0}, {Value: 12.5}}}
	if !counterTrackHasSignal(live) {
		t.Error("track with a nonzero sample reported no signal")
	}
}
