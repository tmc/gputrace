// Package evidence builds canonical reports from GPU trace evidence.
package evidence

import (
	"fmt"
	"sort"
	"time"

	"github.com/tmc/gputrace/internal/counter"
	"github.com/tmc/gputrace/internal/trace"
)

// Report is the common evidence model for summary and inspection views.
type Report struct {
	CommandBuffers    int            `json:"command_buffers"`
	ComputeEncoders   int            `json:"compute_encoders"`
	Dispatches        int            `json:"dispatches"`
	CSLabels          int            `json:"cs_debug_labels"`
	UniqueCSLabels    int            `json:"unique_cs_debug_labels"`
	DispatchSpan      time.Duration  `json:"dispatch_span"`
	CBActiveTime      time.Duration  `json:"command_buffer_active_time"`
	CBWallSpan        time.Duration  `json:"command_buffer_wall_span"`
	EffectiveGPUTime  *time.Duration `json:"effective_gpu_time,omitempty"`
	TimingSource      string         `json:"timing_source"`
	TimingApproximate bool           `json:"timing_approximate"`
	Functions         []Function     `json:"functions"`
	Packing           Packing        `json:"packing"`
	EvidenceGaps      []string       `json:"evidence_gaps,omitempty"`
}

// Function summarizes dispatches attributed to one Metal function.
type Function struct {
	Name       string        `json:"name"`
	Dispatches int           `json:"dispatches"`
	Span       time.Duration `json:"span"`
	SpanShare  float64       `json:"span_share"`
}

// Packing summarizes how work is packed into encoders and command buffers.
type Packing struct {
	MedianDispatchesPerEncoder float64 `json:"median_dispatches_per_encoder"`
	DispatchesPerCommandBuffer float64 `json:"dispatches_per_command_buffer"`
}

// Build constructs a report. Profiler counts define encoder instances; CS
// records contribute labels only and can never increase ComputeEncoders.
func Build(t *trace.Trace, stats *counter.StreamDataStats) (*Report, error) {
	if stats == nil {
		return nil, fmt.Errorf("build evidence report: profiler statistics are required")
	}
	r := &Report{
		ComputeEncoders:   stats.NumEncoders,
		Dispatches:        len(stats.Dispatches),
		DispatchSpan:      time.Duration(stats.TotalDispatchTimeUs) * time.Microsecond,
		CBActiveTime:      time.Duration(stats.CommandBufferActiveNs),
		CBWallSpan:        time.Duration(stats.CommandBufferWallNs),
		TimingSource:      stats.TimingSource,
		TimingApproximate: false,
	}
	if stats.Timeline != nil {
		r.CommandBuffers = len(stats.Timeline.CommandBufferTimestamps)
	}
	if stats.EffectiveGPUTimeNs != nil {
		d := time.Duration(*stats.EffectiveGPUTimeNs)
		r.EffectiveGPUTime = &d
	} else {
		r.EvidenceGaps = append(r.EvidenceGaps, "effective GPU time unavailable")
	}
	r.EvidenceGaps = append(r.EvidenceGaps, "ALU utilization unavailable")
	if t != nil && !t.ProfilerOnly {
		labels := make(map[string]bool)
		for _, event := range t.ParseComputeEncoders() {
			if event.Label == "" {
				continue
			}
			r.CSLabels++
			labels[event.Label] = true
		}
		r.UniqueCSLabels = len(labels)
	}
	if r.CSLabels == 0 {
		r.EvidenceGaps = append(r.EvidenceGaps, "CS/debug labels unavailable")
	}
	r.Functions = functionRows(stats.Dispatches, r.DispatchSpan)
	r.Packing = packing(stats.Dispatches, r.ComputeEncoders, r.CommandBuffers)
	return r, nil
}

func functionRows(dispatches []counter.DispatchInfo, total time.Duration) []Function {
	byName := make(map[string]int)
	var rows []Function
	for _, dispatch := range dispatches {
		name := dispatch.DisplayName()
		index, ok := byName[name]
		if !ok {
			index = len(rows)
			byName[name] = index
			rows = append(rows, Function{Name: name})
		}
		rows[index].Dispatches++
		rows[index].Span += time.Duration(dispatch.DurationUs) * time.Microsecond
	}
	for i := range rows {
		if total > 0 {
			rows[i].SpanShare = float64(rows[i].Span) / float64(total) * 100
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Span > rows[j].Span })
	return rows
}

func packing(dispatches []counter.DispatchInfo, encoders, commandBuffers int) Packing {
	counts := make([]int, encoders)
	for _, dispatch := range dispatches {
		if dispatch.EncoderIndex >= 0 && dispatch.EncoderIndex < len(counts) {
			counts[dispatch.EncoderIndex]++
		}
	}
	sort.Ints(counts)
	var median float64
	if len(counts) > 0 {
		middle := len(counts) / 2
		median = float64(counts[middle])
		if len(counts)%2 == 0 {
			median = float64(counts[middle-1]+counts[middle]) / 2
		}
	}
	var perCB float64
	if commandBuffers > 0 {
		perCB = float64(len(dispatches)) / float64(commandBuffers)
	}
	return Packing{MedianDispatchesPerEncoder: median, DispatchesPerCommandBuffer: perCB}
}
