package difftrace

// QuickReport is the compact JSON shape for diff --quick --json.
type QuickReport struct {
	SchemaVersion         string                 `json:"schema_version"`
	TraceAPath            string                 `json:"trace_a_path"`
	TraceBPath            string                 `json:"trace_b_path"`
	Summary               Summary                `json:"summary"`
	TopFunctionDeltas     []FunctionDelta        `json:"top_function_deltas"`
	TopDispatchOutliers   []MatchPair            `json:"top_dispatch_outliers"`
	UnnamedDispatchDeltas []UnnamedDispatchDelta `json:"unnamed_dispatch_deltas"`
	TimelineSpikeWindows  []SpikeWindow          `json:"timeline_spike_windows"`
	Warnings              []string               `json:"warnings,omitempty"`
}

// NewQuickReport returns the compact report used by quick text output.
func NewQuickReport(report Report, limit int) QuickReport {
	if limit <= 0 {
		limit = 10
	}
	return QuickReport{
		SchemaVersion:         report.SchemaVersion,
		TraceAPath:            report.TraceAPath,
		TraceBPath:            report.TraceBPath,
		Summary:               report.Summary,
		TopFunctionDeltas:     firstN(report.TopFunctionDeltas, limit),
		TopDispatchOutliers:   firstN(report.TopDispatchOutliers, limit),
		UnnamedDispatchDeltas: firstN(report.UnnamedDispatchDeltas, limit),
		TimelineSpikeWindows:  firstN(report.TimelineSpikeWindows, limit),
		Warnings:              report.Warnings,
	}
}

func firstN[T any](v []T, n int) []T {
	if v == nil {
		return []T{}
	}
	if len(v) > n {
		v = v[:n]
	}
	return append([]T(nil), v...)
}
