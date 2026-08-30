// Package gate evaluates GPU captures against workload invariants and
// trajectory stationarity.
//
// A capture can look structurally valid while being incomplete (missing
// kernel dispatches that the tracer never flushed or dropped) or
// non-stationary (disturbed by external contention or thermal throttling).
//
// Three independent checks evaluate capture validity:
//
//  1. Completeness: scored against a caller-supplied workload invariant
//     (an operation that executes once per token), not just the tracer's
//     self-reported drop counter.
//  2. Stationarity: per-token trajectory medians across successive blocks
//     must remain flat, catching mid-run disturbances that per-kernel
//     medians hide.
//  3. Staging/Residency: reports observed data movement (CUDA HtoD bytes or
//     Metal streamData numBlitCalls) with explicit distinction between
//     recorded zero and absent data.
package gate

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tmc/gputrace"
	"github.com/tmc/gputrace/internal/counter"
	"github.com/tmc/gputrace/internal/cupticapture"
	"github.com/tmc/gputrace/internal/gpuevent"
	"github.com/tmc/gputrace/internal/profilerraw"
)

// Verdict classifies the outcome of a gate check.
type Verdict string

const (
	// VerdictPass indicates all evaluated criteria passed.
	VerdictPass Verdict = "PASS"
	// VerdictFail indicates a criterion failed (e.g. loss or excursion).
	VerdictFail Verdict = "FAIL"
	// VerdictNotEvaluable indicates evaluation was blocked (e.g. 0 matches or missing info).
	VerdictNotEvaluable Verdict = "NOT_EVALUABLE"
)

// Options configure gate evaluation.
type Options struct {
	// Tokens is the number of tokens the workload was requested to generate.
	Tokens int `json:"tokens,omitempty"`
	// ExactTokens when true scores want = Tokens instead of Tokens + 1 (prefill).
	ExactTokens bool `json:"exact_tokens,omitempty"`
	// InvariantSymbol is the symbol substring that executes once per token.
	// Required on Metal; defaults to "arg_reduce" on CUDA when empty.
	InvariantSymbol string `json:"invariant_symbol,omitempty"`
	// Slack is the allowed missing tokens due to exit flush residual (default: 2).
	Slack int `json:"slack,omitempty"`
	// StationarityThreshold is the max allowed relative excursion (default: 0.15 for 15%).
	StationarityThreshold float64 `json:"stationarity_threshold,omitempty"`
	// BlockSize is the gap block size for trajectory analysis.
	// Zero means auto: at least 16 gaps per block, at most 8 blocks.
	BlockSize int `json:"block_size,omitempty"`
	// Ranges is reserved for nested half-open range monotonicity checks.
	Ranges []string `json:"ranges,omitempty"`
	// TimingSidecar is a GT_TIMING_OUT sidecar (newline-delimited JSON from
	// the capture interposer). When set, stationarity is scored from its
	// live per-command-buffer wall clock instead of replay-derived
	// streamData timing. Command-buffer granularity: the trajectory is over
	// command buffers, not invariant-matched dispatches.
	TimingSidecar string `json:"timing_sidecar,omitempty"`
}

// CompletenessResult reports whether the capture holds every record the
// workload produced.
type CompletenessResult struct {
	Status              Verdict `json:"status"`
	Evaluated           bool    `json:"evaluated"`
	InvariantSymbol     string  `json:"invariant_symbol"`
	MatchedCount        int     `json:"matched_count"`
	ExpectedCount       int     `json:"expected_count,omitempty"`
	Tokens              int     `json:"tokens,omitempty"`
	PrefillTokens       int     `json:"prefill_tokens,omitempty"`
	Slack               int     `json:"slack,omitempty"`
	MissingCount        int     `json:"missing_count,omitempty"`
	MissingPct          float64 `json:"missing_pct,omitempty"`
	DroppedRecords      int     `json:"dropped_records,omitempty"`
	MissingGraphKernels int     `json:"missing_graph_kernels,omitempty"`
	Reason              string  `json:"reason"`
}

