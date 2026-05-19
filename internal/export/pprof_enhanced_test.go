package export

import (
	"strings"
	"testing"

	"github.com/google/pprof/profile"
	"github.com/tmc/gputrace/internal/counter"
	"github.com/tmc/gputrace/internal/trace"
)

func TestApplyStreamTimingMetadata(t *testing.T) {
	stats := &counter.StreamDataStats{
		TotalEncoderTimeUs:    9876,
		TotalDispatchTimeUs:   10117,
		CommandBufferActiveNs: 2246081,
		CommandBufferWallNs:   356626625,
		TimingSource:          "APSTimelineData Command Buffer Timestamps active time",
	}
	prof := &profile.Profile{}

	applyStreamTimingMetadata(prof, stats)

	if prof.DefaultSampleType != "time" {
		t.Fatalf("DefaultSampleType = %q, want time", prof.DefaultSampleType)
	}
	if got, want := prof.DurationNanos, int64(10117000); got != want {
		t.Fatalf("DurationNanos = %d, want %d", got, want)
	}
	comments := strings.Join(prof.Comments, "\n")
	for _, want := range []string{
		"gputrace timing_source: APSTimelineData Command Buffer Timestamps active time",
		"gputrace display_duration_ns: 2246081",
		"gputrace display_duration_source: APSTimelineData command buffer active time",
		"gputrace command_buffer_active_time_ns: 2246081",
		"gputrace dispatch_span_ns: 10117000",
	} {
		if !strings.Contains(comments, want) {
			t.Fatalf("comments missing %q in:\n%s", want, comments)
		}
	}
}

func TestAddStreamTimingLabels(t *testing.T) {
	stats := &counter.StreamDataStats{
		CommandBufferActiveNs: 2246081,
		TimingSource:          "APSTimelineData Command Buffer Timestamps active time",
	}
	labels := map[string][]string{}

	addStreamTimingLabels(labels, stats)

	if got := labels["timing_source"]; len(got) != 1 || got[0] != stats.TimingSource {
		t.Fatalf("timing_source label = %#v", got)
	}
	if got := labels["display_duration_source"]; len(got) != 1 || got[0] != "APSTimelineData command buffer active time" {
		t.Fatalf("display_duration_source label = %#v", got)
	}
}

func TestDispatchSIMDGroups(t *testing.T) {
	dispatch := trace.DispatchThreads{
		ThreadsX:         1000,
		ThreadsY:         1,
		ThreadsZ:         1,
		ThreadsPerGroupX: 256,
		ThreadsPerGroupY: 1,
		ThreadsPerGroupZ: 1,
	}
	if got, want := dispatchSIMDGroups(dispatch), int64(32); got != want {
		t.Fatalf("dispatchSIMDGroups = %d, want %d", got, want)
	}

	dispatch.ThreadsPerGroupX = 0
	if got := dispatchSIMDGroups(dispatch); got != 0 {
		t.Fatalf("dispatchSIMDGroups with missing group size = %d, want 0", got)
	}
}

func TestDispatchExecutionCostValuesDistributesPipelineCost(t *testing.T) {
	stats := &counter.StreamDataStats{
		Dispatches: []counter.DispatchInfo{
			{PipelineID: 7, ExecutionCostPct: 66.42},
			{PipelineID: 7, ExecutionCostPct: 66.42},
			{PipelineID: 7, ExecutionCostPct: 66.42},
			{PipelineID: 8, ExecutionCostPct: 7.38},
		},
	}

	costs := &counter.ExecutionCostMetrics{
		TotalSamples:       271,
		SamplesPerPipeline: map[int]int{7: 180, 8: 20, 9: 71},
	}
	values := dispatchExecutionCostValues(stats, costs)
	if got, want := len(values), 4; got != want {
		t.Fatalf("len(values) = %d, want %d", got, want)
	}
	sum7 := values[0] + values[1] + values[2]
	if got, want := sum7, int64(6642); got != want {
		t.Fatalf("pipeline 7 total = %d, want %d", got, want)
	}
	if got, want := values[3], int64(738); got != want {
		t.Fatalf("pipeline 8 value = %d, want %d", got, want)
	}
}

func TestExecutionCostBasisPointsSumsTo10000(t *testing.T) {
	costs := &counter.ExecutionCostMetrics{
		TotalSamples:       271,
		SamplesPerPipeline: map[int]int{1: 180, 2: 41, 3: 20, 4: 15, 5: 10, 6: 3, 7: 2},
	}

	values := executionCostBasisPoints(costs)
	var sum int64
	for _, v := range values {
		sum += v
	}
	if got, want := sum, int64(10000); got != want {
		t.Fatalf("basis point sum = %d, want %d", got, want)
	}
}

