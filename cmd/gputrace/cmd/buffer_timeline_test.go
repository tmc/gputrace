package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/gputrace"
)

func TestFormatBufferTimelineJSON(t *testing.T) {
	timeline := &gputrace.BufferTimelineAnalysis{
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

func TestRunBufferTimelineJSONWritesOutputFile(t *testing.T) {
	tracePath := "../../../testdata/traces/01-single-encoder/01-single-encoder-run1.gputrace"
	if _, err := os.Stat(tracePath); os.IsNotExist(err) {
		t.Skipf("skipping test, trace file not found: %s. Run 'make fetch-testdata' to fetch test assets.", tracePath)
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
