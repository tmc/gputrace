package parity

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"text/tabwriter"
)

// Status is the outcome of comparing one column.
type Status int

const (
	// NotProduced means gputrace emits nothing for this column. It is never
	// reported as a match and never rendered as 0.00.
	NotProduced Status = iota
	// NoSignal means the oracle column is empty or zero for every encoder, so
	// the capture says nothing about it. For a compute workload this is the
	// expected state of every graphics column.
	NoSignal
	// OracleSuspect means the oracle column is present but not trustworthy:
	// constant across all encoders, or byte-identical to another column, or
	// listed in KnownOracleDefects.
	OracleSuspect
	// Match means we produce the column and agree with Xcode on every encoder.
	Match
	// Mismatch means we produce the column and disagree on at least one encoder.
	Mismatch
)

func (s Status) String() string {
	switch s {
	case NotProduced:
		return "NOT PRODUCED"
	case NoSignal:
		return "NO SIGNAL"
	case OracleSuspect:
		return "ORACLE SUSPECT"
	case Match:
		return "MATCH"
	case Mismatch:
		return "MISMATCH"
	}
	return "UNKNOWN"
}

// KnownOracleDefects lists oracle columns whose values are wrong or empty in
// Xcode's own output, with the evidence. A disagreement in one of these says
// nothing about gputrace. See testdata/xcode-oracle/PROVENANCE.md.
var KnownOracleDefects = map[string]string{
	"Kernel Texture Cache Miss Rate": "0.00% in all 23 rows: no information",
	"Kernel ALU Performance":         "byte-identical to Kernel ALU Instructions in all 23 rows: a raw count under a performance label",
	"Kernel Invocations":             "0 for two encoders that have non-zero Execution Cost and real dispatches",
}

// ColumnResult is the comparison outcome for one column.
type ColumnResult struct {
	Column   string
	Tab      string
	Status   Status
	Unit     string   // from GPUCounterGraph.plist, empty if unresolved
	Vendor   []string // vendor counters the column is computed from
	Sources  []string // which Xcode exports carry this column
	Note     string   // why, in one line
	Deriv    Derivation
	Failures []CellDiff // every disagreeing cell, never truncated
}

// CellDiff is one disagreeing encoder within a column.
type CellDiff struct {
	Encoder string
	Ours    string
	Xcode   string
}

// Report is the full per-column standing.
type Report struct {
	Trace       string
	Results     []ColumnResult
	Encoders    int
	OracleTabs  int
	CatalogPath string
	Unresolved  []string // oracle columns with no GPUCounterGraph entry
	// Extra are columns gputrace produces for which the *loaded* oracle has no
	// column. That is a statement about the exports that were loaded, not about
	// what Xcode measures: load only Counters.csv and Execution Cost lands here,
	// even though Xcode's Counters tab shows it on every sub-tab.
	Extra        []string
	ObserveNotes []string
	// Disagreements are cells on which Xcode's two exports of this capture do
	// not agree beyond rounding.
	Disagreements []Disagreement
	// CatalogTotal is how many counters GPUCounterGraph.plist defines. The
	// oracle is a subset of it, and the exports are in turn a subset of what
	// Xcode measures: the Timeline's Occupancy filter shows
	// "SIMD Groups Inflight per Core", which appears in no export column.
	CatalogTotal int
}

// Tolerance is the relative agreement required between our value and Xcode's.
// Xcode prints two decimals, so exact string equality is too strict and any
// looser threshold starts calling different measurements the same.
const Tolerance = 0.01

