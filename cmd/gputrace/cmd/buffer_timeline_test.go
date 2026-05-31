package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/gputrace"
)

func TestFormatBufferTimelineJSON(t *testing.T) {
	timeline := testBufferTimelineAnalysis()

	out, err := formatBufferTimelineJSON(timeline)
	if err != nil {
		t.Fatalf("formatBufferTimelineJSON: %v", err)
	}

	var doc struct {
		TotalBuffers int `json:"total_buffers"`
		Buffers      []struct {
			Address         string `json:"address"`
			FirstSeen       int    `json:"first_seen_record"`
			LastSeen        int    `json:"last_seen_record"`
			LifetimeRecords int    `json:"lifetime_records"`
			AccessCount     int    `json:"access_count"`
			SizeBytes       uint64 `json:"size_bytes,omitempty"`
		} `json:"buffers"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("unmarshal json: %v", err)
	}
	if got, want := doc.TotalBuffers, 2; got != want {
		t.Fatalf("total_buffers = %d, want %d", got, want)
	}
	if got, want := len(doc.Buffers), 2; got != want {
		t.Fatalf("buffers = %d, want %d", got, want)
	}
	if got, want := doc.Buffers[0].Address, "0x0000000000000010"; got != want {
		t.Fatalf("first buffer address = %q, want %q", got, want)
	}
	if got, want := doc.Buffers[0].LifetimeRecords, 3; got != want {
		t.Fatalf("first buffer lifetime = %d, want %d", got, want)
	}
	if got, want := doc.Buffers[1].SizeBytes, uint64(64); got != want {
		t.Fatalf("second buffer size = %d, want %d", got, want)
	}
}

func TestFormatBufferTimelineChrome(t *testing.T) {
	timeline := testBufferTimelineAnalysis()

	out, err := formatBufferTimelineChrome(timeline)
	if err != nil {
		t.Fatalf("formatBufferTimelineChrome: %v", err)
	}

	var doc struct {
		TraceEvents []TimelineEvent                 `json:"traceEvents"`
		Metadata    bufferTimelineChromeTraceHeader `json:"gputrace_buffer_timeline"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("unmarshal chrome trace: %v", err)
	}
	if got, want := doc.Metadata.TimeUnit, "record_index_as_microseconds"; got != want {
		t.Fatalf("time unit = %q, want %q", got, want)
	}
	if got, want := doc.Metadata.TotalBuffers, 2; got != want {
		t.Fatalf("total buffers = %d, want %d", got, want)
	}

	var firstLifetime *TimelineEvent
	var accessEvents int
	var lastCounter *TimelineEvent
	for i := range doc.TraceEvents {
		ev := &doc.TraceEvents[i]
		switch ev.Category {
		case "buffer_lifetime":
			if firstLifetime == nil {
				firstLifetime = ev
			}
		case "buffer_access":
			accessEvents++
		case "buffer_counter":
			lastCounter = ev
		}
	}

	if firstLifetime == nil {
		t.Fatal("missing buffer lifetime event")
	}
	if got, want := firstLifetime.Name, "Buffer 0x0000000000000010"; got != want {
		t.Fatalf("first lifetime name = %q, want %q", got, want)
	}
	if got, want := firstLifetime.Timestamp, uint64(0); got != want {
		t.Fatalf("first lifetime timestamp = %d, want %d", got, want)
	}
	if got, want := firstLifetime.Duration, uint64(3); got != want {
		t.Fatalf("first lifetime duration = %d, want %d", got, want)
	}
	if got, want := firstLifetime.Args["address"], "0x0000000000000010"; got != want {
		t.Fatalf("first lifetime address arg = %v, want %q", got, want)
	}
	if got, want := accessEvents, 4; got != want {
		t.Fatalf("access events = %d, want %d", got, want)
	}
	if lastCounter == nil {
		t.Fatal("missing buffer counter events")
	}
	if got, want := lastCounter.Args["active_buffers"], float64(0); got != want {
		t.Fatalf("last active_buffers = %v, want %v", got, want)
	}
}

func TestWriteBufferTimelineOutputStdout(t *testing.T) {
	out, err := captureStdout(t, func() error {
		return writeBufferTimelineOutput("/dev/stdout", "{\"total_buffers\":0}\n")
	})
	if err != nil {
		t.Fatalf("writeBufferTimelineOutput: %v", err)
	}
	if got, want := out, "{\"total_buffers\":0}\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestRunBufferTimelineRejectsInvalidWidthBeforeTraceIO(t *testing.T) {
	oldFormat := bufferTimelineFormat
	oldOutput := bufferTimelineOutput
	oldWidth := bufferTimelineWidth
	t.Cleanup(func() {
		bufferTimelineFormat = oldFormat
		bufferTimelineOutput = oldOutput
		bufferTimelineWidth = oldWidth
	})

	bufferTimelineFormat = "ascii"
	bufferTimelineOutput = ""
	tracePath := filepath.Join(t.TempDir(), "missing.gputrace")

	for _, width := range []int{-1, minBufferTimelineWidth - 1} {
		bufferTimelineWidth = width
		err := runBufferTimeline(nil, []string{tracePath})
		if err == nil {
			t.Fatalf("runBufferTimeline width %d returned nil error", width)
		}
		if got, want := err.Error(), "--width must be >= 21"; got != want {
			t.Fatalf("runBufferTimeline width %d error = %q, want %q", width, got, want)
		}
	}
}

