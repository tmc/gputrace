//go:build darwin

package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"

	"github.com/tmc/gputrace"
	"github.com/tmc/gputrace/internal/counter"
	"github.com/tmc/gputrace/internal/xcodebindings"
)

type xcodeParityOptions struct {
	json bool
}

type xcodeParityReport struct {
	Trace          string                           `json:"trace"`
	KernelEvents   int                              `json:"kernel_events"`
	PresentFields  []string                         `json:"present_fields"`
	AbsentFields   []string                         `json:"absent_fields"`
	CounterTracks  []string                         `json:"counter_tracks"`
	EmptyTracks    []string                         `json:"empty_tracks"`
	Timing         map[string]interface{}           `json:"timing"`
	Bindings       map[string]int                   `json:"bindings"`
	StreamData     *xcodebindings.StreamDataSummary `json:"stream_data,omitempty"`
	RemainingGaps  []xcodeParityGap                 `json:"remaining_gaps"`
	ClosedExamples []string                         `json:"closed_examples,omitempty"`
}

type xcodeParityGap struct {
	Metric  string `json:"metric"`
	Binding string `json:"binding"`
	Status  string `json:"status"`
	Next    string `json:"next"`
}

func runXcodeParity(cmd *cobra.Command, args []string, opts *xcodeParityOptions) error {
	timeline, err := timelineForParity(args[0])
	if err != nil {
		return err
	}
	report := buildXcodeParityReport(args[0], timeline, xcodebindings.Probe())
	if streamPath := streamDataPathForTrace(args[0]); streamPath != "" {
		if summary, err := xcodebindings.ProbeStreamData(streamPath); err == nil {
			report.StreamData = &summary
			report.applyStreamDataEvidence()
		}
	}
	w := cmd.OutOrStdout()
	if opts.json {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}

	fmt.Fprintf(w, "Trace: %s\n", report.Trace)
	fmt.Fprintf(w, "Kernel events: %d\n", report.KernelEvents)
	fmt.Fprintf(w, "Bindings: %d/%d classes, %d/%d selectors\n",
		report.Bindings["classes_present"],
		report.Bindings["classes_present"]+report.Bindings["classes_missing"],
		report.Bindings["selectors_present"],
		report.Bindings["selectors_present"]+report.Bindings["selectors_missing"])
	fmt.Fprintf(w, "Present fields: %s\n", stringsOrNone(report.PresentFields))
	fmt.Fprintf(w, "Absent fields: %s\n", stringsOrNone(report.AbsentFields))
	if source, _ := report.Timing["timing_source"].(string); source != "" {
		fmt.Fprintf(w, "Timing: %s\n", source)
	}
	if has, _ := report.Timing["has_effective_gpu_time"].(bool); !has {
		fmt.Fprintln(w, "Effective GPU time: not archived; using reported display-duration fallback")
	}
	if report.StreamData != nil {
		fmt.Fprintf(w, "StreamData: %d encoders, %d GPU commands, %d pipeline states, %d functions\n",
			report.StreamData.EncoderInfoCount,
			report.StreamData.GPUCommandInfoCount,
			report.StreamData.PipelineStateInfoCount,
			report.StreamData.FunctionInfoCount)
		if report.StreamData.MetalDeviceName != "" {
			fmt.Fprintf(w, "Device: %s (%s)\n", report.StreamData.MetalDeviceName, report.StreamData.MetalPluginName)
		}
	}
	if len(report.ClosedExamples) > 0 {
		fmt.Fprintln(w, "\nClosed in current trace")
		for _, item := range report.ClosedExamples {
			fmt.Fprintf(w, "  %s\n", item)
		}
	}
	if len(report.RemainingGaps) > 0 {
		fmt.Fprintln(w, "\nRemaining gaps")
		for _, gap := range report.RemainingGaps {
			fmt.Fprintf(w, "  %s: %s\n", gap.Metric, gap.Status)
			fmt.Fprintf(w, "    binding: %s\n", gap.Binding)
			fmt.Fprintf(w, "    next: %s\n", gap.Next)
		}
	}
	return nil
}

