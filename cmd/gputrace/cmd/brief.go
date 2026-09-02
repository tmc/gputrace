package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace"
	"github.com/tmc/gputrace/internal/buildinfo"
	"github.com/tmc/gputrace/internal/difftrace"
)

type briefOptions struct {
	format      string
	tokenBudget int
	labelA      string
	labelB      string
}

var briefCmd = newBriefCommand(&briefOptions{format: "json", tokenBudget: 0})

func newBriefCommand(opts *briefOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "brief [trace_a trace_b]",
		Short: "Emit a compact comparison brief for optimization workflows",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBrief(cmd, args, opts)
		},
	}
	cmd.Flags().StringVar(&opts.format, "format", opts.format, "Output format: json, md")
	cmd.Flags().IntVar(&opts.tokenBudget, "token-budget", opts.tokenBudget, "Limit payload outliers to top N by abs_delta_us")
	cmd.Flags().StringVar(&opts.labelA, "label-a", opts.labelA, "Payload label for trace A")
	cmd.Flags().StringVar(&opts.labelB, "label-b", opts.labelB, "Payload label for trace B")
	return cmd
}

func init() {
	rootCmd.AddCommand(briefCmd)
}

func runBrief(cmd *cobra.Command, args []string, opts *briefOptions) error {
	format, err := normalizeBriefFormat(opts.format)
	if err != nil {
		return err
	}
	if opts.tokenBudget < 0 {
		return fmt.Errorf("--token-budget must be >= 0")
	}

	brief, err := buildBrief(args[0], args[1], *opts)
	if err != nil {
		return err
	}
	switch format {
	case "json":
		return writeBriefJSON(cmd.OutOrStdout(), brief)
	case "md":
		return writeBriefMarkdown(cmd.OutOrStdout(), brief)
	default:
		return fmt.Errorf("unsupported brief format %q", format)
	}
}

func normalizeBriefFormat(format string) (string, error) {
	switch strings.TrimSpace(format) {
	case "", "json":
		return "json", nil
	case "md", "markdown":
		return "md", nil
	default:
		return "", fmt.Errorf("invalid brief format %q (must be json or md)", format)
	}
}

type briefDocument struct {
	SchemaVersion string       `json:"schema_version"`
	Header        briefHeader  `json:"header"`
	Payload       briefPayload `json:"payload"`
}

type briefHeader struct {
	Contract    string      `json:"contract"`
	Units       briefUnits  `json:"units"`
	Legend      briefLegend `json:"legend"`
	FieldOrder  []string    `json:"field_order"`
	GeneratedBy string      `json:"generated_by"`
}

type briefUnits struct {
	Time   string `json:"time"`
	Memory string `json:"memory"`
}

type briefLegend struct {
	AbsDeltaUs     string `json:"abs_delta_us"`
	PipelineHash   string `json:"pipeline_hash"`
	StaticCounters string `json:"static_counters"`
}

type briefPayload struct {
	TraceA            briefTraceSummary              `json:"trace_a"`
	TraceB            briefTraceSummary              `json:"trace_b"`
	TotalDeltaUs      int                            `json:"total_delta_us"`
	DispatchDelta     int                            `json:"dispatch_delta"`
	EncoderDivergence difftrace.EncoderDivergence    `json:"encoder_divergence"`
	Outliers          []difftrace.PipelinePair       `json:"outliers"`
	InsightsA         []*gputrace.PerformanceInsight `json:"insights_a"`
	InsightsB         []*gputrace.PerformanceInsight `json:"insights_b"`
	Truncated         bool                           `json:"truncated"`
	DroppedCount      int                            `json:"dropped_count"`
}

