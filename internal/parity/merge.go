package parity

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// MergeTolerance is the relative agreement two exports of the same counter must
// reach to be treated as the same measurement. The Counters.csv export rounds
// to two decimals while the sub-tab exports carry four, so "0.20" and "0.2012"
// are the same number rendered twice, not two measurements.
const MergeTolerance = 0.02

// Disagreement is one counter on which two exports of the same capture do not
// agree, beyond what rounding explains.
type Disagreement struct {
	Column  string
	Encoder string
	A, B    string
}

// Merge unions two oracles for the same capture, keeping the more precise
// rendering of every shared column.
//
// It returns the disagreements rather than failing on them: two of Xcode's own
// exports differing on a cell is a fact about the oracle, and the harness's job
// is to report it, not to pick a winner silently.
func Merge(a, b *Oracle) (*Oracle, []Disagreement, error) {
	if !equalStrings(a.Encoders, b.Encoders) {
		return nil, nil, fmt.Errorf("parity: exports cover different encoders (%d vs %d); they are not the same capture",
			len(a.Encoders), len(b.Encoders))
	}

	out := &Oracle{Encoders: a.Encoders, Display: a.Display, values: make(map[string][]string)}
	byName := make(map[string]Column)
	for _, c := range a.Columns {
		byName[c.Name] = c
		out.values[c.Name] = a.values[c.Name]
	}

	var disagreements []Disagreement
	for _, c := range b.Columns {
		prev, ok := byName[c.Name]
		if !ok {
			byName[c.Name] = c
			out.values[c.Name] = b.values[c.Name]
			continue
		}
		av, bv := a.values[c.Name], b.values[c.Name]
		for i, enc := range a.Encoders {
			if !withinMergeTolerance(av[i], bv[i]) {
				disagreements = append(disagreements, Disagreement{Column: c.Name, Encoder: enc, A: av[i], B: bv[i]})
			}
		}
		// Keep whichever rendering carries more significant digits, so a
		// comparison is never decided by the coarser export's rounding.
		if precision(bv) > precision(av) {
			out.values[c.Name] = bv
			prev.Populated = c.Populated || prev.Populated
			prev.Constant = c.Constant && prev.Constant
		}
		prev.Sources = append(prev.Sources, c.Sources...)
		sort.Strings(prev.Sources)
		if c.RepeatedHeaders > prev.RepeatedHeaders {
			prev.RepeatedHeaders = c.RepeatedHeaders
		}
		if prev.DuplicateOf == "" {
			prev.DuplicateOf = c.DuplicateOf
		}
		byName[c.Name] = prev
	}

	for _, c := range byName {
		out.Columns = append(out.Columns, c)
	}
	sort.Slice(out.Columns, func(i, j int) bool { return out.Columns[i].Name < out.Columns[j].Name })
	out.markDuplicates()
	return out, disagreements, nil
}

func withinMergeTolerance(a, b string) bool {
	if a == b {
		return true
	}
	x, err1 := ParseNumber(a)
	y, err2 := ParseNumber(b)
	if err1 != nil || err2 != nil {
		return false
	}
	d := math.Abs(x - y)
	if d <= 1e-9 {
		return true
	}
	// Both exports round, so a disagreement no larger than the coarser
	// rendering's last place is rounding rather than measurement.
	if d <= 0.5*math.Pow(10, -float64(minDecimals(a, b))) {
		return true
	}
	return d/math.Max(math.Abs(x), math.Abs(y)) <= MergeTolerance
}

func minDecimals(a, b string) int {
	da, db := decimals(a), decimals(b)
	if da < db {
		return da
	}
	return db
}

func decimals(s string) int {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "%"))
	i := strings.IndexByte(s, '.')
	if i < 0 {
		return 0
	}
	n := 0
	for _, r := range s[i+1:] {
		if r < '0' || r > '9' {
			break
		}
		n++
	}
	return n
}

// precision scores a rendering by its total significant decimal places, so the
// finer of two renderings of the same counter can be kept.
func precision(vals []string) int {
	total := 0
	for _, v := range vals {
		total += decimals(v)
	}
	return total
}
