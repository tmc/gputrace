package gputrace

import (
	"testing"
)

func TestXcodeCountersCSVParsing(t *testing.T) {
	tracePath := "/tmp/llm-tool_1762220084.gputrace"

	trace, err := Open(tracePath)
	if err != nil {
		t.Skipf("Test trace not available: %v", err)
	}

	// Try to parse the Xcode Counters.csv
	csvData, err := trace.ParseXcodeCountersCSV("")
	if err != nil {
		t.Fatalf("Failed to parse Counters.csv: %v", err)
	}

	t.Logf("Parsed %d encoders with %d metrics", len(csvData.Encoders), len(csvData.Metrics))

	if len(csvData.Encoders) == 0 {
		t.Fatal("Expected at least one encoder")
	}

	// Check first encoder
	enc := csvData.Encoders[0]
	t.Logf("Encoder 0:")
	t.Logf("  Index: %d", enc.Index)
	t.Logf("  FunctionIndex: %d", enc.FunctionIndex)
	t.Logf("  CommandBuffer: %s", enc.CommandBufferLabel)
	t.Logf("  Encoder: %s", enc.EncoderLabel)
	t.Logf("  Counter count: %d", len(enc.Counters))

	// Check some key metrics
	if aluUtil, ok := enc.Counters["ALU Utilization"]; ok {
		t.Logf("  ALU Utilization: %.2f%%", aluUtil)
	}

	if occupancy, ok := enc.Counters["Kernel Occupancy"]; ok {
		t.Logf("  Kernel Occupancy: %.2f%%", occupancy)
	}

	if invocations, ok := enc.Counters["Kernel Invocations"]; ok {
		t.Logf("  Kernel Invocations: %.0f", invocations)
	}
}

func TestXcodeCSVMetricLookup(t *testing.T) {
	tracePath := "/tmp/llm-tool_1762220084.gputrace"

	trace, err := Open(tracePath)
	if err != nil {
		t.Skipf("Test trace not available: %v", err)
	}

	csvData, err := trace.ParseXcodeCountersCSV("")
	if err != nil {
		t.Skipf("Counters.csv not available: %v", err)
	}

	// Test GetCounterValue
	if val, ok := csvData.GetCounterValue(0, "ALU Utilization"); ok {
		t.Logf("Encoder 0 ALU Utilization: %.2f", val)
	} else {
		t.Error("Could not get ALU Utilization for encoder 0")
	}

	// Test GetMetricNames
	metrics := csvData.GetMetricNames()
	t.Logf("Available metrics: %d", len(metrics))
	if len(metrics) > 5 {
		t.Logf("First 5 metrics: %v", metrics[:5])
	}

	// Test GetEncoderByFunctionIndex
	if len(csvData.Encoders) > 0 {
		functionIdx := csvData.Encoders[0].FunctionIndex
		if enc, ok := csvData.GetEncoderByFunctionIndex(functionIdx); ok {
			t.Logf("Found encoder by function index %d: %s", functionIdx, enc.EncoderLabel)
		}
	}
}
