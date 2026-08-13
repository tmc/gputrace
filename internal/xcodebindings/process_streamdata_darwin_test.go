//go:build darwin

package xcodebindings

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tmc/apple/private/xcode/gtshaderprofiler"
	"github.com/tmc/gputrace/internal/counter"
	"github.com/tmc/gputrace/internal/testtrace"
)

// TestProcessStreamData builds Xcode's shader trace model from a real profiler
// archive. It is opt-in: the run spawns GTLLVMHelper and disassembles every
// shader in the capture, which takes far longer than an ordinary unit test, and
// no repository fixture carries streamData.
func TestProcessStreamData(t *testing.T) {
	streamPath := testtrace.Path("GPUTRACE_PROCESS_STREAMDATA", testtrace.StreamData)
	if streamPath == "" {
		t.Skip("set GPUTRACE_TEST_TRACE to a .gputrace bundle, or GPUTRACE_PROCESS_STREAMDATA to a streamData archive")
	}
	streamPath, err := filepath.Abs(streamPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(streamPath); err != nil {
		t.Skipf("streamData unavailable: %v", err)
	}

	var summary ProcessedStreamData
	err = WithProcessedModel(context.Background(), streamPath, func(model *ProcessedStreamData) error {
		summary = *model
		return nil
	})
	if err != nil {
		t.Fatalf("process streamData: %v", err)
	}
	if summary.LLVMHelperPath == "" {
		t.Error("no GTLLVMHelper path recorded")
	}

	// The processed model must agree with the archive it was built from, which
	// is the check that distinguishes a real build from an empty one.
	stream, err := ProbeStreamData(streamPath)
	if err != nil {
		t.Fatalf("probe streamData: %v", err)
	}
	if summary.EncoderCount != stream.EncoderInfoCount {
		t.Errorf("encoder count = %d, want %d from the archive", summary.EncoderCount, stream.EncoderInfoCount)
	}
	if summary.DrawCount == 0 {
		t.Error("draw count = 0, want the dispatches recorded in the capture")
	}
	checkCommandOwnership(t, summary, streamPath)
	t.Logf("draws=%d encoders=%d costs=%d helper=%s",
		summary.DrawCount, summary.EncoderCount, summary.CostCount, summary.LLVMHelperPath)
	if os.Getenv("GPUTRACE_MIO_SETUP_DATA_PATH") == "1" {
		if !summary.CostModel.Ready {
			t.Fatal("data-path setup did not populate scalar cost totals")
		}
		if summary.CostCount == 0 {
			t.Error("cost count = 0 after data-path setup")
		}
		if math.Abs(summary.CostModel.Scope0DataMaster2-100) > 1e-9 {
			t.Errorf("scope 0 total = %g, want 100", summary.CostModel.Scope0DataMaster2)
		}
		if scope4 := summary.CostModel.Scope4DataMaster2; math.IsNaN(scope4) || math.IsInf(scope4, 0) || scope4 < 0 || scope4 > summary.CostModel.Scope0DataMaster2 {
			t.Errorf("scope 4 total = %g, want a finite value in [0, %g]", scope4, summary.CostModel.Scope0DataMaster2)
		}
	}
	if os.Getenv("GPUTRACE_MIO_TIMELINE_DATA") == "1" {
		timeline := summary.Timeline
		if !timeline.Ready {
			t.Fatal("serialized timeline is not ready")
		}
		if timeline.DrawCount != summary.DrawCount || timeline.PipelineStateCount != uint64(len(summary.Pipelines)) {
			t.Errorf("timeline counts = %#v, want draws=%d pipelines=%d", timeline, summary.DrawCount, len(summary.Pipelines))
		}
		if len(timeline.PipelineDraws) != len(summary.Pipelines) {
			t.Errorf("timeline pipeline records = %d, want %d", len(timeline.PipelineDraws), len(summary.Pipelines))
		}
		var pipelineDraws uint64
		var pipelineDuration uint64
		for _, pipeline := range timeline.PipelineDraws {
			pipelineDraws += pipeline.DrawCount
			pipelineDuration += pipeline.DrawDurationDataMaster2
		}
		if pipelineDraws != timeline.DrawCount {
			t.Errorf("timeline pipeline draw total = %d, want %d", pipelineDraws, timeline.DrawCount)
		}
		wantDrawSamples := int(min(timeline.DrawCount, 3))
		if len(timeline.EncoderDurations) != int(timeline.EncoderCount) || len(timeline.DrawDurationsDataMaster2) != wantDrawSamples {
			t.Errorf("timeline attribution lengths = encoders %d/%d draws %d/%d", len(timeline.EncoderDurations), timeline.EncoderCount, len(timeline.DrawDurationsDataMaster2), wantDrawSamples)
		}
		if os.Getenv("GPUTRACE_MIO_SETUP_DATA_PATH") == "1" && len(timeline.DrawDurationsDataMaster2) > 0 && timeline.DrawDurationsDataMaster2[0] == 0 {
			t.Errorf("setup-backed timeline draw duration is zero: %#v", timeline.DrawDurationsDataMaster2)
		}
		if os.Getenv("GPUTRACE_MIO_SETUP_DATA_PATH") == "1" && pipelineDuration == 0 {
			t.Errorf("setup-backed pipeline duration is zero: %#v", timeline.PipelineDraws)
		}
		t.Logf("timeline: draws=%d encoders=%d costs=%d pipelines=%d scope0=%.6g scope4=%.6g", timeline.DrawCount, timeline.EncoderCount, timeline.CostCount, timeline.PipelineStateCount, timeline.Scope0DataMaster2, timeline.Scope4DataMaster2)
	}
	if os.Getenv("GPUTRACE_MIO_TRACE_TRACKS") == "1" {
		if summary.Tracks.TopDrawCount != summary.DrawCount {
			t.Errorf("top draw tracks = %d, want draw count %d", summary.Tracks.TopDrawCount, summary.DrawCount)
		}
		if summary.Tracks.TopBinaryCount == 0 || summary.Tracks.TopKickCount == 0 {
			t.Errorf("top track counts = %#v, want populated binary and kick tracks", summary.Tracks)
		}
		for _, sample := range append(summary.Tracks.DrawSamples, summary.Tracks.KickSamples...) {
			if sample.Empty {
				t.Error("top track sample is empty")
			}
			if len(sample.Lanes) == 0 {
				t.Error("top track sample has no lanes")
			}
			for _, lane := range sample.Lanes {
				if lane.Empty || lane.IndexCount == 0 {
					t.Errorf("empty track lane: %#v", lane)
				}
			}
		}
		again, err := ProcessStreamData(streamPath)
		if err != nil {
			t.Fatalf("second process for track determinism: %v", err)
		}
		if summary.Tracks.TopDrawCount != again.Tracks.TopDrawCount ||
			summary.Tracks.TopBinaryCount != again.Tracks.TopBinaryCount ||
			summary.Tracks.TopKickCount != again.Tracks.TopKickCount ||
			summary.Tracks.TopRIACount != again.Tracks.TopRIACount ||
			!reflect.DeepEqual(summary.Tracks.DrawSamples, again.Tracks.DrawSamples) ||
			!reflect.DeepEqual(summary.Tracks.KickSamples, again.Tracks.KickSamples) {
			t.Errorf("top track output changed between runs: first=%#v second=%#v", summary.Tracks, again.Tracks)
		}
	}
	if os.Getenv("GPUTRACE_MIO_USC_CLIQUES") == "1" {
		if summary.USC.CoreCount != 40 || summary.USC.TotalCliqueCount == 0 {
			t.Errorf("USC summary = %#v, want 40 populated cores", summary.USC)
		}
		if len(summary.USC.CliqueSamples) == 0 {
			t.Fatal("no USC clique samples")
		}
		again, err := ProcessStreamData(streamPath)
		if err != nil {
			t.Fatalf("second process for USC determinism: %v", err)
		}
		if !reflect.DeepEqual(summary.USC, again.USC) {
			t.Errorf("USC attribution changed between runs: first=%#v second=%#v", summary.USC, again.USC)
		}
	}

	// Every dispatch belongs to exactly one pipeline, so the per-pipeline
	// command counts must account for the whole capture. This is the check
	// that separates a real model from an allocated but empty one.
	if len(summary.Pipelines) == 0 {
		t.Fatal("no pipeline records")
	}
	var commands uint64
	var named int
	for _, p := range summary.Pipelines {
		commands += uint64(p.NumGPUCommands)
		if p.FunctionName != "" {
			named++
		}
	}
	if commands != summary.DrawCount {
		t.Errorf("pipeline commands total = %d, want %d (drawCount)", commands, summary.DrawCount)
	}
	if named == 0 {
		t.Error("no pipeline resolved a Metal function name")
	}
	if summary.GPUTime == 0 {
		t.Error("gpu time = 0")
	}
	if summary.GPUName == "" {
		t.Error("no GPU name reported")
	}
	t.Logf("pipelines=%d named=%d commands=%d gpuTime=%d gpu=%q plugin=%q binaries=%d gpuCommands=%d",
		len(summary.Pipelines), named, commands, summary.GPUTime, summary.GPUName,
		summary.MetalPluginName, summary.ShaderBinaryCount, summary.GPUCommandCount)

	b := summary.Binaries
	if b.Count != summary.ShaderBinaryCount {
		t.Errorf("binary count = %d, want %d", b.Count, summary.ShaderBinaryCount)
	}
	if b.InstructionCount == 0 {
		t.Error("instruction count = 0, want the compiled instruction tables")
	}
	if b.SourceCost.Status == "" || b.SourceCost.Reason == "" {
		t.Errorf("source cost evidence is incomplete: %#v", b.SourceCost)
	}
	if b.SourceCost.Ready {
		t.Error("source cost evidence unexpectedly claims a proven measured join")
	}
	t.Logf("binaries: count=%d instructions=%d executed=%d highRegister=%d debugLocations=%d",
		b.Count, b.InstructionCount, b.InstructionsExecuted, b.HighRegister, b.DebugLocationCount)
	t.Logf("source cost: %#v", b.SourceCost)
	if len(b.DebugLocations) != int(b.DebugLocationCount) {
		t.Errorf("decoded debug locations = %d, want %d", len(b.DebugLocations), b.DebugLocationCount)
	}
	if len(b.DebugLocations) != 0 {
		first := b.DebugLocations[0]
		if b.DebugSelectorFile != first.FilePath || b.DebugSelectorFunction != first.FunctionName {
			t.Errorf("debug location selectors = (%q, %q), want (%q, %q)", b.DebugSelectorFile,
				b.DebugSelectorFunction, first.FilePath, first.FunctionName)
		}
		if b.DebugSelectorString != first.FilePath {
			t.Errorf("debug string selector = %q, want first table value %q", b.DebugSelectorString, first.FilePath)
		}
	}
	for i, location := range b.DebugLocations {
		if location.FilePath == "" || location.FunctionName == "" {
			t.Errorf("debug location %d has empty source mapping", location.BinaryIndex)
		}
		if i < 3 {
			t.Logf("debug location: binary=%d %s:%d:%d %s", location.BinaryIndex, location.FilePath,
				location.Line, location.Column, location.FunctionName)
		}
	}
	for _, p := range summary.Pipelines {
		t.Logf("  objectId=%#x pointerId=%#x fnIndex=%d index=%d commands=%d mcaHighRegister=%d %q",
			p.ObjectID, p.PointerID, p.FunctionIndex, p.Index, p.NumGPUCommands, p.MCAHighRegister, p.FunctionName)
	}
	// MCA registers are read through GTShaderProfilerMCABinaryList, which is
	// keyed by pipeline state ID. On a processor-built model the list is empty,
	// so the only property worth asserting is that the values are stable: the
	// binary-key walk this replaced returned a different number every run.
	if os.Getenv("GPUTRACE_MIO_MCA") != "" {
		again, err := ProcessStreamData(streamPath)
		if err != nil {
			t.Fatalf("second process for MCA determinism: %v", err)
		}
		if len(again.Pipelines) != len(summary.Pipelines) {
			t.Fatalf("pipeline count changed between runs: %d then %d",
				len(summary.Pipelines), len(again.Pipelines))
		}
		for i, p := range summary.Pipelines {
			q := again.Pipelines[i]
			if p.ObjectID != q.ObjectID {
				t.Errorf("pipeline %d: objectId %#x then %#x", i, p.ObjectID, q.ObjectID)
				continue
			}
			if p.MCAHighRegister != q.MCAHighRegister || p.MCAAllocatedGPR != q.MCAAllocatedGPR {
				t.Errorf("pipeline %#x MCA registers not reproducible: high %d then %d, allocated %d then %d",
					p.ObjectID, p.MCAHighRegister, q.MCAHighRegister, p.MCAAllocatedGPR, q.MCAAllocatedGPR)
			}
		}
	}
}

func TestMeasuredCost(t *testing.T) {
	var zero gtshaderprofiler.GTMioCostInfo
	nonzero := zero
	nonzero.Field7[3] = 1
	if measuredCost(nil) || measuredCost(&zero) {
		t.Fatal("empty cost reported as measured")
	}
	if !measuredCost(&nonzero) {
		t.Fatal("nonzero data-master instruction count not reported as measured")
	}
}

func TestSourceCostEvidenceFinish(t *testing.T) {
	tests := []struct {
		name  string
		count uint64
		in    SourceCostEvidence
		want  string
	}{
		{"no table", 0, SourceCostEvidence{}, "no_instruction_table"},
		{"no model", 3, SourceCostEvidence{}, "cost_model_not_built"},
		{"no cost", 3, SourceCostEvidence{CostModelReady: true}, "no_measured_instruction_cost"},
		{"debug ranges", 3, SourceCostEvidence{CostModelReady: true, NonzeroInstructionCostCount: 1}, "incomplete_debug_ranges"},
		{"identity", 3, SourceCostEvidence{CostModelReady: true, NonzeroInstructionCostCount: 1, DebugRangeInstructionCount: 3}, "binary_identity_unproven"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := test.in
			got.finish(test.count)
			if got.Ready || got.Status != test.want || got.Reason == "" {
				t.Fatalf("finish(%d) = %#v, want status %q and a refusal reason", test.count, got, test.want)
			}
		})
	}
}

