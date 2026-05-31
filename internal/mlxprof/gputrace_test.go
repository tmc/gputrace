package mlxprof

import (
	"bytes"
	"strings"
	"testing"

	"github.com/google/pprof/profile"
	"github.com/tmc/gputrace"
)

func TestSelectGPUTraceTimingsSyntheticFallbackIsVisible(t *testing.T) {
	trace := &gputrace.Trace{
		Path:        t.TempDir(),
		KernelNames: []string{"mlx_matmul_kernel"},
	}

	selection, err := selectGPUTraceTimings(trace)
	if err != nil {
		t.Fatalf("selectGPUTraceTimings failed: %v", err)
	}
	if selection.source != TimingSourceSynthetic {
		t.Fatalf("source = %q, want %q", selection.source, TimingSourceSynthetic)
	}
	if !selection.approximate {
		t.Fatal("synthetic timing selection should be approximate")
	}
	if len(selection.timings) != 1 {
		t.Fatalf("got %d timings, want 1", len(selection.timings))
	}
	if selection.timings[0].Label != "mlx_matmul_kernel" {
		t.Fatalf("timing label = %q, want mlx_matmul_kernel", selection.timings[0].Label)
	}

	profiler := &GPUTraceProfiler{
		trace:             trace,
		timings:           selection.timings,
		timingSource:      selection.source,
		timingApproximate: selection.approximate,
	}
	if got := profiler.TimingSource(); got != TimingSourceSynthetic {
		t.Fatalf("TimingSource() = %q, want %q", got, TimingSourceSynthetic)
	}
	if !profiler.TimingsAreApproximate() {
		t.Fatal("TimingsAreApproximate() = false, want true")
	}

	var summary bytes.Buffer
	profiler.writeTimingSummary(&summary)
	if got := summary.String(); !strings.Contains(got, "Timing Source: synthetic (approximate)") {
		t.Fatalf("summary missing synthetic timing source:\n%s", got)
	}

	pprof := &profile.Profile{}
	profiler.addProfileTimingComments(pprof)
	if !hasProfileComment(pprof, "gputrace timing_source: synthetic") {
		t.Fatalf("profile comments missing synthetic source: %#v", pprof.Comments)
	}
	if !hasProfileComment(pprof, "gputrace timing_approximate: true") {
		t.Fatalf("profile comments missing approximate flag: %#v", pprof.Comments)
	}
}

func TestBuildCombinedProfileUsesBasisPointUtilization(t *testing.T) {
	profiler := &GPUTraceProfiler{
		trace: &gputrace.Trace{
			CommandQueueLabel: "queue",
		},
		timings: []*gputrace.EncoderTiming{
			{
				Label:      "encoder",
				DurationNs: 1000,
				Percentage: 12.5,
			},
		},
	}

	prof, err := profiler.buildCombinedProfile()
	if err != nil {
		t.Fatalf("buildCombinedProfile failed: %v", err)
	}
	if len(prof.SampleType) != 2 {
		t.Fatalf("got %d sample types, want 2", len(prof.SampleType))
	}
	if got := prof.SampleType[1].Type; got != "gpu_utilization" {
		t.Fatalf("sample type = %q, want gpu_utilization", got)
	}
	if got := prof.SampleType[1].Unit; got != "basis_points" {
		t.Fatalf("gpu_utilization unit = %q, want basis_points", got)
	}
	if len(prof.Sample) != 1 {
		t.Fatalf("got %d samples, want 1", len(prof.Sample))
	}
	if got := prof.Sample[0].Value[1]; got != 1250 {
		t.Fatalf("gpu_utilization value = %d, want 1250", got)
	}
}

func hasProfileComment(prof *profile.Profile, want string) bool {
	for _, comment := range prof.Comments {
		if comment == want {
			return true
		}
	}
	return false
}
