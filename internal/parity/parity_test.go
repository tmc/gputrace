package parity_test

import (
	"bytes"
	"os"
	"path"
	"slices"
	"strings"
	"testing"

	"github.com/tmc/gputrace/internal/parity"
)

const oracleDir = "../../testdata/xcode-oracle"

// parityOracleDir returns the oracle TestParity scores against. It defaults to
// the 23-encoder oracle; GPUTRACE_PARITY_ORACLE selects another, so the
// 11-encoder oracle is reachable without editing this file.
//
// Pointing this at an oracle the trace did not come from is safe to attempt but
// not safe to believe, and TestParity refuses it: the encoder-count and
// join-key checks below run before any column is scored, so a mismatched pair
// fails rather than producing a match rate for two different captures.
func parityOracleDir() string {
	if dir := os.Getenv("GPUTRACE_PARITY_ORACLE"); dir != "" {
		return dir
	}
	return oracleDir
}

func loadOracle(t *testing.T) *parity.Oracle {
	t.Helper()
	o, _, err := parity.Load(os.DirFS(oracleDir), ".")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return o
}

// TestBothOraclesLoad checks that every checked-in oracle directory loads, so
// that TestParity can be pointed at either one.
//
// The second oracle did not load at all until the loader stopped assuming every
// .txt in the directory was an encoder tab. It also holds Xcode's Shaders tab,
// which is keyed by kernel function and pipeline state rather than by encoder,
// and the encoder-list comparison reported that as "not from one capture" --
// naming a cause that was not the cause.
//
// The encoder counts are asserted because they are the property that makes the
// two oracles worth keeping separately: the same Execution Cost method scores
// 0.911 pp worst-case against one and 2.941 pp against the other.
func TestBothOraclesLoad(t *testing.T) {
	for _, test := range []struct {
		dir      string
		encoders int
		skipped  []string
	}{
		{dir: "../../testdata/xcode-oracle", encoders: 23},
		{dir: "../../testdata/xcode-oracle-static-tokens2to3", encoders: 11, skipped: []string{"shaders.txt"}},
	} {
		t.Run(path.Base(test.dir), func(t *testing.T) {
			o, _, err := parity.Load(os.DirFS(test.dir), ".")
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if len(o.Encoders) != test.encoders {
				t.Errorf("encoders = %d, want %d", len(o.Encoders), test.encoders)
			}
			if !slices.Equal(o.Skipped, test.skipped) {
				t.Errorf("skipped = %v, want %v", o.Skipped, test.skipped)
			}
			if len(o.Columns) == 0 {
				t.Error("no columns loaded")
			}
		})
	}
}

// TestSourcesReconcile checks the two independent Xcode exports of this capture
// against each other. They cover overlapping but different counter sets, so the
// union is the oracle, and any cell they disagree on beyond rounding is a fact
// about Xcode rather than about gputrace.
func TestSourcesReconcile(t *testing.T) {
	fsys := os.DirFS(oracleDir)
	tabs, err := parity.LoadOracle(fsys, ".")
	if err != nil {
		t.Fatalf("LoadOracle: %v", err)
	}
	joined, err := parity.LoadCountersCSV(fsys, parity.CountersCSVName)
	if err != nil {
		t.Fatalf("LoadCountersCSV: %v", err)
	}
	if got, want := len(joined.Encoders), 23; got != want {
		t.Errorf("Counters.csv encoders = %d, want %d", got, want)
	}
	// Xcode names the join key outright in Counters.csv. It must agree with the
	// number we recover from the sub-tab exports' encoder display names.
	for i := range tabs.Encoders {
		if tabs.Encoders[i] != joined.Encoders[i] {
			t.Fatalf("encoder %d: sub-tabs say %q, Counters.csv says %q",
				i, tabs.Encoders[i], joined.Encoders[i])
		}
	}

	merged, disagreements, err := parity.Merge(tabs, joined)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	for _, d := range disagreements {
		t.Errorf("exports disagree: %s at %s: sub-tabs %q, Counters.csv %q",
			d.Column, d.Encoder, d.A, d.B)
	}

	var onlyTabs, onlyCSV int
	for _, c := range merged.Columns {
		_, inTabs := tabs.Column(c.Name)
		_, inCSV := joined.Column(c.Name)
		switch {
		case inTabs && !inCSV:
			onlyTabs++
		case inCSV && !inTabs:
			onlyCSV++
		}
	}
	t.Logf("union %d columns: %d sub-tabs only, %d Counters.csv only",
		len(merged.Columns), onlyTabs, onlyCSV)
	if onlyTabs == 0 || onlyCSV == 0 {
		t.Errorf("one export is a superset of the other (%d tabs-only, %d csv-only); "+
			"both are checked in because neither is", onlyTabs, onlyCSV)
	}
	if _, ok := joined.Column("Execution Cost"); ok {
		t.Error("Counters.csv now carries Execution Cost; it did not, which is why the sub-tabs are kept")
	}
}

// TestNoSIMDInflightColumn records a negative result. Xcode's Timeline shows
// "SIMD Groups Inflight per Core" under its Occupancy filter, but no Counters
// export carries it, so the exports are a subset of what Xcode measures and the
// per-column accounting must not be read as covering everything.
func TestNoSIMDInflightColumn(t *testing.T) {
	o := loadOracle(t)
	for _, c := range o.Columns {
		for _, needle := range []string{"SIMD", "Inflight", "Active Core"} {
			if strings.Contains(c.Name, needle) {
				t.Errorf("column %q matches %q; the exports were thought to omit occupancy-mechanism counters", c.Name, needle)
			}
		}
	}
}

