//go:build darwin && gputrace_private_bindings

package shader

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/gputrace/internal/testtrace"

	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/objectivec"
	"github.com/tmc/apple/private/xcode/gtshaderprofiler"
	"github.com/tmc/gputrace/internal/counter"
	"github.com/tmc/gputrace/internal/xcodebindings"
)

// TestApplyPipelineShaderMetricsFromPerfFixture is opt-in because the real
// fixture is several gigabytes and is not part of the repository.
func TestApplyPipelineShaderMetricsFromPerfFixture(t *testing.T) {
	fixture := testtrace.Path("GPUTRACE_PERF_FIXTURE", testtrace.Bundle)
	if fixture == "" {
		t.Skip("set GPUTRACE_TEST_TRACE or GPUTRACE_PERF_FIXTURE to a .gputrace bundle")
	}
	profilerDir := fixture
	entries, err := os.ReadDir(fixture)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasSuffix(entry.Name(), ".gpuprofiler_raw") {
			profilerDir = filepath.Join(fixture, entry.Name())
			break
		}
	}
	stats, err := counter.ParseStreamData(profilerDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	metrics := make(map[int]*ShaderMetrics, len(stats.Pipelines))
	for i := range stats.Pipelines {
		pipeline := &stats.Pipelines[i]
		if pipeline.FunctionName != "" {
			metrics[pipeline.PipelineID] = &ShaderMetrics{Name: pipeline.FunctionName}
		}
	}
	streamPath := filepath.Join(profilerDir, "streamData")
	if err := ApplyPipelineShaderMetricsFromStreamData(metrics, streamPath); err != nil {
		t.Skipf("source-backed binary enumeration is unavailable: %v", err)
	}
	for pipelineID, metric := range metrics {
		if metric.HighRegister > 0 {
			t.Logf("pipeline %d function %q high_register=%d", pipelineID, metric.Name, metric.HighRegister)
			return
		}
	}
	t.Skip("streamData fixture exposed no source-backed high-register values")
}

func TestProbeGTMioTraceDataChildFromPipelineInfo(t *testing.T) {
	fixture := testtrace.Path("GPUTRACE_PERF_FIXTURE", testtrace.Bundle)
	if fixture == "" {
		t.Skip("set GPUTRACE_TEST_TRACE or GPUTRACE_PERF_FIXTURE to a .gputrace bundle")
	}
	profilerDir := fixture
	entries, err := os.ReadDir(fixture)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasSuffix(entry.Name(), ".gpuprofiler_raw") {
			profilerDir = filepath.Join(fixture, entry.Name())
			break
		}
	}
	streamPath := filepath.Join(profilerDir, "streamData")
	err = xcodebindings.WithStreamData(streamPath, func(parent objc.ID) error {
		data := objc.Send[objc.ID](parent, objc.Sel("pipelineStateInfoData"))
		if data == 0 {
			t.Log("pipelineStateInfoData is nil")
			return nil
		}
		child, childErr := gtshaderprofiler.GetGTMioTraceDataClass().TraceDataFromDataError(foundation.NSDataFromID(data))
		if childErr != nil {
			t.Logf("pipelineStateInfoData is not GTMioTraceData: %v", childErr)
			return nil
		}
		if child == nil || child.GetID() == 0 {
			t.Log("pipelineStateInfoData produced no GTMioTraceData")
			return nil
		}
		t.Logf("pipelineStateInfoData produced GTMioTraceData; enumerate=%t", objc.RespondsToSelector(child.GetID(), objc.Sel("enumerateBinariesForPipelineState:enumerator:")))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestShaderProfilerStreamDataMethodEncodings(t *testing.T) {
	class := objc.GetClass("GTMutableShaderProfilerStreamData")
	if class == 0 {
		t.Skip("GTMutableShaderProfilerStreamData is unavailable")
	}
	pipelineStatesEncoding := ""
	enumeratePresent := false
	for _, selector := range []string{"pipelineStates", "enumerateBinariesForPipelineState:enumerator:", "pipelineStateInfoData"} {
		method := objectivec.Class_getInstanceMethod(class, objectivec.SEL(objc.Sel(selector)))
		if method == 0 {
			t.Logf("selector %s is absent", selector)
			continue
		}
		encoding := objc.GoString(objectivec.Method_getTypeEncoding(method))
		t.Logf("selector %s encoding %q", selector, encoding)
		if selector == "pipelineStates" {
			pipelineStatesEncoding = encoding
		}
		if selector == "enumerateBinariesForPipelineState:enumerator:" {
			enumeratePresent = true
		}
	}
	if enumeratePresent {
		t.Fatal("GTMutableShaderProfilerStreamData unexpectedly exposes binary enumeration")
	}
	if !strings.HasPrefix(pipelineStatesEncoding, "r^{?=") {
		t.Fatalf("pipelineStates encoding = %q, want a pointer-to-struct return", pipelineStatesEncoding)
	}
}
