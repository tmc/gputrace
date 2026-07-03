package difftrace

import (
	"fmt"
	"os"
	"strings"
)

// RenderQuick renders the quick triage report.
func RenderQuick(report Report, limit int) string {
	if limit <= 0 {
		limit = 10
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Quick Triage\n")
	fmt.Fprintf(&b, "Trace A: %s\n", report.TraceAPath)
	fmt.Fprintf(&b, "Trace B: %s\n", report.TraceBPath)
	fmt.Fprintf(&b, "Total GPU delta (matched common work): %+dus\n", report.Summary.MatchedDeltaUs)
	fmt.Fprintf(&b, "Total GPU delta (all dispatches): %+dus  (A=%dus B=%dus)\n", report.Summary.TotalDeltaUs, report.Summary.TotalGPUTimeAUs, report.Summary.TotalGPUTimeBUs)
	fmt.Fprintf(&b, "Structural/unmatched delta: %+dus\n", report.Summary.UnmatchedDeltaUs)
	fmt.Fprintf(&b, "Dispatch delta (A-B): %+d\n", report.Summary.DispatchCountDelta)

	fmt.Fprintf(&b, "\nTop Function Deltas\n")
	fmt.Fprintf(&b, "%-52s %8s %8s %10s\n", "function", "countA", "countB", "delta_us")
	for i, f := range report.TopFunctionDeltas {
		if i >= limit {
			break
		}
		fmt.Fprintf(&b, "%-52s %8d %8d %+10d\n", truncate(f.FunctionName, 52), f.DispatchCountA, f.DispatchCountB, f.TotalDeltaUs)
	}

	fmt.Fprintf(&b, "\nTop Dispatch Outliers\n")
	fmt.Fprintf(&b, "%-7s %-7s %-7s %-8s %-8s %-40s %8s %8s %9s\n", "left", "right", "enc", "pipe_l", "pipe_r", "function", "left_us", "right_us", "delta")
	for i, m := range report.TopDispatchOutliers {
		if i >= limit {
			break
		}
		fmt.Fprintf(&b, "%-7d %-7d %-7d %-8d %-8d %-40s %8d %8d %+9d\n", m.SourceIndexA, m.SourceIndexB, m.EncoderIndex, m.PipelineIDA, m.PipelineIDB, truncate(safeFunctionName(m.FunctionName), 40), m.DurationAUs, m.DurationBUs, m.DeltaUs)
	}

	fmt.Fprintf(&b, "\nUnnamed Dispatch Summary\n")
	fmt.Fprintf(&b, "%-10s %-24s %8s %8s %10s %10s %10s\n", "pipeline", "kernel_id", "countA", "countB", "left_us", "right_us", "delta")
	for i, u := range report.UnnamedDispatchDeltas {
		if i >= limit {
			break
		}
		fmt.Fprintf(&b, "%-10d %-24s %8d %8d %10d %10d %+10d\n", u.PipelineID, truncate(u.KernelID, 24), u.DispatchCountA, u.DispatchCountB, u.TotalAUs, u.TotalBUs, u.TotalDeltaUs)
	}

	fmt.Fprintf(&b, "\nSpike Windows\n")
	fmt.Fprintf(&b, "%-7s %-10s %-10s %-10s %-10s %-10s\n", "enc", "start_idx", "end_idx", "right_start", "right_end", "cum_delta")
	for i, w := range report.TimelineSpikeWindows {
		if i >= limit {
			break
		}
		fmt.Fprintf(&b, "%-7d %-10d %-10d %-10d %-10d %+10d\n", w.EncoderIndex, w.StartSourceIndexA, w.EndSourceIndexA, w.StartSourceIndexB, w.EndSourceIndexB, w.TotalDeltaUs)
	}
	return b.String()
}

// RenderEncoderFocus renders encoder-dominance diagnostics.
func RenderEncoderFocus(report Report, limit int) string {
	if limit <= 0 {
		limit = 20
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Encoder Focus\n")
	fmt.Fprintf(&b, "%-10s %8s %8s %12s %12s %10s\n", "encoder", "countA", "countB", "matched_delta", "unmatched_cnt", "unmatched")
	totalAbs := 0
	for _, e := range report.EncoderReports {
		totalAbs += absInt(e.MatchedDeltaUs)
	}
	for i, e := range report.EncoderReports {
		if i >= limit {
			break
		}
		fmt.Fprintf(&b, "%-10d %8d %8d %+12d %12d %+10d\n", e.EncoderIndex, e.DispatchCountA, e.DispatchCountB, e.MatchedDeltaUs, e.UnmatchedCount, e.UnmatchedDeltaUs)
		for j, m := range e.TopDispatches {
			if j >= 3 {
				break
			}
			fmt.Fprintf(&b, "  top %-2d a=%-6d b=%-6d pipe=%-6d fn=%-28s delta=%+7d\n", j+1, m.SourceIndexA, m.SourceIndexB, m.PipelineIDA, truncate(safeFunctionName(m.FunctionName), 28), m.DeltaUs)
		}
	}
	if len(report.EncoderReports) > 0 {
		top := report.EncoderReports[0]
		share := 0.0
		if totalAbs > 0 {
			share = float64(absInt(top.MatchedDeltaUs)) * 100 / float64(totalAbs)
		}
		dominance := "does not dominate"
		if share >= 60 {
			dominance = "dominates"
		}
		fmt.Fprintf(&b, "\nDominant encoder: %d (%+dus matched, %.1f%% of matched encoder delta) -> %s\n", top.EncoderIndex, top.MatchedDeltaUs, share, dominance)
	}
	return b.String()
}

// RenderEncoderDivergence renders encoder timing divergence diagnostics.
func RenderEncoderDivergence(div EncoderDivergence) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Encoder Divergence\n")
	fmt.Fprintf(&b, "Threshold: %dus\n", div.ThresholdUs)
	fmt.Fprintf(&b, "First divergent index: %d\n", div.FirstDivergentIndex)
	fmt.Fprintf(&b, "Tail slope A: %.1f us/encoder\n", div.TailSlopeAUsPerEncoder)
	fmt.Fprintf(&b, "Tail slope B: %.1f us/encoder\n", div.TailSlopeBUsPerEncoder)
	return b.String()
}

// RenderMarkdown renders a markdown report for sharing.
func RenderMarkdown(report Report, limit int) string {
	if limit <= 0 {
		limit = 20
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# gputrace diff report\n\n")
	fmt.Fprintf(&b, "- Trace A: `%s`\n", report.TraceAPath)
	fmt.Fprintf(&b, "- Trace B: `%s`\n", report.TraceBPath)
	fmt.Fprintf(&b, "- Total GPU delta (A-B): `%+dus` (A=`%dus`, B=`%dus`)\n", report.Summary.TotalDeltaUs, report.Summary.TotalGPUTimeAUs, report.Summary.TotalGPUTimeBUs)
	fmt.Fprintf(&b, "- Dispatch delta (A-B): `%+d`\n", report.Summary.DispatchCountDelta)
	fmt.Fprintf(&b, "- Likely cause: `%s`\n\n", report.Summary.LikelyCause)

	fmt.Fprintf(&b, "## Top Function Deltas\n\n")
	fmt.Fprintf(&b, "| Function | Count A | Count B | Delta us |\n")
	fmt.Fprintf(&b, "|---|---:|---:|---:|\n")
	for i, f := range report.TopFunctionDeltas {
		if i >= limit {
			break
		}
		fmt.Fprintf(&b, "| `%s` | %d | %d | %+d |\n", escapeCell(f.FunctionName), f.DispatchCountA, f.DispatchCountB, f.TotalDeltaUs)
	}

	fmt.Fprintf(&b, "\n## Top Dispatch Outliers\n\n")
	fmt.Fprintf(&b, "| left_idx | right_idx | encoder_index | pipeline_left | pipeline_right | function | left_us | right_us | delta_us |\n")
	fmt.Fprintf(&b, "|---:|---:|---:|---:|---:|---|---:|---:|---:|\n")
	for i, m := range report.TopDispatchOutliers {
		if i >= limit {
			break
		}
		fmt.Fprintf(&b, "| %d | %d | %d | %d | %d | `%s` | %d | %d | %+d |\n", m.SourceIndexA, m.SourceIndexB, m.EncoderIndex, m.PipelineIDA, m.PipelineIDB, escapeCell(safeFunctionName(m.FunctionName)), m.DurationAUs, m.DurationBUs, m.DeltaUs)
	}

	fmt.Fprintf(&b, "\n## Spike Windows\n\n")
	fmt.Fprintf(&b, "| encoder_index | start_idx | end_idx | right_start | right_end | cumulative_delta_us |\n")
	fmt.Fprintf(&b, "|---:|---:|---:|---:|---:|---:|\n")
	for i, w := range report.TimelineSpikeWindows {
		if i >= limit {
			break
		}
		fmt.Fprintf(&b, "| %d | %d | %d | %d | %d | %+d |\n", w.EncoderIndex, w.StartSourceIndexA, w.EndSourceIndexA, w.StartSourceIndexB, w.EndSourceIndexB, w.TotalDeltaUs)
	}

	fmt.Fprintf(&b, "\n## Unnamed Dispatch Deltas\n\n")
	fmt.Fprintf(&b, "| pipeline_id | kernel_id | count_a | count_b | left_us | right_us | delta_us |\n")
	fmt.Fprintf(&b, "|---:|---|---:|---:|---:|---:|---:|\n")
	for i, u := range report.UnnamedDispatchDeltas {
		if i >= limit {
			break
		}
		fmt.Fprintf(&b, "| %d | `%s` | %d | %d | %d | %d | %+d |\n", u.PipelineID, escapeCell(u.KernelID), u.DispatchCountA, u.DispatchCountB, u.TotalAUs, u.TotalBUs, u.TotalDeltaUs)
	}
	return b.String()
}

// WriteMarkdown writes markdown report to path.
func WriteMarkdown(path string, report Report, limit int) error {
	text := RenderMarkdown(report, limit)
	return os.WriteFile(path, []byte(text), 0o644)
}

func escapeCell(s string) string {
	return strings.ReplaceAll(s, "|", `\|`)
}
