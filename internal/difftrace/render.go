package difftrace

import (
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"
)

// RenderText renders a human-readable report.
func RenderText(report Report, by string, showMatches, showUnmatched, showOccurrences, explain bool, limit int) string {
	if limit <= 0 {
		limit = 20
	}
	sections := parseViews(by)
	all := len(sections) == 0

	var b strings.Builder
	fmt.Fprintf(&b, "Trace A: %s\n", report.TraceAPath)
	fmt.Fprintf(&b, "Trace B: %s\n\n", report.TraceBPath)
	fmt.Fprintf(&b, "Total GPU delta (A-B): %+dus  |  A=%dus B=%dus\n", report.Summary.TotalDeltaUs, report.Summary.TotalGPUTimeAUs, report.Summary.TotalGPUTimeBUs)
	fmt.Fprintf(&b, "Dispatch delta (A-B): %+d      |  matched=%+dus unmatched=%+dus\n", report.Summary.DispatchCountDelta, report.Summary.MatchedDeltaUs, report.Summary.UnmatchedDeltaUs)
	fmt.Fprintf(&b, "Likely cause: %s\n", report.Summary.LikelyCause)
	if explain {
		fmt.Fprintf(&b, "Interpretation: Trace A is %+dus vs Trace B, with unmatched dispatch impact %+dus and dominant function-level shifts in the top contributors below.\n", report.Summary.TotalDeltaUs, report.Summary.UnmatchedDeltaUs)
	}
	if len(report.Warnings) > 0 {
		fmt.Fprintf(&b, "Warnings:\n")
		for _, w := range report.Warnings {
			fmt.Fprintf(&b, "  - %s\n", w)
		}
	}

	if all || sections["function"] {
		fmt.Fprintf(&b, "\nBy Function\n")
		fmt.Fprintf(&b, "%-52s %8s %8s %10s %10s %10s\n", "Function", "CountA", "CountB", "A(us)", "B(us)", "Delta")
		for i, f := range report.TopFunctionDeltas {
			if i >= limit {
				break
			}
			fmt.Fprintf(&b, "%-52s %8d %8d %10d %10d %+10d\n", truncate(f.FunctionName, 52), f.DispatchCountA, f.DispatchCountB, f.TotalAUs, f.TotalBUs, f.TotalDeltaUs)
		}
	}

	if all || sections["encoder"] {
		fmt.Fprintf(&b, "\nBy Encoder\n")
		fmt.Fprintf(&b, "%-10s %8s %8s %12s %12s %10s\n", "Encoder", "CountA", "CountB", "MatchedDelta", "UnmatchedCnt", "Unmatched")
		for i, e := range report.EncoderReports {
			if i >= limit {
				break
			}
			fmt.Fprintf(&b, "%-10d %8d %8d %+12d %12d %+10d\n", e.EncoderIndex, e.DispatchCountA, e.DispatchCountB, e.MatchedDeltaUs, e.UnmatchedCount, e.UnmatchedDeltaUs)
		}
	}

	if all || sections["pipeline"] {
		fmt.Fprintf(&b, "\nBy Pipeline\n")
		fmt.Fprintf(&b, "%-10s %-44s %8s %8s %10s\n", "Pipeline", "Function", "CountA", "CountB", "Delta")
		for i, p := range report.PipelineDeltas {
			if i >= limit {
				break
			}
			fmt.Fprintf(&b, "%-10d %-44s %8d %8d %+10d\n", p.PipelineID, truncate(p.FunctionName, 44), p.DispatchCountA, p.DispatchCountB, p.TotalDeltaUs)
		}
	}

	if all || sections["dispatch"] {
		fmt.Fprintf(&b, "\nTop Dispatch Outliers\n")
		fmt.Fprintf(&b, "%-7s %-7s %-7s %-9s %-44s %8s %8s %9s\n", "a_idx", "b_idx", "enc", "pipe(a)", "function", "a_us", "b_us", "delta")
		for i, m := range report.TopDispatchOutliers {
			if i >= limit {
				break
			}
			fmt.Fprintf(&b, "%-7d %-7d %-7d %-9d %-44s %8d %8d %+9d\n", m.SourceIndexA, m.SourceIndexB, m.EncoderIndex, m.PipelineIDA, truncate(safeFunctionName(m.FunctionName), 44), m.DurationAUs, m.DurationBUs, m.DeltaUs)
		}
	}

	if all || sections["occurrences"] || showOccurrences {
		fmt.Fprintf(&b, "\nPer-Occurrence Matches\n")
		fmt.Fprintf(&b, "%-44s %-7s %-7s %-7s %-7s %8s %8s %9s\n", "function", "occA", "occB", "a_idx", "b_idx", "left", "right", "delta")
		for i, m := range report.OccurrenceMatches {
			if i >= limit*4 {
				break
			}
			fmt.Fprintf(&b, "%-44s %-7d %-7d %-7d %-7d %8d %8d %+9d\n", truncate(m.FunctionName, 44), m.OccurrenceOrdinalA, m.OccurrenceOrdinalB, m.SourceIndexA, m.SourceIndexB, m.LeftUs, m.RightUs, m.DeltaUs)
		}
	}

	if all || sections["timeline-windows"] {
		fmt.Fprintf(&b, "\nTimeline Spike Windows\n")
		fmt.Fprintf(&b, "%-7s %-10s %-10s %-10s %-10s %-8s %-10s\n", "enc", "a_start", "a_end", "b_start", "b_end", "matches", "delta")
		for i, w := range report.TimelineSpikeWindows {
			if i >= limit {
				break
			}
			fmt.Fprintf(&b, "%-7d %-10d %-10d %-10d %-10d %-8d %+10d\n", w.EncoderIndex, w.StartSourceIndexA, w.EndSourceIndexA, w.StartSourceIndexB, w.EndSourceIndexB, w.MatchCount, w.TotalDeltaUs)
		}
	}

	if all || sections["unmatched"] {
		fmt.Fprintf(&b, "\nUnnamed Dispatch Deltas\n")
		fmt.Fprintf(&b, "%-10s %-24s %8s %8s %10s %10s %10s\n", "Pipeline", "KernelID", "CountA", "CountB", "A(us)", "B(us)", "Delta")
		for i, u := range report.UnnamedDispatchDeltas {
			if i >= limit {
				break
			}
			fmt.Fprintf(&b, "%-10d %-24s %8d %8d %10d %10d %+10d\n", u.PipelineID, truncate(u.KernelID, 24), u.DispatchCountA, u.DispatchCountB, u.TotalAUs, u.TotalBUs, u.TotalDeltaUs)
		}
	}

	if showMatches || sections["matches"] {
		fmt.Fprintf(&b, "\nMatched Dispatches\n")
		fmt.Fprintf(&b, "%-7s %-7s %-7s %-44s %8s %8s %9s %8s\n", "a_idx", "b_idx", "enc", "function", "a_us", "b_us", "delta", "conf")
		for i, m := range report.MatchedPairs {
			if i >= limit {
				break
			}
			fmt.Fprintf(&b, "%-7d %-7d %-7d %-44s %8d %8d %+9d %8.2f\n", m.SourceIndexA, m.SourceIndexB, m.EncoderIndex, truncate(safeFunctionName(m.FunctionName), 44), m.DurationAUs, m.DurationBUs, m.DeltaUs, m.Confidence)
		}
	}
	if showUnmatched || sections["unmatched"] {
		fmt.Fprintf(&b, "\nUnmatched Dispatches\n")
		fmt.Fprintf(&b, "%-6s %-7s %-7s %-10s %-24s %-24s %8s\n", "trace", "idx", "enc", "pipeline", "function", "kernel_id", "dur")
		for i, u := range report.Unmatched {
			if i >= limit*4 {
				break
			}
			fmt.Fprintf(&b, "%-6s %-7d %-7d %-10d %-24s %-24s %8d\n", u.Trace, u.SourceIndex, u.EncoderIndex, u.PipelineID, truncate(u.FunctionName, 24), truncate(u.KernelID, 24), u.DurationUs)
		}
	}

	return b.String()
}

