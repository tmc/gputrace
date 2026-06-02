//go:build darwin

package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/counter"
)

var (
	counterRowsJSON          bool
	counterRowsTracePath     string
	counterRowsProfilerDir   string
	counterRowsIntervals     string
	counterRowsIntervalsJSON string
	counterRowsProcessName   string
	counterRowsMaxRows       int
)

// routeJoinedCounterRow is the claim-safe counter evidence row downstream
// profilers need.
// Do not relax these fields to timestamp-only correlation: downstream graph
// joins require stable xctrace command-buffer and encoder IDs.
type routeJoinedCounterRow struct {
	CounterName             string   `json:"counter_name"`
	CounterStartNs          uint64   `json:"counter_start_ns"`
	CounterEndNs            uint64   `json:"counter_end_ns"`
	CounterValue            []uint64 `json:"counter_value"`
	XctraceCommandBufferID  uint64   `json:"xctrace_command_buffer_id"`
	XctraceEncoderID        uint64   `json:"xctrace_encoder_id"`
	SampleIndex             int      `json:"sample_index"`
	DerivedCounterBlock     int      `json:"derived_counter_block"`
	XctraceIntervalLabel    string   `json:"xctrace_interval_label,omitempty"`
	XctraceIntervalProcess  string   `json:"xctrace_interval_process,omitempty"`
	JoinKeySource           string   `json:"join_key_source"`
	TimestampOverlapAllowed bool     `json:"timestamp_overlap_allowed"`
}

type counterRowsOutput struct {
	ProfilerDir           string                  `json:"profiler_dir"`
	IntervalsXML          string                  `json:"intervals_xml"`
	ProcessName           string                  `json:"process_name"`
	IntervalRowsRead      int                     `json:"interval_rows_read"`
	IntervalRowsMatched   int                     `json:"interval_rows_matched"`
	DerivedCounterBlocks  int                     `json:"derived_counter_blocks"`
	DerivedCounterSamples int                     `json:"derived_counter_samples"`
	Rows                  []routeJoinedCounterRow `json:"rows"`
	CounterRowsUsable     bool                    `json:"counter_rows_usable"`
	CounterClaimsAllowed  bool                    `json:"counter_claims_allowed"`
	HardwareClaimsAllowed bool                    `json:"hardware_claims_allowed"`
	RAMClaimsAllowed      bool                    `json:"ram_claims_allowed"`
	RAMCounterNames       []string                `json:"ram_counter_names"`
	Reason                string                  `json:"reason"`
}

var counterRowsCmd = &cobra.Command{
	Use:   "counter-rows (--intervals metal-gpu-intervals.xml | --intervals-json trace-timing-rows.json) --process name (--profiler-dir dir | --trace trace.gputrace)",
	Short: "Export route-joined profiler counter rows",
	Long: `Export counter-bearing rows that can be joined to xctrace command-buffer
and encoder IDs.

This command is intentionally fail-closed for profiler evidence: it rejects
timestamp-only attribution and emits rows only when each counter sample can be
paired with nonzero xctrace command-buffer and encoder IDs from
metal-gpu-intervals.`,
	Args: cobra.NoArgs,
	RunE: runCounterRows,
}

func init() {
	rootCmd.AddCommand(counterRowsCmd)
	counterRowsCmd.Flags().BoolVar(&counterRowsJSON, "json", false, "Output in JSON format")
	counterRowsCmd.Flags().StringVar(&counterRowsTracePath, "trace", "", "Trace bundle containing a .gpuprofiler_raw directory")
	counterRowsCmd.Flags().StringVar(&counterRowsProfilerDir, "profiler-dir", "", "Profiler .gpuprofiler_raw directory")
	counterRowsCmd.Flags().StringVar(&counterRowsIntervals, "intervals", "", "Exported xctrace metal-gpu-intervals XML")
	counterRowsCmd.Flags().StringVar(&counterRowsIntervalsJSON, "intervals-json", "", "JSON timing rows with start_ns/duration_ns/command_buffer_id/encoder_id")
	counterRowsCmd.Flags().StringVar(&counterRowsProcessName, "process", "", "Process name substring required for interval rows")
	counterRowsCmd.Flags().IntVar(&counterRowsMaxRows, "max-rows", 20000, "Maximum interval rows to read")
}

