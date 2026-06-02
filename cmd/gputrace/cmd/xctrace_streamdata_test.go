//go:build darwin

package cmd

import (
	"path/filepath"
	"testing"
)

func TestParseXctraceGPUIntervalsXMLResolvesRefsAndFiltersProcess(t *testing.T) {
	path := filepath.Join("testdata", "metal_gpu_intervals.xml")
	rows, rowsRead, err := parseXctraceGPUIntervalsXML(path, "target_proc", 10)
	if err != nil {
		t.Fatal(err)
	}
	if rowsRead != 2 {
		t.Fatalf("rowsRead = %d, want 2", rowsRead)
	}
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(rows))
	}
	if rows[0].StartNs != 1000 || rows[0].DurationNs != 4000 {
		t.Fatalf("unexpected timing row: %+v", rows[0])
	}
	if rows[0].Process != "target_proc (42)" {
		t.Fatalf("process = %q", rows[0].Process)
	}
	if rows[0].CommandBufferID != 291 || rows[0].EncoderID != 2748 {
		t.Fatalf("unexpected ids: %+v", rows[0])
	}
}
