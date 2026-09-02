package export

import (
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/google/pprof/profile"

	"github.com/tmc/gputrace/internal/counter"
	"github.com/tmc/gputrace/internal/profilerraw"
	"github.com/tmc/gputrace/internal/timing"
	"github.com/tmc/gputrace/internal/trace"
)

// Type aliases
var NewTimingMetricsExtractor = timing.NewTimingMetricsExtractor

func applyStreamTimingMetadata(prof *profile.Profile, stats *counter.StreamDataStats) {
	if prof == nil || stats == nil {
		return
	}
	prof.DefaultSampleType = "time"
	if stats.TotalDispatchTimeUs > 0 {
		prof.DurationNanos = int64(stats.TotalDispatchTimeUs) * 1000
	} else if stats.TotalEncoderTimeUs > 0 {
		prof.DurationNanos = int64(stats.TotalEncoderTimeUs) * 1000
	}
	if prof.TimeNanos == 0 {
		prof.TimeNanos = time.Now().UnixNano()
	}

	displayNs, displaySource := streamDisplayDuration(stats)
	if stats.TimingSource != "" {
		prof.Comments = append(prof.Comments, "gputrace timing_source: "+stats.TimingSource)
	}
	if displayNs > 0 {
		prof.Comments = append(prof.Comments, fmt.Sprintf("gputrace display_duration_ns: %d", displayNs))
	}
	if displaySource != "" {
		prof.Comments = append(prof.Comments, "gputrace display_duration_source: "+displaySource)
	}
	if stats.EffectiveGPUTimeNs != nil {
		prof.Comments = append(prof.Comments, fmt.Sprintf("gputrace effective_gpu_time_ns: %d", *stats.EffectiveGPUTimeNs))
	}
	if stats.CommandBufferActiveNs > 0 {
		prof.Comments = append(prof.Comments, fmt.Sprintf("gputrace command_buffer_active_time_ns: %d", stats.CommandBufferActiveNs))
	}
	if stats.CommandBufferWallNs > 0 {
		prof.Comments = append(prof.Comments, fmt.Sprintf("gputrace command_buffer_wall_time_ns: %d", stats.CommandBufferWallNs))
	}
	if stats.TotalEncoderTimeUs > 0 {
		prof.Comments = append(prof.Comments, fmt.Sprintf("gputrace encoder_span_ns: %d", int64(stats.TotalEncoderTimeUs)*1000))
	}
	if stats.TotalDispatchTimeUs > 0 {
		prof.Comments = append(prof.Comments, fmt.Sprintf("gputrace dispatch_span_ns: %d", int64(stats.TotalDispatchTimeUs)*1000))
	}
}

func addStreamTimingLabels(labels map[string][]string, stats *counter.StreamDataStats) {
	if labels == nil || stats == nil {
		return
	}
	if stats.TimingSource != "" {
		labels["timing_source"] = []string{stats.TimingSource}
	}
	if _, displaySource := streamDisplayDuration(stats); displaySource != "" {
		labels["display_duration_source"] = []string{displaySource}
	}
}

func streamDisplayDuration(stats *counter.StreamDataStats) (uint64, string) {
	if stats == nil {
		return 0, ""
	}
	switch {
	case stats.EffectiveGPUTimeNs != nil:
		return *stats.EffectiveGPUTimeNs, "APSTimelineData ReplayerGPUTime"
	case stats.CommandBufferActiveNs > 0:
		return stats.CommandBufferActiveNs, "APSTimelineData command buffer active time"
	case stats.TotalEncoderTimeUs > 0:
		return uint64(stats.TotalEncoderTimeUs) * 1000, "encoderInfoData cumulative encoder span"
	default:
		return 0, ""
	}
}

func profileBasisPoints(v float64) int64 {
	if v <= 0 {
		return 0
	}
	return int64(math.Round(v * 100))
}

const (
	pprofValueCount          = 37
	pprofExecutionCostIdx    = 33
	pprofProfilerCountIdx    = 34
	pprofUniformRegsIdx      = 35
	pprofUnattributedRowsIdx = 36
)

func applyEncoderCounterMetrics(values []int64, m *counter.EncoderCounterMetrics) {
	if len(values) < pprofValueCount || m == nil {
		return
	}
	if values[7] == 0 {
		values[7] = profileBasisPoints(m.ALUUtilization)
	}
	if values[8] == 0 {
		if m.ComputeShaderUtilization > 0 {
			values[8] = profileBasisPoints(m.ComputeShaderUtilization)
		} else {
			values[8] = profileBasisPoints(m.ComputeUtilization)
		}
	}
	if values[9] == 0 {
		values[9] = profileBasisPoints(m.FragmentShaderUtilization)
	}
	if values[10] == 0 {
		values[10] = profileBasisPoints(m.VertexShaderUtilization)
	}
	if values[11] == 0 {
		values[11] = profileBasisPoints(m.F32Utilization)
	}
	if values[12] == 0 {
		values[12] = profileBasisPoints(m.F32Limiter)
	}
	if values[13] == 0 {
		values[13] = profileBasisPoints(m.L1CacheLimiter)
	}
	if values[14] == 0 {
		values[14] = profileBasisPoints(m.LastLevelCacheLimiter)
	}
	if values[15] == 0 {
		values[15] = profileBasisPoints(m.ControlFlowLimiter)
	}
	if values[16] == 0 {
		values[16] = profileBasisPoints(m.BufferL1MissRate)
	}
	if values[17] == 0 {
		values[17] = profileBasisPoints(m.InstructionThroughputLimiter)
	}
	if values[18] == 0 {
		values[18] = int64(m.BytesReadFromDeviceMemory)
	}
	if values[19] == 0 {
		values[19] = int64(m.BytesWrittenToDeviceMemory)
	}
	if values[20] == 0 {
		values[20] = int64(m.BufferDeviceMemoryBytesRead)
	}
	if values[21] == 0 {
		values[21] = int64(m.BufferDeviceMemoryBytesWritten)
	}
	if values[22] == 0 {
		values[22] = int64(m.DeviceMemoryBandwidthGBps * 1000)
	}
	if values[23] == 0 {
		values[23] = int64(m.BufferL1ReadBandwidth * 1000)
	}
	if values[24] == 0 {
		values[24] = int64(m.BufferL1WriteBandwidth * 1000)
	}
}