// Compare joins an observation to the oracle on encoder key and classifies
// every oracle column.
func Compare(o *Oracle, obs *Observation, cat *Catalog, tracePath string) *Report {
	rep := &Report{
		Trace:        tracePath,
		Encoders:     len(o.Encoders),
		ObserveNotes: obs.Notes,
	}
	if cat != nil {
		rep.CatalogPath = cat.Path
		rep.CatalogTotal = len(cat.Counters)
	}

	// Map oracle row index by join key so a column can be looked up per encoder.
	ourIndex := make(map[string]int, len(obs.Encoders))
	for i, k := range obs.Encoders {
		ourIndex[k] = i
	}

	tabs := make(map[string]bool)
	for _, col := range o.Columns {
		tabs[col.Tab] = true
		res := ColumnResult{Column: col.Name, Tab: col.Tab, Sources: col.Sources}
		if e, ok := cat.Lookup(col.Name); ok {
			res.Unit = e.Unit
			res.Vendor = e.VendorCounters
		} else {
			rep.Unresolved = append(rep.Unresolved, col.Name)
		}

		ours, produced := obs.Values[col.Name]
		if produced {
			res.Deriv = obs.Derivations[col.Name]
		}

		switch {
		case !col.Populated:
			res.Status = NoSignal
			res.Note = "oracle column is zero or empty for every encoder in this capture"
			if produced {
				res.Note += "; gputrace produces a value, so there is nothing to check it against"
			}
		case KnownOracleDefects[col.Name] != "":
			res.Status = OracleSuspect
			res.Note = KnownOracleDefects[col.Name]
		case col.DuplicateOf != "":
			res.Status = OracleSuspect
			res.Note = "byte-identical to " + col.DuplicateOf + " in every row"
		case col.Constant:
			res.Status = OracleSuspect
			res.Note = "constant across all encoders: carries no per-encoder information"
		case !produced:
			res.Status = NotProduced
			res.Note = "gputrace emits no value for this column"
		default:
			res.Failures = compareColumn(o, col.Name, obs.Encoders, ours, ourIndex)
			if len(res.Failures) == 0 {
				res.Status = Match
			} else {
				res.Status = Mismatch
				res.Note = fmt.Sprintf("%d of %d encoders disagree", len(res.Failures), len(o.Encoders))
			}
		}
		rep.Results = append(rep.Results, res)
	}
	rep.OracleTabs = len(tabs)
	sort.Strings(rep.Unresolved)
	for _, name := range obs.Columns() {
		if _, ok := o.Column(name); !ok {
			rep.Extra = append(rep.Extra, fmt.Sprintf("%s [%s] %s",
				name, obs.Derivations[name].Kind, obs.Derivations[name].How))
		}
	}
	return rep
}

func compareColumn(o *Oracle, column string, keys, ours []string, ourIndex map[string]int) []CellDiff {
	var diffs []CellDiff
	for _, enc := range o.Encoders {
		want, _ := o.Value(column, enc)
		i, ok := ourIndex[enc]
		if !ok {
			diffs = append(diffs, CellDiff{Encoder: o.DisplayName(enc), Ours: "(no such encoder)", Xcode: want})
			continue
		}
		got := ours[i]
		if agree(got, want) {
			continue
		}
		diffs = append(diffs, CellDiff{Encoder: o.DisplayName(enc), Ours: got, Xcode: want})
	}
	return diffs
}

func agree(got, want string) bool {
	if strings.TrimSpace(got) == strings.TrimSpace(want) {
		return true
	}
	a, err1 := ParseNumber(got)
	b, err2 := ParseNumber(want)
	if err1 != nil || err2 != nil {
		return false
	}
	if a == b {
		return true
	}
	d := math.Abs(a - b)
	if d <= 1e-9 {
		return true
	}
	return d/math.Max(math.Abs(a), math.Abs(b)) <= Tolerance
}

// sourceTag abbreviates which Xcode exports carry a column: "both", "csv" for
// Counters.csv only, "tabs" for the sub-tab exports only.
func sourceTag(sources []string) string {
	var tabs, csv bool
	for _, s := range sources {
		if strings.Contains(s, "Counters.csv") {
			csv = true
		} else {
			tabs = true
		}
	}
	switch {
	case tabs && csv:
		return "both"
	case csv:
		return "csv"
	case tabs:
		return "tabs"
	}
	return "-"
}

// Counts returns the number of columns in each status.
func (r *Report) Counts() map[Status]int {
	m := make(map[Status]int)
	for _, res := range r.Results {
		m[res.Status]++
	}
	return m
}

// Scored returns how many columns the comparison actually decided: the ones
// gputrace produced and the oracle could check.
//
// It is the only number that says whether a run compared anything. The other
// four statuses are reached without looking at a gputrace value at all, so a
// report can fill a screen with rows, carry no failure, and still have compared
// nothing -- which is what a Counters.csv-only oracle produces.
func (r *Report) Scored() int {
	c := r.Counts()
	return c[Match] + c[Mismatch]
}

// CheckScored returns an error when the comparison decided no column.
//
// A caller that only prints the report cannot tell that case apart from a clean
// run: the table is full either way and no row carries a failure. The error
// names the produced columns the oracle had nothing to check, because the way
// this state is reached in practice is a partial oracle rather than a gputrace
// that produces nothing.
func (r *Report) CheckScored() error {
	if r.Scored() > 0 {
		return nil
	}
	return fmt.Errorf("compared nothing: 0 of %d oracle columns were decided; "+
		"%d columns gputrace produces have no column in this oracle (%s). "+
		"Xcode's Counters.csv omits %s, and Execution Cost is the only one of those gputrace produces, "+
		"so an oracle loaded from Counters.csv alone reaches exactly this state",
		len(r.Results), len(r.Extra), strings.Join(r.Extra, "; "), strings.Join(CountersCSVOmits, ", "))
}

