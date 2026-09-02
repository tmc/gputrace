package cmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/tmc/gputrace"
	"github.com/tmc/gputrace/internal/counter"
	gputraceTrace "github.com/tmc/gputrace/internal/trace"
	"github.com/tmc/gputrace/internal/tracebundle"
)

func benchfmtDefaults(tracePath, timingSource string) []benchfmtConfig {
	config := []benchfmtConfig{
		{Key: "goos", Value: runtime.GOOS},
		{Key: "goarch", Value: runtime.GOARCH},
		{Key: "pkg", Value: "github.com/tmc/gputrace"},
		{Key: "cpu", Value: benchfmtCPU()},
		{Key: "runtime", Value: inferBenchfmtRuntime(tracePath)},
		{Key: "model", Value: inferBenchfmtModel(tracePath)},
		{Key: "prompt-tokens", Value: "unknown"},
		{Key: "capture-range", Value: inferBenchfmtCaptureRange(tracePath)},
		{Key: "compile-mode", Value: "unknown"},
		{Key: "cache-mode", Value: inferBenchfmtCacheMode(tracePath)},
		{Key: "trace-uuid", Value: "unknown"},
		{Key: "mlx-version", Value: "unknown"},
		{Key: "payload", Value: "unknown"},
	}
	if metadata, err := gputraceTrace.ReadMetadata(tracePath); err == nil && metadata.UUID != "" {
		setBenchfmtConfig(config, "trace-uuid", metadata.UUID)
	}
	if payload, err := tracebundle.InspectPayload(tracePath); err == nil {
		setBenchfmtConfig(config, "payload", string(payload.Class))
	}
	if timingSource != "" {
		config = append(config, benchfmtConfig{Key: "timing-source", Value: timingSource})
	}
	return config
}

func setBenchfmtConfig(config []benchfmtConfig, key, value string) {
	for i := range config {
		if config[i].Key == key {
			config[i].Value = value
			return
		}
	}
}

func benchfmtCPU() string {
	if runtime.GOOS == "darwin" {
		for _, key := range []string{"machdep.cpu.brand_string", "hw.model"} {
			out, err := exec.Command("sysctl", "-n", key).Output()
			if err == nil {
				if value := strings.TrimSpace(string(out)); value != "" {
					return value
				}
			}
		}
	}
	return "unknown"
}

func inferBenchfmtRuntime(path string) string {
	lower := strings.ToLower(filepath.ToSlash(path))
	for _, name := range []string{"go", "python", "swift"} {
		if strings.Contains(lower, "/"+name+"/") ||
			strings.Contains(filepath.Base(lower), "-"+name+"-") {
			return name
		}
	}
	return "unknown"
}

func inferBenchfmtModel(path string) string {
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	lower := strings.ToLower(name)
	end := len(name)
	for _, marker := range []string{
		"_tokens", "-tokens", "-staticmask", "-staticfix", "-warm",
		"-producer", "-addmm", "-perfdata",
	} {
		if i := strings.Index(lower, marker); i >= 0 && i < end {
			end = i
		}
	}
	if end == 0 {
		return "unknown"
	}
	return name[:end]
}

func inferBenchfmtCaptureRange(path string) string {
	name := strings.ToLower(filepath.Base(path))
	for _, marker := range []string{"tokens_", "tokens-", "tokens"} {
		start := strings.Index(name, marker)
		if start < 0 {
			continue
		}
		s := name[start+len(marker):]
		var left, right string
		switch {
		case strings.Contains(s, "_to_"):
			left, right, _ = strings.Cut(s, "_to_")
		case strings.Contains(s, "-to-"):
			left, right, _ = strings.Cut(s, "-to-")
		default:
			left, right, _ = strings.Cut(s, "-")
		}
		left = leadingDigits(left)
		right = leadingDigits(right)
		if left != "" && right != "" {
			return left + ":" + right
		}
	}
	return "unknown"
}

func leadingDigits(s string) string {
	for i, r := range s {
		if r < '0' || r > '9' {
			return s[:i]
		}
	}
	return s
}

func inferBenchfmtCacheMode(path string) string {
	if strings.Contains(strings.ToLower(filepath.Base(path)), "warm") {
		return "warm"
	}
	return "unknown"
}

