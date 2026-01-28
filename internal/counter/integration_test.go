//go:build darwin

package counter

import (
    "os"
    "testing"

    "github.com/tmc/gputrace/internal/trace"
)

func TestMetricsIntegration(t *testing.T) {
    tracePath := "/tmp/mlx-lm-generate_tokens_8_to_9-perfdata.gputrace"
    
    if _, err := os.Stat(tracePath); os.IsNotExist(err) {
        t.Skip("Test trace not available")
    }
    
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