// Write renders the full report. Every column appears; nothing is truncated,
// and every disagreeing cell of every mismatched column is listed.
func (r *Report) Write(w io.Writer) {
	fmt.Fprintf(w, "gputrace vs Xcode Counters parity\n")
	fmt.Fprintf(w, "trace:    %s\n", r.Trace)
	fmt.Fprintf(w, "encoders: %d\n", r.Encoders)
	fmt.Fprintf(w, "oracle:   %d distinct columns from %d Xcode exports\n", len(r.Results), r.OracleTabs)
	if r.CatalogPath != "" {
		fmt.Fprintf(w, "catalog:  %s (%d counters defined)\n", r.CatalogPath, r.CatalogTotal)
	} else {
		fmt.Fprintf(w, "catalog:  not installed; units and vendor counters unavailable\n")
	}
	fmt.Fprintf(w, "\nThe oracle is not the universe. GPUCounterGraph.plist defines %d counters;\n"+
		"Xcode's exports expose %d of them here, and the Timeline's Occupancy filter shows\n"+
		"at least one more (\"SIMD Groups Inflight per Core\") that no export column carries.\n"+
		"NOT PRODUCED counts below are against what Xcode exports, not against what it measures.\n\n",
		r.CatalogTotal, len(r.Results))

	counts := r.Counts()
	fmt.Fprintln(w, "standing")
	for _, s := range []Status{Match, Mismatch, NotProduced, OracleSuspect, NoSignal} {
		fmt.Fprintf(w, "  %-15s %4d\n", s, counts[s])
	}
	fmt.Fprintf(w, "  %-15s %4d  (MATCH+MISMATCH: the columns this run actually decided)\n", "SCORED", r.Scored())
	if r.Scored() == 0 {
		fmt.Fprintf(w, "\nThis run decided nothing. Every row below was classified without comparing a\n"+
			"gputrace value to an Xcode one. Read it as a coverage inventory, not as a result.\n")
	}
	fmt.Fprintln(w)

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "STATUS\tCOLUMN\tUNIT\tIN\tOURS\tNOTE")
	for _, res := range r.Results {
		src := res.Deriv.Kind
		if src == "" {
			src = "-"
		}
		unit := res.Unit
		if unit == "" {
			unit = "(unresolved)"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", res.Status, res.Column, unit,
			sourceTag(res.Sources), src, res.Note)
	}
	tw.Flush()

	if len(r.Disagreements) > 0 {
		fmt.Fprintf(w, "\ncells on which Xcode's two exports of this capture disagree beyond rounding (%d)\n", len(r.Disagreements))
		dt := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(dt, "  COLUMN\tENCODER\tSUB-TABS\tCOUNTERS.CSV")
		for _, d := range r.Disagreements {
			fmt.Fprintf(dt, "  %s\t%s\t%s\t%s\n", d.Column, d.Encoder, d.A, d.B)
		}
		dt.Flush()
	}

	for _, res := range r.Results {
		if len(res.Failures) == 0 {
			continue
		}
		fmt.Fprintf(w, "\nmismatch detail: %s (%s)\n", res.Column, res.Deriv.How)
		dt := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(dt, "  ENCODER\tGPUTRACE\tXCODE")
		for _, f := range res.Failures {
			fmt.Fprintf(dt, "  %s\t%s\t%s\n", f.Encoder, f.Ours, f.Xcode)
		}
		dt.Flush()
	}

	if len(r.Extra) > 0 {
		fmt.Fprintf(w, "\nper-encoder values gputrace produces that the loaded oracle has no column for (%d)\n"+
			"  This says the loaded exports lack the column, not that Xcode does. Counters.csv\n"+
			"  omits Execution Cost, so loading it alone files our one comparable column here.\n", len(r.Extra))
		for _, n := range r.Extra {
			fmt.Fprintf(w, "  %s\n", n)
		}
	}
	if len(r.ObserveNotes) > 0 {
		fmt.Fprintln(w, "\nwhat gputrace could not produce, and why")
		for _, n := range r.ObserveNotes {
			fmt.Fprintf(w, "  %s\n", n)
		}
	}
	if len(r.Unresolved) > 0 {
		fmt.Fprintf(w, "\noracle columns with no GPUCounterGraph entry (%d)\n", len(r.Unresolved))
		for _, n := range r.Unresolved {
			fmt.Fprintf(w, "  %s\n", n)
		}
	}
}
