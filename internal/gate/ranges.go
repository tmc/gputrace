package gate

import (
	"fmt"
	"strconv"
	"strings"
)

// TokenRange is a half-open token range [Lo, Hi).
type TokenRange struct {
	Lo int `json:"lo"`
	Hi int `json:"hi"`
}

// Width returns the number of tokens covered by the range.
func (r TokenRange) Width() int { return r.Hi - r.Lo }

// Contains reports whether r fully contains s.
func (r TokenRange) Contains(s TokenRange) bool { return r.Lo <= s.Lo && r.Hi >= s.Hi }

func (r TokenRange) String() string { return fmt.Sprintf("[%d,%d)", r.Lo, r.Hi) }

// RangeArm pairs one bundle with its declared token range and the
// invariant-matched dispatch count observed in it.
type RangeArm struct {
	Bundle       string     `json:"bundle"`
	Range        TokenRange `json:"range"`
	MatchedCount int        `json:"matched_count"`
	Result       *Result    `json:"result"`
}

// RangesResult reports the nested-range monotonicity check: captures of
// nested half-open token ranges must show strictly growing invariant
// dispatch counts. It is the only check that catches a capture hook sitting
// downstream of already-queued GPU work, which a single-bundle invariant
// count cannot.
type RangesResult struct {
	Verdict Verdict    `json:"verdict"`
	Arms    []RangeArm `json:"arms"`
	Notes   []string   `json:"notes,omitempty"`
	Summary string     `json:"summary"`
}

// ParseTokenRange parses "lo:hi" as a half-open token range [lo, hi).
func ParseTokenRange(s string) (TokenRange, error) {
	lo, hi, ok := strings.Cut(s, ":")
	if !ok {
		return TokenRange{}, fmt.Errorf("range %q: want lo:hi", s)
	}
	l, err := strconv.Atoi(strings.TrimSpace(lo))
	if err != nil {
		return TokenRange{}, fmt.Errorf("range %q: bad lo: %w", s, err)
	}
	h, err := strconv.Atoi(strings.TrimSpace(hi))
	if err != nil {
		return TokenRange{}, fmt.Errorf("range %q: bad hi: %w", s, err)
	}
	if h <= l {
		return TokenRange{}, fmt.Errorf("range %q: hi must exceed lo (half-open [lo,hi))", s)
	}
	return TokenRange{Lo: l, Hi: h}, nil
}

// EvaluateRanges evaluates each bundle and checks that invariant dispatch
// counts grow monotonically with nested token ranges. ranges[i] labels
// bundles[i]; each range must contain the previous one with strictly
// larger width. Counts must strictly increase, and each width increase must
// add at least (widthDelta - slack) invariant dispatches.
func EvaluateRanges(bundles, ranges []string, opts Options) (*RangesResult, error) {
	if len(bundles) != len(ranges) {
		return nil, fmt.Errorf("got %d bundles but %d ranges: pass one lo:hi per bundle", len(bundles), len(ranges))
	}
	if len(bundles) < 2 {
		return nil, fmt.Errorf("nested-range check needs at least 2 bundles, got %d", len(bundles))
	}

	parsed := make([]TokenRange, len(ranges))
	for i, s := range ranges {
		r, err := ParseTokenRange(s)
		if err != nil {
			return nil, err
		}
		parsed[i] = r
	}
	for i := 1; i < len(parsed); i++ {
		if !parsed[i].Contains(parsed[i-1]) || parsed[i].Width() <= parsed[i-1].Width() {
			return nil, fmt.Errorf("range %s does not nest strictly inside %s: pass ranges innermost first", parsed[i-1], parsed[i])
		}
	}

	// Completeness scoring against -t belongs to single-bundle mode; here
	// only the count trend across arms is under test.
	armOpts := opts
	armOpts.Tokens = 0

	res := &RangesResult{}
	for i, bundle := range bundles {
		r, err := Evaluate(bundle, armOpts)
		if err != nil {
			return nil, fmt.Errorf("evaluate %s: %w", bundle, err)
		}
		res.Arms = append(res.Arms, RangeArm{
			Bundle:       bundle,
			Range:        parsed[i],
			MatchedCount: r.Completeness.MatchedCount,
			Result:       r,
		})
	}

	invariant := res.Arms[0].Result.Completeness.InvariantSymbol
	res.Verdict = scoreRangeTrend(res, invariant, opts.Slack)

	var lines []string
	lines = append(lines, "Nested-range monotonicity:")
	for _, arm := range res.Arms {
		lines = append(lines, fmt.Sprintf("  %-10s %4d %s dispatches  (%s)",
			arm.Range, arm.MatchedCount, invariant, arm.Bundle))
	}
	switch res.Verdict {
	case VerdictPass:
		lines = append(lines, "  monotonicity ok    counts grow with range width")
	case VerdictFail:
		lines = append(lines, "  monotonicity FAIL")
	default:
		lines = append(lines, "  monotonicity NOT EVALUABLE")
	}
	for _, n := range res.Notes {
		lines = append(lines, "  "+n)
	}
	res.Summary = strings.Join(lines, "\n")
	return res, nil
}

// scoreRangeTrend scores the count trend across nested arms and appends
// explanatory notes to res.
func scoreRangeTrend(res *RangesResult, invariant string, slack int) Verdict {
	for _, arm := range res.Arms {
		if arm.MatchedCount == 0 {
			res.Notes = append(res.Notes, fmt.Sprintf("%s: invariant matched 0 dispatches: cannot evaluate", arm.Bundle))
			return VerdictNotEvaluable
		}
	}
	verdict := VerdictPass
	for i := 1; i < len(res.Arms); i++ {
		prev, cur := res.Arms[i-1], res.Arms[i]
		countDelta := cur.MatchedCount - prev.MatchedCount
		widthDelta := cur.Range.Width() - prev.Range.Width()
		switch {
		case countDelta <= 0:
			verdict = VerdictFail
			res.Notes = append(res.Notes, fmt.Sprintf(
				"count did not grow %s -> %s (%d -> %d): capture hook may sit downstream of queued GPU work",
				prev.Range, cur.Range, prev.MatchedCount, cur.MatchedCount))
		case countDelta < widthDelta-slack:
			verdict = VerdictFail
			res.Notes = append(res.Notes, fmt.Sprintf(
				"%s -> %s added %d tokens but only %d %s dispatches (slack %d)",
				prev.Range, cur.Range, widthDelta, countDelta, invariant, slack))
		default:
			res.Notes = append(res.Notes, fmt.Sprintf(
				"%s -> %s: +%d tokens, +%d dispatches ok",
				prev.Range, cur.Range, widthDelta, countDelta))
		}
	}
	return verdict
}