type briefTraceSummary struct {
	Label                 string   `json:"label"`
	Path                  string   `json:"path"`
	TotalGPUUs            int      `json:"total_gpu_us"`
	Dispatches            int      `json:"dispatches"`
	ProfilerEncoders      int      `json:"profiler_encoders"`
	RawComputeEncoders    *int     `json:"raw_compute_encoders"`
	RawEncodersAvailable  bool     `json:"raw_compute_encoders_available"`
	RawEncodersSource     string   `json:"raw_compute_encoders_source"`
	Buffers               int      `json:"buffers"`
	BufferBytes           uint64   `json:"buffer_bytes"`
	CommandBufferActiveUs int      `json:"command_buffer_active_us,omitempty"`
	EffectiveGPUTimeUs    *int     `json:"effective_gpu_time_us,omitempty"`
	TimingSource          string   `json:"timing_source,omitempty"`
	AttributionLimited    bool     `json:"attribution_limited,omitempty"`
	Warnings              []string `json:"warnings,omitempty"`
}

func newBriefHeader() briefHeader {
	return briefHeader{
		Contract: "brief compares trace A (left) vs B (right); positive *_delta = A slower than B",
		Units: briefUnits{
			Time:   "us",
			Memory: "bytes",
		},
		Legend: briefLegend{
			AbsDeltaUs:     "abs(A_us - B_us) for the matched kernel",
			PipelineHash:   "per-side pipeline object hash; keyed by pipeline ID (gputrace 80d8c2b), so identical function+threadgroup now yields the SAME hash across processes — a delta here means genuinely different pipeline objects",
			StaticCounters: "per-pipeline static shader metrics (instructions/registers/loads/stores)",
		},
		FieldOrder: []string{
			"function",
			"threadgroup_sig",
			"a_us",
			"b_us",
			"abs_delta_us",
			"a_pipeline_id",
			"b_pipeline_id",
			"a_pipeline_hash",
			"b_pipeline_hash",
			"static_counter_delta",
		},
		GeneratedBy: fmt.Sprintf("gputrace brief v%s (schema 1)", buildinfo.EffectiveVersion()),
	}
}

func buildBrief(leftPath, rightPath string, opts briefOptions) (briefDocument, error) {
	left, err := loadBriefTrace(leftPath, opts.labelA)
	if err != nil {
		return briefDocument{}, fmt.Errorf("load trace A: %w", err)
	}
	right, err := loadBriefTrace(rightPath, opts.labelB)
	if err != nil {
		return briefDocument{}, fmt.Errorf("load trace B: %w", err)
	}

	aligned := difftrace.AlignDispatches(left.data, right.data, difftrace.AlignOptions{})
	report := difftrace.BuildReport(left.data, right.data, aligned, difftrace.ReportOptions{Limit: maxBriefOutliers(left.data, right.data)})
	outliers := difftrace.BuildPipelinePairs(left.data, right.data)
	sort.Slice(outliers, func(i, j int) bool {
		if outliers[i].AbsDeltaUs == outliers[j].AbsDeltaUs {
			return outliers[i].FunctionName < outliers[j].FunctionName
		}
		return outliers[i].AbsDeltaUs > outliers[j].AbsDeltaUs
	})

	budgeted := applyBriefTokenBudget(outliers, opts.tokenBudget)

	return briefDocument{
		SchemaVersion: "1",
		Header:        newBriefHeader(),
		Payload: briefPayload{
			TraceA:            left.summary,
			TraceB:            right.summary,
			TotalDeltaUs:      report.Summary.TotalDeltaUs,
			DispatchDelta:     report.Summary.DispatchCountDelta,
			EncoderDivergence: difftrace.AnalyzeEncoderDivergence(left.data.Encoders, right.data.Encoders, 20),
			Outliers:          budgeted.outliers,
			InsightsA:         left.insights,
			InsightsB:         right.insights,
			Truncated:         budgeted.truncated,
			DroppedCount:      budgeted.dropped,
		},
	}, nil
}

type briefOutlierBudget struct {
	outliers  []difftrace.PipelinePair
	truncated bool
	dropped   int
}

