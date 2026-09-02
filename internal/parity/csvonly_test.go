package parity_test

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/tmc/gputrace/internal/parity"
)

// These tests record why the Counters.csv export cannot stand in for a lost
// sub-tab oracle, and pin the refusal that says so.
//
// The tempting move, when the sub-tab exports of a capture are gone but its
// Counters.csv survives, is to let parity.Load fall back to the CSV. It looks
// like a reduced oracle: 226 columns against the sub-tabs' 205, and 29 columns
// the sub-tabs do not have at all. It is not reduced. It is empty, in exactly
// the direction that matters, and it prints a full table while being so.

// TestCountersCSVOmitsIsMeasured rederives parity.CountersCSVOmits from the
// fixture. The constant exists so the refusal message can name the cost of the
// fallback; if Xcode's export changes, the constant must change with it rather
// than keep asserting an old shape.
func TestCountersCSVOmitsIsMeasured(t *testing.T) {
	fsys := os.DirFS(oracleDir)
	tabs, err := parity.LoadOracle(fsys, ".")
	if err != nil {
		t.Fatalf("LoadOracle: %v", err)
	}
	joined, err := parity.LoadCountersCSV(fsys, parity.CountersCSVName)
	if err != nil {
		t.Fatalf("LoadCountersCSV: %v", err)
	}
	var missing []string
	for _, c := range tabs.Columns {
		if _, ok := joined.Column(c.Name); !ok {
			missing = append(missing, c.Name)
		}
	}
	sort.Strings(missing)
	want := slices.Clone(parity.CountersCSVOmits)
	sort.Strings(want)
	if !slices.Equal(missing, want) {
		t.Errorf("Counters.csv omits %v, CountersCSVOmits says %v", missing, want)
	}
	// Of those, only some carry per-encoder information in this capture. The
	// refusal rests on Execution Cost specifically, so check it is one of them.
	c, ok := tabs.Column("Execution Cost")
	if !ok {
		t.Fatal("Execution Cost missing from the sub-tab oracle")
	}
	if !c.Populated || c.Constant {
		t.Errorf("Execution Cost populated=%v constant=%v; the refusal assumes it is a varying measurement",
			c.Populated, c.Constant)
	}
}

// TestLoadRefusesCountersCSVAlone pins the refusal a previous session declined
// to overturn: a directory holding only Counters.csv is not an oracle.
//
// Before this test the refusal was incidental -- Load failed because a glob for
// *.txt matched nothing, and said so, which reads like a missing-file accident
// rather than a decision. The error now states the cost of the fallback, and
// this test is what keeps a future reader from adding the fallback back.
func TestLoadRefusesCountersCSVAlone(t *testing.T) {
	dir := t.TempDir()
	data, err := os.ReadFile(filepath.Join(oracleDir, parity.CountersCSVName))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, parity.CountersCSVName), data, 0o644); err != nil {
		t.Fatalf("write copy: %v", err)
	}

	// The copy is a real, loadable export: LoadCountersCSV reads it. So the
	// refusal below is about the CSV not being sufficient, not about it being
	// unreadable.
	joined, err := parity.LoadCountersCSV(os.DirFS(dir), parity.CountersCSVName)
	if err != nil {
		t.Fatalf("LoadCountersCSV on the copy: %v", err)
	}
	if len(joined.Encoders) == 0 || len(joined.Columns) == 0 {
		t.Fatalf("copy loaded empty: %d encoders, %d columns", len(joined.Encoders), len(joined.Columns))
	}

	_, _, err = parity.Load(os.DirFS(dir), ".")
	if err == nil {
		t.Fatal("Load accepted a directory holding only Counters.csv; it is not an oracle")
	}
	for _, want := range []string{parity.CountersCSVName, "Execution Cost", "no column at all"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Load error does not mention %q; a reader cannot tell why the CSV is insufficient.\ngot: %v", want, err)
		}
	}
}