// StationarityResult reports whether the execution trajectory was steady-state.
type StationarityResult struct {
	Status         Verdict   `json:"status"`
	Evaluated      bool      `json:"evaluated"`
	GapsCount      int       `json:"gaps_count"`
	BlockSize      int       `json:"block_size,omitempty"`
	BlockMediansMS []float64 `json:"block_medians_ms,omitempty"`
	BaseMedianMS   float64   `json:"base_median_ms,omitempty"`
	WorstExcursion float64   `json:"worst_excursion_pct,omitempty"`
	Threshold      float64   `json:"threshold_pct,omitempty"`
	Shape          string    `json:"shape,omitempty"`
	// TimingSource records where the timestamps came from. Timing derived
	// from a profile-replay export describes the replay execution, not the
	// original live run; a live mid-run excursion is invisible there.
	TimingSource string `json:"timing_source,omitempty"`
	Reason       string `json:"reason"`
}

// StagingObservation reports observed data movement or residency indicators.
// It reports observations and provenance only; pass/fail comparison belongs
// in a two-bundle comparison mode.
type StagingObservation struct {
	Backend          string `json:"backend"`
	Source           string `json:"source"`
	Recorded         bool   `json:"recorded"`
	BlitCalls        *int64 `json:"blit_calls,omitempty"`
	HtoDTransfers    int    `json:"htod_transfers,omitempty"`
	HtoDBytes        uint64 `json:"htod_bytes,omitempty"`
	StorageModeNotes string `json:"storage_mode_notes,omitempty"`
	Summary          string `json:"summary"`
}

// Result is the composite outcome of all evaluated gates for a bundle.
type Result struct {
	Bundle       string             `json:"bundle"`
	Backend      string             `json:"backend"`
	Verdict      Verdict            `json:"verdict"`
	Completeness CompletenessResult `json:"completeness"`
	Stationarity StationarityResult `json:"stationarity"`
	Staging      StagingObservation `json:"staging"`
	Summary      string             `json:"summary"`
}

// DefaultOptions returns standard defaults for gate evaluation.
func DefaultOptions() Options {
	return Options{
		Slack:                 2,
		StationarityThreshold: 0.15,
	}
}

// Evaluate evaluates a capture or trace bundle at bundlePath against the
// provided gate options.
func Evaluate(bundlePath string, opts Options) (*Result, error) {
	if opts.Slack <= 0 {
		opts.Slack = 2
	}
	if opts.StationarityThreshold <= 0 {
		opts.StationarityThreshold = 0.15
	}

	info, err := os.Stat(bundlePath)
	if err != nil {
		return nil, fmt.Errorf("stat bundle: %w", err)
	}

	// Detect backend: CUDA events vs Metal trace
	isCUDA := false
	if !info.IsDir() && (strings.HasSuffix(bundlePath, ".jsonl") || strings.HasSuffix(bundlePath, ".jsonl.zst")) {
		isCUDA = true
	} else if info.IsDir() {
		if _, err := os.Stat(filepath.Join(bundlePath, "events.jsonl")); err == nil {
			isCUDA = true
		} else if matches, _ := filepath.Glob(filepath.Join(bundlePath, "events*.jsonl*")); len(matches) > 0 {
			isCUDA = true
		}
	}

	if isCUDA {
		return evaluateCUDA(bundlePath, opts)
	}
	return evaluateMetal(bundlePath, opts)
}