func applyBriefTokenBudget(outliers []difftrace.PipelinePair, tokenBudget int) briefOutlierBudget {
	if tokenBudget <= 0 || len(outliers) <= tokenBudget {
		return briefOutlierBudget{outliers: append([]difftrace.PipelinePair(nil), outliers...)}
	}
	return briefOutlierBudget{
		outliers:  append([]difftrace.PipelinePair(nil), outliers[:tokenBudget]...),
		truncated: true,
		dropped:   len(outliers) - tokenBudget,
	}
}

type briefTraceData struct {
	data     *difftrace.TraceData
	summary  briefTraceSummary
	insights []*gputrace.PerformanceInsight
}

func loadBriefTrace(path, label string) (briefTraceData, error) {
	if err := checkTraceFile(path); err != nil {
		return briefTraceData{}, err
	}
	data, err := difftrace.LoadTraceData(path, -1, (*regexp.Regexp)(nil))
	if err != nil {
		return briefTraceData{}, err
	}
	if label == "" {
		label = filepath.Base(path)
	}
	data.Label = label

	trace, err := gputrace.Open(path)
	if err != nil {
		return briefTraceData{}, fmt.Errorf("open trace: %w", err)
	}
	defer trace.Close()

	buffers, bytes, err := briefBufferSummary(path, trace)
	if err != nil {
		return briefTraceData{}, fmt.Errorf("summarize buffers: %w", err)
	}
	rawComputeEncoders, rawEncodersAvailable, rawEncodersSource := inspectRawComputeEncoders(trace)
	insights, err := gputrace.GenerateInsights(trace)
	if err != nil {
		return briefTraceData{}, fmt.Errorf("generate insights: %w", err)
	}

	return briefTraceData{
		data: data,
		summary: briefTraceSummary{
			Label:                 label,
			Path:                  path,
			TotalGPUUs:            totalBriefGPUUs(data.Dispatches),
			Dispatches:            len(data.Dispatches),
			ProfilerEncoders:      len(data.Encoders),
			RawComputeEncoders:    rawComputeEncoders,
			RawEncodersAvailable:  rawEncodersAvailable,
			RawEncodersSource:     rawEncodersSource,
			Buffers:               buffers,
			BufferBytes:           bytes,
			CommandBufferActiveUs: data.CommandBufferActiveUs,
			EffectiveGPUTimeUs:    data.EffectiveGPUTimeUs,
			TimingSource:          data.TimingSource,
			AttributionLimited:    data.AttributionLimited,
			Warnings:              append([]string(nil), data.Warnings...),
		},
		insights: insights.Insights,
	}, nil
}

func inspectRawComputeEncoders(trace *gputrace.Trace) (*int, bool, string) {
	count := trace.InspectComputeEncoderCount()
	if !count.Available {
		return nil, false, count.Source
	}
	n := count.Count
	return &n, true, count.Source
}

func briefBufferSummary(path string, trace *gputrace.Trace) (int, uint64, error) {
	inventory, err := extractBufferResourceInventory(path, trace)
	if err == nil && inventory.FinalBuffers > 0 {
		return inventory.FinalBuffers, inventory.FinalBytes, nil
	}
	buffers, fallbackErr := extractBufferInfo(path, trace, false)
	if fallbackErr != nil {
		if err != nil {
			return 0, 0, err
		}
		return 0, 0, fallbackErr
	}
	var bytes uint64
	for _, b := range buffers {
		bytes += b.Size
	}
	return len(buffers), bytes, nil
}

func totalBriefGPUUs(dispatches []difftrace.Dispatch) int {
	total := 0
	for _, d := range dispatches {
		total += d.DurationUs
	}
	return total
}

func maxBriefOutliers(a, b *difftrace.TraceData) int {
	n := len(a.Dispatches) + len(b.Dispatches)
	if n < 20 {
		return 20
	}
	return n
}

func writeBriefJSON(w io.Writer, brief briefDocument) error {
	data, err := json.MarshalIndent(brief, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal brief json: %w", err)
	}
	data = append(data, '\n')
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("write brief json: %w", err)
	}
	return nil
}

