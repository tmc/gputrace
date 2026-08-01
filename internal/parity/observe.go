package parity

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/tmc/gputrace/internal/counter"
	"github.com/tmc/gputrace/internal/trace"
)

// Observation is what gputrace reports for one capture: for each Xcode column
// name we claim to produce, a value per encoder.
//
// A column absent from Values is a column gputrace does not produce. That is a
// distinct outcome from producing zero, and the two must not be conflated: the
// Counters.csv exporter writes "0.00" into every column it has no mapping for,
// which reads as a measurement and is not one.
type Observation struct {
	Encoders    []string              // join keys, in capture order
	Values      map[string][]string   // Xcode column name -> value per encoder
	Derivations map[string]Derivation // how each column was arrived at
	Notes       []string              // what could not be produced, and why
}

// Derivation labels the evidence behind a column. Kind is "runtime" for a value
// read out of the capture and "inference" for one computed under an assumption
// that could be wrong. Nothing else is allowed: a value that is neither is a
// value that should not be published.
type Derivation struct {
	Kind string
	How  string
}

func (o *Observation) set(column string, vals []string, d Derivation) {
	if o.Values == nil {
		o.Values = make(map[string][]string)
		o.Derivations = make(map[string]Derivation)
	}
	o.Values[column] = vals
	o.Derivations[column] = d
}