func evaluateCUDA(bundlePath string, opts Options) (*Result, error) {
	f, closers, err := cupticapture.OpenEvents(bundlePath)
	if err != nil {
		return nil, fmt.Errorf("open cuda events: %w", err)
	}
	defer closers()

	cap, err := gpuevent.DecodeJSONL(f)
	if err != nil {
		return nil, fmt.Errorf("decode events: %w", err)
	}

	name := filepath.Base(strings.TrimSuffix(bundlePath, "/"))
	res := &Result{
		Bundle:  bundlePath,
		Backend: "cuda",
	}

	if len(cap.Events) == 0 {
		res.Verdict = VerdictNotEvaluable
		res.Summary = fmt.Sprintf("%s: FAIL no events found in capture", name)
		res.Completeness = CompletenessResult{
			Status: VerdictNotEvaluable,
			Reason: "no events found in capture",
		}
		res.Stationarity = StationarityResult{
			Status: VerdictNotEvaluable,
			Reason: "no events found in capture",
		}
		res.Staging = StagingObservation{
			Backend:  "cuda",
			Source:   "memcpy",
			Recorded: false,
			Summary:  "staging: absent from capture",
		}
		return res, nil
	}

	invariant := opts.InvariantSymbol
	if invariant == "" {
		invariant = "arg_reduce"
	}

	// Filter and sort kernel events
	var kernelMarks []uint64
	var kernels []gpuevent.Event
	for _, e := range cap.Events {
		if e.Kind == gpuevent.KindKernel {
			kernels = append(kernels, e)
			if strings.Contains(e.RawSymbol, invariant) || strings.Contains(e.Name, invariant) {
				kernelMarks = append(kernelMarks, e.StartNS)
			}
		}
	}
	sort.Slice(kernels, func(i, j int) bool { return kernels[i].StartNS < kernels[j].StartNS })
	sort.Slice(kernelMarks, func(i, j int) bool { return kernelMarks[i] < kernelMarks[j] })

	// Gate 1: Completeness
	comp := EvaluateCompleteness(name, kernelMarks, invariant, opts)
	comp.DroppedRecords = cap.DroppedRecords
	cComp := gpuevent.MeasureCompleteness(cap)
	comp.MissingGraphKernels = cComp.MissingGraphKernels()
	if cap.DroppedRecords > 0 {
		comp.Status = VerdictFail
		comp.Reason = fmt.Sprintf("the tracer dropped %d records", cap.DroppedRecords)
	}
	res.Completeness = comp

	// Gate 2: Stationarity
	res.Stationarity = EvaluateStationarity(kernelMarks, opts)
	res.Stationarity.TimingSource = "live (CUPTI activity timestamps)"

	// Gate 3: Staging observation
	var htodCount int
	var htodBytes uint64
	for _, e := range cap.Events {
		if e.Kind == gpuevent.KindMemcpy {
			if e.SrcKind == "host" || e.DstKind == "device" || e.DstKind == "device_ptr" ||
				strings.EqualFold(e.SrcKind, "HtoD") || strings.EqualFold(e.DstKind, "HtoD") {
				htodCount++
				htodBytes += e.Bytes
			} else if e.Attrs != nil {
				if dir, ok := e.Attrs["dir"].(string); ok && dir == "HtoD" {
					htodCount++
					htodBytes += e.Bytes
				}
			}
		}
	}
	res.Staging = StagingObservation{
		Backend:          "cuda",
		Source:           "memcpy",
		Recorded:         true,
		HtoDTransfers:    htodCount,
		HtoDBytes:        htodBytes,
		StorageModeNotes: "not recorded in bundle",
	}
	if htodCount > 0 {
		res.Staging.Summary = fmt.Sprintf("staging: %d HtoD transfers (%.1f MB)", htodCount, float64(htodBytes)/1e6)
	} else {
		res.Staging.Summary = "staging: 0 HtoD transfers (0.0 MB recorded)"
	}

	res.Verdict = resolveVerdict(res.Completeness, res.Stationarity)
	res.Summary = formatSummary(name, res)
	return res, nil
}