func testBufferTimelineAnalysis() *gputrace.BufferTimelineAnalysis {
	return &gputrace.BufferTimelineAnalysis{
		BufferEvents: map[uint64]*gputrace.BufferLifecycle{
			0x20: {
				Address:       0x20,
				FirstSeen:     3,
				LastSeen:      9,
				AccessCount:   2,
				EncoderIDs:    []int{2},
				AccessIndices: []int{3, 9},
				IsActive:      true,
				Size:          64,
			},
			0x10: {
				Address:       0x10,
				FirstSeen:     1,
				LastSeen:      4,
				AccessCount:   2,
				EncoderIDs:    []int{1},
				AccessIndices: []int{1, 4},
				IsActive:      true,
			},
		},
		TotalBuffers:     2,
		PeakMemoryBytes:  64,
		PeakMemoryMB:     64.0 / (1024 * 1024),
		TotalAllocations: 2,
		AverageLifetime:  4.5,
		MinRecordIndex:   1,
		MaxRecordIndex:   9,
	}
}

func TestRunBufferTimelineJSONWritesOutputFile(t *testing.T) {
	tracePath := "../../../testdata/traces/01-single-encoder/01-single-encoder-run1.gputrace"
	if _, err := os.Stat(tracePath); os.IsNotExist(err) {
		t.Skipf("skipping test, trace file not found: %s. See docs/TESTING.md for fixture setup.", tracePath)
	} else if err != nil {
		t.Fatalf("stat trace fixture: %v", err)
	}

	oldFormat := bufferTimelineFormat
	oldOutput := bufferTimelineOutput
	oldWidth := bufferTimelineWidth
	t.Cleanup(func() {
		bufferTimelineFormat = oldFormat
		bufferTimelineOutput = oldOutput
		bufferTimelineWidth = oldWidth
	})

	outPath := filepath.Join(t.TempDir(), "buffers.json")
	bufferTimelineFormat = "json"
	bufferTimelineOutput = outPath
	bufferTimelineWidth = 100

	if err := runBufferTimeline(nil, []string{tracePath}); err != nil {
		t.Fatalf("runBufferTimeline json: %v", err)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	var doc struct {
		TotalBuffers int `json:"total_buffers"`
		Buffers      []struct {
			Address string `json:"address"`
		} `json:"buffers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if doc.TotalBuffers != len(doc.Buffers) {
		t.Fatalf("total_buffers = %d, buffers len = %d", doc.TotalBuffers, len(doc.Buffers))
	}
	if doc.TotalBuffers == 0 {
		t.Fatal("expected at least one buffer in fixture export")
	}
	if doc.Buffers[0].Address == "" {
		t.Fatal("first buffer address is empty")
	}
}

func TestRunBufferTimelineChromeWritesOutputFile(t *testing.T) {
	tracePath := "../../../testdata/traces/01-single-encoder/01-single-encoder-run1.gputrace"
	if _, err := os.Stat(tracePath); os.IsNotExist(err) {
		t.Skipf("skipping test, trace file not found: %s. See docs/TESTING.md for fixture setup.", tracePath)
	} else if err != nil {
		t.Fatalf("stat trace fixture: %v", err)
	}

	oldFormat := bufferTimelineFormat
	oldOutput := bufferTimelineOutput
	oldWidth := bufferTimelineWidth
	t.Cleanup(func() {
		bufferTimelineFormat = oldFormat
		bufferTimelineOutput = oldOutput
		bufferTimelineWidth = oldWidth
	})

	outPath := filepath.Join(t.TempDir(), "buffers.chrome.json")
	bufferTimelineFormat = "chrome"
	bufferTimelineOutput = outPath
	bufferTimelineWidth = 100

	if err := runBufferTimeline(nil, []string{tracePath}); err != nil {
		t.Fatalf("runBufferTimeline chrome: %v", err)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	var doc struct {
		TraceEvents []TimelineEvent `json:"traceEvents"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	for _, ev := range doc.TraceEvents {
		if ev.Category == "buffer_lifetime" {
			return
		}
	}
	t.Fatal("expected at least one buffer_lifetime event")
}
