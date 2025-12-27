package export

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/pprof/profile"

	"github.com/tmc/gputrace/internal/timing"
	"github.com/tmc/gputrace/internal/trace"
)

// Type aliases
var NewTimingMetricsExtractor = timing.NewTimingMetricsExtractor

// ToPprofWithMetrics converts GPU trace timing metrics to pprof format with improved accuracy.
// This version constructs a full hierarchy (GPU -> Queue -> CommandBuffer -> Encoder)
// and includes dependency information.
func ToPprofWithMetrics(t *trace.Trace, mapper *ShaderSourceMapper) (*profile.Profile, error) {
	// Extract timing metrics
	extractor := NewTimingMetricsExtractor(t)
	metrics, err := extractor.Extract()
	if err != nil {
		return nil, fmt.Errorf("extract timing metrics: %w", err)
	}

	// Create profile
	prof := &profile.Profile{
		SampleType: []*profile.ValueType{
			{Type: "time", Unit: "nanoseconds"},
			{Type: "count", Unit: "count"},
			{Type: "edges", Unit: "count"},
		},
		PeriodType: &profile.ValueType{
			Type: "gpu",
			Unit: "nanoseconds",
		},
		Period: 1,
	}

	// Set timing info
	if metrics.TotalDuration > 0 {
		prof.DurationNanos = metrics.TotalDuration.Nanoseconds()
		prof.TimeNanos = time.Now().UnixNano()
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

	fmt.Printf("Pprof: Found %d debug group mappings\n", len(t.EncoderDebugGroups))

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

	encoders, _ := t.ParseComputeEncoders()

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
			if src, line := mapper.GetSourceLocation(enc.Label); src != "" {
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

		regionData := t.CaptureData[startOffset:endOffset]
		dispatches, _ := t.ParseDispatchInRegion(regionData, startOffset)

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

		// Get duration
		duration := int64(0)
		if t, ok := timingMap[enc.Label]; ok {
			duration = int64(t.DurationNs)
			matches++
		}

		labels := map[string][]string{
			"label": {enc.Label},
		}
		if gridSize != "" {
			labels["grid_size"] = []string{gridSize}
			labels["group_size"] = []string{groupSize}
		}

		prof.Sample = append(prof.Sample, &profile.Sample{
			Location: locStack,
			Value:    []int64{duration, 1, 0},
			Label:    labels,
		})
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
					// Add dependency sample
					prof.Sample = append(prof.Sample, &profile.Sample{
						Location: []*profile.Location{producerLoc, consumerLoc},
						Value:    []int64{0, 0, 1}, // 0 duration, 0 count, 1 edge
						Label: map[string][]string{
							"dependency": {edge.Buffer},
						},
					})
				}
			}
		}
	}

	return prof, nil
}

// ToPprofFlat converts GPU trace timing to a flat pprof format without hierarchy.
// This is useful for seeing kernel costs without the GPU/Queue overhead.
func ToPprofFlat(t *trace.Trace) (*profile.Profile, error) {
	// Extract timing metrics
	extractor := NewTimingMetricsExtractor(t)
	metrics, err := extractor.Extract()
	if err != nil {
		return nil, fmt.Errorf("extract timing metrics: %w", err)
	}

	// Create profile
	prof := &profile.Profile{
		SampleType: []*profile.ValueType{
			{Type: "gpu", Unit: "nanoseconds"},
		},
		PeriodType: &profile.ValueType{
			Type: "gpu",
			Unit: "nanoseconds",
		},
		Period: 1,
	}

	// Set timing info
	if metrics.TotalDuration > 0 {
		prof.DurationNanos = metrics.TotalDuration.Nanoseconds()
		prof.TimeNanos = time.Now().UnixNano()
	}

	// Add kernel samples (flat, no hierarchy)
	nextID := uint64(1)
	for _, kt := range metrics.KernelTimings {
		fnID := nextID
		locID := nextID
		nextID++

		// Create function for this kernel
		fn := &profile.Function{
			ID:         fnID,
			Name:       kt.Name,
			SystemName: kt.Name,
			Filename:   "metal",
		}
		prof.Function = append(prof.Function, fn)

		// Create location
		loc := &profile.Location{
			ID:   locID,
			Line: []profile.Line{{Function: fn, Line: 1}},
		}
		prof.Location = append(prof.Location, loc)

		// Create sample (flat - just the kernel)
		sample := &profile.Sample{
			Location: []*profile.Location{loc},
			Value:    []int64{kt.TotalDuration.Nanoseconds()},
			Label: map[string][]string{
				"invocations": {fmt.Sprintf("%d", kt.InvocationCount)},
			},
			NumLabel: map[string][]int64{
				"count":   {int64(kt.InvocationCount)},
				"avg_ns":  {kt.AvgDuration.Nanoseconds()},
				"percent": {int64(kt.PercentOfTotal * 100)},
			},
		}
		prof.Sample = append(prof.Sample, sample)
	}

	return prof, nil
}

