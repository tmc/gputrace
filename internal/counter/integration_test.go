//go:build darwin

package counter

import (
	"os"
	"testing"

	"github.com/tmc/gputrace/internal/trace"
)

const perfCountersIntegrationTraceEnv = "GPUTRACE_COUNTER_INTEGRATION_TRACE"

func TestParsePerfCountersIntegration(t *testing.T) {
	tracePath := integrationPathFromEnv(t, perfCountersIntegrationTraceEnv)

	tr, err := trace.Open(tracePath)
	if err != nil {
		t.Fatalf("Error loading trace: %v", err)
	}

	stats, err := ParsePerfCounters(tr)
	if err != nil {
		t.Fatalf("Error parsing counters: %v", err)
	}

	t.Logf("Total shader metrics: %d", len(stats.ShaderMetrics))

	// Show first 10 with their register info
	withRegs := 0
	for i, m := range stats.ShaderMetrics {
		if m.AllocatedRegs > 0 {
			withRegs++
		}
		if i < 10 {
			name := m.ShaderName
			if len(name) > 40 {
				name = name[:37] + "..."
			}
			t.Logf("%3d: PipelineState=%d, Name=%s, Regs=%d, Spill=%d, SIMD=%d",
				i, m.PipelineState, name, m.AllocatedRegs, m.SpilledBytes, m.SIMDGroups)
		}
	}

	t.Logf("Metrics with registers: %d/%d", withRegs, len(stats.ShaderMetrics))
}

func integrationPathFromEnv(t *testing.T, env string) string {
	t.Helper()

	path := os.Getenv(env)
	if path == "" {
		t.Skipf("set %s to run this integration test", env)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("%s=%q is not available: %v", env, path, err)
	}
	return path
}