func benchfmtProfilerValues(stats *counter.StreamDataStats, executionCost []counter.ExecutionCostByFunction) []benchfmtValue {
	values := make([]benchfmtValue, 0, 9)
	if stats.TotalDispatchTimeUs > 0 {
		values = append(values, benchfmtValue{Value: float64(stats.TotalDispatchTimeUs) * 1000, Unit: benchfmtDispatchSpanUnit})
	}
	if stats.CommandBufferActiveNs > 0 {
		values = append(values, benchfmtValue{Value: float64(stats.CommandBufferActiveNs), Unit: benchfmtCBActiveUnit})
	}
	if stats.CommandBufferWallNs > 0 {
		values = append(values, benchfmtValue{Value: float64(stats.CommandBufferWallNs), Unit: benchfmtCBWallUnit})
	}
	if stats.EffectiveGPUTimeNs != nil {
		values = append(values, benchfmtValue{Value: float64(*stats.EffectiveGPUTimeNs), Unit: benchfmtEffectiveGPUUnit})
	}
	samples := 0
	for _, dispatch := range stats.Dispatches {
		samples += dispatch.SampleCount
	}
	if samples > 0 {
		values = append(values, benchfmtValue{Value: float64(samples), Unit: benchfmtGPRWCNTRSamplesUnit})
	}
	costSamples := 0
	for _, cost := range executionCost {
		costSamples += cost.SampleCount
	}
	if costSamples > 0 {
		values = append(values, benchfmtValue{Value: float64(costSamples), Unit: benchfmtProfilerCostSamplesUnit})
	}
	values = append(values, benchfmtValue{Value: float64(stats.NumGPUCommands), Unit: benchfmtDispatchesUnit})
	if stats.Timeline != nil {
		values = append(values, benchfmtValue{Value: float64(len(stats.Timeline.CommandBufferTimestamps)), Unit: benchfmtCommandBuffersUnit})
	}
	values = append(values, benchfmtValue{Value: float64(stats.NumEncoders), Unit: benchfmtEncodersUnit})
	return values
}

func benchfmtStructuralValues(stats *gputrace.TraceStatistics) []benchfmtValue {
	values := []benchfmtValue{
		{Value: float64(stats.DispatchCalls), Unit: benchfmtDispatchesUnit},
		{Value: float64(stats.CommandBuffers), Unit: benchfmtCommandBuffersUnit},
	}
	if stats.ComputeEncodersAvailable {
		values = append(values, benchfmtValue{Value: float64(stats.ComputeEncoders), Unit: benchfmtEncodersUnit})
	}
	return values
}

func writeProfilerBenchfmt(w io.Writer, tracePath string, stats *counter.StreamDataStats, executionCost []counter.ExecutionCostByFunction, flags benchfmtConfigFlags) error {
	config, err := mergeBenchfmtConfig(benchfmtDefaults(tracePath, stats.TimingSource), flags)
	if err != nil {
		return err
	}
	var out bytes.Buffer
	if err := writeBenchfmt(&out, benchfmtRecord{
		Config: config,
		Values: benchfmtProfilerValues(stats, executionCost),
	}); err != nil {
		return err
	}
	for _, cost := range executionCost {
		if err := writeBenchfmt(&out, benchfmtRecord{
			NameConfig: []benchfmtConfig{{
				Key:   "function",
				Value: benchfmtSampleCostName(cost.FunctionName),
			}},
			Config: config,
			Values: []benchfmtValue{{
				Value: cost.CostPercent,
				Unit:  benchfmtProfilerSampleCostUnit,
			}},
		}); err != nil {
			return err
		}
	}
	if _, err := w.Write(out.Bytes()); err != nil {
		return fmt.Errorf("write benchfmt: %w", err)
	}
	return nil
}

func benchfmtSampleCostName(function string) string {
	name := []rune(sanitizeBenchfmtSuffix(function))
	if len(name) > 80 {
		name = name[:80]
	}
	sum := sha256.Sum256([]byte(function))
	return string(name) + "_" + hex.EncodeToString(sum[:4])
}