// TestRepeatedHeadersAreIdentical pins the Counters.csv export quirk: sixteen
// column names appear twice. Every pair holds the same values, so keying on
// name is safe here -- but only because this test says so.
func TestRepeatedHeadersAreIdentical(t *testing.T) {
	// LoadCountersCSV fails if any repeated pair differs, so reaching here with
	// the expected count is the check.
	joined, err := parity.LoadCountersCSV(os.DirFS(oracleDir), parity.CountersCSVName)
	if err != nil {
		t.Fatalf("LoadCountersCSV: %v", err)
	}
	var repeated int
	for _, c := range joined.Columns {
		if c.RepeatedHeaders > 1 {
			repeated++
		}
	}
	if got, want := repeated, 16; got != want {
		t.Errorf("repeated header names = %d, want %d", got, want)
	}
}

// TestOracleJoins checks the fixture itself: every sub-tab export lists the
// same 23 encoders, and re-exports of the same tab agree cell for cell. If this
// fails, the exports are not from one capture, or Xcode's export is not
// deterministic, and no comparison built on them means anything.
func TestOracleJoins(t *testing.T) {
	o := loadOracle(t)
	if got, want := len(o.Encoders), 23; got != want {
		t.Errorf("encoders = %d, want %d", got, want)
	}
	if len(o.Columns) == 0 {
		t.Fatal("no columns")
	}
	for _, c := range o.Columns {
		if _, ok := o.Value(c.Name, o.Encoders[0]); !ok {
			t.Errorf("column %q has no value for the first encoder", c.Name)
		}
	}
}

// TestOracleDefectsStillPresent pins the cells we have decided not to trust. If
// one of these starts looking healthy the fixture was re-exported from a
// different capture and KnownOracleDefects needs revisiting.
func TestOracleDefectsStillPresent(t *testing.T) {
	o := loadOracle(t)

	c, ok := o.Column("Kernel Texture Cache Miss Rate")
	if !ok {
		t.Fatal("Kernel Texture Cache Miss Rate missing from oracle")
	}
	if c.Populated {
		t.Error("Kernel Texture Cache Miss Rate is populated; it was all-zero when the defect was recorded")
	}

	c, ok = o.Column("Kernel ALU Performance")
	if !ok {
		t.Fatal("Kernel ALU Performance missing from oracle")
	}
	if c.DuplicateOf != "Kernel ALU Instructions" {
		t.Errorf("Kernel ALU Performance duplicates %q, want Kernel ALU Instructions", c.DuplicateOf)
	}
}

// TestALUUtilizationHasSignal records the fact that motivated the harness:
// ALU Utilization is a real, varying measurement in Xcode's export. Any claim
// that gputrace has closed this gap has to beat these numbers, not zero.
func TestALUUtilizationHasSignal(t *testing.T) {
	o := loadOracle(t)
	c, ok := o.Column("ALU Utilization")
	if !ok {
		t.Fatal("ALU Utilization missing from oracle")
	}
	if !c.Populated || c.Constant {
		t.Fatalf("ALU Utilization populated=%v constant=%v, want a varying column", c.Populated, c.Constant)
	}
	v, _ := o.Value("ALU Utilization", o.Encoders[0])
	if got, want := v, "1.59%"; got != want {
		t.Errorf("ALU Utilization[0] = %q, want %q", got, want)
	}
}

// TestParity runs the comparison against a real capture. The bundle is ~17 GB
// and is not in the repository, so the test skips without it.
//
//	go test ./internal/parity -run TestParity -v \
//	    -gputrace.trace=/path/to/x.gputrace
func TestParity(t *testing.T) {
	tracePath := os.Getenv("GPUTRACE_PARITY_TRACE")
	if tracePath == "" {
		t.Skip("set GPUTRACE_PARITY_TRACE to a .gputrace bundle matching the oracle in GPUTRACE_PARITY_ORACLE (default testdata/xcode-oracle)")
	}
	dir := parityOracleDir()
	t.Logf("oracle: %s", dir)
	o, disagreements, err := parity.Load(os.DirFS(dir), ".")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	obs, err := parity.Observe(tracePath)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if len(obs.Encoders) != len(o.Encoders) {
		t.Fatalf("gputrace sees %d encoders, oracle %s has %d: the trace does not match the oracle",
			len(obs.Encoders), dir, len(o.Encoders))
	}
	for i, enc := range o.Encoders {
		if got := obs.Encoders[i]; got != enc {
			t.Fatalf("encoder %d join key = %q, oracle %q (%q)", i, got, enc, o.DisplayName(enc))
		}
	}

	cat, err := parity.LoadCatalog(parity.CounterGraphPaths())
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	rep := parity.Compare(o, obs, cat, tracePath)
	rep.Disagreements = disagreements

	var buf bytes.Buffer
	rep.Write(&buf)
	t.Log("\n" + buf.String())

	if out := os.Getenv("GPUTRACE_PARITY_OUT"); out != "" {
		if err := os.WriteFile(out, buf.Bytes(), 0o644); err != nil {
			t.Fatalf("write report: %v", err)
		}
	}
}