func runCounterRows(cmd *cobra.Command, args []string) error {
	if counterRowsIntervals == "" && counterRowsIntervalsJSON == "" {
		return fmt.Errorf("one of --intervals or --intervals-json is required")
	}
	if counterRowsIntervals != "" && counterRowsIntervalsJSON != "" {
		return fmt.Errorf("choose only one of --intervals or --intervals-json")
	}
	if counterRowsProcessName == "" {
		return fmt.Errorf("--process is required to avoid system-wide counter attribution")
	}
	profilerDir, err := resolveCounterRowsProfilerDir(counterRowsTracePath, counterRowsProfilerDir)
	if err != nil {
		return err
	}

	intervals, rowsRead, inputPath, err := loadCounterRowIntervals(counterRowsIntervals, counterRowsIntervalsJSON, counterRowsProcessName, counterRowsMaxRows)
	if err != nil {
		return err
	}
	stats, err := counter.ParseStreamData(profilerDir)
	if err != nil {
		return fmt.Errorf("parse streamData: %w", err)
	}

	rows, err := buildRouteJoinedCounterRows(stats, intervals)
	if err != nil {
		return err
	}
	counterRowsUsable := len(rows) > 0
	ramCounterNames := ramCounterNames(rows)
	ramClaimsAllowed := len(ramCounterNames) > 0
	out := counterRowsOutput{
		ProfilerDir:           profilerDir,
		IntervalsXML:          inputPath,
		ProcessName:           counterRowsProcessName,
		IntervalRowsRead:      rowsRead,
		IntervalRowsMatched:   len(intervals),
		DerivedCounterBlocks:  len(stats.DerivedCounters),
		DerivedCounterSamples: stats.DerivedCounterSampleCount(),
		Rows:                  rows,
		CounterRowsUsable:     counterRowsUsable,
		CounterClaimsAllowed:  counterRowsUsable,
		HardwareClaimsAllowed: counterRowsUsable,
		RAMClaimsAllowed:      ramClaimsAllowed,
		RAMCounterNames:       ramCounterNames,
		Reason:                counterRowsDecisionReason(counterRowsUsable, ramClaimsAllowed),
	}
	if counterRowsJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	fmt.Printf("Counter rows: %d\n", len(out.Rows))
	fmt.Printf("Counter rows usable: %v\n", out.CounterRowsUsable)
	fmt.Printf("Counter claims allowed: %v\n", out.CounterClaimsAllowed)
	fmt.Printf("Hardware claims allowed: %v\n", out.HardwareClaimsAllowed)
	fmt.Printf("RAM claims allowed: %v\n", out.RAMClaimsAllowed)
	fmt.Println(out.Reason)
	return nil
}

func counterRowsDecisionReason(counterRowsUsable bool, ramClaimsAllowed bool) string {
	if counterRowsUsable && ramClaimsAllowed {
		return "counter samples are joined to stable xctrace command-buffer/encoder IDs and include RAM/bandwidth counters"
	}
	if counterRowsUsable {
		return "counter samples are joined to stable xctrace command-buffer/encoder IDs, but no RAM/bandwidth counter names were found"
	}
	return "counter claims disallowed: no route-joined counter rows were emitted"
}

