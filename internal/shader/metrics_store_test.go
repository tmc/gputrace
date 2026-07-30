package shader

import (
	"testing"

	"github.com/tmc/gputrace/internal/counter"
	"github.com/tmc/gputrace/internal/trace"
)

// TestExtractShaderMetricsUsesStoreStats checks that a capture-only bundle,
// which has no streamData, still reports the shader statistics Xcode archived
// in its store sections.
func TestExtractShaderMetricsUsesStoreStats(t *testing.T) {
	tr, err := trace.Open("../../testdata/traces/06-six-encoders/06-six-encoders-run1.gputrace")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	report, err := ExtractShaderMetrics(tr)
	if err != nil {
		t.Fatalf("extract shader metrics: %v", err)
	}

	byName := make(map[string]*ShaderMetrics)
	for _, metrics := range report.Shaders {
		byName[metrics.Name] = metrics
	}

	tests := []struct {
		name         string
		instructions int
		allocated    int
		uniform      int
	}{
		{"simple_add", 6, 3, 8},
		{"simple_divide", 8, 3, 8},
		{"complex_math", 59, 19, 16},
		{"low_register_pressure", 5, 2, 4},
		// Encoder-numbered labels resolve to the same compiled function.
		{"Encoder_5_complex_math", 59, 19, 16},
	}

	for _, tt := range tests {
		metrics, ok := byName[tt.name]
		if !ok {
			t.Errorf("no metrics for %q", tt.name)
			continue
		}
		if metrics.InstructionCount != tt.instructions {
			t.Errorf("%s instruction count = %d, want %d", tt.name, metrics.InstructionCount, tt.instructions)
		}
		if metrics.AllocatedRegisters != tt.allocated {
			t.Errorf("%s allocated registers = %d, want %d", tt.name, metrics.AllocatedRegisters, tt.allocated)
		}
		if metrics.Address == 0 {
			t.Errorf("%s address was cleared; store stats carry no pipeline address", tt.name)
		}
		// Store sections do not archive the highest live register.
		if metrics.HighRegister != 0 {
			t.Errorf("%s high register = %d, want 0", tt.name, metrics.HighRegister)
		}
	}

	// A debug-group label names no compiled function and must stay empty.
	if group, ok := byName["MultipleEncoders_6"]; ok && group.InstructionCount != 0 {
		t.Errorf("MultipleEncoders_6 instruction count = %d, want 0", group.InstructionCount)
	}
}

// TestApplyPipelineStatsKeepsEncoderAddress checks that statistics archived
// without a pipeline address leave an existing address in place.
func TestApplyPipelineStatsKeepsEncoderAddress(t *testing.T) {
	metrics := &ShaderMetrics{Address: 0x996cacd00}
	applyPipelineStatsToMetrics(metrics, &counter.PipelineStats{InstructionCount: 6})
	if metrics.Address != 0x996cacd00 {
		t.Errorf("address = %#x, want 0x996cacd00", metrics.Address)
	}

	applyPipelineStatsToMetrics(metrics, &counter.PipelineStats{PipelineAddress: 0x1050bb390})
	if metrics.Address != 0x1050bb390 {
		t.Errorf("address = %#x, want 0x1050bb390", metrics.Address)
	}
}