// Columns returns the produced column names, sorted.
func (o *Observation) Columns() []string {
	names := make([]string, 0, len(o.Values))
	for n := range o.Values {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Observe builds the gputrace side of the comparison for a .gputrace bundle.
//
// It reports only columns whose value comes from the capture. It does not fill
// unmapped columns and it does not fall back to synthetic estimates: the
// question the harness answers is which of Xcode's columns gputrace can
// actually reproduce, and a placeholder answers it wrongly.
func Observe(tracePath string) (*Observation, error) {
	dir, err := profilerDir(tracePath)
	if err != nil {
		return nil, err
	}
	stats, err := counter.ParseStreamData(dir, nil)
	if err != nil {
		return nil, fmt.Errorf("parse streamData: %w", err)
	}
	if len(stats.EncoderTimings) == 0 {
		return nil, fmt.Errorf("streamData has no encoder timings")
	}

	obs := &Observation{Encoders: encoderKeys(stats.EncoderTimings)}

	// Dispatch count per encoder. This is NOT Xcode's "Kernel Invocations":
	// that column runs to tens of thousands per encoder against 958 dispatches
	// in the whole capture, so it counts threads or threadgroups. Publishing it
	// as "Kernel Invocations" would be a false match, so it goes out under its
	// own name and "Kernel Invocations" is reported as not produced.
	counts := dispatchesPerEncoder(stats)
	vals := make([]string, len(counts))
	for i, c := range counts {
		vals[i] = fmt.Sprintf("%d", c)
	}
	obs.set("gputrace Dispatches", vals, Derivation{
		Kind: "inference",
		How:  "gpuCommandInfoData records bucketed into encoderInfoData cumulative offsets",
	})

	dur := make([]string, len(stats.EncoderTimings))
	for i, e := range stats.EncoderTimings {
		dur[i] = fmt.Sprintf("%d", e.DurationMicros)
	}
	obs.set("gputrace Encoder Duration us", dur, Derivation{
		Kind: "runtime",
		How:  "successive differences of encoderInfoData cumulative end offsets",
	})

	obs.observeExecutionCost(dir, stats)
	obs.observeCounterFiles(tracePath, len(stats.Pipelines))
	return obs, nil
}

// observeExecutionCost records why Execution Cost, the leading column of every
// Xcode Counters sub-tab, is not produced per encoder.
func (o *Observation) observeExecutionCost(dir string, stats *counter.StreamDataStats) {
	costs, err := counter.ExtractExecutionCostFromDir(dir)
	if err != nil {
		o.Notes = append(o.Notes, fmt.Sprintf("Execution Cost: Profiling_f_*.raw parsing failed: %v", err))
		return
	}
	o.Notes = append(o.Notes, fmt.Sprintf(
		"Execution Cost: Profiling_f_*.raw yields cost for %d pipelines from %d samples, keyed by pipeline ID. Xcode's column is per encoder, and we have no per-encoder sample attribution, so no value is published",
		len(costs.PipelineCosts), costs.TotalSamples))
}

// observeCounterFiles adds whatever the Counters_f_*.raw path yields per
// encoder, and records a note when it yields nothing.
func (o *Observation) observeCounterFiles(tracePath string, pipelines int) {
	// ParsePerfCounters reads only Path, so it works on a profiler-only bundle
	// that trace.Open rejects for lack of unsorted-capture.
	t := &trace.Trace{Path: tracePath}
	metrics, err := counter.PopulateEncoderMetricsFromBinaryParsing(t)
	if err != nil {
		o.Notes = append(o.Notes, fmt.Sprintf("Counters_f_*.raw parsing failed: %v", err))
		return
	}
	if len(metrics) != len(o.Encoders) {
		note := fmt.Sprintf(
			"Counters_f_*.raw parsing produced %d rows for %d encoders; not joinable, so no counter-file column is published",
			len(metrics), len(o.Encoders))
		if len(metrics) == pipelines {
			note += fmt.Sprintf(
				". The row count equals the pipeline count (%d), so PopulateEncoderMetricsFromBinaryParsing returns one row per pipeline, not per encoder"+
					" -- and the Counters.csv exporter indexes it by encoder position, which mislabels pipeline data as encoder data",
				pipelines)
		}
		o.Notes = append(o.Notes, note)
		return
	}

	// Only publish a counter-file column when the parse actually varies across
	// encoders. A field that is identical in every row is either unpopulated or
	// a constant we invented, and either way it is not a measurement of this
	// encoder.
	add := func(column string, pick func(counter.EncoderCounterMetrics) float64, format string) {
		vals := make([]string, len(metrics))
		nonZero := false
		for i, m := range metrics {
			v := pick(m)
			if v != 0 {
				nonZero = true
			}
			vals[i] = fmt.Sprintf(format, v)
		}
		if !nonZero {
			o.Notes = append(o.Notes, fmt.Sprintf("%s: counter-file parse returned zero for every encoder; not published", column))
			return
		}
		o.set(column, vals, Derivation{Kind: "runtime", How: "Counters_f_*.raw via PopulateEncoderMetricsFromBinaryParsing"})
	}

	add("ALU Utilization", func(m counter.EncoderCounterMetrics) float64 { return m.ALUUtilization }, "%.2f%%")
	add("Kernel Occupancy", func(m counter.EncoderCounterMetrics) float64 { return m.KernelOccupancy }, "%.2f%%")
	add("Compute Shader Utilization", func(m counter.EncoderCounterMetrics) float64 { return m.ComputeShaderUtilization }, "%.2f%%")
	add("Control Flow Utilization", func(m counter.EncoderCounterMetrics) float64 { return m.ControlFlowUtilization }, "%.2f%%")
	add("Instruction Throughput Utilization", func(m counter.EncoderCounterMetrics) float64 { return m.InstructionThroughputUtil }, "%.2f%%")
	add("F16 Utilization", func(m counter.EncoderCounterMetrics) float64 { return m.F16Utilization }, "%.2f%%")
	add("F32 Utilization", func(m counter.EncoderCounterMetrics) float64 { return m.F32Utilization }, "%.2f%%")
	add("Bytes Read From Device Memory", func(m counter.EncoderCounterMetrics) float64 {
		return float64(m.BytesReadFromDeviceMemory)
	}, "%.0f")
	add("Bytes Written To Device Memory", func(m counter.EncoderCounterMetrics) float64 {
		return float64(m.BytesWrittenToDeviceMemory)
	}, "%.0f")
}

// encoderKeys renders the join key for each encoder: its cumulative end offset
// in microseconds. Xcode's Counters tab names an encoder
// "<end offset us> Compute Encoder <i> 0x<addr>", and the leading number equals
// encoderInfoData's cumulative end offset for every encoder in the captures
// checked so far. The address is not recoverable from streamData, and is not
// unique anyway, so the offset is the whole key.
func encoderKeys(timings []counter.EncoderTimingInfo) []string {
	keys := make([]string, len(timings))
	for i, t := range timings {
		keys[i] = fmt.Sprintf("%d", t.EndOffsetMicros)
	}
	return keys
}

// JoinKey reduces an Xcode encoder name to its leading cumulative end offset.
func JoinKey(name string) string {
	for i := 0; i < len(name); i++ {
		if name[i] < '0' || name[i] > '9' {
			return name[:i]
		}
	}
	return name
}

// dispatchesPerEncoder assigns each dispatch to an encoder by its cumulative
// timestamp.
//
// The encoder index stored in each gpuCommandInfoData record is deliberately
// not used: on this capture it is a constant, so it puts all 958 dispatches in
// one encoder. Bucketing by cumulative time uses the same clock as
// encoderInfoData's cumulative end offsets and reproduces the total, but it is
// an inference and a dispatch on a boundary can land on either side.
func dispatchesPerEncoder(stats *counter.StreamDataStats) []int {
	ends := make([]int, len(stats.EncoderTimings))
	for i, e := range stats.EncoderTimings {
		ends[i] = e.EndOffsetMicros
	}
	counts := make([]int, len(ends))
	for _, d := range stats.Dispatches {
		i := sort.SearchInts(ends, d.CumulativeUs)
		if i >= len(ends) {
			i = len(ends) - 1
		}
		counts[i]++
	}
	return counts
}

// profilerDir locates the .gpuprofiler_raw directory for a trace bundle, either
// as a sibling or inside the bundle.
func profilerDir(tracePath string) (string, error) {
	if dir := tracePath + ".gpuprofiler_raw"; isDir(dir) {
		return dir, nil
	}
	entries, err := os.ReadDir(tracePath)
	if err != nil {
		return "", fmt.Errorf("read trace bundle: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() && filepath.Ext(e.Name()) == ".gpuprofiler_raw" {
			return filepath.Join(tracePath, e.Name()), nil
		}
	}
	return "", fmt.Errorf("no .gpuprofiler_raw directory for %s", tracePath)
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}
