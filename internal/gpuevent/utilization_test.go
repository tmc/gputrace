package gpuevent

import "testing"

func kernelAt(start, end uint64, name string) Event {
	return Event{Kind: KindKernel, Name: name, StartNS: start, EndNS: end}
}

func TestUtilizationOf(t *testing.T) {
	tests := []struct {
		name          string
		events        []Event
		wantWall      uint64
		wantBusy      uint64
		wantIdle      uint64
		wantGaps      int
		wantMaxGap    uint64
		wantOccupancy float64
	}{
		{
			name:          "back to back",
			events:        []Event{kernelAt(0, 100, "a"), kernelAt(100, 200, "b")},
			wantWall:      200,
			wantBusy:      200,
			wantOccupancy: 100,
		},
		{
			name:          "one gap",
			events:        []Event{kernelAt(0, 100, "a"), kernelAt(300, 400, "b")},
			wantWall:      400,
			wantBusy:      200,
			wantIdle:      200,
			wantGaps:      1,
			wantMaxGap:    200,
			wantOccupancy: 50,
		},
		{
			// Concurrent kernels overlap in wall time; summing their
			// durations would report 150% occupancy.
			name:          "overlapping streams",
			events:        []Event{kernelAt(0, 100, "a"), kernelAt(50, 100, "b")},
			wantWall:      100,
			wantBusy:      100,
			wantOccupancy: 100,
		},
		{
			name:          "unsorted input",
			events:        []Event{kernelAt(300, 400, "b"), kernelAt(0, 100, "a")},
			wantWall:      400,
			wantBusy:      200,
			wantIdle:      200,
			wantGaps:      1,
			wantMaxGap:    200,
			wantOccupancy: 50,
		},
		{name: "no events"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := UtilizationOf(tt.events)
			if got.WallSpanNS != tt.wantWall || got.BusyNS != tt.wantBusy || got.IdleNS != tt.wantIdle {
				t.Errorf("wall/busy/idle = %d/%d/%d, want %d/%d/%d",
					got.WallSpanNS, got.BusyNS, got.IdleNS, tt.wantWall, tt.wantBusy, tt.wantIdle)
			}
			if got.GapCount != tt.wantGaps || got.MaxGapNS != tt.wantMaxGap {
				t.Errorf("gaps = %d (max %d), want %d (max %d)", got.GapCount, got.MaxGapNS, tt.wantGaps, tt.wantMaxGap)
			}
			if got.OccupancyPct != tt.wantOccupancy {
				t.Errorf("OccupancyPct = %v, want %v", got.OccupancyPct, tt.wantOccupancy)
			}
		})
	}
}

func TestUtilizationConcurrencyAndTopGaps(t *testing.T) {
	events := []Event{
		kernelAt(0, 100, "a"),
		kernelAt(0, 100, "b"), // fully concurrent with a
		kernelAt(500, 600, "c"),
		kernelAt(700, 750, "d"),
	}
	got := UtilizationOf(events)
	// 350 ns of activity inside a 250 ns busy window.
	if got.Concurrency != 1.4 {
		t.Errorf("Concurrency = %v, want 1.4", got.Concurrency)
	}
	if len(got.TopGaps) != 2 || got.TopGaps[0].DurationNS != 400 {
		t.Fatalf("TopGaps = %+v, want the 400 ns gap first", got.TopGaps)
	}
	if got.TopGaps[0].AfterName != "b" && got.TopGaps[0].AfterName != "a" {
		t.Errorf("gap opened after %q, want a concurrent kernel name", got.TopGaps[0].AfterName)
	}
	if got.TopGaps[0].BeforeName != "c" {
		t.Errorf("gap closed by %q, want c", got.TopGaps[0].BeforeName)
	}
}

func TestIdleFindingReportedWhenDeviceWaits(t *testing.T) {
	events := []Event{kernelAt(0, 1_000_000, "a"), kernelAt(9_000_000, 10_000_000, "b")}
	rep := Analyze(events, nil)
	var found *Finding
	for i := range rep.Findings {
		if rep.Findings[i].Kind == FindingGPUIdle {
			found = &rep.Findings[i]
		}
	}
	if found == nil {
		t.Fatalf("no gpu-idle finding in %+v", rep.Findings)
	}
	if found.Severity != SeverityHigh {
		t.Errorf("Severity = %s, want high at 20%% occupancy", found.Severity)
	}
}