func evaluateMetal(bundlePath string, opts Options) (*Result, error) {
	name := filepath.Base(strings.TrimSuffix(bundlePath, "/"))
	res := &Result{
		Bundle:  bundlePath,
		Backend: "metal",
	}

	// Try reading streamData for profiling timing and metadata
	profilerDir := profilerraw.FindDirWithStreamData(bundlePath)
	var streamStats *counter.StreamDataStats
	if profilerDir != "" {
		stats, err := counter.ParseStreamData(profilerDir, nil)
		if err == nil {
			streamStats = stats
		}
	}

	// Also open trace for capture structure
	t, err := gputrace.Open(bundlePath)
	if err != nil && streamStats == nil {
		return nil, fmt.Errorf("open metal trace: %w", err)
	}

	// Staging / Blit observation
	res.Staging = StagingObservation{
		Backend:          "metal",
		Source:           "streamData",
		StorageModeNotes: "no buffer-creation records in bundle",
	}
	if t != nil {
		if modes := t.BufferStorageModes(); len(modes) > 0 {
			names := make([]string, 0, len(modes))
			for name := range modes {
				names = append(names, name)
			}
			sort.Strings(names)
			parts := make([]string, 0, len(names))
			for _, name := range names {
				parts = append(parts, fmt.Sprintf("%d %s", modes[name], name))
			}
			res.Staging.StorageModeNotes = fmt.Sprintf("buffer storage modes: %s (capture buffer-creation records)",
				strings.Join(parts, ", "))
		}
	}
	if streamStats != nil && streamStats.Metadata.NumBlitCalls != nil {
		res.Staging.Recorded = true
		res.Staging.BlitCalls = streamStats.Metadata.NumBlitCalls
		res.Staging.Summary = fmt.Sprintf("blit calls: %d (recorded in streamData)", *streamStats.Metadata.NumBlitCalls)
	} else {
		res.Staging.Recorded = false
		res.Staging.Summary = "blit calls: absent from streamData"
	}

	// Invariant symbol must be explicitly supplied on Metal
	invariant := opts.InvariantSymbol
	if invariant == "" {
		res.Verdict = VerdictNotEvaluable
		res.Completeness = CompletenessResult{
			Status: VerdictNotEvaluable,
			Reason: "invariant symbol (-k) is required on Metal: specify an op that runs once per token",
		}
		res.Stationarity = StationarityResult{
			Status: VerdictNotEvaluable,
			Reason: "stationarity UNSCORED (pass -k to identify per-token marks)",
		}
		res.Summary = formatSummary(name, res)
		return res, nil
	}

	// Collect dispatches and timestamps
	var marks []uint64
	if streamStats != nil && len(streamStats.Dispatches) > 0 {
		for _, d := range streamStats.Dispatches {
			// Match the display name, not the raw field: a dispatch with no
			// function name is still nameable as its pipeline, and matching
			// the empty field made it invisible to -k instead of unmatched.
			if strings.Contains(d.DisplayName(), invariant) {
				// Use CumulativeUs converted to nanoseconds for stationarity
				marks = append(marks, uint64(d.CumulativeUs)*1000)
			}
		}
	} else if t != nil && !t.ProfilerOnly {
		kernelStats, err := t.AnalyzeKernels()
		if err == nil {
			for name, stat := range kernelStats {
				if strings.Contains(name, invariant) {
					for i := 0; i < stat.DispatchCount; i++ {
						marks = append(marks, 0)
					}
				}
			}
		}
	}

	// Gate 1: Completeness
	comp := EvaluateCompleteness(name, marks, invariant, opts)
	res.Completeness = comp

	// Gate 2: Stationarity. A live sidecar outranks replay timing: replay
	// cannot witness a mid-run excursion in the original execution.
	if opts.TimingSidecar != "" {
		marks, err := readSidecarMarks(opts.TimingSidecar)
		if err != nil {
			return nil, fmt.Errorf("timing sidecar: %w", err)
		}
		res.Stationarity = EvaluateStationarity(marks, opts)
		res.Stationarity.TimingSource = "live (command-buffer wall clock from GT_TIMING_OUT sidecar)"
		if res.Stationarity.Status == VerdictPass || res.Stationarity.Status == VerdictFail {
			res.Stationarity.Reason += "  (live command-buffer timing)"
		}
	} else if streamStats == nil || len(streamStats.Dispatches) == 0 {
		res.Stationarity = StationarityResult{
			Status: VerdictNotEvaluable,
			Reason: "stationarity UNSCORED (timing data absent from raw capture; add with profile-replay)",
		}
	} else {
		res.Stationarity = EvaluateStationarity(marks, opts)
		// streamData CumulativeUs timing comes from Xcode's profile-replay
		// of the capture, not the original live run. A live mid-run
		// excursion (contention, throttling) is invisible in replay timing.
		res.Stationarity.TimingSource = "profile-replay (streamData) — does not witness the live run"
		if res.Stationarity.Status == VerdictPass || res.Stationarity.Status == VerdictFail {
			res.Stationarity.Reason += "  (replay timing — does not witness the live run)"
		}
	}

	res.Verdict = resolveVerdict(res.Completeness, res.Stationarity)
	res.Summary = formatSummary(name, res)
	return res, nil
}

