package difftrace

import (
	"sort"
)

// BuildReport builds aggregate diagnostics from an alignment result.
func BuildReport(a, b *TraceData, aligned AlignmentResult, opts ReportOptions) Report {
	if opts.Limit <= 0 {
		opts.Limit = 20
	}
	if opts.MinDeltaUs < 0 {
		opts.MinDeltaUs = 0
	}

	report := Report{
		SchemaVersion: SchemaVersion,
		TraceAPath:    a.Path,
		TraceBPath:    b.Path,
		MatchedPairs:  append([]MatchPair(nil), aligned.Matches...),
		Warnings:      uniqueStrings(append(append([]string(nil), a.Warnings...), b.Warnings...)),
	}

	totalA := totalDuration(aligned.TraceA)
	totalB := totalDuration(aligned.TraceB)
	matchedDelta := 0
	for _, m := range aligned.Matches {
		matchedDelta += m.DeltaUs
	}
	unmatchedDelta := totalDuration(aligned.UnmatchedA) - totalDuration(aligned.UnmatchedB)
	dispatchCountA := len(aligned.TraceA)
	dispatchCountB := len(aligned.TraceB)
	if !a.TimingAvailable && a.StructuralDispatches != nil {
		dispatchCountA = *a.StructuralDispatches
	}
	if !b.TimingAvailable && b.StructuralDispatches != nil {
		dispatchCountB = *b.StructuralDispatches
	}
	timingAvailable := (a.TimingAvailable || len(a.Dispatches) > 0) &&
		(b.TimingAvailable || len(b.Dispatches) > 0)

	report.Summary = Summary{
		TraceALabel:            a.Label,
		TraceBLabel:            b.Label,
		DispatchCountA:         dispatchCountA,
		DispatchCountB:         dispatchCountB,
		DispatchCountDelta:     dispatchCountA - dispatchCountB,
		TimingAvailable:        timingAvailable,
		TotalGPUTimeAUs:        totalA,
		TotalGPUTimeBUs:        totalB,
		DispatchSpanAUs:        totalA,
		DispatchSpanBUs:        totalB,
		EffectiveGPUTimeAUs:    a.EffectiveGPUTimeUs,
		EffectiveGPUTimeBUs:    b.EffectiveGPUTimeUs,
		CommandBufferActiveAUs: a.CommandBufferActiveUs,
		CommandBufferActiveBUs: b.CommandBufferActiveUs,
		TimingMetric:           timingMetric(timingAvailable),
		AttributionLimited:     a.AttributionLimited || b.AttributionLimited,
		TotalDeltaUs:           totalA - totalB,
		MatchedDeltaUs:         matchedDelta,
		UnmatchedDeltaUs:       unmatchedDelta,
	}

	report.TopFunctionDeltas = buildFunctionDeltas(aligned)
	if !timingAvailable {
		// Neither side has profiler dispatches to align, but the capture
		// records still say which kernels ran and how often.
		report.TopFunctionDeltas = StructuralFunctionDeltas(a.StructuralFunctions, b.StructuralFunctions)
		report.Summary.StructuralFunctions = len(report.TopFunctionDeltas) > 0
	}
	report.EncoderDeltas = buildEncoderDeltas(aligned)
	report.EncoderReports = buildEncoderReports(aligned, opts.Limit)
	report.PipelineDeltas = buildPipelineDeltas(aligned)
	report.UnnamedDispatchDeltas = buildUnnamedDeltas(aligned)
	report.TopDispatchOutliers = topDispatchOutliers(aligned.Matches, opts.Limit, opts.MinDeltaUs)
	report.TimelineSpikeWindows = buildSpikeWindows(aligned.Matches, opts.MinDeltaUs, opts.Limit)
	report.OccurrenceMatches = buildOccurrenceMatches(aligned)
	report.Unmatched = buildUnmatched(aligned)
	report.Summary.LikelyCause = inferLikelyCause(report)

	report.TopFunctionDeltas = nonNilFunctionDeltas(report.TopFunctionDeltas)
	report.TopDispatchOutliers = nonNilMatches(report.TopDispatchOutliers)
	report.EncoderDeltas = nonNilEncoderDeltas(report.EncoderDeltas)
	report.EncoderReports = nonNilEncoderReports(report.EncoderReports)
	report.PipelineDeltas = nonNilPipelineDeltas(report.PipelineDeltas)
	report.UnnamedDispatchDeltas = nonNilUnnamedDeltas(report.UnnamedDispatchDeltas)
	report.TimelineSpikeWindows = nonNilSpikeWindows(report.TimelineSpikeWindows)
	report.OccurrenceMatches = nonNilOccurrenceMatches(report.OccurrenceMatches)
	report.MatchedPairs = nonNilMatches(report.MatchedPairs)
	report.Unmatched = nonNilUnmatched(report.Unmatched)

	// Check comparability against every function delta, before the limit
	// below discards the tail. The rows that carry the signal are the
	// smallest in the table, so a cost-ordered top-N is precisely what drops
	// them.
	report.RenamePairs, report.AmbiguousRenames = renamePairing(report.TopFunctionDeltas)
	report.Comparability = CheckComparability(report.TopFunctionDeltas, report.RenamePairs)
	if warning := report.Comparability.Warning(); warning != "" {
		report.Warnings = append(report.Warnings, warning)
	}

	if len(report.TopFunctionDeltas) > opts.Limit {
		report.TopFunctionDeltas = report.TopFunctionDeltas[:opts.Limit]
	}
	if len(report.EncoderDeltas) > opts.Limit {
		report.EncoderDeltas = report.EncoderDeltas[:opts.Limit]
	}
	if len(report.EncoderReports) > opts.Limit {
		report.EncoderReports = report.EncoderReports[:opts.Limit]
	}
	if len(report.PipelineDeltas) > opts.Limit {
		report.PipelineDeltas = report.PipelineDeltas[:opts.Limit]
	}
	if len(report.UnnamedDispatchDeltas) > opts.Limit {
		report.UnnamedDispatchDeltas = report.UnnamedDispatchDeltas[:opts.Limit]
	}
	if len(report.Unmatched) > opts.Limit*4 {
		report.Unmatched = report.Unmatched[:opts.Limit*4]
	}
	return report
}

