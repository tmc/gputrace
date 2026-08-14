package counter

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/gputrace/internal/testtrace"
)

// TestCounterArchiveFromTrace checks the decode against a real archive. Set
// GPUTRACE_TEST_GPUPROFILER_DIR to a .gpuprofiler_raw directory to run it.
//
// The numbers below are not asserted as constants — they vary per capture.
// What is asserted are the invariants that a wrong record layout breaks:
// every blob's stride divides its length, the attributed samples all name an
// encoder of the capture, and machine-wide samples are counted separately
// rather than folded into per-encoder figures.
func TestCounterArchiveFromTrace(t *testing.T) {
	dir := testtrace.Path("GPUTRACE_TEST_GPUPROFILER_DIR", testtrace.ProfilerDir)
	if dir == "" {
		t.Skip("set GPUTRACE_TEST_TRACE to a .gputrace bundle, or GPUTRACE_TEST_GPUPROFILER_DIR to a .gpuprofiler_raw directory")
	}
	if _, err := os.Stat(filepath.Join(dir, "streamData")); err != nil {
		t.Skipf("no streamData in %s", dir)
	}

	stats, err := ParseStreamData(dir, nil)
	if err != nil {
		t.Fatalf("ParseStreamData: %v", err)
	}
	a := stats.CounterArchive
	if a == nil {
		t.Fatal("no counter archive parsed from APSCounterData")
	}
	t.Logf("counter archive: %s", a)
	t.Logf("blobs=%d stride_mismatches=%d known_encoder_ids=%d passes=%d",
		a.Blobs, a.StrideMismatches, a.KnownEncoderIDs, len(a.PassColumns))

	if a.StrideMismatches != 0 {
		t.Errorf("%d blobs rejected: derived stride did not divide the blob", a.StrideMismatches)
	}
	if a.TotalSamples == 0 {
		t.Fatal("no samples decoded")
	}
	if a.AttributedSamples == 0 {
		t.Error("no sample attributed to an encoder of this capture")
	}
	if len(a.AttributedRecords) != a.AttributedSamples {
		t.Errorf("retained %d attributed source records, want %d", len(a.AttributedRecords), a.AttributedSamples)
	}
	if a.AttributedSamples+a.MachineWideSamples > a.TotalSamples {
		t.Errorf("attributed(%d) + machine-wide(%d) exceeds total(%d)",
			a.AttributedSamples, a.MachineWideSamples, a.TotalSamples)
	}
	if len(a.Encoders) == 0 {
		t.Fatal("no per-encoder attribution")
	}

	var counted int
	for _, e := range a.Encoders {
		counted += e.SampleCount
		if e.EncoderID == GRCMachineWideID {
			t.Errorf("machine-wide id %#x folded into per-encoder attribution", e.EncoderID)
		}
		if e.EndTicks < e.StartTicks {
			t.Errorf("encoder %#x has end before start", e.EncoderID)
		}
	}
	if counted != a.AttributedSamples {
		t.Errorf("per-encoder counts sum to %d, want %d", counted, a.AttributedSamples)
	}
	for _, sample := range a.AttributedRecords {
		if sample.EncoderID == GRCMachineWideID {
			t.Errorf("machine-wide sample retained as capture-attributed: %#v", sample)
		}
		if sample.BlobOrdinal < 0 || sample.BlobOrdinal >= a.Blobs || sample.RecordOrdinal < 0 {
			t.Errorf("invalid source position: %#v", sample)
		}
	}

	// Every pass column list must begin with the seven GRC columns; that is
	// what makes the fixed prefix of a record decodable.
	for i, cols := range a.PassColumns {
		if len(cols) < len(GRCColumnNames) {
			t.Errorf("pass %d has %d columns, fewer than the fixed seven", i, len(cols))
			continue
		}
		for j, name := range GRCColumnNames {
			if cols[j] != name {
				t.Errorf("pass %d column %d = %q, want %q", i, j, cols[j], name)
			}
		}
	}
}

// TestTraceIDTableFromTrace checks the TraceId hop against a real archive.
//
// The ids in the TraceId tables do not equal any GRC_ENCODER_ID or
// GRC_KICK_TRACE_ID; the connection is positional. What is asserted here is
// what that positional reading requires: the tables cover every ordinal an
// encoder group uses, and the sample indices ascend with the ordinal, which is
// the encoder execution order.
func TestTraceIDTableFromTrace(t *testing.T) {
	dir := testtrace.Path("GPUTRACE_TEST_GPUPROFILER_DIR", testtrace.ProfilerDir)
	if dir == "" {
		t.Skip("set GPUTRACE_TEST_TRACE to a .gputrace bundle, or GPUTRACE_TEST_GPUPROFILER_DIR to a .gpuprofiler_raw directory")
	}
	stats, err := ParseStreamData(dir, nil)
	if err != nil {
		t.Fatalf("ParseStreamData: %v", err)
	}
	if stats.CounterArchive == nil || stats.CounterArchive.TraceIDs == nil {
		t.Fatal("no TraceId table parsed")
	}
	rows := stats.CounterArchive.TraceIDs.Rows
	t.Logf("trace ids: %d rows, %#x..%#x", len(rows), rows[0].TraceID, rows[len(rows)-1].TraceID)

	seenBatch := make(map[int]bool, len(rows))
	for i, r := range rows {
		if seenBatch[r.BatchID] {
			t.Errorf("batch id %d appears twice", r.BatchID)
		}
		seenBatch[r.BatchID] = true
		if i > 0 && r.SampleIndex <= rows[i-1].SampleIndex {
			t.Errorf("row %d sample index %d does not exceed the previous %d",
				i, r.SampleIndex, rows[i-1].SampleIndex)
		}
	}

	// Every encoder the counter samples attribute must have an ordinal the
	// table covers; otherwise the positional reading is claiming a batch it
	// cannot support.
	for _, e := range stats.CounterArchive.Encoders {
		if e.Ordinal >= len(rows) {
			t.Errorf("encoder %#x has ordinal %d, beyond the %d trace ids",
				e.EncoderID, e.Ordinal, len(rows))
		}
	}

	// Encoders of one group must have distinct ordinals, and each group must
	// use the same ordinal range.
	byGroup := make(map[int]map[int]bool)
	for _, e := range stats.CounterArchive.Encoders {
		if byGroup[e.Group] == nil {
			byGroup[e.Group] = make(map[int]bool)
		}
		if byGroup[e.Group][e.Ordinal] {
			t.Errorf("group %d has two encoders at ordinal %d", e.Group, e.Ordinal)
		}
		byGroup[e.Group][e.Ordinal] = true
	}
	for g, ords := range byGroup {
		if len(ords) != len(rows) {
			t.Errorf("group %d covers %d ordinals, want %d", g, len(ords), len(rows))
		}
	}
	t.Logf("%d groups, each covering %d ordinals", len(byGroup), len(rows))
}
