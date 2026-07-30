//go:build darwin

package xcodebindings

import (
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestProcessStreamData builds Xcode's shader trace model from a real profiler
// archive. It is opt-in: the run spawns GTLLVMHelper and disassembles every
// shader in the capture, which takes far longer than an ordinary unit test, and
// no repository fixture carries streamData.
func TestProcessStreamData(t *testing.T) {
	streamPath := os.Getenv("GPUTRACE_PROCESS_STREAMDATA")
	if streamPath == "" {
		t.Skip("set GPUTRACE_PROCESS_STREAMDATA to a profiler streamData archive")
	}
	streamPath, err := filepath.Abs(streamPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(streamPath); err != nil {
		t.Skipf("streamData unavailable: %v", err)
	}

	summary, err := ProcessStreamData(streamPath)
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
	t.Logf("draws=%d encoders=%d costs=%d helper=%s",
		summary.DrawCount, summary.EncoderCount, summary.CostCount, summary.LLVMHelperPath)
	if os.Getenv("GPUTRACE_MIO_SETUP_DATA_PATH") == "1" {
		if !summary.CostModel.Ready {
			t.Fatal("data-path setup did not populate scalar cost totals")
		}
		if summary.CostCount != 606 {
			t.Errorf("cost count = %d, want 606 for the checked-in external fixture", summary.CostCount)
		}
		if math.Abs(summary.CostModel.Scope0DataMaster2-100) > 1e-9 || math.Abs(summary.CostModel.Scope4DataMaster2-0.396351) > 1e-6 {
			t.Errorf("cost scope totals = %#v, want scope0=100 scope4=0.396351", summary.CostModel)
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
		var pipelineGPUTime uint64
		for _, pipeline := range timeline.PipelineDraws {
			pipelineDraws += pipeline.DrawCount
			pipelineGPUTime += pipeline.GPUTimeDataMaster2
		}
		if pipelineDraws != timeline.DrawCount {
			t.Errorf("timeline pipeline draw total = %d, want %d", pipelineDraws, timeline.DrawCount)
		}
		if len(timeline.EncoderDurations) != int(timeline.EncoderCount) || len(timeline.DrawDurationsDataMaster2) != 3 {
			t.Errorf("timeline attribution lengths = encoders %d/%d draws %d/3", len(timeline.EncoderDurations), timeline.EncoderCount, len(timeline.DrawDurationsDataMaster2))
		}
		if os.Getenv("GPUTRACE_MIO_SETUP_DATA_PATH") == "1" && len(timeline.DrawDurationsDataMaster2) > 0 && timeline.DrawDurationsDataMaster2[0] == 0 {
			t.Errorf("setup-backed timeline draw duration is zero: %#v", timeline.DrawDurationsDataMaster2)
		}
		if os.Getenv("GPUTRACE_MIO_SETUP_DATA_PATH") == "1" && pipelineGPUTime == 0 {
			t.Errorf("setup-backed pipeline GPU time is zero: %#v", timeline.PipelineDraws)
		}
		t.Logf("timeline: draws=%d encoders=%d costs=%d pipelines=%d scope0=%.6g scope4=%.6g pipelineDraws=%#v encoders=%#v drawDurations=%#v", timeline.DrawCount, timeline.EncoderCount, timeline.CostCount, timeline.PipelineStateCount, timeline.Scope0DataMaster2, timeline.Scope4DataMaster2, timeline.PipelineDraws, timeline.EncoderDurations, timeline.DrawDurationsDataMaster2)
	}
	if os.Getenv("GPUTRACE_MIO_TRACE_TRACKS") == "1" {
		if summary.Tracks.TopDrawCount != summary.DrawCount {
			t.Errorf("top draw tracks = %d, want draw count %d", summary.Tracks.TopDrawCount, summary.DrawCount)
		}
		if summary.Tracks.TopBinaryCount != 592 || summary.Tracks.TopKickCount != 3 || summary.Tracks.TopRIACount != 0 {
			t.Errorf("top track counts = %#v, want binary=592 kick=3 ria=0", summary.Tracks)
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
	// Live register counts come from the instruction tables, so they are
	// available whenever those are, unlike the execution counters.
	if b.HighRegister <= 0 {
		t.Errorf("high register = %d, want a positive live-register count", b.HighRegister)
	}
	t.Logf("binaries: count=%d instructions=%d executed=%d highRegister=%d debugLocations=%d",
		b.Count, b.InstructionCount, b.InstructionsExecuted, b.HighRegister, b.DebugLocationCount)
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