func writeBriefMarkdown(w io.Writer, brief briefDocument) error {
	a, b := brief.Payload.TraceA, brief.Payload.TraceB
	_, err := fmt.Fprintf(w, "# gputrace brief\n\n%s.\n\n", brief.Header.Contract)
	if err != nil {
		return fmt.Errorf("write brief markdown: %w", err)
	}
	if _, err := fmt.Fprintf(w, "| | Trace A | Trace B |\n|---|---:|---:|\n"+
		"| Label | `%s` | `%s` |\n"+
		"| Path | `%s` | `%s` |\n"+
		"| Dispatch span | %d µs | %d µs |\n"+
		"| Dispatches | %d | %d |\n"+
		"| Profiler encoders | %d | %d |\n"+
		"| Command-buffer active time | %s | %s |\n"+
		"| Xcode Effective GPU Time | %s | %s |\n\n",
		a.Label, b.Label, a.Path, b.Path,
		a.TotalGPUUs, b.TotalGPUUs, a.Dispatches, b.Dispatches,
		a.ProfilerEncoders, b.ProfilerEncoders,
		formatBriefMicros(a.CommandBufferActiveUs, false), formatBriefMicros(b.CommandBufferActiveUs, false),
		formatBriefOptionalMicros(a.EffectiveGPUTimeUs), formatBriefOptionalMicros(b.EffectiveGPUTimeUs)); err != nil {
		return fmt.Errorf("write brief markdown summary: %w", err)
	}
	if a.TimingSource != "" || b.TimingSource != "" {
		fmt.Fprintf(w, "Timing sources: A `%s`; B `%s`.\n\n", briefValueOrUnavailable(a.TimingSource), briefValueOrUnavailable(b.TimingSource))
	}
	if a.AttributionLimited || b.AttributionLimited {
		fmt.Fprintln(w, "> Attribution warning: per-dispatch values are cumulative-offset deltas and may include boundary or gap time. Function outliers are hypotheses, not direct device-duration measurements.")
		fmt.Fprintln(w)
	}
	for _, warning := range uniqueBriefWarnings(a.Warnings, b.Warnings) {
		fmt.Fprintf(w, "- Warning: %s\n", warning)
	}
	if len(a.Warnings)+len(b.Warnings) > 0 {
		fmt.Fprintln(w)
	}
	fmt.Fprintf(w, "Dispatch-span delta (A-B): **%+d µs**\n\n## Top attributed outliers\n\n", brief.Payload.TotalDeltaUs)
	for _, outlier := range brief.Payload.Outliers {
		if _, err := fmt.Fprintf(w, "- `%s` `%s`: %d µs vs %d µs, absolute delta %d µs\n", outlier.FunctionName, outlier.ThreadgroupSig, outlier.AUs, outlier.BUs, outlier.AbsDeltaUs); err != nil {
			return fmt.Errorf("write brief markdown: %w", err)
		}
	}
	if brief.Payload.Truncated {
		fmt.Fprintf(w, "\n_Outliers truncated: %d additional rows omitted by `--token-budget`._\n", brief.Payload.DroppedCount)
	}
	return nil
}

func formatBriefMicros(value int, zeroAvailable bool) string {
	if value == 0 && !zeroAvailable {
		return "unavailable"
	}
	return fmt.Sprintf("%d µs", value)
}

func formatBriefOptionalMicros(value *int) string {
	if value == nil {
		return "unavailable"
	}
	return formatBriefMicros(*value, true)
}

func briefValueOrUnavailable(value string) string {
	if value == "" {
		return "unavailable"
	}
	return value
}

func uniqueBriefWarnings(groups ...[]string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, group := range groups {
		for _, warning := range group {
			if warning == "" || seen[warning] {
				continue
			}
			seen[warning] = true
			out = append(out, warning)
		}
	}
	return out
}