func TestProfileBasisPoints(t *testing.T) {
	tests := []struct {
		name string
		in   float64
		want int64
	}{
		{"zero", 0, 0},
		{"fractional percent", 0.805, 81},
		{"whole percent", 80.5, 8050},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := profileBasisPoints(tt.in); got != tt.want {
				t.Fatalf("profileBasisPoints(%v) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestPprofValueIndexes(t *testing.T) {
	if pprofValueCount <= pprofUniformRegsIdx {
		t.Fatalf("pprofValueCount = %d, uniform index = %d", pprofValueCount, pprofUniformRegsIdx)
	}
	if pprofExecutionCostIdx != 34 || pprofProfilerCountIdx != 35 || pprofUniformRegsIdx != 36 {
		t.Fatalf("pprof indexes changed: execution=%d profiler=%d uniform=%d", pprofExecutionCostIdx, pprofProfilerCountIdx, pprofUniformRegsIdx)
	}
}

func TestApplyEncoderCounterMetricsIncludesBytesAndBandwidth(t *testing.T) {
	values := make([]int64, pprofValueCount)
	values[19] = 7
	applyEncoderCounterMetrics(values, &counter.EncoderCounterMetrics{
		ALUUtilization:                 1.25,
		KernelOccupancy:                0.81,
		BytesReadFromDeviceMemory:      100,
		BytesWrittenToDeviceMemory:     200,
		BufferDeviceMemoryBytesRead:    300,
		BufferDeviceMemoryBytesWritten: 400,
		DeviceMemoryBandwidthGBps:      1.5,
		BufferL1ReadBandwidth:          2.5,
		BufferL1WriteBandwidth:         3.5,
	})
	if got := values[7]; got != 125 {
		t.Fatalf("alu_util = %d, want 125", got)
	}
	if got := values[8]; got != 81 {
		t.Fatalf("occupancy = %d, want 81", got)
	}
	if got := values[19]; got != 7 {
		t.Fatalf("read_bytes overwritten with %d, want 7", got)
	}
	if got := values[20]; got != 200 {
		t.Fatalf("write_bytes = %d, want 200", got)
	}
	if got := values[21]; got != 300 {
		t.Fatalf("buffer_read_bytes = %d, want 300", got)
	}
	if got := values[22]; got != 400 {
		t.Fatalf("buffer_write_bytes = %d, want 400", got)
	}
	if got := values[23]; got != 1500 {
		t.Fatalf("device_bandwidth = %d, want 1500", got)
	}
	if got := values[24]; got != 2500 {
		t.Fatalf("buffer_l1_read_bw = %d, want 2500", got)
	}
	if got := values[25]; got != 3500 {
		t.Fatalf("buffer_l1_write_bw = %d, want 3500", got)
	}
}

func TestPprofSampleTotals(t *testing.T) {
	prof := &profile.Profile{
		SampleType: []*profile.ValueType{
			{Type: "simd_groups", Unit: "count"},
			{Type: "high_reg", Unit: "count"},
			{Type: "read_bytes", Unit: "bytes"},
		},
		Sample: []*profile.Sample{
			{Value: []int64{32, 0, 100}},
			{Value: []int64{16, 0, 200}},
		},
	}
	totals := pprofSampleTotals(prof)
	if got, want := totals["simd_groups"], int64(48); got != want {
		t.Fatalf("simd_groups total = %d, want %d", got, want)
	}
	if got := totals["high_reg"]; got != 0 {
		t.Fatalf("high_reg total = %d, want 0", got)
	}
	appendXcodeMetricCoverageComments(prof)
	comments := strings.Join(prof.Comments, "\n")
	if !strings.Contains(comments, "gputrace xcode_metric_total simd_groups: 48") {
		t.Fatalf("comments missing simd total: %s", comments)
	}
	if !strings.Contains(comments, "gputrace xcode_metric_total high_reg: 0") {
		t.Fatalf("comments missing high_reg total: %s", comments)
	}
	if !strings.Contains(comments, "gputrace xcode_metric_binding_candidate high_reg: GTMioShaderBinaryData.LiveRegisterForInstructionAtIndex") {
		t.Fatalf("comments missing high_reg binding candidate: %s", comments)
	}
	if !strings.Contains(comments, "gputrace xcode_metric_total read_bytes: 300") {
		t.Fatalf("comments missing read_bytes total: %s", comments)
	}
}