func EvaluateCompleteness(name string, marks []uint64, invariant string, opts Options) CompletenessResult {
	res := CompletenessResult{
		InvariantSymbol: invariant,
		MatchedCount:    len(marks),
		Slack:           opts.Slack,
	}

	if len(marks) == 0 {
		res.Status = VerdictNotEvaluable
		res.Reason = fmt.Sprintf("invariant symbol %q matched 0 dispatches/kernels: cannot evaluate", invariant)
		return res
	}

	if opts.Tokens <= 0 {
		res.Status = VerdictNotEvaluable
		res.Reason = fmt.Sprintf("completeness UNSCORED (%d %s); pass -t", len(marks), invariant)
		return res
	}

	res.Evaluated = true
	res.Tokens = opts.Tokens
	if opts.ExactTokens {
		res.ExpectedCount = opts.Tokens
		res.PrefillTokens = 0
	} else {
		res.ExpectedCount = opts.Tokens + 1
		res.PrefillTokens = 1
	}

	want := res.ExpectedCount
	matched := len(marks)

	switch {
	case matched == want:
		res.Status = VerdictPass
		if res.PrefillTokens > 0 {
			res.Reason = fmt.Sprintf("completeness ok    %d/%d %s (want %d = %d tokens + %d prefill)",
				matched, want, invariant, want, res.Tokens, res.PrefillTokens)
		} else {
			res.Reason = fmt.Sprintf("completeness ok    %d/%d %s", matched, want, invariant)
		}
	case matched >= want-opts.Slack:
		res.Status = VerdictPass
		res.MissingCount = want - matched
		res.Reason = fmt.Sprintf("completeness ok    %d/%d %s (-%d, within flush residual of %d)",
			matched, want, invariant, res.MissingCount, opts.Slack)
	default:
		res.Status = VerdictFail
		res.MissingCount = want - matched
		res.MissingPct = float64(res.MissingCount) / float64(want) * 100.0
		res.Reason = fmt.Sprintf("completeness FAIL  %d/%d %s (%.0f%% missing)",
			matched, want, invariant, res.MissingPct)
	}

	return res
}