func ramCounterNames(rows []routeJoinedCounterRow) []string {
	seen := map[string]bool{}
	names := []string{}
	for _, row := range rows {
		name := row.CounterName
		lower := strings.ToLower(name)
		if strings.Contains(lower, "bandwidth") ||
			strings.Contains(lower, "dram") ||
			strings.Contains(lower, "memory") ||
			strings.Contains(lower, "mem_") ||
			strings.Contains(lower, "bytes") ||
			strings.Contains(lower, "read_bytes") ||
			strings.Contains(lower, "write_bytes") {
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}
	sort.Strings(names)
	return names
}

func loadCounterRowIntervals(xmlPath, jsonPath, processName string, maxRows int) ([]xctraceIntervalRow, int, string, error) {
	if xmlPath != "" {
		rows, rowsRead, err := parseXctraceGPUIntervalsXML(xmlPath, processName, maxRows)
		return rows, rowsRead, xmlPath, err
	}
	rows, rowsRead, err := parseTimingRowsJSON(jsonPath, processName, maxRows)
	return rows, rowsRead, jsonPath, err
}

func parseTimingRowsJSON(path, processName string, maxRows int) ([]xctraceIntervalRow, int, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()
	var raw []xctraceIntervalRow
	if err := json.NewDecoder(file).Decode(&raw); err != nil {
		return nil, 0, err
	}
	rows := make([]xctraceIntervalRow, 0, len(raw))
	for _, row := range raw {
		if row.StartNs == 0 || row.DurationNs == 0 {
			continue
		}
		if processName != "*" && row.Process != "" && !strings.Contains(row.Process, processName) {
			continue
		}
		rows = append(rows, row)
		if maxRows > 0 && len(rows) >= maxRows {
			break
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].StartNs < rows[j].StartNs
	})
	return rows, len(raw), nil
}

func resolveCounterRowsProfilerDir(tracePath, profilerDir string) (string, error) {
	if tracePath != "" && profilerDir != "" {
		return "", fmt.Errorf("choose only one of --trace or --profiler-dir")
	}
	if profilerDir != "" {
		if filepath.Ext(profilerDir) != ".gpuprofiler_raw" {
			return "", fmt.Errorf("--profiler-dir must point to a .gpuprofiler_raw directory")
		}
		if _, err := os.Stat(filepath.Join(profilerDir, "streamData")); err != nil {
			return "", fmt.Errorf("profiler streamData not found: %w", err)
		}
		return profilerDir, nil
	}
	if tracePath == "" {
		return "", fmt.Errorf("one of --trace or --profiler-dir is required")
	}
	profiler := findAnyProfilerDir(tracePath)
	if profiler == "" {
		return "", fmt.Errorf("no .gpuprofiler_raw directory found in %s", tracePath)
	}
	return profiler, nil
}

func buildRouteJoinedCounterRows(stats *counter.StreamDataStats, intervals []xctraceIntervalRow) ([]routeJoinedCounterRow, error) {
	if stats == nil {
		return nil, fmt.Errorf("missing streamData stats")
	}
	if len(intervals) == 0 {
		return nil, fmt.Errorf("no xctrace interval rows available for counter attribution")
	}
	for _, interval := range intervals {
		if interval.CommandBufferID == 0 || interval.EncoderID == 0 {
			return nil, fmt.Errorf("xctrace interval row lacks command-buffer/encoder IDs; timestamp-only counter attribution rejected")
		}
	}
	if len(stats.DerivedCounters) == 0 || stats.DerivedCounterSampleCount() == 0 {
		return nil, fmt.Errorf("no derived counter samples found in streamData")
	}

	rows := []routeJoinedCounterRow{}
	for blockIdx, block := range stats.DerivedCounters {
		for sampleIdx, sample := range block.Samples {
			if sampleIdx >= len(intervals) {
				return nil, fmt.Errorf("derived counter sample %d has no matching xctrace interval ID row", sampleIdx)
			}
			interval := intervals[sampleIdx]
			names := sortedCounterNames(sample.Values)
			for _, name := range names {
				rows = append(rows, routeJoinedCounterRow{
					CounterName:             name,
					CounterStartNs:          interval.StartNs,
					CounterEndNs:            interval.StartNs + interval.DurationNs,
					CounterValue:            append([]uint64(nil), sample.Values[name]...),
					XctraceCommandBufferID:  interval.CommandBufferID,
					XctraceEncoderID:        interval.EncoderID,
					SampleIndex:             sampleIdx,
					DerivedCounterBlock:     blockIdx,
					XctraceIntervalLabel:    interval.Label,
					XctraceIntervalProcess:  interval.Process,
					JoinKeySource:           "xctrace-metal-gpu-intervals-command-buffer-id+encoder-id",
					TimestampOverlapAllowed: false,
				})
			}
		}
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("no route-joined counter rows emitted")
	}
	return rows, nil
}

func sortedCounterNames(values map[string][]uint64) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