func appendUnattributedCounterSamples(prof *profile.Profile, metrics []counter.EncoderCounterMetrics, nextID uint64, parents ...*profile.Location) uint64 {
	if prof == nil || len(metrics) == 0 {
		return nextID
	}
	groupFn := &profile.Function{
		ID:         nextID,
		Name:       "Unattributed counter metrics",
		SystemName: "unattributed_counter_metrics",
		Filename:   "gpu_counter",
	}
	nextID++
	groupLoc := &profile.Location{ID: nextID, Line: []profile.Line{{Function: groupFn}}}
	nextID++
	prof.Function = append(prof.Function, groupFn)
	prof.Location = append(prof.Location, groupLoc)

	for _, metric := range metrics {
		label := metric.EncoderLabel
		if label == "" {
			label = "(pipeline unknown)"
		}
		rowFn := &profile.Function{
			ID:         nextID,
			Name:       "[unattributed] " + label,
			SystemName: "unattributed_pipeline_counter",
			Filename:   "gpu_counter",
		}
		nextID++
		rowLoc := &profile.Location{ID: nextID, Line: []profile.Line{{Function: rowFn}}}
		nextID++
		prof.Function = append(prof.Function, rowFn)
		prof.Location = append(prof.Location, rowLoc)

		values := make([]int64, pprofValueCount)
		applyEncoderCounterMetrics(values, &metric)
		values[pprofUnattributedRowsIdx] = 1
		locations := []*profile.Location{rowLoc, groupLoc}
		locations = append(locations, parents...)
		prof.Sample = append(prof.Sample, &profile.Sample{
			Location: locations,
			Value:    values,
			Label: map[string][]string{
				"attribution":    {string(counter.CounterAttributionUnknown)},
				"counter_source": {"PerfCounterStats pipeline row"},
				"metric_scope":   {"pipeline"},
				"pipeline_label": {label},
			},
		})
	}
	return nextID
}

func splitEncoderCounterMetrics(metrics []counter.EncoderCounterMetrics) (map[int]*counter.EncoderCounterMetrics, []counter.EncoderCounterMetrics) {
	attributed := make(map[int]*counter.EncoderCounterMetrics)
	var unattributed []counter.EncoderCounterMetrics
	for i := range metrics {
		metric := &metrics[i]
		if metric.Attribution == counter.CounterAttributionEncoder && metric.EncoderIndex >= 0 {
			attributed[metric.EncoderIndex] = metric
			continue
		}
		unattributed = append(unattributed, *metric)
	}
	return attributed, unattributed
}

func dispatchSIMDGroupsByIndex(t *trace.Trace, stats *counter.StreamDataStats) []int64 {
	if t == nil || stats == nil || len(stats.Dispatches) == 0 || len(t.CaptureData) == 0 {
		return nil
	}
	dispatches := t.ParseDispatchInRegion(t.CaptureData, 0)
	if len(dispatches) != len(stats.Dispatches) {
		return nil
	}
	groups := make([]int64, len(dispatches))
	for i, d := range dispatches {
		groups[i] = int64(d.SIMDGroups())
	}
	return groups
}

func findTraceProfilerDir(tracePath string) string {
	return profilerraw.FindDir(tracePath)
}

func applyProfilingExecutionCosts(stats *counter.StreamDataStats, tracePath string) *counter.ExecutionCostMetrics {
	if stats == nil || len(stats.Pipelines) == 0 {
		return nil
	}
	profilerDir := findTraceProfilerDir(tracePath)
	if profilerDir == "" {
		return nil
	}
	pipelineIDs := make([]int, 0, len(stats.Pipelines))
	for _, p := range stats.Pipelines {
		pipelineIDs = append(pipelineIDs, p.PipelineID)
	}
	costs, err := counter.ParseExecutionCost(profilerDir, pipelineIDs)
	if err != nil {
		return nil
	}
	for i := range stats.Dispatches {
		if cost, ok := costs.PipelineCosts[stats.Dispatches[i].PipelineID]; ok {
			stats.Dispatches[i].ExecutionCostPct = cost
		}
	}
	return costs
}

func executionCostBasisPoints(costs *counter.ExecutionCostMetrics) map[int]int64 {
	if costs == nil || costs.TotalSamples == 0 {
		return nil
	}
	type pipelineCost struct {
		pipelineID int
		value      int64
		remainder  int64
	}
	pipelines := make([]pipelineCost, 0, len(costs.SamplesPerPipeline))
	var sum int64
	for pipelineID, samples := range costs.SamplesPerPipeline {
		numer := int64(samples) * 10000
		value := numer / int64(costs.TotalSamples)
		pipelines = append(pipelines, pipelineCost{
			pipelineID: pipelineID,
			value:      value,
			remainder:  numer % int64(costs.TotalSamples),
		})
		sum += value
	}
	sort.Slice(pipelines, func(i, j int) bool {
		if pipelines[i].remainder != pipelines[j].remainder {
			return pipelines[i].remainder > pipelines[j].remainder
		}
		return pipelines[i].pipelineID < pipelines[j].pipelineID
	})
	for i := int64(0); i < 10000-sum && int(i) < len(pipelines); i++ {
		pipelines[i].value++
	}
	out := make(map[int]int64, len(pipelines))
	for _, p := range pipelines {
		out[p.pipelineID] = p.value
	}
	return out
}