// TestCountersCSVAloneDecidesNothing is the measurement behind the refusal.
//
// The observation here is a stub: only its column *set* is load-bearing, and
// the values are deliberately not plausible so that no reading of this test can
// mistake them for a measurement. Which columns Observe publishes is the real
// input, and on every bundle measured so far that set contains exactly one
// Xcode column name, "Execution Cost" -- the Counters_f_*.raw path yields one
// row per pipeline rather than per encoder, so Observe publishes none of the
// utilization columns.
//
// The positive control against the sub-tab oracle is the point of the test: the
// same stub scores one column there and zero against the CSV, so the difference
// is the oracle and not the stub.
func TestCountersCSVAloneDecidesNothing(t *testing.T) {
	fsys := os.DirFS(oracleDir)
	tabs, err := parity.LoadOracle(fsys, ".")
	if err != nil {
		t.Fatalf("LoadOracle: %v", err)
	}
	joined, err := parity.LoadCountersCSV(fsys, parity.CountersCSVName)
	if err != nil {
		t.Fatalf("LoadCountersCSV: %v", err)
	}

	obs := &parity.Observation{
		Encoders:    slices.Clone(tabs.Encoders),
		Values:      map[string][]string{},
		Derivations: map[string]parity.Derivation{},
	}
	// -1% is not a value any counter can take, so a cell that agreed with it
	// would be a bug in the comparison rather than a match.
	bogus := make([]string, len(tabs.Encoders))
	for i := range bogus {
		bogus[i] = "-1.000%"
	}
	obs.Values["Execution Cost"] = bogus
	obs.Derivations["Execution Cost"] = parity.Derivation{Kind: "inference", How: "test stub, not a measurement"}

	csvRep := parity.Compare(joined, obs, nil, "test stub")
	if got := csvRep.Scored(); got != 0 {
		t.Errorf("Counters.csv-only oracle decided %d columns, want 0", got)
	}
	// This is the state TestParity used to pass in. CheckScored is what turns it
	// into a failure, so it is checked here on the real oracle rather than only
	// wired into a test that cannot run without a 2 GB bundle.
	err = csvRep.CheckScored()
	if err == nil {
		t.Error("CheckScored accepted a report that decided nothing")
	} else if !strings.Contains(err.Error(), "Execution Cost") {
		t.Errorf("CheckScored does not name the column that went unchecked: %v", err)
	}
	if !slices.ContainsFunc(csvRep.Extra, func(s string) bool { return strings.HasPrefix(s, "Execution Cost ") }) {
		t.Errorf("Execution Cost is not filed under Extra against the CSV-only oracle; Extra=%v", csvRep.Extra)
	}
	if len(csvRep.Results) == 0 {
		t.Fatal("CSV-only report has no rows; the point is that it has many and decides none of them")
	}
	t.Logf("Counters.csv-only: %d rows printed, %d decided", len(csvRep.Results), csvRep.Scored())

	tabRep := parity.Compare(tabs, obs, nil, "test stub")
	if got := tabRep.Scored(); got != 1 {
		t.Errorf("sub-tab oracle decided %d columns for the same observation, want 1", got)
	}
	if len(tabRep.Extra) != 0 {
		t.Errorf("sub-tab oracle files %v under Extra; it has a column for everything the stub produces", tabRep.Extra)
	}
	if err := tabRep.CheckScored(); err != nil {
		t.Errorf("CheckScored rejected a report that decided a column: %v", err)
	}
}

// TestObservationJoinsCountersCSV checks the half of the corpus that is
// regenerable: our own measurements.
//
// The sub-tab exports of a capture can be gone while its Counters.csv survives,
// and the bundle itself is reproducible -- a headless MTLReplayer replay writes
// a fresh .gpuprofiler_raw in about 25 s from the raw capture, with no Xcode UI:
//
//	open -W -n -a /System/Library/CoreServices/MTLReplayer.app --args \
//	  -CLI raw.gputrace -profileTrace -collectProfilerData -outputPath out.gputrace
//
// What that regenerates is the observation side. This test checks the one thing
// about it that can silently go wrong -- whether it joins to the surviving CSV
// on encoder key rather than by position -- and checks nothing else. It does
// not score a single counter, and must not be read as parity:
//
//	GPUTRACE_PARITY_TRACE=out.gputrace \
//	GPUTRACE_PARITY_COUNTERS_CSV=.../xcode-counters-export.csv \
//	go test ./internal/parity -run TestObservationJoinsCountersCSV -v
func TestObservationJoinsCountersCSV(t *testing.T) {
	tracePath := os.Getenv("GPUTRACE_PARITY_TRACE")
	csvPath := os.Getenv("GPUTRACE_PARITY_COUNTERS_CSV")
	if tracePath == "" || csvPath == "" {
		t.Skip("set GPUTRACE_PARITY_TRACE and GPUTRACE_PARITY_COUNTERS_CSV to a bundle and the Counters.csv Xcode exported for that same capture")
	}
	joined, err := parity.LoadCountersCSV(os.DirFS(filepath.Dir(csvPath)), filepath.Base(csvPath))
	if err != nil {
		t.Fatalf("LoadCountersCSV: %v", err)
	}
	obs, err := parity.Observe(tracePath)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if len(obs.Encoders) != len(joined.Encoders) {
		t.Fatalf("gputrace sees %d encoders, the export has %d: not the same capture",
			len(obs.Encoders), len(joined.Encoders))
	}
	// Equal counts are not a join. Xcode names the key outright in Counters.csv;
	// we recover it from encoderInfoData. They must agree value for value, or the
	// only thing relating the two tables is row order.
	for i := range joined.Encoders {
		if obs.Encoders[i] != joined.Encoders[i] {
			t.Fatalf("encoder %d: gputrace key %q, Xcode key %q (%q)",
				i, obs.Encoders[i], joined.Encoders[i], joined.DisplayName(joined.Encoders[i]))
		}
	}
	t.Logf("join verified on %d encoder keys: %v", len(obs.Encoders), obs.Encoders)
	t.Logf("NOT a parity result: no counter value was compared. gputrace publishes %v; "+
		"of those the CSV has a column for none, because Counters.csv omits %v",
		obs.Columns(), parity.CountersCSVOmits)
}