func timingMetric(available bool) string {
	if available {
		return "cumulative_offset_delta"
	}
	return "unavailable"
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func totalDuration(dispatches []Dispatch) int {
	total := 0
	for _, d := range dispatches {
		total += d.DurationUs
	}
	return total
}

func buildFunctionDeltas(aligned AlignmentResult) []FunctionDelta {
	type occ struct {
		delta int
	}
	type agg struct {
		FunctionDelta
		seenFirst bool
		maxAbs    int
		occ       []occ
	}
	by := map[string]*agg{}
	get := func(name string) *agg {
		name = safeFunctionName(name)
		a := by[name]
		if a == nil {
			a = &agg{}
			a.FunctionName = name
			by[name] = a
		}
		return a
	}
	for _, d := range aligned.TraceA {
		a := get(d.FunctionName)
		a.DispatchCountA++
		a.TotalAUs += d.DurationUs
	}
	for _, d := range aligned.TraceB {
		a := get(d.FunctionName)
		a.DispatchCountB++
		a.TotalBUs += d.DurationUs
	}
	for _, m := range aligned.Matches {
		a := get(m.FunctionName)
		a.MatchedPairs++
		if !a.seenFirst {
			a.FirstOccurrenceDeltaUs = m.DeltaUs
			a.seenFirst = true
		}
		if absInt(m.DeltaUs) > a.maxAbs {
			a.maxAbs = absInt(m.DeltaUs)
			a.MaxOccurrenceDeltaUs = m.DeltaUs
		}
		a.occ = append(a.occ, occ{delta: m.DeltaUs})
	}

	out := make([]FunctionDelta, 0, len(by))
	for _, a := range by {
		a.DispatchCountDelta = a.DispatchCountA - a.DispatchCountB
		a.TotalDeltaUs = a.TotalAUs - a.TotalBUs
		out = append(out, a.FunctionDelta)
	}
	sort.Slice(out, func(i, j int) bool {
		di := absInt(out[i].TotalDeltaUs)
		dj := absInt(out[j].TotalDeltaUs)
		if di == dj {
			return out[i].FunctionName < out[j].FunctionName
		}
		return di > dj
	})
	return out
}

func buildEncoderDeltas(aligned AlignmentResult) []EncoderDelta {
	type agg struct{ EncoderDelta }
	by := map[int]*agg{}
	get := func(idx int) *agg {
		a := by[idx]
		if a == nil {
			a = &agg{}
			a.EncoderIndex = idx
			by[idx] = a
		}
		return a
	}
	for _, d := range aligned.TraceA {
		a := get(d.EncoderIndex)
		a.DispatchCountA++
		a.TotalAUs += d.DurationUs
	}
	for _, d := range aligned.TraceB {
		a := get(d.EncoderIndex)
		a.DispatchCountB++
		a.TotalBUs += d.DurationUs
	}
	out := make([]EncoderDelta, 0, len(by))
	for _, a := range by {
		a.DispatchCountDelta = a.DispatchCountA - a.DispatchCountB
		a.TotalDeltaUs = a.TotalAUs - a.TotalBUs
		out = append(out, a.EncoderDelta)
	}
	sort.Slice(out, func(i, j int) bool {
		if absInt(out[i].TotalDeltaUs) == absInt(out[j].TotalDeltaUs) {
			return out[i].EncoderIndex < out[j].EncoderIndex
		}
		return absInt(out[i].TotalDeltaUs) > absInt(out[j].TotalDeltaUs)
	})
	return out
}

func buildPipelineDeltas(aligned AlignmentResult) []PipelineDelta {
	type agg struct{ PipelineDelta }
	by := map[int]*agg{}
	pickName := func(existing, next string) string {
		if existing != "" {
			return existing
		}
		return next
	}
	get := func(id int) *agg {
		a := by[id]
		if a == nil {
			a = &agg{}
			a.PipelineID = id
			by[id] = a
		}
		return a
	}
	for _, d := range aligned.TraceA {
		a := get(d.PipelineID)
		a.FunctionName = pickName(a.FunctionName, safeFunctionName(d.FunctionName))
		a.DispatchCountA++
		a.TotalAUs += d.DurationUs
	}
	for _, d := range aligned.TraceB {
		a := get(d.PipelineID)
		a.FunctionName = pickName(a.FunctionName, safeFunctionName(d.FunctionName))
		a.DispatchCountB++
		a.TotalBUs += d.DurationUs
	}
	out := make([]PipelineDelta, 0, len(by))
	for _, a := range by {
		a.DispatchCountDelta = a.DispatchCountA - a.DispatchCountB
		a.TotalDeltaUs = a.TotalAUs - a.TotalBUs
		out = append(out, a.PipelineDelta)
	}
	sort.Slice(out, func(i, j int) bool {
		if absInt(out[i].TotalDeltaUs) == absInt(out[j].TotalDeltaUs) {
			return out[i].PipelineID < out[j].PipelineID
		}
		return absInt(out[i].TotalDeltaUs) > absInt(out[j].TotalDeltaUs)
	})
	return out
}

func buildUnnamedDeltas(aligned AlignmentResult) []UnnamedDispatchDelta {
	type agg struct{ UnnamedDispatchDelta }
	by := map[string]*agg{}
	get := func(k string) *agg {
		a := by[k]
		if a == nil {
			a = &agg{}
			a.KernelID = k
			a.PipelineID = -1
			a.TopOutlierSourceA = -1
			a.TopOutlierSourceB = -1
			by[k] = a
		}
		return a
	}
	for _, d := range aligned.TraceA {
		if d.FunctionName != "" {
			continue
		}
		key := d.KernelID
		if key == "" {
			key = kernelIdentity(d.FunctionName, d.PipelineHash, d.ThreadgroupSig)
		}
		a := get(key)
		if a.PipelineID < 0 || d.PipelineID < a.PipelineID {
			a.PipelineID = d.PipelineID
		}
		if a.PipelineHash == "" {
			a.PipelineHash = d.PipelineHash
		}
		if a.ThreadgroupSig == "" || a.ThreadgroupSig == "unknown" {
			a.ThreadgroupSig = d.ThreadgroupSig
		}
		a.DispatchCountA++
		a.TotalAUs += d.DurationUs
	}
	for _, d := range aligned.TraceB {
		if d.FunctionName != "" {
			continue
		}
		key := d.KernelID
		if key == "" {
			key = kernelIdentity(d.FunctionName, d.PipelineHash, d.ThreadgroupSig)
		}
		a := get(key)
		if a.PipelineID < 0 || d.PipelineID < a.PipelineID {
			a.PipelineID = d.PipelineID
		}
		if a.PipelineHash == "" {
			a.PipelineHash = d.PipelineHash
		}
		if a.ThreadgroupSig == "" || a.ThreadgroupSig == "unknown" {
			a.ThreadgroupSig = d.ThreadgroupSig
		}
		a.DispatchCountB++
		a.TotalBUs += d.DurationUs
	}
	for _, m := range aligned.Matches {
		if m.FunctionName != "" {
			continue
		}
		key := m.KernelID
		if key == "" {
			key = kernelIdentity("", m.PipelineHashA, m.ThreadgroupSigA)
		}
		a := get(key)
		if a.PipelineID < 0 || m.PipelineIDA < a.PipelineID {
			a.PipelineID = m.PipelineIDA
		}
		if a.PipelineHash == "" {
			a.PipelineHash = m.PipelineHashA
		}
		if a.ThreadgroupSig == "" || a.ThreadgroupSig == "unknown" {
			a.ThreadgroupSig = m.ThreadgroupSigA
		}
		if absInt(m.DeltaUs) > absInt(a.TopOutlierDeltaUs) {
			a.TopOutlierDeltaUs = m.DeltaUs
			a.TopOutlierSourceA = m.SourceIndexA
			a.TopOutlierSourceB = m.SourceIndexB
		}
	}
	out := make([]UnnamedDispatchDelta, 0, len(by))
	for _, a := range by {
		a.DispatchCountDelta = a.DispatchCountA - a.DispatchCountB
		a.TotalDeltaUs = a.TotalAUs - a.TotalBUs
		out = append(out, a.UnnamedDispatchDelta)
	}
	sort.Slice(out, func(i, j int) bool {
		if absInt(out[i].TotalDeltaUs) == absInt(out[j].TotalDeltaUs) {
			if out[i].PipelineID == out[j].PipelineID {
				return out[i].KernelID < out[j].KernelID
			}
			return out[i].PipelineID < out[j].PipelineID
		}
		return absInt(out[i].TotalDeltaUs) > absInt(out[j].TotalDeltaUs)
	})
	return out
}

func buildEncoderReports(aligned AlignmentResult, limit int) []EncoderReport {
	type agg struct {
		EncoderReport
		top []MatchPair
	}
	by := map[int]*agg{}
	get := func(idx int) *agg {
		a := by[idx]
		if a == nil {
			a = &agg{}
			a.EncoderIndex = idx
			by[idx] = a
		}
		return a
	}

	for _, d := range aligned.TraceA {
		a := get(d.EncoderIndex)
		a.DispatchCountA++
	}
	for _, d := range aligned.TraceB {
		a := get(d.EncoderIndex)
		a.DispatchCountB++
	}
	for _, d := range aligned.UnmatchedA {
		a := get(d.EncoderIndex)
		a.UnmatchedCountA++
		a.UnmatchedDeltaUs += d.DurationUs
	}
	for _, d := range aligned.UnmatchedB {
		a := get(d.EncoderIndex)
		a.UnmatchedCountB++
		a.UnmatchedDeltaUs -= d.DurationUs
	}
	for _, m := range aligned.Matches {
		a := get(m.EncoderIndex)
		a.MatchedCount++
		a.MatchedDeltaUs += m.DeltaUs
		a.top = append(a.top, m)
	}

	out := make([]EncoderReport, 0, len(by))
	for _, a := range by {
		a.UnmatchedCount = a.UnmatchedCountA + a.UnmatchedCountB
		sort.Slice(a.top, func(i, j int) bool {
			di := absInt(a.top[i].DeltaUs)
			dj := absInt(a.top[j].DeltaUs)
			if di == dj {
				if a.top[i].SourceIndexA == a.top[j].SourceIndexA {
					return a.top[i].SourceIndexB < a.top[j].SourceIndexB
				}
				return a.top[i].SourceIndexA < a.top[j].SourceIndexA
			}
			return di > dj
		})
		topN := 5
		if limit > 0 && limit < topN {
			topN = limit
		}
		if len(a.top) > topN {
			a.TopDispatches = append([]MatchPair(nil), a.top[:topN]...)
		} else if len(a.top) > 0 {
			a.TopDispatches = append([]MatchPair(nil), a.top...)
		} else {
			a.TopDispatches = []MatchPair{}
		}
		out = append(out, a.EncoderReport)
	}

	sort.Slice(out, func(i, j int) bool {
		di := absInt(out[i].MatchedDeltaUs)
		dj := absInt(out[j].MatchedDeltaUs)
		if di == dj {
			return out[i].EncoderIndex < out[j].EncoderIndex
		}
		return di > dj
	})
	return out
}

func topDispatchOutliers(matches []MatchPair, limit int, minDelta int) []MatchPair {
	out := make([]MatchPair, 0, len(matches))
	for _, m := range matches {
		if absInt(m.DeltaUs) < minDelta {
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		di := absInt(out[i].DeltaUs)
		dj := absInt(out[j].DeltaUs)
		if di == dj {
			if out[i].SourceIndexA == out[j].SourceIndexA {
				return out[i].SourceIndexB < out[j].SourceIndexB
			}
			return out[i].SourceIndexA < out[j].SourceIndexA
		}
		return di > dj
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func buildSpikeWindows(matches []MatchPair, minDelta, limit int) []SpikeWindow {
	if len(matches) == 0 {
		return nil
	}
	threshold := minDelta
	if threshold < 75 {
		threshold = 75
	}
	var candidates []MatchPair
	for _, m := range matches {
		if absInt(m.DeltaUs) >= threshold {
			candidates = append(candidates, m)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].SourceIndexA == candidates[j].SourceIndexA {
			return candidates[i].SourceIndexB < candidates[j].SourceIndexB
		}
		return candidates[i].SourceIndexA < candidates[j].SourceIndexA
	})
	var windows []SpikeWindow
	cur := SpikeWindow{}
	reset := func(m MatchPair) {
		cur = SpikeWindow{
			EncoderIndex:      m.EncoderIndex,
			StartSourceIndexA: m.SourceIndexA,
			EndSourceIndexA:   m.SourceIndexA,
			StartSourceIndexB: m.SourceIndexB,
			EndSourceIndexB:   m.SourceIndexB,
			MatchCount:        1,
			TotalDeltaUs:      m.DeltaUs,
			MaxAbsDeltaUs:     absInt(m.DeltaUs),
		}
	}
	for i, m := range candidates {
		if i == 0 {
			reset(m)
			continue
		}
		contiguous := m.EncoderIndex == cur.EncoderIndex &&
			m.SourceIndexA <= cur.EndSourceIndexA+2 &&
			m.SourceIndexB <= cur.EndSourceIndexB+2
		if !contiguous {
			windows = append(windows, cur)
			reset(m)
			continue
		}
		cur.EndSourceIndexA = m.SourceIndexA
		cur.EndSourceIndexB = m.SourceIndexB
		cur.MatchCount++
		cur.TotalDeltaUs += m.DeltaUs
		if absInt(m.DeltaUs) > cur.MaxAbsDeltaUs {
			cur.MaxAbsDeltaUs = absInt(m.DeltaUs)
		}
	}
	windows = append(windows, cur)
	sort.Slice(windows, func(i, j int) bool {
		if absInt(windows[i].TotalDeltaUs) == absInt(windows[j].TotalDeltaUs) {
			if windows[i].EncoderIndex == windows[j].EncoderIndex {
				return windows[i].StartSourceIndexA < windows[j].StartSourceIndexA
			}
			return windows[i].EncoderIndex < windows[j].EncoderIndex
		}
		return absInt(windows[i].TotalDeltaUs) > absInt(windows[j].TotalDeltaUs)
	})
	if len(windows) > limit {
		windows = windows[:limit]
	}
	return windows
}

func buildUnmatched(aligned AlignmentResult) []UnmatchedDispatch {
	out := make([]UnmatchedDispatch, 0, len(aligned.UnmatchedA)+len(aligned.UnmatchedB))
	for _, d := range aligned.UnmatchedA {
		out = append(out, UnmatchedDispatch{
			Trace:          "a",
			SourceIndex:    d.SourceIndex,
			FunctionName:   safeFunctionName(d.FunctionName),
			KernelID:       d.KernelID,
			EncoderIndex:   d.EncoderIndex,
			PipelineID:     d.PipelineID,
			PipelineHash:   d.PipelineHash,
			ThreadgroupSig: d.ThreadgroupSig,
			DurationUs:     d.DurationUs,
		})
	}
	for _, d := range aligned.UnmatchedB {
		out = append(out, UnmatchedDispatch{
			Trace:          "b",
			SourceIndex:    d.SourceIndex,
			FunctionName:   safeFunctionName(d.FunctionName),
			KernelID:       d.KernelID,
			EncoderIndex:   d.EncoderIndex,
			PipelineID:     d.PipelineID,
			PipelineHash:   d.PipelineHash,
			ThreadgroupSig: d.ThreadgroupSig,
			DurationUs:     d.DurationUs,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Trace == out[j].Trace {
			return out[i].SourceIndex < out[j].SourceIndex
		}
		return out[i].Trace < out[j].Trace
	})
	return out
}

func inferLikelyCause(r Report) string {
	if !r.Summary.TimingAvailable {
		if r.Summary.DispatchCountDelta != 0 {
			return "structural dispatch count difference; timing unavailable"
		}
		return "timing unavailable"
	}
	totalDeltaAbs := absInt(r.Summary.TotalDeltaUs)
	if totalDeltaAbs == 0 {
		return "no measurable delta"
	}
	if absInt(r.Summary.UnmatchedDeltaUs)*100/totalDeltaAbs >= 35 && len(r.Unmatched) > 0 {
		return "structural command stream overhead"
	}
	if len(r.TopFunctionDeltas) > 0 {
		top := r.TopFunctionDeltas[0]
		if absInt(top.FirstOccurrenceDeltaUs) >= 250 && absInt(top.FirstOccurrenceDeltaUs)*100/absIntOrOne(top.TotalDeltaUs) >= 45 {
			return "one-time warmup/growth spike"
		}
	}
	return "repeated per-step slowdown"
}

func absIntOrOne(v int) int {
	av := absInt(v)
	if av == 0 {
		return 1
	}
	return av
}

func nonNilFunctionDeltas(v []FunctionDelta) []FunctionDelta {
	if v == nil {
		return []FunctionDelta{}
	}
	return v
}

func nonNilMatches(v []MatchPair) []MatchPair {
	if v == nil {
		return []MatchPair{}
	}
	return v
}

func nonNilEncoderDeltas(v []EncoderDelta) []EncoderDelta {
	if v == nil {
		return []EncoderDelta{}
	}
	return v
}

func nonNilEncoderReports(v []EncoderReport) []EncoderReport {
	if v == nil {
		return []EncoderReport{}
	}
	return v
}

func nonNilPipelineDeltas(v []PipelineDelta) []PipelineDelta {
	if v == nil {
		return []PipelineDelta{}
	}
	return v
}

func nonNilUnnamedDeltas(v []UnnamedDispatchDelta) []UnnamedDispatchDelta {
	if v == nil {
		return []UnnamedDispatchDelta{}
	}
	return v
}

func nonNilSpikeWindows(v []SpikeWindow) []SpikeWindow {
	if v == nil {
		return []SpikeWindow{}
	}
	return v
}

func nonNilUnmatched(v []UnmatchedDispatch) []UnmatchedDispatch {
	if v == nil {
		return []UnmatchedDispatch{}
	}
	return v
}

func nonNilOccurrenceMatches(v []OccurrenceMatch) []OccurrenceMatch {
	if v == nil {
		return []OccurrenceMatch{}
	}
	return v
}