func streamDataPathForTrace(tracePath string) string {
	profilerDir := ""
	if filepath.Ext(tracePath) == ".gpuprofiler_raw" {
		profilerDir = tracePath
	} else {
		profilerDir = findProfilerDir(tracePath)
	}
	if profilerDir == "" {
		return ""
	}
	streamPath := filepath.Join(profilerDir, "streamData")
	if _, err := os.Stat(streamPath); err != nil {
		return ""
	}
	return streamPath
}

func timelineForParity(tracePath string) (*Timeline, error) {
	trace, err := gputrace.Open(tracePath)
	if err == nil {
		defer trace.Close()
		return generateTimeline(trace)
	}
	profilerDir, stats, err := loadProfilerStats(tracePath)
	if err != nil {
		return nil, err
	}
	counter.CorrelateDispatchSamples(stats)
	annotateDispatchExecutionCosts(stats, profilerDir)
	return buildTimelineFromProfilerData(tracePath, stats), nil
}

func buildXcodeParityReport(tracePath string, timeline *Timeline, bindings xcodebindings.Report) xcodeParityReport {
	metrics := timelineXcodeMetricsArgs(timeline)
	report := xcodeParityReport{
		Trace:          tracePath,
		KernelEvents:   intFromMetrics(metrics, "kernel_events"),
		PresentFields:  stringSliceFromMetrics(metrics, "kernel_arg_fields"),
		AbsentFields:   stringSliceFromMetrics(metrics, "absent_kernel_arg_fields"),
		CounterTracks:  stringSliceFromMetrics(metrics, "counter_tracks"),
		EmptyTracks:    stringSliceFromMetrics(metrics, "empty_counter_tracks"),
		Timing:         make(map[string]interface{}),
		Bindings:       bindings.Summary,
		RemainingGaps:  make([]xcodeParityGap, 0),
		ClosedExamples: make([]string, 0),
	}
	for _, key := range []string{"timing_source", "display_duration_source", "has_effective_gpu_time"} {
		if v, ok := metrics[key]; ok {
			report.Timing[key] = v
		}
	}

	present := make(map[string]bool)
	for _, field := range report.PresentFields {
		present[field] = true
	}
	if present["occupancy_pct"] {
		report.ClosedExamples = append(report.ClosedExamples, "occupancy_pct present on kernel events")
	}
	if present["alu_utilization_pct"] {
		report.ClosedExamples = append(report.ClosedExamples, "alu_utilization_pct present on kernel events")
	}
	if containsTrack(report.CounterTracks, "ALU Utilization") {
		report.ClosedExamples = append(report.ClosedExamples, "ALU Utilization counter track is source-backed")
	}
	if !boolFromMetrics(metrics, "has_effective_gpu_time") {
		report.RemainingGaps = append(report.RemainingGaps, xcodeParityGap{
			Metric:  "effective_gpu_time",
			Binding: "GTShaderProfilerStreamData.unarchivedAPSTimelineData / ReplayerGPUTime",
			Status:  "not archived in this trace",
			Next:    "capture or decode APSTimelineData ReplayerGPUTime; keep command-buffer active time as fallback",
		})
	}
	bindingByMetric := make(map[string]xcodebindings.Gap)
	for _, gap := range bindings.Gaps {
		bindingByMetric[gap.Metric] = gap
	}
	bindingField := map[string]string{
		"high_register":       "high_register",
		"alu_utilization_pct": "alu_utilization_pct",
		"occupancy_pct":       "occupancy_pct",
	}
	for bindingMetric, field := range bindingField {
		if present[field] {
			continue
		}
		gap := bindingByMetric[bindingMetric]
		if gap.Metric == "" {
			continue
		}
		report.RemainingGaps = append(report.RemainingGaps, xcodeParityGap{
			Metric:  field,
			Binding: gap.Binding,
			Status:  gap.Status,
			Next:    gap.Next,
		})
	}
	sort.Slice(report.RemainingGaps, func(i, j int) bool {
		return report.RemainingGaps[i].Metric < report.RemainingGaps[j].Metric
	})
	return report
}