// RenderCSV renders one report view as CSV.
func RenderCSV(report Report, by string, limit int) (string, error) {
	if by == "" {
		by = "function"
	}
	if limit <= 0 {
		limit = 20
	}
	var rows [][]string
	switch by {
	case "function":
		rows = append(rows, []string{"function_name", "dispatch_count_a", "dispatch_count_b", "dispatch_count_delta", "total_a_us", "total_b_us", "total_delta_us", "first_occurrence_delta_us", "max_occurrence_delta_us"})
		for i, f := range report.TopFunctionDeltas {
			if i >= limit {
				break
			}
			rows = append(rows, []string{f.FunctionName, itoa(f.DispatchCountA), itoa(f.DispatchCountB), itoa(f.DispatchCountDelta), itoa(f.TotalAUs), itoa(f.TotalBUs), itoa(f.TotalDeltaUs), itoa(f.FirstOccurrenceDeltaUs), itoa(f.MaxOccurrenceDeltaUs)})
		}
	case "encoder":
		rows = append(rows, []string{"encoder_index", "dispatch_count_a", "dispatch_count_b", "matched_count", "matched_delta_us", "unmatched_count_a", "unmatched_count_b", "unmatched_count", "unmatched_delta_us"})
		for i, e := range report.EncoderReports {
			if i >= limit {
				break
			}
			rows = append(rows, []string{itoa(e.EncoderIndex), itoa(e.DispatchCountA), itoa(e.DispatchCountB), itoa(e.MatchedCount), itoa(e.MatchedDeltaUs), itoa(e.UnmatchedCountA), itoa(e.UnmatchedCountB), itoa(e.UnmatchedCount), itoa(e.UnmatchedDeltaUs)})
		}
	case "pipeline":
		rows = append(rows, []string{"pipeline_id", "function_name", "dispatch_count_a", "dispatch_count_b", "dispatch_count_delta", "total_a_us", "total_b_us", "total_delta_us"})
		for i, p := range report.PipelineDeltas {
			if i >= limit {
				break
			}
			rows = append(rows, []string{itoa(p.PipelineID), p.FunctionName, itoa(p.DispatchCountA), itoa(p.DispatchCountB), itoa(p.DispatchCountDelta), itoa(p.TotalAUs), itoa(p.TotalBUs), itoa(p.TotalDeltaUs)})
		}
	case "timeline-windows":
		rows = append(rows, []string{"encoder_index", "start_source_index_a", "end_source_index_a", "start_source_index_b", "end_source_index_b", "match_count", "total_delta_us", "max_abs_delta_us"})
		for i, w := range report.TimelineSpikeWindows {
			if i >= limit {
				break
			}
			rows = append(rows, []string{itoa(w.EncoderIndex), itoa(w.StartSourceIndexA), itoa(w.EndSourceIndexA), itoa(w.StartSourceIndexB), itoa(w.EndSourceIndexB), itoa(w.MatchCount), itoa(w.TotalDeltaUs), itoa(w.MaxAbsDeltaUs)})
		}
	case "unmatched":
		rows = append(rows, []string{"trace", "source_index", "encoder_index", "pipeline_id", "function_name", "kernel_id", "pipeline_hash", "threadgroup_signature", "duration_us"})
		for i, u := range report.Unmatched {
			if i >= limit*4 {
				break
			}
			rows = append(rows, []string{u.Trace, itoa(u.SourceIndex), itoa(u.EncoderIndex), itoa(u.PipelineID), u.FunctionName, u.KernelID, u.PipelineHash, u.ThreadgroupSig, itoa(u.DurationUs)})
		}
	case "dispatch":
		rows = append(rows, []string{"source_index_a", "source_index_b", "encoder_index", "pipeline_id_a", "pipeline_id_b", "function_name", "kernel_id", "pipeline_hash_a", "pipeline_hash_b", "threadgroup_signature_a", "threadgroup_signature_b", "trace_a_us", "trace_b_us", "delta_us", "match_method", "confidence"})
		for i, m := range report.TopDispatchOutliers {
			if i >= limit {
				break
			}
			rows = append(rows, []string{itoa(m.SourceIndexA), itoa(m.SourceIndexB), itoa(m.EncoderIndex), itoa(m.PipelineIDA), itoa(m.PipelineIDB), m.FunctionName, m.KernelID, m.PipelineHashA, m.PipelineHashB, m.ThreadgroupSigA, m.ThreadgroupSigB, itoa(m.DurationAUs), itoa(m.DurationBUs), itoa(m.DeltaUs), m.MatchMethod, fmt.Sprintf("%.3f", m.Confidence)})
		}
	case "matches":
		rows = append(rows, []string{"source_index_a", "source_index_b", "encoder_index", "pipeline_id_a", "pipeline_id_b", "function_name", "kernel_id", "pipeline_hash_a", "pipeline_hash_b", "threadgroup_signature_a", "threadgroup_signature_b", "trace_a_us", "trace_b_us", "delta_us", "match_method", "confidence"})
		for i, m := range report.MatchedPairs {
			if i >= limit*4 {
				break
			}
			rows = append(rows, []string{itoa(m.SourceIndexA), itoa(m.SourceIndexB), itoa(m.EncoderIndex), itoa(m.PipelineIDA), itoa(m.PipelineIDB), m.FunctionName, m.KernelID, m.PipelineHashA, m.PipelineHashB, m.ThreadgroupSigA, m.ThreadgroupSigB, itoa(m.DurationAUs), itoa(m.DurationBUs), itoa(m.DeltaUs), m.MatchMethod, fmt.Sprintf("%.3f", m.Confidence)})
		}
	case "occurrences":
		rows = append(rows, []string{"function_name", "occurrence_ordinal_a", "occurrence_ordinal_b", "source_index_a", "source_index_b", "encoder_index", "pipeline_id_a", "pipeline_id_b", "left_us", "right_us", "delta_us", "match_method", "confidence"})
		for i, m := range report.OccurrenceMatches {
			if i >= limit*4 {
				break
			}
			rows = append(rows, []string{m.FunctionName, itoa(m.OccurrenceOrdinalA), itoa(m.OccurrenceOrdinalB), itoa(m.SourceIndexA), itoa(m.SourceIndexB), itoa(m.EncoderIndex), itoa(m.PipelineIDA), itoa(m.PipelineIDB), itoa(m.LeftUs), itoa(m.RightUs), itoa(m.DeltaUs), m.MatchMethod, fmt.Sprintf("%.3f", m.Confidence)})
		}
	default:
		return "", fmt.Errorf("unsupported --by view for csv: %s", by)
	}

	var b strings.Builder
	w := csv.NewWriter(&b)
	for _, row := range rows {
		if err := w.Write(row); err != nil {
			return "", err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return "", err
	}
	return b.String(), nil
}

func parseViews(by string) map[string]bool {
	by = strings.TrimSpace(by)
	if by == "" {
		return nil
	}
	out := map[string]bool{}
	for _, part := range strings.Split(by, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out[part] = true
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 3 {
		return s[:n]
	}
	return s[:n-3] + "..."
}

func itoa(v int) string {
	return strconv.Itoa(v)
}