func dispatchExecutionCostValues(stats *counter.StreamDataStats, costs *counter.ExecutionCostMetrics) []int64 {
	if stats == nil || len(stats.Dispatches) == 0 {
		return nil
	}
	values := make([]int64, len(stats.Dispatches))
	counts := make(map[int]int)
	totals := executionCostBasisPoints(costs)
	if totals == nil {
		totals = make(map[int]int64)
		for _, d := range stats.Dispatches {
			if d.ExecutionCostPct <= 0 {
				continue
			}
			totals[d.PipelineID] = int64(math.Round(d.ExecutionCostPct * 100))
		}
	}
	for _, d := range stats.Dispatches {
		if totals[d.PipelineID] > 0 {
			counts[d.PipelineID]++
		}
	}
	seen := make(map[int]int64)
	for i, d := range stats.Dispatches {
		count := counts[d.PipelineID]
		total := totals[d.PipelineID]
		if count == 0 || total == 0 {
			continue
		}
		base := total / int64(count)
		rem := total % int64(count)
		n := seen[d.PipelineID]
		values[i] = base
		if n < rem {
			values[i]++
		}
		seen[d.PipelineID] = n + 1
	}
	return values
}

func appendXcodeMetricCoverageComments(prof *profile.Profile) {
	totals := pprofSampleTotals(prof)
	if len(totals) == 0 {
		return
	}
	counterSource := pprofHasLabelValue(prof, "counter_source", "Counters_f_*.raw/Profiling_f_*.raw")
	for _, name := range []string{
		"simd_groups",
		"execution_cost",
		"alu_util",
		"alloc_regs",
		"uniform_regs",
		"high_reg",
		"spilled_bytes",
		"read_bytes",
		"write_bytes",
		"buffer_read_bytes",
		"buffer_write_bytes",
		"device_bandwidth",
		"instructions",
		"profiler_samples",
		"unattributed_counter_rows",
	} {
		prof.Comments = append(prof.Comments, fmt.Sprintf("gputrace xcode_metric_total %s: %d", name, totals[name]))
	}
	if counterSource {
		prof.Comments = append(prof.Comments, "gputrace xcode_metric_source alu_util: Counters_f_*.raw/Profiling_f_*.raw")
	}
	for _, gap := range []struct {
		name    string
		binding string
	}{
		{"high_reg", "GTMioShaderBinaryData.LiveRegisterForInstructionAtIndex"},
		{"alu_util", "XRGPUAPSDataProcessor derived counters"},
	} {
		if counterSource && gap.name == "alu_util" {
			continue
		}
		if totals[gap.name] == 0 {
			prof.Comments = append(prof.Comments,
				fmt.Sprintf("gputrace xcode_metric_binding_candidate %s: %s", gap.name, gap.binding))
		}
	}
}

func pprofHasLabelValue(prof *profile.Profile, key, value string) bool {
	if prof == nil {
		return false
	}
	for _, sample := range prof.Sample {
		for _, got := range sample.Label[key] {
			if got == value {
				return true
			}
		}
	}
	return false
}

func pprofSampleTotals(prof *profile.Profile) map[string]int64 {
	totals := make(map[string]int64)
	if prof == nil {
		return totals
	}
	for i, sampleType := range prof.SampleType {
		var total int64
		for _, sample := range prof.Sample {
			if i < len(sample.Value) {
				total += sample.Value[i]
			}
		}
		totals[sampleType.Type] = total
	}
	return totals
}

