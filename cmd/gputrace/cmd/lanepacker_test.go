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