// ToPprofPerInvocation creates a pprof profile with one sample per kernel invocation.
// This preserves timing variance and shows the distribution of execution times.
func ToPprofPerInvocation(t *trace.Trace) (*profile.Profile, error) {
	// Extract timing metrics
	extractor := NewTimingMetricsExtractor(t)
	metrics, err := extractor.Extract()
	if err != nil {
		return nil, fmt.Errorf("extract timing metrics: %w", err)
	}

	// Create profile
	prof := &profile.Profile{
		SampleType: []*profile.ValueType{
			{Type: "gpu", Unit: "nanoseconds"},
		},
		PeriodType: &profile.ValueType{
			Type: "gpu",
			Unit: "nanoseconds",
		},
		Period: 1,
	}

	if metrics.TotalDuration > 0 {
		prof.DurationNanos = metrics.TotalDuration.Nanoseconds()
		prof.TimeNanos = time.Now().UnixNano()
	}

	// Create root
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

	queueFunc := &profile.Function{
		ID:         2,
		Name:       t.CommandQueueLabel,
		SystemName: t.CommandQueueLabel,
	}
	queueLoc := &profile.Location{
		ID:   2,
		Line: []profile.Line{{Function: queueFunc}},
	}

	prof.Function = []*profile.Function{gpuTraceFunc, queueFunc}
	prof.Location = []*profile.Location{gpuTraceLoc, queueLoc}

	// Create function and location for each unique kernel
	kernelFuncs := make(map[string]*profile.Function)
	kernelLocs := make(map[string]*profile.Location)
	nextID := uint64(3)

	for _, kt := range metrics.KernelTimings {
		fnID := nextID
		locID := nextID
		nextID++

		fn := &profile.Function{
			ID:         fnID,
			Name:       kt.Name,
			SystemName: kt.Name,
			Filename:   "metal",
		}
		prof.Function = append(prof.Function, fn)
		kernelFuncs[kt.Name] = fn

		loc := &profile.Location{
			ID:   locID,
			Line: []profile.Line{{Function: fn, Line: 1}},
		}
		prof.Location = append(prof.Location, loc)
		kernelLocs[kt.Name] = loc
	}

	// Add one sample per invocation
	invocationNum := make(map[string]int)
	for _, kt := range metrics.KernelTimings {
		loc := kernelLocs[kt.Name]

		// Create a sample for each invocation
		for _, duration := range kt.Durations {
			invocationNum[kt.Name]++

			sample := &profile.Sample{
				Location: []*profile.Location{loc, queueLoc, gpuTraceLoc},
				Value:    []int64{duration.Nanoseconds()},
				Label: map[string][]string{
					"invocation": {fmt.Sprintf("%d", invocationNum[kt.Name])},
				},
			}
			prof.Sample = append(prof.Sample, sample)
		}
	}

	return prof, nil
}