func (r *xcodeParityReport) applyStreamDataEvidence() {
	if r == nil || r.StreamData == nil {
		return
	}
	if r.streamValueCount("Binaries") > 0 {
		r.updateGap("high_register",
			"binary blobs present in Xcode streamData; adapter missing",
			"build a safe parent-aware GTMioShaderBinaryData adapter or offline binary decoder; the nil-parent constructor path is unsafe")
	}
	if r.streamValueCount("Derived Counter Sample Data") > 0 {
		next := "decode Derived Counter Sample Data and map ALU utilization into dispatch timeline and pprof samples"
		if r.streamValueEntryCount("Derived Counters Info Data") == 0 {
			next = "decode Derived Counter Sample Data; counter info dictionary is empty in this trace, so names may need XRGPUAPSDataProcessor resolution"
		}
		r.updateGap("alu_utilization_pct",
			"derived counter samples present in Xcode streamData; adapter missing",
			next)
	}
	hasReplayerKey := false
	for _, value := range r.StreamData.SelectedValues {
		if value.Key == "ReplayerGPUTime" {
			hasReplayerKey = true
			break
		}
	}
	if r.StreamData.ReplayerGPUTimeNs > 0 {
		r.Timing["xcode_stream_replayer_gpu_time_ns"] = r.StreamData.ReplayerGPUTimeNs
		r.ClosedExamples = append(r.ClosedExamples, "ReplayerGPUTime decoded from Xcode streamData")
		return
	}
	if !hasReplayerKey {
		return
	}
	for i := range r.RemainingGaps {
		if r.RemainingGaps[i].Metric != "effective_gpu_time" {
			continue
		}
		r.RemainingGaps[i].Status = "archived as zero in Xcode streamData"
		r.RemainingGaps[i].Next = "keep command-buffer active time fallback; compare with a capture whose ReplayerGPUTime is nonzero"
		return
	}
}

func (r *xcodeParityReport) streamValueCount(key string) uint64 {
	if r == nil || r.StreamData == nil {
		return 0
	}
	var total uint64
	for _, value := range r.StreamData.SelectedValues {
		if value.Key != key {
			continue
		}
		if value.Count > 0 {
			total += value.Count
		} else {
			total++
		}
	}
	return total
}

func (r *xcodeParityReport) streamValueEntryCount(key string) uint64 {
	if r == nil || r.StreamData == nil {
		return 0
	}
	var total uint64
	for _, value := range r.StreamData.SelectedValues {
		if value.Key == key {
			total += value.Count
		}
	}
	return total
}

func (r *xcodeParityReport) updateGap(metric, status, next string) {
	for i := range r.RemainingGaps {
		if r.RemainingGaps[i].Metric != metric {
			continue
		}
		r.RemainingGaps[i].Status = status
		r.RemainingGaps[i].Next = next
		return
	}
}

func intFromMetrics(metrics map[string]interface{}, key string) int {
	switch v := metrics[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}

func boolFromMetrics(metrics map[string]interface{}, key string) bool {
	v, _ := metrics[key].(bool)
	return v
}

func stringSliceFromMetrics(metrics map[string]interface{}, key string) []string {
	switch v := metrics[key].(type) {
	case []string:
		return append([]string(nil), v...)
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func containsTrack(tracks []string, name string) bool {
	for _, track := range tracks {
		if len(track) >= len(name) && track[:len(name)] == name {
			return true
		}
	}
	return false
}

func stringsOrNone(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return fmt.Sprintf("%v", values)
}
