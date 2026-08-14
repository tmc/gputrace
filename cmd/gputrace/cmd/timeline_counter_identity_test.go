//go:build darwin

package cmd

import (
	"encoding/csv"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tmc/gputrace/internal/counter"
	"github.com/tmc/gputrace/internal/perfettosql"
)

func TestExportEncoderCounterIdentityReachesPerfettoSQL(t *testing.T) {
	processor := os.Getenv("TRACE_PROCESSOR_SHELL")
	if processor == "" {
		t.Skip("set TRACE_PROCESSOR_SHELL to run native PerfettoSQL integration")
	}
	timeline := &Timeline{Events: []TimelineEvent{{
		Name: "Encoder 0", Category: "encoder", Phase: "X", TimestampNS: 1_000,
		DurationNS: 100, ProcessID: 1, ThreadID: 1,
		Args: map[string]interface{}{"index": 0},
	}}}
	archive := &counter.CounterArchive{
		Encoders: []counter.EncoderSamples{{Ordinal: 0, GPUCycles: 100, EndSamples: 16}},
		TraceIDs: &counter.TraceIDTable{Rows: []counter.TraceIDInfo{{
			TraceID: 123, BatchID: 7, SampleIndex: 44,
		}}},
	}
	annotateEncoderCounterArchive(timeline, archive)

	trace := filepath.Join(t.TempDir(), "encoder-counter-identity.pftrace")
	if err := exportPerfettoForClock(timeline, trace, timelineClockBusy); err != nil {
		t.Fatal(err)
	}
	query := perfettosql.Module + `
SELECT encoder_id, counter_batch_id, counter_sample_index,
       counter_batch_id_source, counter_sample_index_source,
       counter_trace_id_relation, counter_clock_relation
FROM gputrace_encoder;
`
	command := exec.Command(processor, "query", trace)
	command.Stdin = strings.NewReader(query)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("trace processor: %v\n%s", err, output)
	}
	rows, err := csv.NewReader(strings.NewReader(string(output))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"0", "7", "44",
		"APSCounterData TraceId to BatchId by encoder execution ordinal",
		"APSCounterData TraceId to SampleIndex by encoder execution ordinal",
		"positional only; TraceId does not equal GRC encoder or kick trace id",
		"aggregate details only; counter sample timestamps are not joined to the busy clock",
	}
	if len(rows) != 2 || !slices.Equal(rows[1], want) {
		t.Fatalf("PerfettoSQL rows = %q, want header and %q", rows, want)
	}
}
