package parity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/gputrace/internal/counter"
)

func TestInspectParityExport(t *testing.T) {
	traceBundle := "/Users/tmc/tmp/parity-asymmetric-perfdata.gputrace"
	gpuprofilerDir := filepath.Join(traceBundle, "parity-asymmetric.gputrace.gpuprofiler_raw")
	stats, err := counter.ParseStreamData(gpuprofilerDir, nil)
	if err != nil {
		t.Fatalf("ParseStreamData error: %v", err)
	}

	t.Log("=== STREAMDATA INSPECTION ===")
	t.Logf("TimingSource: %s", stats.TimingSource)
	t.Logf("NumEncoders: %d, NumGPUCommands: %d, NumPipelines: %d", stats.NumEncoders, stats.NumGPUCommands, stats.NumPipelines)
	t.Logf("Pipelines count: %d", len(stats.Pipelines))
	for i, p := range stats.Pipelines {
		t.Logf("  Pipeline[%d]: Address=0x%x FunctionName=%q ID=%d", i, p.PipelineAddress, p.FunctionName, p.PipelineID)
	}

	t.Logf("EncoderTimings count: %d", len(stats.EncoderTimings))
	for i, enc := range stats.EncoderTimings {
		t.Logf("  Encoder[%d]: Index=%d Label=%q DurationMicros=%d EndOffsetMicros=%d", i, enc.Index, enc.Label, enc.DurationMicros, enc.EndOffsetMicros)
	}

	if stats.Timeline != nil {
		t.Logf("Timeline CommandBufferTimestamps count: %d (Timebase %d/%d)", len(stats.Timeline.CommandBufferTimestamps), stats.Timeline.TimebaseNumer, stats.Timeline.TimebaseDenom)
		for i, cb := range stats.Timeline.CommandBufferTimestamps {
			durNs := cb.DurationNs(stats.Timeline.TimebaseNumer, stats.Timeline.TimebaseDenom)
			t.Logf("  CB[%d]: Index=%d StartTicks=%d EndTicks=%d DurationNs=%d",
				i, cb.Index, cb.StartTicks, cb.EndTicks, durNs)
		}
	}

	t.Logf("Dispatches count: %d", len(stats.Dispatches))
	for i, d := range stats.Dispatches {
		t.Logf("  Dispatch[%d]: FunctionName=%q DisplayName=%q EncoderIdx=%d DurationUs=%d StartTicks=%d EndTicks=%d",
			i, d.FunctionName, d.DisplayName(), d.EncoderIndex, d.DurationUs, d.StartTicks, d.EndTicks)
	}

	obs, err := Observe(traceBundle)
	if err != nil {
		t.Fatalf("Observe error: %v", err)
	}
	t.Log("=== PARITY OBSERVATION RESULTS ===")
	t.Logf("Encoders: %v", obs.Encoders)
	t.Logf("Columns (%d): %v", len(obs.Columns()), obs.Columns())
	for _, col := range obs.Columns() {
		t.Logf("  Col %q: %v (Derivation: %s - %s)", col, obs.Values[col], obs.Derivations[col].Kind, obs.Derivations[col].How)
	}
	t.Logf("Notes (%d):", len(obs.Notes))
	for _, note := range obs.Notes {
		t.Logf("  Note: %s", note)
	}

	gtPath := "/Users/tmc/tmp/gputrace-parity-smoke/capture/parity-asymmetric.ground-truth.json"
	gtData, err := os.ReadFile(gtPath)
	if err == nil {
		var gt map[string]interface{}
		json.Unmarshal(gtData, &gt)
		b, _ := json.MarshalIndent(gt, "", "  ")
		t.Logf("\n=== GROUND TRUTH JSON ===\n%s", string(b))
	}
}