// ToPprofWithMetrics converts GPU trace timing metrics to pprof format with improved accuracy.
// This version constructs a full hierarchy (GPU -> Queue -> CommandBuffer -> Encoder)
// and includes dependency information.
func ToPprofWithMetrics(t *trace.Trace, mapper *ShaderSourceMapper, stats *counter.PerfCounterStats) (*profile.Profile, error) {
	// Extract timing metrics
	extractor := NewTimingMetricsExtractor(t)
	metrics, err := extractor.Extract()
	if err != nil {
		return nil, fmt.Errorf("extract timing metrics: %w", err)
	}

	// Try to get real timing from profiler data (streamData)
	realTimings, totalTimeUs, realTimingErr := counter.ExtractEncoderTimingsFromProfiler(t)
	useRealTiming := realTimingErr == nil && len(realTimings) > 0
	if useRealTiming {
		fmt.Fprintf(os.Stderr, "Using real GPU timing: %d encoders, %.2f ms total\n",
			len(realTimings), float64(totalTimeUs)/1000)
	}

	// Try to get per-dispatch timing with function names from streamData
	streamStats, streamStatsErr := counter.ExtractPipelineStatsFromTraceStreamData(t)
	useDispatchTiming := streamStatsErr == nil && len(streamStats.Dispatches) > 0
	var executionCosts *counter.ExecutionCostMetrics
	if useDispatchTiming {
		counter.CorrelateDispatchSamples(streamStats)
		executionCosts = applyProfilingExecutionCosts(streamStats, t.Path)
		fmt.Fprintf(os.Stderr, "Using dispatch timing: %d dispatches, %d pipelines\n",
			len(streamStats.Dispatches), len(streamStats.Pipelines))
	}

	// Create profile with expanded counter types from GPUCounterGraph.plist
	// Categories: timing, counts, percentage metrics, byte metrics, bandwidth metrics
	// Indices must match the values array construction below
	prof := &profile.Profile{
		SampleType: []*profile.ValueType{
			// Core timing and structure (indices 0-2)
			{Type: "time", Unit: "nanoseconds"},
			{Type: "count", Unit: "count"},
			{Type: "edges", Unit: "count"},

			// Hardware metrics matching Xcode's view (indices 3-6)
			{Type: "simd_groups", Unit: "count"},   // Xcode "Cost" is based on this
			{Type: "alloc_regs", Unit: "count"},    // Allocated Registers
			{Type: "high_reg", Unit: "count"},      // High Register
			{Type: "spilled_bytes", Unit: "bytes"}, // Spilled Bytes

			// Percentage metrics - utilization (indices 7-11).
			{Type: "alu_util", Unit: "basis_points"},
			{Type: "compute_util", Unit: "basis_points"},
			{Type: "fragment_util", Unit: "basis_points"},
			{Type: "vertex_util", Unit: "basis_points"},
			{Type: "f32_util", Unit: "basis_points"},

			// Percentage metrics - limiters (indices 13-18).
			{Type: "f32_limiter", Unit: "basis_points"},
			{Type: "l1_limiter", Unit: "basis_points"},
			{Type: "llc_limiter", Unit: "basis_points"},
			{Type: "control_flow_limiter", Unit: "basis_points"},
			{Type: "buffer_l1_miss", Unit: "basis_points"},
			{Type: "instruction_throughput", Unit: "basis_points"},

			// Byte metrics (indices 19-22)
			{Type: "read_bytes", Unit: "bytes"},
			{Type: "write_bytes", Unit: "bytes"},
			{Type: "buffer_read_bytes", Unit: "bytes"},
			{Type: "buffer_write_bytes", Unit: "bytes"},

			// Bandwidth metrics (indices 23-25)
			{Type: "device_bandwidth", Unit: "GB/s"},
			{Type: "buffer_l1_read_bw", Unit: "GB/s"},
			{Type: "buffer_l1_write_bw", Unit: "GB/s"},

			// Instruction counts from PipelineStats/streamData (indices 26-33)
			{Type: "instructions", Unit: "count"},        // Total instruction count
			{Type: "alu_instructions", Unit: "count"},    // ALU instruction count
			{Type: "fp32_instructions", Unit: "count"},   // FP32 instruction count
			{Type: "fp16_instructions", Unit: "count"},   // FP16 instruction count
			{Type: "int32_instructions", Unit: "count"},  // INT32 instruction count
			{Type: "int16_instructions", Unit: "count"},  // INT16 instruction count
			{Type: "branch_instructions", Unit: "count"}, // Branch instruction count
			{Type: "threadgroup_mem", Unit: "bytes"},     // Threadgroup memory

			// Execution cost from Profiling_f_*.raw (index 34)
			{Type: "execution_cost", Unit: "basis_points"}, // Statistical GPU profiling cost

			// GPRWCNTR encoder profile data (index 35)
			{Type: "profiler_samples", Unit: "count"}, // GPRWCNTR sample count from ShaderProfilerData

			// Register footprint from streamData pipeline stats (index 36)
			{Type: "uniform_regs", Unit: "count"}, // Uniform registers

			// Rows whose raw source carries no encoder identity (index 37)
			{Type: "unattributed_counter_rows", Unit: "count"},
		},
		PeriodType: &profile.ValueType{
			Type: "gpu",
			Unit: "nanoseconds",
		},
		Period:            1,
		DefaultSampleType: "time",
	}

	// Set timing info - prefer real timing from profiler data
	if useRealTiming {
		// Use real GPU timing from profiler data
		prof.DurationNanos = int64(totalTimeUs) * 1000 // microseconds to nanoseconds
		prof.TimeNanos = time.Now().UnixNano()
	} else if metrics.TotalDuration > 0 {
		prof.DurationNanos = metrics.TotalDuration.Nanoseconds()
		prof.TimeNanos = time.Now().UnixNano()
	}
	if useDispatchTiming {
		applyStreamTimingMetadata(prof, streamStats)
	}
	dispatchSIMDGroupsByDispatch := dispatchSIMDGroupsByIndex(t, streamStats)
	if len(dispatchSIMDGroupsByDispatch) > 0 {
		prof.Comments = append(prof.Comments, "gputrace simd_groups_source: capture dispatch geometry")
	}
	if executionCosts != nil {
		prof.Comments = append(prof.Comments,
			"gputrace execution_cost_source: Profiling_f_*.raw",
			"gputrace execution_cost_unit: basis points",
			fmt.Sprintf("gputrace execution_cost_samples: %d", executionCosts.TotalSamples),
		)
	}
	dispatchExecutionCosts := dispatchExecutionCostValues(streamStats, executionCosts)
	var encoderCounters []counter.EncoderCounterMetrics
	if stats != nil {
		var err error
		encoderCounters, err = counter.PopulateEncoderMetricsFromPerfCounterStats(stats)
		if err != nil {
			return nil, fmt.Errorf("populate counter attribution: %w", err)
		}
	}
	encoderCounterByIndex, unattributedCounters := splitEncoderCounterMetrics(encoderCounters)
	if len(encoderCounterByIndex) > 0 {
		prof.Comments = append(prof.Comments, "gputrace encoder_counters_source: explicitly attributed counter metrics")
	}
	if len(unattributedCounters) > 0 {
		prof.Comments = append(prof.Comments,
			"gputrace counter_attribution: unknown",
			fmt.Sprintf("gputrace unattributed_counter_rows: %d", len(unattributedCounters)),
			"gputrace counter_attribution_reason: no capture-backed encoder identity",
		)
	}

	// Create root node
	gpuTraceFunc := &profile.Function{
		ID:         1,
		Name:       "GPU",
		SystemName: "GPU",
		Filename:   t.Path,
	}
	gpuTraceLoc := &profile.Location{
		ID:   1,
		Line: []profile.Line{{Function: gpuTraceFunc}},
	}

	// Create command queue node
	queueLabel := t.CommandQueueLabel
	if queueLabel == "" {
		queueLabel = "MTLCommandQueue"
	}
	queueFunc := &profile.Function{
		ID:         2,
		Name:       queueLabel,
		SystemName: queueLabel,
	}
	queueLoc := &profile.Location{
		ID:   2,
		Line: []profile.Line{{Function: queueFunc}},
	}

	prof.Function = []*profile.Function{gpuTraceFunc, queueFunc}
	prof.Location = []*profile.Location{gpuTraceLoc, queueLoc}

	nextID := uint64(3)
	nextID = appendUnattributedCounterSamples(prof, unattributedCounters, nextID, queueLoc, gpuTraceLoc)

	// Map to track created locations/functions to avoid duplicates
	// Key: "cbIndex" -> *profile.Location
	cbLocs := make(map[int]*profile.Location)
	// Key: "encAddress" -> *profile.Location
	encLocs := make(map[uint64]*profile.Location)
	// Key: "debugGroupLabel" -> *profile.Location
	debugGroupLocs := make(map[string]*profile.Location)

	// Helper to truncate long names
	truncate := func(s string, n int) string {
		if len(s) > n {
			return s[:n-3] + "..."
		}
		return s
	}

	fmt.Fprintf(os.Stderr, "Pprof: Found %d debug group mappings\n", len(t.EncoderDebugGroups))

	// Build Command Buffer Nodes
	cbs, _ := t.ParseCommandBuffers()

	// Pre-create Command Buffer Locations
	for _, cb := range cbs {
		fnID := nextID
		nextID++
		locID := nextID
		nextID++

		name := fmt.Sprintf("CommandBuffer %d (T+%d)", cb.Index, cb.Timestamp)
		fn := &profile.Function{
			ID:         fnID,
			Name:       name,
			SystemName: name,
		}
		prof.Function = append(prof.Function, fn)

		loc := &profile.Location{
			ID:   locID,
			Line: []profile.Line{{Function: fn}},
		}
		prof.Location = append(prof.Location, loc)
		cbLocs[cb.Index] = loc
	}

	// Build Encoder Nodes
	// We need to map encoders to command buffers.
	// Heuristic: Encoder belongs to the CB with largest offset <= encoder offset.
	// Ensure CBs are sorted by offset
	sort.Slice(cbs, func(i, j int) bool {
		return cbs[i].Offset < cbs[j].Offset
	})

	encoders := t.ParseComputeEncoders()

	// Map encoder timing by label (approximation as metrics are aggregated)
	// We'll distribute the aggregated time across instances or just use average?
	// The timing extraction in metrics.go gives aggregate stats.
	// But ExtractTimingData gives per-encoder instances in `metrics.EncoderTimings`.
	// We should try to match `metrics.EncoderTimings` to `encoders`.

	// EncoderTimings logic in timing.go iterates unique labels, then finds timestamps.
	// This might be misaligned with actual instances if there are multiple same-label encoders.
	// However, `metrics.EncoderTimings` is a flat list.
	// Let's assume matching order for same-labeled encoders?
	// Or just use the label match.

	timingMap := make(map[string]*trace.EncoderTiming) // Label -> Timing (last valid found?)
	// Actually metrics.EncoderTimings is a slice of *EncoderTiming.
	// The length might not match `encoders` slice length if timing failed for some.

	// Let's just lookup by label for now.
	for _, et := range metrics.EncoderTimings {
		timingMap[et.Label] = et
	}

	// Parse all dispatches once? No, simpler to parse per region or we need to map them.
	// Since we need to associate dispatches with specific encoders, parsing per region [enc.Offset, nextEnc.Offset] is safer.

	if stats == nil {
		fmt.Fprintln(os.Stderr, "No stats provided to ToPprofWithMetrics")
	}

	matches := 0
	for i, enc := range encoders {
		// Find parent CommandBuffer
		var parentCB *trace.CommandBuffer
		for j := len(cbs) - 1; j >= 0; j-- {
			if cbs[j].Offset <= enc.Offset {
				parentCB = cbs[j]
				break
			}
		}

		fnID := nextID
		nextID++
		locID := nextID
		nextID++

		displayName := truncate(enc.Label, 80)

		fn := &profile.Function{
			ID:         fnID,
			Name:       displayName,
			SystemName: enc.Label,
			Filename:   "metal",
		}

		// Add source mapping
		if mapper != nil {
			if src, line := mapper.SourceLocation(enc.Label); src != "" {
				fn.Filename = src
				fn.StartLine = int64(line)
			}
		}

		prof.Function = append(prof.Function, fn)

		loc := &profile.Location{
			ID:   locID,
			Line: []profile.Line{{Function: fn, Line: fn.StartLine}},
		}

		if fn.Filename != "" && fn.Filename != "metal" {
			loc.Mapping = &profile.Mapping{ID: 1, File: fn.Filename}
			if len(prof.Mapping) == 0 {
				prof.Mapping = []*profile.Mapping{loc.Mapping}
			}
		}

		prof.Location = append(prof.Location, loc)
		encLocs[enc.Address] = loc

		// Determine region to scan for dispatches
		startOffset := enc.Offset
		endOffset := int64(len(t.CaptureData))
		if i < len(encoders)-1 {
			endOffset = encoders[i+1].Offset
		}

		// Bounds check to prevent slice out of range
		var dispatches []trace.DispatchThreads
		captureLen := int64(len(t.CaptureData))
		if startOffset >= 0 && startOffset < captureLen && endOffset <= captureLen && startOffset < endOffset {
			regionData := t.CaptureData[startOffset:endOffset]
			dispatches = t.ParseDispatchInRegion(regionData, startOffset)
		}

		// Aggregate dispatch info
		gridSize := ""
		groupSize := ""

		if len(dispatches) > 0 {
			d := dispatches[0]
			gridSize = fmt.Sprintf("%d,%d,%d", d.ThreadsX, d.ThreadsY, d.ThreadsZ)
			groupSize = fmt.Sprintf("%d,%d,%d", d.ThreadsPerGroupX, d.ThreadsPerGroupY, d.ThreadsPerGroupZ)

			if len(dispatches) > 1 {
				gridSize += fmt.Sprintf(" (+%d more)", len(dispatches)-1)
			}
		}

		// Lookup Debug Group
		groupLabel := t.EncoderDebugGroups[enc.Label]
		if groupLabel == "" {
			// Fallback: Infer debug group from kernel name/label
			lower := strings.ToLower(enc.Label)
			// Simple inference heuristics matching cmd/tree.go
			if strings.Contains(lower, "qmv") || strings.Contains(lower, "quantized") {
				groupLabel = "QuantizedMatmul"
			} else if strings.Contains(lower, "copy") {
				groupLabel = "Copy"
			} else if strings.Contains(lower, "sdpa") || strings.Contains(lower, "attention") {
				groupLabel = "Attention"
			} else if strings.Contains(lower, "rms") || strings.Contains(lower, "norm") {
				groupLabel = "RMSNorm"
			} else if strings.Contains(lower, "rope") {
				groupLabel = "RoPE"
			} else if strings.Contains(lower, "argmax") || strings.Contains(lower, "softmax") {
				groupLabel = "Argmax"
			} else if strings.Contains(lower, "gemm") || strings.Contains(lower, "matmul") {
				groupLabel = "Matmul"
			} else if strings.Contains(lower, "add") || strings.Contains(lower, "multiply") || strings.Contains(lower, "div") || strings.Contains(lower, "sub") {
				// Group elementwise logic
				groupLabel = "Elementwise"
			}
		}

		var debugGroupLoc *profile.Location
		if groupLabel != "" {
			if loc, exists := debugGroupLocs[groupLabel]; exists {
				debugGroupLoc = loc
			} else {
				// Create new DebugGroup node
				dgFnID := nextID
				nextID++
				dgLocID := nextID
				nextID++

				dgFn := &profile.Function{
					ID:         dgFnID,
					Name:       groupLabel,
					SystemName: groupLabel,
					Filename:   "debug_group",
				}
				prof.Function = append(prof.Function, dgFn)

				dgLoc := &profile.Location{
					ID:   dgLocID,
					Line: []profile.Line{{Function: dgFn}},
				}
				prof.Location = append(prof.Location, dgLoc)
				debugGroupLocs[groupLabel] = dgLoc
				debugGroupLoc = dgLoc
			}
		}

		// Add Sample
		// Hierarchy: enc -> [debug_group] -> cb -> queue -> gpu

		locStack := []*profile.Location{loc}

		if debugGroupLoc != nil {
			locStack = append(locStack, debugGroupLoc)
		}

		if parentCB != nil && cbLocs[parentCB.Index] != nil {
			locStack = append(locStack, cbLocs[parentCB.Index])
		}
		locStack = append(locStack, queueLoc, gpuTraceLoc)

		// Get duration - prefer dispatch samples when streamData is available so
		// encoder summary nodes do not double-count the primary time sample.
		duration := int64(0)
		if useDispatchTiming {
			duration = 0
		} else if useRealTiming && i < len(realTimings) {
			// Use real GPU timing (microseconds to nanoseconds)
			duration = int64(realTimings[i].DurationMicros) * 1000
		} else if t, ok := timingMap[enc.Label]; ok {
			duration = int64(t.DurationNs)
			matches++
		}

		// Prepare sample values matching SampleType.
		// Indices: 0-2 core, 3-6 hardware, 7-18 percentages,
		// 19-25 bytes and bandwidth, 26-33 instructions.
		values := make([]int64, pprofValueCount)
		values[0] = duration // time
		values[1] = 1        // count
		// values[2] = edges (set later for dependency samples)
		numLabels := make(map[string][]int64)
		counterSource := false

		if m := encoderCounterByIndex[i]; m != nil {
			applyEncoderCounterMetrics(values, m)
			counterSource = true
		}

		labels := map[string][]string{
			"label": {enc.Label},
		}
		if counterSource {
			labels["counter_source"] = []string{"Counters_f_*.raw/Profiling_f_*.raw"}
		}
		addStreamTimingLabels(labels, streamStats)
		if gridSize != "" {
			labels["grid_size"] = []string{gridSize}
			labels["group_size"] = []string{groupSize}
		}

		prof.Sample = append(prof.Sample, &profile.Sample{
			Location: locStack,
			Value:    values,
			Label:    labels,
			NumLabel: numLabels,
		})

		// Add dispatch-level samples for finer granularity
		// Each dispatch becomes a child of its encoder in the hierarchy
		for dispatchIdx, d := range dispatches {
			dispFnID := nextID
			nextID++
			dispLocID := nextID
			nextID++

			// Create descriptive name for dispatch
			dispName := fmt.Sprintf("dispatch_%d [%dx%dx%d]", dispatchIdx,
				d.ThreadsX, d.ThreadsY, d.ThreadsZ)

			dispFn := &profile.Function{
				ID:         dispFnID,
				Name:       dispName,
				SystemName: fmt.Sprintf("%s::dispatch_%d", enc.Label, dispatchIdx),
				Filename:   "dispatch",
			}
			prof.Function = append(prof.Function, dispFn)

			dispLoc := &profile.Location{
				ID:   dispLocID,
				Line: []profile.Line{{Function: dispFn}},
			}
			prof.Location = append(prof.Location, dispLoc)

			// Dispatch location stack: dispatch -> encoder -> [debug_group] -> cb -> queue -> gpu
			dispLocStack := []*profile.Location{dispLoc}
			dispLocStack = append(dispLocStack, locStack...)

			// Calculate thread count for this dispatch
			totalThreads := int64(d.ThreadsX) * int64(d.ThreadsY) * int64(d.ThreadsZ)
			threadsPerGroup := int64(d.ThreadsPerGroupX) * int64(d.ThreadsPerGroupY) * int64(d.ThreadsPerGroupZ)
			numGroups := int64(0)
			if threadsPerGroup > 0 {
				numGroups = totalThreads / threadsPerGroup
			}

			// Dispatch sample values available directly from capture data.
			dispValues := make([]int64, pprofValueCount)
			dispValues[1] = 1 // count
			simdGroups := int64(d.SIMDGroups())
			if simdGroups > 0 {
				dispValues[3] = simdGroups
			}

			dispLabels := map[string][]string{
				"dispatch_idx": {fmt.Sprintf("%d", dispatchIdx)},
				"grid":         {fmt.Sprintf("%d,%d,%d", d.ThreadsX, d.ThreadsY, d.ThreadsZ)},
				"group":        {fmt.Sprintf("%d,%d,%d", d.ThreadsPerGroupX, d.ThreadsPerGroupY, d.ThreadsPerGroupZ)},
			}
			addStreamTimingLabels(dispLabels, streamStats)
			dispNumLabels := map[string][]int64{
				"threads":       {totalThreads},
				"thread_groups": {numGroups},
			}
			if simdGroups > 0 {
				dispNumLabels["simd_groups"] = []int64{simdGroups}
			}

			prof.Sample = append(prof.Sample, &profile.Sample{
				Location: dispLocStack,
				Value:    dispValues,
				Label:    dispLabels,
				NumLabel: dispNumLabels,
			})
		}
	}
	if stats != nil {
		fmt.Fprintf(os.Stderr, "Total hardware metric matches: %d/%d encoders\n", matches, len(encoders))
	}

	// Add dispatch samples with real timing from streamData if available
	if useDispatchTiming {
		// Create function locations for each unique kernel from streamData dispatches
		dispatchFuncLocs := make(map[string]*profile.Location)

		// streamStats.Pipelines comes from pipelinePerformanceStatistics, an
		// NSDictionary, so its slice order is unrelated to the pipeline index
		// carried by gpuCommandInfoData records. Join on the pipeline ID.
		streamPipelinesByID := make(map[int]counter.PipelineStats, len(streamStats.Pipelines))
		for _, p := range streamStats.Pipelines {
			streamPipelinesByID[p.PipelineID] = p
		}

		// Calculate total dispatch time for percentage calculation
		var totalDispatchTimeUs int
		for _, d := range streamStats.Dispatches {
			totalDispatchTimeUs += d.DurationUs
		}

		for i, d := range streamStats.Dispatches {
			funcName := d.DisplayName()

			// Get or create location for this function
			var funcLoc *profile.Location
			if loc, exists := dispatchFuncLocs[funcName]; exists {
				funcLoc = loc
			} else {
				fnID := nextID
				nextID++
				locID := nextID
				nextID++

				fn := &profile.Function{
					ID:         fnID,
					Name:       funcName,
					SystemName: funcName,
					Filename:   "metal_kernel",
				}

				// Add source mapping if available
				if mapper != nil {
					if src, line := mapper.SourceLocation(funcName); src != "" {
						fn.Filename = src
						fn.StartLine = int64(line)
					}
				}

				prof.Function = append(prof.Function, fn)

				funcLoc = &profile.Location{
					ID:   locID,
					Line: []profile.Line{{Function: fn, Line: fn.StartLine}},
				}
				prof.Location = append(prof.Location, funcLoc)
				dispatchFuncLocs[funcName] = funcLoc
			}

			// Keep the streamData dispatch stack shader-centric so pprof -top
			// matches Xcode's Shaders view. Encoder context is preserved in
			// numeric labels below; capture encoder names can otherwise skew
			// flat shader totals when pprof merges duplicate function names.
			dispLocStack := []*profile.Location{funcLoc, queueLoc, gpuTraceLoc}

			// Create sample with real timing
			dispValues := make([]int64, pprofValueCount)
			dispValues[0] = int64(d.DurationUs) * 1000 // Convert µs to ns
			dispValues[1] = 1                          // count
			if d.Index >= 0 && d.Index < len(dispatchSIMDGroupsByDispatch) {
				dispValues[3] = dispatchSIMDGroupsByDispatch[d.Index]
			}
			if i < len(dispatchExecutionCosts) {
				dispValues[pprofExecutionCostIdx] = dispatchExecutionCosts[i]
			}

			// Add instruction count from pipeline if available
			if p, ok := streamPipelinesByID[d.PipelineID]; ok {
				dispValues[4] = int64(p.TemporaryRegisterCount)
				dispValues[6] = int64(p.SpilledBytes)
				dispValues[pprofUniformRegsIdx] = int64(p.UniformRegisterCount)
				dispValues[25] = int64(p.InstructionCount)
				dispValues[26] = int64(p.ALUInstructionCount)
				dispValues[27] = int64(p.FP32InstructionCount)
				dispValues[28] = int64(p.FP16InstructionCount)
				dispValues[29] = int64(p.INT32InstructionCount)
				dispValues[30] = int64(p.INT16InstructionCount)
				dispValues[31] = int64(p.BranchInstructionCount)
				dispValues[32] = int64(p.ThreadgroupMemory)
			}

			costPct := 0.0
			if totalDispatchTimeUs > 0 {
				costPct = float64(d.DurationUs) / float64(totalDispatchTimeUs) * 100
			}

			dispLabels := map[string][]string{
				"kernel":   {funcName},
				"source":   {"streamData"},
				"dispatch": {fmt.Sprintf("%d", d.Index)},
			}
			addStreamTimingLabels(dispLabels, streamStats)
			dispNumLabels := map[string][]int64{
				"pipeline_idx": {int64(d.PipelineIndex)},
				"encoder_idx":  {int64(d.EncoderIndex)},
				"duration_us":  {int64(d.DurationUs)},
				"cost_pct":     {int64(costPct * 100)}, // Scale for precision
			}
			if d.ExecutionCostPct > 0 {
				dispNumLabels["profiling_cost_bp"] = []int64{int64(math.Round(d.ExecutionCostPct * 100))}
			}
			if d.Index >= 0 && d.Index < len(dispatchSIMDGroupsByDispatch) {
				dispNumLabels["simd_groups"] = []int64{dispatchSIMDGroupsByDispatch[d.Index]}
			}

			prof.Sample = append(prof.Sample, &profile.Sample{
				Location: dispLocStack,
				Value:    dispValues,
				Label:    dispLabels,
				NumLabel: dispNumLabels,
			})
		}
		fmt.Fprintf(os.Stderr, "Added %d dispatch samples with real timing from streamData\n", len(streamStats.Dispatches))

		// Add encoder profile samples from GPRWCNTR ShaderProfilerData
		if streamStats.Timeline != nil && len(streamStats.Timeline.EncoderProfiles) > 0 {
			for _, ep := range streamStats.Timeline.EncoderProfiles {
				if ep.SampleCount == 0 {
					continue
				}

				// Create a function/location for this encoder profile
				fnID := nextID
				nextID++
				locID := nextID
				nextID++

				fn := &profile.Function{
					ID:         fnID,
					Name:       fmt.Sprintf("GPRWCNTR_Enc%d_%s", ep.Index, ep.Source),
					SystemName: fmt.Sprintf("encoder_profile_%d", ep.Index),
					Filename:   "gprwcntr",
				}
				prof.Function = append(prof.Function, fn)

				funcLoc := &profile.Location{
					ID:   locID,
					Line: []profile.Line{{Function: fn}},
				}
				prof.Location = append(prof.Location, funcLoc)

				// Location stack: encoder_profile -> queue -> gpu
				epLocStack := []*profile.Location{funcLoc, queueLoc, gpuTraceLoc}

				// Create sample with encoder profile data
				epValues := make([]int64, pprofValueCount)
				epValues[1] = 1                                         // count
				epValues[pprofProfilerCountIdx] = int64(ep.SampleCount) // profiler_samples

				epLabels := map[string][]string{
					"source":      {ep.Source},
					"data_source": {"GPRWCNTR"},
				}
				addStreamTimingLabels(epLabels, streamStats)
				epNumLabels := map[string][]int64{
					"ring_buffer_idx": {int64(ep.RingBufferIndex)},
					"sample_count":    {int64(ep.SampleCount)},
					"start_ticks":     {int64(ep.StartTicks)},
					"end_ticks":       {int64(ep.EndTicks)},
					"duration_ns":     {int64(ep.DurationNs)},
				}

				prof.Sample = append(prof.Sample, &profile.Sample{
					Location: epLocStack,
					Value:    epValues,
					Label:    epLabels,
					NumLabel: epNumLabels,
				})
			}
			fmt.Fprintf(os.Stderr, "Added %d encoder profile samples from GPRWCNTR\n", len(streamStats.Timeline.EncoderProfiles))
		}
	}

	// Build Dependencies
	depGraph, err := t.BuildDependencyGraph()
	if err == nil {
		// Map depNodes to Encoders
		// DependencyNode.ID looks like it matches ParseComputeEncoders index if we are careful.
		// trace.dependencies.go: "currentNodeID = len(graph.Nodes)" inside loop over events.
		// Events come from parsing capture data sequentially.
		// ParseComputeEncoders also parses sequentially.
		// So graph.Nodes[i] should correspond to encoders[i] IF ParseDependencyEvents and ParseComputeEncoders logic aligns.
		// They both scan for CS records.

		// Let's assume 1:1 mapping for now.
		if len(depGraph.Nodes) == len(encoders) {
			for _, edge := range depGraph.Edges {
				// Edge: From(Producer) -> To(Consumer)
				// We want to visualize this dependency.
				// In callgraph: Caller -> Callee.
				// Dependency: Consumer depends on Producer.
				// "Consumer 'calls' Producer" to wait for it?
				// Or "Producer 'flows to' Consumer"?
				// Pprof graph shows A -> B if A calls B.
				// If we want arrows Producer -> Consumer: Producer calls Consumer?
				// No, usually "Root -> specific".
				// Let's represent it as "Consumer calls Producer" (Consumer requires Producer).

				consumerLoc := encLocs[encoders[edge.To].Address]
				producerLoc := encLocs[encoders[edge.From].Address]

				if consumerLoc != nil && producerLoc != nil {
					// Add dependency sample with edge count at index 2.
					edgeValues := make([]int64, pprofValueCount)
					edgeValues[2] = 1 // edges count
					prof.Sample = append(prof.Sample, &profile.Sample{
						Location: []*profile.Location{producerLoc, consumerLoc},
						Value:    edgeValues,
						Label: map[string][]string{
							"dependency": {edge.Buffer},
						},
					})
				}
			}
		}
	}

	appendXcodeMetricCoverageComments(prof)
	return prof, nil
}