func EvaluateStationarity(marks []uint64, opts Options) StationarityResult {
	res := StationarityResult{
		GapsCount: len(marks) - 1,
		Threshold: opts.StationarityThreshold,
	}

	if len(marks) < 2 {
		res.Status = VerdictNotEvaluable
		res.Reason = "stationarity UNSCORED (need >= 2 timestamps)"
		return res
	}

	gaps := make([]float64, len(marks)-1)
	for i := 0; i < len(marks)-1; i++ {
		gaps[i] = float64(marks[i+1]-marks[i]) / 1e6 // in milliseconds
	}

	// Two blocks minimum: one block yields one median and a trivially flat
	// trajectory. Auto keeps the historical 32-gap threshold (two 16-gap
	// blocks); an explicit block size needs two of its own blocks, but never
	// less than 16 gaps total.
	minGaps := 32
	if opts.BlockSize > 0 {
		minGaps = max(2*opts.BlockSize, 16)
	}
	if len(gaps) < minGaps {
		res.Status = VerdictNotEvaluable
		res.Reason = fmt.Sprintf("stationarity UNSCORED (need >=%d gaps, have %d)", minGaps, len(gaps))
		return res
	}

	res.Evaluated = true
	// An explicit block size is honored exactly so callers can re-block a
	// trajectory (say, to isolate a steady-state window). Zero means auto:
	// at least 16 gaps per block, at most 8 blocks.
	block := opts.BlockSize
	if block <= 0 {
		block = max(16, len(gaps)/8)
	}
	res.BlockSize = block

	var medians []float64
	for i := 0; i+block <= len(gaps); i += block {
		chunk := append([]float64(nil), gaps[i:i+block]...)
		medians = append(medians, median(chunk))
	}

	if len(medians) == 0 {
		res.Status = VerdictNotEvaluable
		res.Reason = "stationarity UNSCORED (insufficient blocks)"
		return res
	}

	res.BlockMediansMS = medians
	base := median(medians)
	res.BaseMedianMS = base

	worst := 0.0
	for _, m := range medians {
		if base > 0 {
			exc := math.Abs(m-base) / base
			if exc > worst {
				worst = exc
			}
		}
	}
	res.WorstExcursion = worst

	var shapeBuilder strings.Builder
	for i, m := range medians {
		if i > 0 {
			shapeBuilder.WriteString(" ")
		}
		// Four significant figures so "flat within 0%" cannot be
		// formatting quantization.
		shapeBuilder.WriteString(fmt.Sprintf("%.4g", m))
	}
	res.Shape = shapeBuilder.String()

	if worst <= opts.StationarityThreshold {
		res.Status = VerdictPass
		res.Reason = fmt.Sprintf("stationarity ok    flat within %.1f%%  [%s] ms", worst*100, res.Shape)
	} else {
		res.Status = VerdictFail
		res.Reason = fmt.Sprintf("stationarity FAIL  %.1f%% excursion  [%s] ms", worst*100, res.Shape)
	}

	return res
}

// readSidecarMarks reads command-buffer GPU start timestamps from a
// GT_TIMING_OUT sidecar, in nanoseconds, sorted ascending.
func readSidecarMarks(path string) ([]uint64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var marks []uint64
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec struct {
			Kind            string  `json:"kind"`
			GPUStartSeconds float64 `json:"gpu_start_seconds"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue // sidecar may hold other record kinds and partial tails
		}
		if rec.Kind != "command_buffer" || rec.GPUStartSeconds <= 0 {
			continue
		}
		marks = append(marks, uint64(rec.GPUStartSeconds*1e9))
	}
	if len(marks) == 0 {
		return nil, fmt.Errorf("%s holds no command_buffer records with gpu_start_seconds", path)
	}
	sort.Slice(marks, func(i, j int) bool { return marks[i] < marks[j] })
	return marks, nil
}

func resolveVerdict(comp CompletenessResult, stat StationarityResult) Verdict {
	if comp.Status == VerdictFail || stat.Status == VerdictFail {
		return VerdictFail
	}
	if comp.Status == VerdictNotEvaluable && comp.Evaluated {
		return VerdictNotEvaluable
	}
	if comp.Status == VerdictNotEvaluable && comp.MatchedCount == 0 {
		return VerdictNotEvaluable
	}
	if stat.Status == VerdictNotEvaluable && stat.Evaluated {
		return VerdictNotEvaluable
	}
	return VerdictPass
}

func formatSummary(name string, r *Result) string {
	var lines []string
	lines = append(lines, fmt.Sprintf("%s: %s", name, r.Completeness.Reason))
	lines = append(lines, fmt.Sprintf("%s: %s", name, r.Staging.Summary))
	if r.Staging.StorageModeNotes != "" {
		lines = append(lines, fmt.Sprintf("%s: storage: %s", name, r.Staging.StorageModeNotes))
	}
	lines = append(lines, fmt.Sprintf("%s: %s", name, r.Stationarity.Reason))
	return strings.Join(lines, "\n")
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	sorted := append([]float64(nil), v...)
	sort.Float64s(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2.0
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
