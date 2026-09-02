package mlxprof

import (
	"testing"

	"github.com/google/pprof/profile"
	"github.com/tmc/gputrace"
)

func TestSelectGPUTraceTimingsReportsUnavailableWithoutRealTiming(t *testing.T) {
	trace := &gputrace.Trace{
		Path:        t.TempDir(),
		KernelNames: []string{"mlx_matmul_kernel"},
	}

	// Kernel names used to be turned into durations from a lookup table. They
	// yield no timings now -- but the absence of timing is not a reason to
	// refuse the profile, because most of its value types are compiler
	// statistics that never depended on a duration.
	got, err := selectGPUTraceTimings(trace)
	if err != nil {
		t.Fatalf("selectGPUTraceTimings failed: %v", err)
	}
	if got.source != TimingSourceUnavailable {
		t.Fatalf("source = %q, want %q", got.source, TimingSourceUnavailable)
	}
	if len(got.timings) != 0 {
		t.Fatalf("timings = %d, want none invented", len(got.timings))
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
