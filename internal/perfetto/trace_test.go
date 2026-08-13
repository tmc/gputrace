package perfetto

import (
	"bytes"
	"strings"
	"testing"
)

func TestWriteDeterministic(t *testing.T) {
	track := TrackUUID("test", "compute")
	trace := &Trace{
		ClockDomain: "busy",
		Tracks:      []Track{{UUID: track, Name: "Compute encoders"}},
		Events: []Event{
			{ID: 2, Name: "kernel", Kind: EventGPUCompute, StartNS: 20, DurationNS: 5, Args: map[string]any{"z": 1, "a": true}},
			{ID: 1, TrackUUID: track, Name: "encoder", Kind: EventSlice, StartNS: 10, DurationNS: 20},
		},
		Counters: []Counter{{ID: 1, Name: "GPU cycles", Samples: []CounterSample{{TimestampNS: 15, Value: 42}}}},
		Metadata: map[string]any{"clock_domain": "busy", "complete": true},
	}
	var first, second bytes.Buffer
	if err := Write(&first, trace); err != nil {
		t.Fatal(err)
	}
	if err := Write(&second, trace); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("repeated writes differ")
	}
	if first.Len() == 0 || first.Bytes()[0] != 0x0a {
		t.Fatalf("trace framing = %x, want field 1", first.Bytes()[:1])
	}
}

func TestWriteRejectsDanglingTrack(t *testing.T) {
	err := Write(&bytes.Buffer{}, &Trace{
		ClockDomain: "busy",
		Events:      []Event{{Name: "encoder", Kind: EventSlice, TrackUUID: 99}},
	})
	if err == nil || !strings.Contains(err.Error(), "unknown track 99") {
		t.Fatalf("Write error = %v, want dangling-track error", err)
	}
}

func TestTrackUUID(t *testing.T) {
	a := TrackUUID("busy", "1/2")
	b := TrackUUID("busy", "1/2")
	c := TrackUUID("wall", "1/2")
	if a == 0 || a != b || a == c {
		t.Fatalf("TrackUUID = %d, %d, %d", a, b, c)
	}
}
