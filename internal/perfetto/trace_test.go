package perfetto

import (
	"bytes"
	"fmt"
	"reflect"
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

func TestOrderedTracksParentsBeforeChildren(t *testing.T) {
	tracks := []Track{
		{UUID: 1, ParentUUID: 9, Name: "child-b"},
		{UUID: 2, ParentUUID: 9, Name: "child-a"},
		{UUID: 9, Name: "parent"},
	}
	ordered, err := orderedTracks(tracks)
	if err != nil {
		t.Fatal(err)
	}
	got := []uint64{ordered[0].UUID, ordered[1].UUID, ordered[2].UUID}
	want := []uint64{9, 1, 2}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("track order = %v, want %v", got, want)
	}
}

func TestWriteRejectsInvalidTrackHierarchy(t *testing.T) {
	tests := []struct {
		name   string
		tracks []Track
		want   string
	}{
		{"unknown parent", []Track{{UUID: 1, ParentUUID: 2}}, "unknown parent 2"},
		{"cycle", []Track{{UUID: 1, ParentUUID: 2}, {UUID: 2, ParentUUID: 1}}, "parent cycle"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := Write(&bytes.Buffer{}, &Trace{ClockDomain: "busy", Tracks: test.tracks})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Write error = %v, want %q", err, test.want)
			}
		})
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

func TestWriteWithBudget(t *testing.T) {
	track := TrackUUID("test", "events")
	trace := &Trace{Identity: "capture", ClockDomain: "busy", Tracks: []Track{{UUID: track, Name: "events"}}}
	for i := 0; i < 100; i++ {
		trace.Events = append(trace.Events, Event{
			ID: uint64(i + 1), TrackUUID: track, Name: "event", Kind: EventSlice,
			StartNS: uint64(i * 10), DurationNS: 5, Args: map[string]any{"index": i},
		})
	}
	trace.Events[0].Required = true
	var full bytes.Buffer
	if err := Write(&full, trace); err != nil {
		t.Fatal(err)
	}
	limit := int64(full.Len() - 1000)
	var first, second bytes.Buffer
	receipt, err := WriteWithOptions(&first, trace, WriteOptions{MaxBytes: limit})
	if err != nil {
		t.Fatal(err)
	}
	secondReceipt, err := WriteWithOptions(&second, trace, WriteOptions{MaxBytes: limit})
	if err != nil {
		t.Fatal(err)
	}
	if int64(first.Len()) > limit {
		t.Fatalf("output bytes = %d, limit %d", first.Len(), limit)
	}
	if receipt.EventsDropped == 0 || receipt.EventsRetained == 0 {
		t.Fatalf("receipt = %+v, want partial retention", receipt)
	}
	if receipt.ItemsDroppedByClass["event"] == 0 || receipt.BytesDroppedByClass["event"] == 0 {
		t.Fatalf("receipt = %+v, want event-class loss", receipt)
	}
	if receipt.FirstDroppedIdentity == "" || receipt.LastDroppedIdentity == "" || receipt.DependencySkeletonsRetained == 0 {
		t.Fatalf("receipt = %+v, want dropped identity bounds and skeleton count", receipt)
	}
	if !reflect.DeepEqual(receipt, secondReceipt) || !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("budgeted export is not deterministic")
	}
}

func TestWriteWithBudgetRejectsMissingSkeletonSpace(t *testing.T) {
	errTrace := &Trace{ClockDomain: "busy", Tracks: []Track{{UUID: 1, Name: "track"}}}
	if _, err := WriteWithOptions(&bytes.Buffer{}, errTrace, WriteOptions{MaxBytes: 10}); err == nil || !strings.Contains(err.Error(), "required descriptors") {
		t.Fatalf("WriteWithOptions error = %v", err)
	}
}

type boundedWriteRecorder struct {
	max    int
	writes int
	bytes  int
}

func (w *boundedWriteRecorder) Write(p []byte) (int, error) {
	if len(p) > w.max {
		return 0, fmt.Errorf("write of %d bytes exceeds packet bound %d", len(p), w.max)
	}
	w.writes++
	w.bytes += len(p)
	return len(p), nil
}

func TestWriteStreamsPackets(t *testing.T) {
	track := TrackUUID("test", "stream")
	trace := &Trace{Identity: "capture", ClockDomain: "busy", Tracks: []Track{{UUID: track, Name: "events"}}}
	for i := 0; i < 1000; i++ {
		trace.Events = append(trace.Events, Event{
			ID: uint64(i + 1), TrackUUID: track, Name: "event", Kind: EventInstant,
			StartNS: uint64(i), Args: map[string]any{"index": i},
		})
	}
	w := &boundedWriteRecorder{max: 2 << 10}
	receipt, err := WriteWithOptions(w, trace, WriteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if w.writes < 1000 {
		t.Fatalf("writes = %d, want packet-by-packet output", w.writes)
	}
	if int64(w.bytes) != receipt.LogicalBytes {
		t.Fatalf("written bytes = %d, receipt = %d", w.bytes, receipt.LogicalBytes)
	}
}