// checkCommandOwnership verifies the capture-local encoder-to-command ranges
// Xcode's processed model exposes. It deliberately does not treat a command
// buffer index as a timing value: this establishes hierarchy only.
func checkCommandOwnership(t *testing.T, summary ProcessedStreamData, streamPath string) {
	t.Helper()
	if len(summary.Encoders) != int(summary.EncoderCount) {
		t.Fatalf("processed encoders = %d, want %d", len(summary.Encoders), summary.EncoderCount)
	}
	if len(summary.GPUCommands) != int(summary.GPUCommandCount) {
		t.Fatalf("processed GPU commands = %d, want %d", len(summary.GPUCommands), summary.GPUCommandCount)
	}
	covered := make([]bool, len(summary.GPUCommands))
	commandBuffers := make(map[uint32]bool)
	for _, command := range summary.GPUCommands {
		commandBuffers[command.CommandBufferIndex] = true
	}
	for _, encoder := range summary.Encoders {
		start := uint64(encoder.GPUCommandStartIndex)
		end := start + uint64(encoder.NumGPUCommands)
		if end > uint64(len(summary.GPUCommands)) {
			t.Fatalf("encoder %d command range %d..%d exceeds %d commands", encoder.Index, start, end, len(summary.GPUCommands))
		}
		for index := start; index < end; index++ {
			if covered[index] {
				t.Fatalf("GPU command %d belongs to more than one encoder", index)
			}
			covered[index] = true
			command := summary.GPUCommands[index]
			if command.EncoderInfoIndex != encoder.Index {
				t.Fatalf("GPU command %d encoder index = %d, want %d", index, command.EncoderInfoIndex, encoder.Index)
			}
		}
	}
	for index, ok := range covered {
		if !ok {
			t.Fatalf("GPU command %d is not covered by an encoder range", index)
		}
	}
	stats, err := counter.ParseStreamData(filepath.Dir(streamPath), nil)
	if err != nil {
		t.Fatalf("parse streamData command buffers: %v", err)
	}
	if stats.Timeline == nil {
		t.Fatal("streamData has no command-buffer timeline")
	}
	t.Logf("processed command ownership: encoders=%d commands=%d processed_command_buffers=%d archived_command_buffers=%d",
		len(summary.Encoders), len(summary.GPUCommands), len(commandBuffers), len(stats.Timeline.CommandBufferTimestamps))
}

