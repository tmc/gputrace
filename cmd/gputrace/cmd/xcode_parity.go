//go:build darwin

package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tmc/gputrace"
	"github.com/tmc/gputrace/internal/counter"
	"github.com/tmc/gputrace/internal/xcodebindings"
)

type xcodeParityOptions struct {
	json bool
}

var xcodeParityCmd = newXcodeParityCommand(&xcodeParityOptions{})

func newXcodeParityCommand(opts *xcodeParityOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "xcode-parity <trace.gputrace>",
		Short: "Audit Xcode metric parity for a trace",
		Long: `Audit Xcode metric parity for a trace.

The report compares the trace's timeline metadata against the private
GTShaderProfiler binding surface and lists the remaining adapter work for any
missing Xcode-style fields.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runXcodeParity(cmd, args, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.json, "json", false, "Output in JSON format")
	return cmd
}

type xcodeParityReport struct {
	Trace           string                           `json:"trace"`
	KernelEvents    int                              `json:"kernel_events"`
	PresentFields   []string                         `json:"present_fields"`
	AbsentFields    []string                         `json:"absent_fields"`
	CounterTracks   []string                         `json:"counter_tracks"`
	EmptyTracks     []string                         `json:"empty_tracks"`
	Timing          map[string]interface{}           `json:"timing"`
	TraceCounts     *xcodeParityTraceCounts          `json:"trace_counts,omitempty"`
	Bindings        map[string]int                   `json:"bindings"`
	StreamData      *xcodebindings.StreamDataSummary `json:"stream_data,omitempty"`
	FeatureCoverage []xcodeParityFeature             `json:"feature_coverage,omitempty"`
	ReportingDeltas []xcodeParityDelta               `json:"reporting_deltas,omitempty"`
	RemainingGaps   []xcodeParityGap                 `json:"remaining_gaps"`
	ClosedExamples  []string                         `json:"closed_examples,omitempty"`
}

type xcodeParityTraceCounts struct {
	RawComputeEncoders int `json:"raw_compute_encoders,omitempty"`
}

type xcodeParityDelta struct {
	Metric string `json:"metric"`
	Trace  int    `json:"trace"`
	Xcode  int    `json:"xcode"`
	Status string `json:"status"`
}

type xcodeParityFeature struct {
	Feature  string `json:"feature"`
	Status   string `json:"status"`
	Evidence string `json:"evidence,omitempty"`
	Next     string `json:"next,omitempty"`
}

type xcodeParityGap struct {
	Metric  string `json:"metric"`
	Binding string `json:"binding"`
	Status  string `json:"status"`
	Next    string `json:"next"`
}

func init() {
	rootCmd.AddCommand(xcodeParityCmd)
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
	report.applyTraceCounts(xcodeParityTraceCounts{
		RawComputeEncoders: countRawComputeEncodersForParity(args[0]),
	})
	report.refreshFeatureCoverage()
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
	if len(report.ReportingDeltas) > 0 {
		fmt.Fprintln(w, "\nReporting deltas")
		for _, delta := range report.ReportingDeltas {
			fmt.Fprintf(w, "  %s: trace=%d xcode=%d (%s)\n", delta.Metric, delta.Trace, delta.Xcode, delta.Status)
		}
	}
	if len(report.FeatureCoverage) > 0 {
		fmt.Fprintln(w, "\nFeature coverage")
		for _, feature := range report.FeatureCoverage {
			fmt.Fprintf(w, "  %s: %s", feature.Feature, feature.Status)
			if feature.Evidence != "" {
				fmt.Fprintf(w, " (%s)", feature.Evidence)
			}
			fmt.Fprintln(w)
			if feature.Next != "" {
				fmt.Fprintf(w, "    next: %s\n", feature.Next)
			}
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
	report.FeatureCoverage = featureCoverageFromMetrics(metrics)
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

func countRawComputeEncodersForParity(tracePath string) int {
	trace, err := gputrace.Open(tracePath)
	if err != nil {
		return 0
	}
	defer trace.Close()
	n, err := trace.CountComputeEncoders()
	if err != nil {
		return 0
	}
	return n
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

func (r *xcodeParityReport) applyTraceCounts(counts xcodeParityTraceCounts) {
	if r == nil {
		return
	}
	r.TraceCounts = &counts
	if r.StreamData == nil || counts.RawComputeEncoders == 0 || r.StreamData.EncoderInfoCount == 0 {
		return
	}
	traceEncoders := counts.RawComputeEncoders
	xcodeEncoders := int(r.StreamData.EncoderInfoCount)
	if traceEncoders == xcodeEncoders {
		return
	}
	r.ReportingDeltas = append(r.ReportingDeltas, xcodeParityDelta{
		Metric: "compute_encoders",
		Trace:  traceEncoders,
		Xcode:  xcodeEncoders,
		Status: "raw trace compute encoders differ from Xcode profiler encoderInfoCount; Xcode reports coalesced profiler encoders",
	})
}

func featureCoverageFromMetrics(metrics map[string]interface{}) []xcodeParityFeature {
	present := make(map[string]bool)
	for _, field := range stringSliceFromMetrics(metrics, "kernel_arg_fields") {
		present[field] = true
	}
	tracks := stringSliceFromMetrics(metrics, "counter_tracks")
	kernelEvents := intFromMetrics(metrics, "kernel_events")
	features := []xcodeParityFeature{
		featureFromBool("shader_table.dispatch_rows", kernelEvents > 0,
			fmt.Sprintf("%d kernel events", kernelEvents),
			"produce kernel events from capture or profiler streamData"),
		featureFromBool("shader_table.cost", present["xcode_cost_pct"] || present["profiling_cost_pct"],
			presentEvidence(present, "xcode_cost_pct", "profiling_cost_pct"),
			"attach Xcode cost or profiling cost to kernel rows"),
		featureFromBool("shader_table.simd_groups", present["simd_groups"],
			presentEvidence(present, "simd_groups"),
			"decode SIMD group count for kernel rows"),
		featureFromBool("shader_table.register_footprint", present["allocated_registers"] && present["uniform_registers"],
			presentEvidence(present, "allocated_registers", "uniform_registers"),
			"decode allocated and uniform register counts"),
		featureFromBool("shader_table.high_register", present["high_register"],
			presentEvidence(present, "high_register"),
			"decode high register from shader binary live-register data"),
		featureFromBool("shader_table.memory", present["spilled_bytes"] && present["threadgroup_memory"],
			presentEvidence(present, "spilled_bytes", "threadgroup_memory"),
			"decode spill and threadgroup memory for kernel rows"),
		featureFromBool("shader_table.instructions", present["instruction_count"],
			presentEvidence(present, "instruction_count"),
			"decode instruction count for kernel rows"),
		featureFromBool("shader_table.occupancy", present["occupancy_pct"],
			presentEvidence(present, "occupancy_pct"),
			"map Xcode derived occupancy counters to kernel rows"),
		featureFromBool("shader_table.alu_utilization", present["alu_utilization_pct"],
			presentEvidence(present, "alu_utilization_pct"),
			"map Xcode ALU utilization counters to kernel rows"),
		featureFromBool("pipeline_identity", present["pipeline_id"] && present["pipeline_state"],
			presentEvidence(present, "pipeline_id", "pipeline_state"),
			"attach pipeline identity and state to kernel rows"),
		featureFromBool("counter_tracks.alu_utilization", containsTrack(tracks, "ALU Utilization"),
			trackEvidence(tracks, "ALU Utilization"),
			"populate ALU utilization counter track"),
		featureFromBool("counter_tracks.occupancy", containsTrack(tracks, "Occupancy"),
			trackEvidence(tracks, "Occupancy"),
			"populate occupancy counter track"),
	}
	if boolFromMetrics(metrics, "has_effective_gpu_time") {
		features = append(features, xcodeParityFeature{
			Feature:  "timing.effective_gpu_time",
			Status:   "covered",
			Evidence: "timeline effective GPU time",
		})
	} else {
		features = append(features, xcodeParityFeature{
			Feature: "timing.effective_gpu_time",
			Status:  "missing",
			Next:    "capture or decode nonzero APSTimelineData ReplayerGPUTime",
		})
	}
	sort.Slice(features, func(i, j int) bool {
		return features[i].Feature < features[j].Feature
	})
	return features
}

func (r *xcodeParityReport) refreshFeatureCoverage() {
	if r == nil {
		return
	}
	r.upsertFeature(featureFromBool("stream_data.encoder_info", r.StreamData != nil && r.StreamData.EncoderInfoCount > 0,
		uintEvidence(r.StreamData, func(s *xcodebindings.StreamDataSummary) uint64 { return s.EncoderInfoCount }, "encoderInfo records"),
		"load GTShaderProfiler streamData encoderInfoData"))
	r.upsertFeature(featureFromBool("stream_data.gpu_commands", r.StreamData != nil && r.StreamData.GPUCommandInfoCount > 0,
		uintEvidence(r.StreamData, func(s *xcodebindings.StreamDataSummary) uint64 { return s.GPUCommandInfoCount }, "gpuCommandInfo records"),
		"load GTShaderProfiler streamData gpuCommandInfoData"))
	r.upsertFeature(featureFromBool("stream_data.pipeline_states", r.StreamData != nil && r.StreamData.PipelineStateInfoCount > 0,
		uintEvidence(r.StreamData, func(s *xcodebindings.StreamDataSummary) uint64 { return s.PipelineStateInfoCount }, "pipelineStateInfo records"),
		"load GTShaderProfiler streamData pipelineStateInfoData"))
	r.upsertFeature(featureFromBool("stream_data.functions", r.StreamData != nil && r.StreamData.FunctionInfoCount > 0,
		uintEvidence(r.StreamData, func(s *xcodebindings.StreamDataSummary) uint64 { return s.FunctionInfoCount }, "functionInfo records"),
		"load GTShaderProfiler streamData functionInfoData"))
	if r.streamValueCount("Binaries") > 0 {
		r.upsertFeature(xcodeParityFeature{
			Feature:  "shader_table.high_register",
			Status:   "partial",
			Evidence: fmt.Sprintf("%d binary entries", r.streamValueCount("Binaries")),
			Next:     "decode high register from shader binary live-register data",
		})
		r.upsertFeature(xcodeParityFeature{
			Feature:  "stream_data.shader_binaries",
			Status:   "partial",
			Evidence: fmt.Sprintf("%d binary entries", r.streamValueCount("Binaries")),
			Next:     "map binary entries into high-register decoding",
		})
	} else {
		r.upsertFeature(xcodeParityFeature{
			Feature: "stream_data.shader_binaries",
			Status:  "missing",
			Next:    "capture or decode shader binary payloads",
		})
	}
	if r.streamValueCount("Derived Counter Sample Data") > 0 {
		r.upsertFeature(xcodeParityFeature{
			Feature:  "stream_data.derived_counter_samples",
			Status:   "partial",
			Evidence: fmt.Sprintf("%d derived counter sample groups", r.streamValueCount("Derived Counter Sample Data")),
			Next:     "decode derived counter sample buffers into named counter series",
		})
	} else {
		r.upsertFeature(xcodeParityFeature{
			Feature: "stream_data.derived_counter_samples",
			Status:  "missing",
			Next:    "capture or decode Xcode derived counter sample data",
		})
	}
	if r.TraceCounts != nil && r.TraceCounts.RawComputeEncoders > 0 && r.StreamData != nil && r.StreamData.EncoderInfoCount > 0 {
		status := "covered"
		if r.TraceCounts.RawComputeEncoders != int(r.StreamData.EncoderInfoCount) {
			status = "partial"
		}
		r.upsertFeature(xcodeParityFeature{
			Feature:  "encoder_counts",
			Status:   status,
			Evidence: fmt.Sprintf("raw=%d xcode=%d", r.TraceCounts.RawComputeEncoders, r.StreamData.EncoderInfoCount),
			Next:     nextIf(status == "partial", "document or reconcile raw compute encoder count against Xcode coalesced profiler encoder count"),
		})
	}
	if r.StreamData == nil {
		r.markProfilerFeaturesUnverified()
	}
	sort.Slice(r.FeatureCoverage, func(i, j int) bool {
		return r.FeatureCoverage[i].Feature < r.FeatureCoverage[j].Feature
	})
}

func (r *xcodeParityReport) markProfilerFeaturesUnverified() {
	for i := range r.FeatureCoverage {
		if !profilerBackedFeature(r.FeatureCoverage[i].Feature) || r.FeatureCoverage[i].Status != "missing" {
			continue
		}
		r.FeatureCoverage[i].Status = "unverified"
		r.FeatureCoverage[i].Evidence = "no profiler streamData"
		if r.FeatureCoverage[i].Next == "" {
			r.FeatureCoverage[i].Next = "rerun on a trace with embedded Xcode performance data"
		}
	}
}

func profilerBackedFeature(feature string) bool {
	switch feature {
	case "shader_table.cost",
		"shader_table.register_footprint",
		"shader_table.high_register",
		"shader_table.memory",
		"shader_table.instructions",
		"shader_table.occupancy",
		"shader_table.alu_utilization",
		"pipeline_identity",
		"counter_tracks.alu_utilization",
		"counter_tracks.occupancy",
		"timing.effective_gpu_time":
		return true
	default:
		return strings.HasPrefix(feature, "stream_data.")
	}
}

func featureFromBool(feature string, ok bool, evidence, next string) xcodeParityFeature {
	if ok {
		return xcodeParityFeature{Feature: feature, Status: "covered", Evidence: evidence}
	}
	return xcodeParityFeature{Feature: feature, Status: "missing", Next: next}
}

func presentEvidence(present map[string]bool, fields ...string) string {
	var got []string
	for _, field := range fields {
		if present[field] {
			got = append(got, field)
		}
	}
	return stringsOrNone(got)
}

func trackEvidence(tracks []string, name string) string {
	for _, track := range tracks {
		if strings.Contains(track, name) {
			return track
		}
	}
	return ""
}

func uintEvidence(summary *xcodebindings.StreamDataSummary, value func(*xcodebindings.StreamDataSummary) uint64, suffix string) string {
	if summary == nil {
		return ""
	}
	n := value(summary)
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%d %s", n, suffix)
}

func nextIf(ok bool, next string) string {
	if ok {
		return next
	}
	return ""
}

func (r *xcodeParityReport) upsertFeature(feature xcodeParityFeature) {
	for i := range r.FeatureCoverage {
		if r.FeatureCoverage[i].Feature == feature.Feature {
			r.FeatureCoverage[i] = feature
			return
		}
	}
	r.FeatureCoverage = append(r.FeatureCoverage, feature)
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