// TestLLVMHelperForFramework checks that the helper is resolved by walking up
// from the framework, which is what keeps the two in the same Xcode install.
func TestLLVMHelperForFramework(t *testing.T) {
	root := t.TempDir()
	developerDir := filepath.Join(root, "Xcode.app", "Contents", "Developer")
	helper := filepath.Join(root, "Xcode.app", "Contents", "Developer", llvmHelperRelPath)
	if err := os.MkdirAll(filepath.Dir(helper), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(helper, nil, 0o755); err != nil {
		t.Fatal(err)
	}

	// Both plugin layouts must find the same helper.
	for _, framework := range []string{
		filepath.Join(root, "Xcode.app", "Contents", "PlugIns", "GPUDebugger.ideplugin",
			"Contents", "Frameworks", "GTShaderProfiler.framework", "Versions", "A", "GTShaderProfiler"),
		frameworkPathForDeveloperDir(developerDir),
	} {
		got, ok := llvmHelperForFramework(framework)
		if !ok {
			t.Errorf("no helper found for %s", framework)
			continue
		}
		if got != helper {
			t.Errorf("helper = %q, want %q", got, helper)
		}
	}

	if _, ok := llvmHelperForFramework(filepath.Join(t.TempDir(), "GTShaderProfiler")); ok {
		t.Error("found a helper for a framework with no Xcode alongside it")
	}
}

func TestWithProcessedModelContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := WithProcessedModel(ctx, "nonexistent", func(m *ProcessedStreamData) error {
		t.Fatal("callback should not run when context is canceled")
		return nil
	})
	if err == nil {
		t.Fatal("expected error for canceled context, got nil")
	}
}

func TestMioDataPathRequired(t *testing.T) {
	names := []string{
		"GPUTRACE_MIO_SETUP_DATA_PATH",
		"GPUTRACE_MIO_TIMELINE_DATA",
		"GPUTRACE_MIO_TRACE_TRACKS",
		"GPUTRACE_MIO_USC_CLIQUES",
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			for _, other := range names {
				t.Setenv(other, "")
			}
			t.Setenv(name, "1")
			if !mioDataPathRequired() {
				t.Fatal("mioDataPathRequired = false, want true")
			}
		})
	}
}
