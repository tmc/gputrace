package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/tmc/gputrace"
	"github.com/tmc/gputrace/internal/buildinfo"
	"github.com/tmc/gputrace/internal/counter"
	"github.com/tmc/gputrace/internal/mlxsemantic"
	"github.com/tmc/gputrace/internal/perfetto"
	"github.com/tmc/gputrace/internal/perfettosql"
	"github.com/tmc/gputrace/internal/profilerraw"
	tracepkg "github.com/tmc/gputrace/internal/trace"
)

var timelineCmd = newTimelineCommand(&timelineOptions{
	format:           "text",
	clock:            timelineClockBusy,
	kernelOccurrence: -1,
	timeStart:        -1,
	timeEnd:          -1,
})

type timelineOptions struct {
	output              string
	format              string
	clock               timelineClock
	rawProfilerSamples  bool
	xcodeGPUTime        bool
	sidecar             string
	hostCorrelation     string
	liveTiming          string
	openViewer          bool
	serveViewer         bool
	uiDir               string
	uiRevision          string
	remoteUI            bool
	listen              string
	maxOutputBytes      int64
	sqlOutput           string
	kernel              string
	kernelOccurrence    int
	timeStart           float64
	timeEnd             float64
	navigationStartNS   uint64
	navigationEndNS     uint64
	selectionStartNS    uint64
	selectionDurationNS uint64
}

// timelineClock selects one measured timestamp domain. The profiler records
// command buffers in wall-clock ticks and encoders in cumulative GPU-busy
// offsets. Those domains have no measured correspondence.
type timelineClock string

const (
	timelineClockBusy timelineClock = "busy"
	timelineClockWall timelineClock = "wall"
	timelineClockLive timelineClock = "live"
	timelineClockBoth timelineClock = "both"
)

func newTimelineCommand(opts *timelineOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "timeline <trace.gputrace>",
		Short: "Generate timeline visualization from GPU trace",
		Long: `Generate an interactive timeline visualization showing:
  - Chronological API call sequence with timestamps
  - Concurrent command buffer execution
  - Encoder lifecycle (creation -> encoding -> commit)
  - Buffer binding events mapped to kernels
  - GPU execution timeline

Output formats:
  - text: Hierarchical text output to stdout
  - chrome: Chrome tracing format (chrome://tracing)
  - perfetto: Native Perfetto protobuf format (ui.perfetto.dev)
  - html: Interactive standalone HTML timeline viewer
  - json: Raw timeline data in JSON format

Native Perfetto exports include the evidence manifest, environment projection,
resource policy, and loss receipt. These are part of the timeline export, not
separate commands. Use --max-output-bytes for an explicit constrained export
and --sql-out to write the matching PerfettoSQL views.

Capture-only launches with no profiler timing are instant track events, not GPU
duration slices. CS/debug labels remain separate observed annotations.

Clock domains:
  - busy (default): cumulative GPU execution offsets for encoders, dispatches,
    and counter series only when their clock is established
  - wall: APSTimelineData command-buffer scheduling and encoder profiles
  - live: original-execution command-buffer intervals from --live-timing
  - both: a two-panel or two-section report containing both domains

There is no measured mapping between cumulative GPU-busy offsets and
command-buffer wall time. The both view preserves the domains separately; it
does not place them on one shared timeline axis.

Examples:
  # Generate interactive HTML timeline viewer
  gputrace timeline trace.gputrace -o timeline.html --format html

  # Generate Chrome tracing format
  gputrace timeline trace.gputrace --format chrome -o timeline.json

  # Inspect wall-clock command-buffer scheduling separately
  gputrace timeline trace.gputrace --format perfetto --clock wall -o command-buffers.pftrace

  # Add Xcode Overview GPU Time without aligning the two timeline clocks
  gputrace timeline trace.gputrace --format perfetto --xcode-gpu-time -o timeline.pftrace

  # Inspect both domains without inventing a clock mapping
  gputrace timeline trace.gputrace --format html --clock both -o timeline.html

  # View in Chrome
  # 1. Open chrome://tracing in Chrome
  # 2. Click "Load" and select timeline.json
  # 3. Use WASD keys to navigate, mouse wheel to zoom

  # View in Perfetto UI (recommended)
  # 1. Open https://ui.perfetto.dev
  # 2. Drag and drop timeline.pftrace or click "Open trace file"
  # 3. Use keyboard shortcuts: W/S zoom, A/D pan, F fit

  # Generate raw JSON for custom processing
  gputrace timeline trace.gputrace -o timeline.json --format json

  # Emit stable PerfettoSQL views beside a native trace
  gputrace timeline trace.gputrace --format perfetto --sql-out gputrace.sql

  # Write a constrained native trace with an embedded loss receipt
  gputrace timeline trace.gputrace --format perfetto \
    --max-output-bytes 500000 -o timeline.pftrace

  # Open one exact kernel occurrence
  gputrace timeline trace.gputrace --format perfetto --open --remote-ui \
    --kernel rmsbfloat16 --kernel-occurrence 0`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTimeline(cmd, args, opts)
		},
	}

	cmd.Flags().StringVarP(&opts.output, "output", "o", opts.output, "Output file path (default: stdout for text, timeline.html for html, timeline.pftrace for perfetto, timeline.json otherwise)")
	cmd.Flags().StringVar(&opts.format, "format", opts.format, "Output format: chrome, perfetto, html, json, text")
	cmd.Flags().Var(&opts.clock, "clock", "Timeline clock domain: busy (default), wall, live, or both")
	cmd.Flags().BoolVar(&opts.rawProfilerSamples, "include-raw-samples", opts.rawProfilerSamples, "Include raw GPRWCNTR profiler records in wall-clock output (they are not decoded hardware counters)")
	cmd.Flags().BoolVar(&opts.xcodeGPUTime, "xcode-gpu-time", opts.xcodeGPUTime, "Read Xcode Overview GPU Time through GTShaderProfiler (Darwin only; runs a private-framework model pass)")
	cmd.Flags().StringVar(&opts.sidecar, "sidecar", opts.sidecar, "Attach a strictly trace-identified MLX semantic sidecar")
	cmd.Flags().StringVar(&opts.hostCorrelation, "host-correlation", opts.hostCorrelation, "Attach a trace-identified host-event correlation receipt (Perfetto only)")
	cmd.Flags().StringVar(&opts.liveTiming, "live-timing", opts.liveTiming, "Attach original-execution command-buffer timing from capture --timing-sidecar")
	cmd.Flags().BoolVar(&opts.openViewer, "open", opts.openViewer, "Serve the native trace and open it in Perfetto")
	cmd.Flags().BoolVar(&opts.serveViewer, "serve", opts.serveViewer, "Serve the native trace without opening a browser")
	cmd.Flags().StringVar(&opts.uiDir, "ui-dir", opts.uiDir, "Pinned local Perfetto UI directory containing perfetto-ui.json (with --open or --serve)")
	cmd.Flags().BoolVar(&opts.remoteUI, "remote-ui", opts.remoteUI, "Embed https://ui.perfetto.dev (with --open or --serve)")
	cmd.Flags().StringVar(&opts.listen, "listen", "127.0.0.1:0", "Loopback viewer listen address")
	cmd.Flags().Int64Var(&opts.maxOutputBytes, "max-output-bytes", opts.maxOutputBytes, "Maximum logical native protobuf bytes; zero is lossless")
	cmd.Flags().StringVar(&opts.sqlOutput, "sql-out", opts.sqlOutput, "Write the gputrace PerfettoSQL views (with --format perfetto)")
	cmd.Flags().StringVar(&opts.kernel, "kernel", opts.kernel, "Focus an exact kernel name in the viewer")
	cmd.Flags().IntVar(&opts.kernelOccurrence, "kernel-occurrence", -1, "Zero-based occurrence for --kernel; required when the name is repeated")
	cmd.Flags().Float64Var(&opts.timeStart, "time-start", -1, "Initial viewer range start in seconds")
	cmd.Flags().Float64Var(&opts.timeEnd, "time-end", -1, "Initial viewer range end in seconds")
	return cmd
}

func init() {
	rootCmd.AddCommand(timelineCmd)
}

func runTimeline(cmd *cobra.Command, args []string, opts *timelineOptions) error {
	tracePath := args[0]
	if err := validateTimelineFormat(opts.format); err != nil {
		return err
	}
	if err := validateTimelineClock(opts.clock); err != nil {
		return err
	}
	if err := validateTimelineSQLOutput(opts); err != nil {
		return err
	}

	// Verify trace file exists
	if err := checkTraceFile(tracePath); err != nil {
		return err
	}

	// Try to open full trace first
	trace, err := gputrace.Open(tracePath)
	if err != nil || trace.ProfilerOnly {
		// Fall back to profiler-only mode when there is no capture stream.
		// Open now succeeds on such bundles, so the flag, not the error, is
		// what distinguishes them.
		return runTimelineFromProfiler(cmd, tracePath, opts)
	}

	// Generate timeline data
	timeline, err := generateTimeline(trace)
	if err != nil {
		return fmt.Errorf("failed to generate timeline: %w", err)
	}
	if err := enrichTimelineWithXcodeGPUTime(tracePath, timeline, opts.xcodeGPUTime); err != nil {
		return err
	}
	if trace.Metadata != nil {
		timeline.TraceUUID = trace.Metadata.UUID
		timeline.DeviceID = trace.Metadata.DeviceID
	}
	if opts.sidecar != "" {
		uuid := ""
		if trace.Metadata != nil {
			uuid = trace.Metadata.UUID
		}
		if err := attachMLXSidecar(timeline, tracePath, uuid, opts.sidecar); err != nil {
			return err
		}
	}
	if opts.liveTiming != "" {
		if err := attachLiveTiming(timeline, trace, opts.liveTiming); err != nil {
			return err
		}
	}
	if opts.clock == timelineClockLive && opts.liveTiming == "" {
		return fmt.Errorf("--clock live requires --live-timing")
	}
	if opts.hostCorrelation != "" {
		if opts.format != "perfetto" {
			return fmt.Errorf("attach host correlation: --format perfetto is required")
		}
		if err := attachHostCorrelation(timeline, tracePath, opts.clock, opts.hostCorrelation); err != nil {
			return err
		}
	}

	// Enhance with raw GPRWCNTR data if available.
	if findProfilerDir(tracePath) != "" {
		if err := EnhanceTimelineWithRawData(timeline, tracePath); err != nil {
			// Just warn, don't fail as this is optional/experimental
			fmt.Fprintf(os.Stderr, "Warning: failed to enhance timeline with raw data: %v\n", err)
		} else {
			// Check if we actually added samples
			sampleCount := 0
			for _, ev := range timeline.Events {
				if ev.Category == "gprwcntr" {
					sampleCount++
				}
			}
			if sampleCount > 0 {
				if opts.rawProfilerSamples && (opts.clock == timelineClockWall || opts.clock == timelineClockBoth) {
					fmt.Fprintf(cmd.ErrOrStderr(), "Raw profiler samples included: %d GPRWCNTR records\n", sampleCount)
				} else {
					fmt.Fprintf(cmd.ErrOrStderr(), "Raw profiler samples available: %d GPRWCNTR records (excluded by default; use --clock wall --include-raw-samples to export)\n", sampleCount)
				}
			}
		}
	}

	// Warn if trace timing data is missing or approximate
	if opts.clock != timelineClockLive && (timeline.Timing == nil || timeline.Timing.EncoderTimingApproximate || timeline.Timing.TimingSource == "" || timeline.Timing.TimingSource == "unavailable") {
		fmt.Fprintf(cmd.ErrOrStderr(), "Warning: trace lacks precise hardware timing data; encoder/dispatch durations are estimated.\n")
		fmt.Fprint(cmd.ErrOrStderr(), profileReplayHint(tracePath))
	}

	outputPath := timelineOutputPath(opts.format, opts.output)
	if err := validateTimelineViewerOptions(opts, outputPath); err != nil {
		return err
	}
	if opts.clock == timelineClockBoth {
		if err := exportTimelineBothWithRawSamples(timeline, opts.format, outputPath, opts.rawProfilerSamples); err != nil {
			return err
		}
		if opts.format != "text" || (outputPath != "" && !commandOutputPathIsStdout(outputPath)) {
			printTimelineExportStatus(outputPath, opts.format, false)
		}
		return nil
	}
	fullTimeline := timeline // Keep pre-clock-filtered timeline for text export wall-time gaps.
	timeline = timelineForClockWithRawSamples(timeline, opts.clock, opts.rawProfilerSamples)
	if err := resolveTimelineNavigation(timeline, opts); err != nil {
		return err
	}

	// Export based on format
	switch opts.format {
	case "chrome":
		if err := exportChromeTracingForClock(timeline, outputPath, opts.clock); err != nil {
			return fmt.Errorf("failed to export Chrome tracing: %w", err)
		}
	case "perfetto":
		if err := exportPerfettoForClockWithBudget(timeline, outputPath, opts.clock, opts.maxOutputBytes); err != nil {
			return fmt.Errorf("failed to export Perfetto tracing: %w", err)
		}
		if err := writeTimelinePerfettoSQL(opts.sqlOutput); err != nil {
			return err
		}
	case "html":
		if err := exportHTML(timeline, outputPath); err != nil {
			return fmt.Errorf("failed to export HTML: %w", err)
		}
	case "json":
		if err := exportTimelineJSON(timeline, outputPath); err != nil {
			return fmt.Errorf("failed to export JSON: %w", err)
		}
	case "text":
		if err := exportTextTimeline(timeline, fullTimeline, outputPath); err != nil {
			return fmt.Errorf("failed to export text: %w", err)
		}
		if outputPath != "" && !commandOutputPathIsStdout(outputPath) {
			printTimelineExportStatus(outputPath, opts.format, false)
		}
		return nil
	default:
		return validateTimelineFormat(opts.format)
	}

	printTimelineExportStatus(outputPath, opts.format, false)
	return serveTimelinePerfetto(cmd, tracePath, outputPath, opts)
}

func validateTimelineClock(clock timelineClock) error {
	switch clock {
	case timelineClockBusy, timelineClockWall, timelineClockLive, timelineClockBoth:
		return nil
	default:
		return fmt.Errorf("invalid timeline clock %q (supported: busy, wall, live, both)", clock)
	}
}

func validateTimelineSQLOutput(opts *timelineOptions) error {
	if opts.sqlOutput != "" && opts.format != "perfetto" {
		return fmt.Errorf("--sql-out requires --format perfetto")
	}
	return nil
}

func writeTimelinePerfettoSQL(path string) error {
	if path == "" {
		return nil
	}
	w, closeOutput, err := createCommandOutput(path)
	if err != nil {
		return fmt.Errorf("write PerfettoSQL views: %w", err)
	}
	if closeOutput != nil {
		defer closeOutput()
	}
	if err := perfettosql.Write(w); err != nil {
		return err
	}
	return nil
}

// Set implements pflag.Value.
func (c *timelineClock) Set(value string) error {
	clock := timelineClock(value)
	if err := validateTimelineClock(clock); err != nil {
		return err
	}
	*c = clock
	return nil
}

func (c *timelineClock) Type() string { return "clock" }

func (c *timelineClock) String() string { return string(*c) }

// timelineForClock copies the timeline and retains only events whose
// timestamps have the requested meaning. A wall-clock coordinate is never
// inferred for a cumulative GPU-busy event, or vice versa.
func timelineForClock(timeline *Timeline, clock timelineClock) *Timeline {
	return timelineForClockWithRawSamples(timeline, clock, true)
}

// timelineForClockWithRawSamples filters a timeline to one measured clock
// domain. Raw GPRWCNTR records are opt-in because their payload has not been
// decoded into user-facing hardware counters.
func timelineForClockWithRawSamples(timeline *Timeline, clock timelineClock, rawProfilerSamples bool) *Timeline {
	if timeline == nil {
		return nil
	}
	selected := *timeline
	if selected.EvidenceInventory == nil {
		inventory := timelineEvidenceInventory(timeline)
		selected.EvidenceInventory = &inventory
	}
	selected.ClockDomain = string(clock)
	selected.RawProfilerSamples = clock == timelineClockWall && rawProfilerSamples
	selected.Events = make([]TimelineEvent, 0, len(timeline.Events))
	for _, event := range timeline.Events {
		if timelineEventInClockWithRawSamples(event, clock, rawProfilerSamples) {
			selected.Events = append(selected.Events, event)
		}
	}
	if clock == timelineClockWall || clock == timelineClockLive {
		selected.Encoders = []EncoderInfo{}
		selected.Kernels = []KernelInfo{}
		selected.CounterTracks = []CounterTrack{}
	} else {
		tracks := make([]CounterTrack, 0, len(timeline.CounterTracks))
		for _, track := range timeline.CounterTracks {
			if counterTrackHasSignal(track) {
				tracks = append(tracks, track)
			}
		}
		selected.CounterTracks = tracks
	}
	// API calls are not timestamped in this capture, so neither selected clock
	// can place them honestly. Keep them out of raw and HTML exports too.
	selected.APICallseq = []APICall{}
	selected.StartTime = 0
	selected.EndTime = 0
	for _, event := range selected.Events {
		if end := (event.Timestamp + event.Duration) * 1000; end > selected.EndTime {
			selected.EndTime = end
		}
	}
	for _, track := range selected.CounterTracks {
		for _, sample := range track.Samples {
			if sample.Timestamp > selected.EndTime {
				selected.EndTime = sample.Timestamp
			}
		}
	}
	selected.Duration = selected.EndTime
	return &selected
}

func timelineEventInClock(event TimelineEvent, clock timelineClock) bool {
	return timelineEventInClockWithRawSamples(event, clock, true)
}

func timelineEventInClockWithRawSamples(event TimelineEvent, clock timelineClock, rawProfilerSamples bool) bool {
	switch clock {
	case timelineClockBusy:
		return event.Category == "encoder" || event.Category == "kernel" || event.Category == "dispatch"
	case timelineClockWall:
		return event.Category == "command_buffer" || event.Category == "restore" || (rawProfilerSamples && (event.Category == "profiler_stream" || event.Category == "gprwcntr"))
	case timelineClockLive:
		return event.Category == "live_command_buffer"
	default:
		return false
	}
}

func validateTimelineFormat(format string) error {
	switch format {
	case "chrome", "perfetto", "html", "json", "text":
		return nil
	default:
		return fmt.Errorf("invalid timeline format %q (supported: chrome, perfetto, html, json, text)", format)
	}
}

// timelineOutputPath picks the default output file for a format. text goes to
// stdout, so it has none. html gets an .html name: writing a whole HTML
// document into timeline.json leaves a file no viewer will open.
func timelineOutputPath(format, output string) string {
	if output != "" || format == "text" {
		return output
	}
	if format == "html" {
		return "timeline.html"
	}
	if format == "perfetto" {
		return "timeline.pftrace"
	}
	return "timeline.json"
}

func printTimelineExportStatus(output, format string, profilerOnly bool) {
	suffix := ""
	if profilerOnly {
		suffix = " (profiler-only mode)"
	}
	fmt.Fprintf(os.Stderr, "Timeline written: %s%s\n", output, suffix)
	if format == "chrome" {
		fmt.Fprintln(os.Stderr, "\nView in Chrome:")
		fmt.Fprintln(os.Stderr, "  1. Open chrome://tracing")
		fmt.Fprintln(os.Stderr, "  2. Click 'Load' and select", output)
		fmt.Fprintln(os.Stderr, "  3. Use WASD to navigate, mouse wheel to zoom")
	} else if format == "perfetto" {
		fmt.Fprintln(os.Stderr, "\nView in Perfetto:")
		fmt.Fprintln(os.Stderr, "  1. Open https://ui.perfetto.dev")
		fmt.Fprintln(os.Stderr, "  2. Drag and drop", output, "onto the page")
		fmt.Fprintln(os.Stderr, "  3. Use WASD to navigate, mouse wheel to zoom")
	} else if format == "html" {
		fmt.Fprintln(os.Stderr, "\nView timeline:")
		fmt.Fprintf(os.Stderr, "  open %s\n", output)
	}
}

// exportTextTimeline writes the timeline in a hierarchical text format.
// fullTimeline is the pre-clock-filtered timeline used to extract wall-clock
// command buffer events for showing idle gaps. It may be nil.
func exportTextTimeline(timeline, fullTimeline *Timeline, outputPath string) error {
	w, closeOutput, err := createCommandOutput(outputPath)
	if err != nil {
		return err
	}
	if closeOutput != nil {
		defer closeOutput()
	}
	return writeTextTimeline(w, timeline, fullTimeline)
}

func writeTextTimeline(w io.Writer, timeline *Timeline, fullTimeline ...*Timeline) error {
	if len(timeline.Encoders) == 0 && len(timeline.Events) == 0 {
		fmt.Fprintln(w, "No timeline data available.")
		return nil
	}

	fmt.Fprintln(w, "GPU Timeline")
	if timeline.TracePath != "" {
		fmt.Fprintf(w, "Trace: %s\n", timeline.TracePath)
	}

	// Find command buffer events before printing the summary.
	var cbs []TimelineEvent
	for _, event := range timeline.Events {
		if event.Category == "command_buffer" {
			cbs = append(cbs, event)
		}
	}
	fmt.Fprintf(w, "Events: %d %s, %d %s, %d %s\n",
		len(cbs), Pluralize(len(cbs), "command buffer", "command buffers"),
		len(timeline.Encoders), Pluralize(len(timeline.Encoders), "encoder", "encoders"),
		len(timeline.Kernels), Pluralize(len(timeline.Kernels), "kernel dispatch", "kernel dispatches"))

	if timeline.Timing != nil && timeline.Timing.EncoderTimingSource != "" {
		sourceKind := "measured"
		if timeline.Timing.EncoderTimingApproximate {
			sourceKind = "approximate"
		}
		fmt.Fprintf(w, "Timing source: %s (%s)\n", timeline.Timing.EncoderTimingSource, sourceKind)
	}
	if timing := timeline.Timing; timing != nil {
		if timing.EncoderSpanNs > 0 {
			fmt.Fprintf(w, "Encoder span: %s\n", FormatDurationNs(timing.EncoderSpanNs))
		}
		if timing.DispatchSpanNs > 0 {
			fmt.Fprintf(w, "Dispatch span: %s\n", FormatDurationNs(timing.DispatchSpanNs))
		}
		if timing.CommandBufferActiveNs > 0 {
			fmt.Fprintf(w, "Command-buffer active time: %s\n", FormatDurationNs(timing.CommandBufferActiveNs))
		}
		if timing.CommandBufferWallNs > 0 {
			fmt.Fprintf(w, "Command-buffer wall span: %s\n", FormatDurationNs(timing.CommandBufferWallNs))
		}
		if timing.EffectiveGPUTimeNs != nil {
			fmt.Fprintf(w, "Xcode Effective GPU Time: %s\n", FormatDurationNs(*timing.EffectiveGPUTimeNs))
		} else if !timing.EncoderTimingApproximate {
			fmt.Fprintln(w, "Xcode Effective GPU Time: unavailable")
		}
	}
	fmt.Fprintln(w, "Row units: start and duration are milliseconds; capture-only coordinates are byte offsets.")
	fmt.Fprintln(w)

	// Extract wall-clock CB events from the full timeline to annotate gaps.
	var wallCBs []TimelineEvent
	if len(fullTimeline) > 0 && fullTimeline[0] != nil {
		for _, event := range fullTimeline[0].Events {
			if event.Category == "command_buffer" {
				wallCBs = append(wallCBs, event)
			}
		}
	}
	// Build wall-CB lookup by index for gap annotation.
	wallCBByIndex := make(map[int]TimelineEvent)
	hasRealWallTiming := false
	for _, wcb := range wallCBs {
		if idx, ok := wcb.Args["index"].(int); ok {
			wallCBByIndex[idx] = wcb
		}
		if wcb.Timestamp > 0 {
			hasRealWallTiming = true
		}
	}

	// When the busy-clock timeline has no native CB events but the full
	// timeline has wall-clock CBs, use those as the structural grouping.
	// Each encoder maps 1:1 by index to a wall-clock CB.
	if len(cbs) == 0 && len(wallCBs) > 0 {
		// Build an encoder lookup by index.
		encoderByIndex := make(map[int]EncoderInfo)
		for _, enc := range timeline.Encoders {
			encoderByIndex[enc.Index] = enc
		}

		firstTimestamp := timeline.StartTime

		var prevWallEndUs uint64
		for i, wcb := range wallCBs {
			cbIndex, ok := wcb.Args["index"].(int)
			if !ok {
				continue
			}

			// Show idle gap between consecutive command buffers.
			if hasRealWallTiming && i > 0 && prevWallEndUs > 0 && wcb.Timestamp > prevWallEndUs {
				gapUs := wcb.Timestamp - prevWallEndUs
				gapMs := float64(gapUs) / 1000.0
				fmt.Fprintf(w, "  ⏳ idle gap: %.2fms (wall time)\n", gapMs)
			}
			if hasRealWallTiming {
				prevWallEndUs = wcb.Timestamp + wcb.Duration
			}

			if hasRealWallTiming {
				wallMs := float64(wcb.Timestamp) / 1000.0
				wallDurMs := float64(wcb.Duration) / 1000.0
				fmt.Fprintf(w, "%s [wall=%.2fms, wall-dur=%.2fms]\n", wcb.Name, wallMs, wallDurMs)
			} else {
				fmt.Fprintf(w, "%s\n", wcb.Name)
			}

			// Find the encoder for this CB index and print it with its kernels.
			enc, found := encoderByIndex[cbIndex]
			if !found {
				continue
			}
			writeTimelineEncoders(w, timeline, []EncoderInfo{enc}, firstTimestamp)
		}

		// Show any encoders that didn't map to a wall-clock CB.
		mapped := make(map[int]bool)
		for _, wcb := range wallCBs {
			if idx, ok := wcb.Args["index"].(int); ok {
				mapped[idx] = true
			}
		}
		var unmapped []EncoderInfo
		for _, enc := range timeline.Encoders {
			if !mapped[enc.Index] {
				unmapped = append(unmapped, enc)
			}
		}
		if len(unmapped) > 0 {
			fmt.Fprintf(w, "\nEncoders not attributed to a command buffer (%d):\n", len(unmapped))
			writeTimelineEncoders(w, timeline, unmapped, timeline.StartTime)
		}
		return nil
	}

	// If no CB events at all, create a dummy one.
	if len(cbs) == 0 {
		cbs = append(cbs, TimelineEvent{
			Name:      "CB#0",
			Timestamp: timeline.StartTime,
			Duration:  timeline.Duration / 1000, // Timeline duration is ns; events use µs.
			Args:      map[string]interface{}{"index": 0},
		})
	}

	encodersByCB, unattributed := attributeEncodersToCBs(timeline, cbs)

	firstTimestamp := timeline.StartTime
	if len(cbs) > 0 && cbs[0].Timestamp < firstTimestamp {
		firstTimestamp = cbs[0].Timestamp
	}

	var prevWallEndUs uint64
	for i, cb := range cbs {
		cbIndex, ok := cb.Args["index"].(int)

		// Annotate wall-time gap between consecutive command buffers.
		if ok && hasRealWallTiming && len(wallCBs) > 0 {
			if wcb, found := wallCBByIndex[cbIndex]; found {
				if i > 0 && prevWallEndUs > 0 && wcb.Timestamp > prevWallEndUs {
					gapUs := wcb.Timestamp - prevWallEndUs
					gapMs := float64(gapUs) / 1000.0
					fmt.Fprintf(w, "  ⏳ idle gap: %.2fms (wall time)\n", gapMs)
				}
				prevWallEndUs = wcb.Timestamp + wcb.Duration
			}
		}

		if source, _ := cb.Args["coordinate_source"].(string); source == "capture byte offset" {
			fmt.Fprintf(w, "%s [capture offset %v]\n", cb.Name, cb.Args["offset"])
		} else {
			var cbStart float64
			if cb.Timestamp >= firstTimestamp {
				cbStart = float64(cb.Timestamp-firstTimestamp) / 1000.0
			}
			// Show duration and wall-time anchor if available.
			var wallNote string
			if ok {
				if wcb, found := wallCBByIndex[cbIndex]; found {
					wallMs := float64(wcb.Timestamp) / 1000.0
					wallDurMs := float64(wcb.Duration) / 1000.0
					wallNote = fmt.Sprintf(", wall=%.2fms/%.2fms", wallMs, wallDurMs)
				}
			}
			if cb.Duration > 0 {
				cbDurationMs := float64(cb.Duration) / 1000.0 // Duration is in µs, convert to ms
				fmt.Fprintf(w, "%s [%.1fms, duration=%.2fms%s]\n", cb.Name, cbStart, cbDurationMs, wallNote)
			} else {
				fmt.Fprintf(w, "%s [%.1fms, duration unavailable: no end timestamp%s]\n", cb.Name, cbStart, wallNote)
			}
		}

		if !ok {
			continue
		}

		writeTimelineEncoders(w, timeline, encodersByCB[cbIndex], firstTimestamp)
	}

	if len(unattributed) > 0 {
		fmt.Fprintf(w, "\nEncoders not attributed to a command buffer (%d):\n", len(unattributed))
		writeTimelineEncoders(w, timeline, unattributed, firstTimestamp)
	}

	return nil
}

// writeTimelineEncoders prints one command buffer's encoders and the kernels
// each ran, as a nested tree structure under the command buffer line.
func writeTimelineEncoders(w io.Writer, timeline *Timeline, encoders []EncoderInfo, firstTimestamp uint64) {
	for i, encoder := range encoders {
		startMs := float64(encoder.StartTime-firstTimestamp) / 1e6
		durationMs := float64(encoder.Duration) / 1e6

		label := encoder.Label
		if label == "" {
			label = fmt.Sprintf("Encoder#%d", encoder.Index)
		}

		var encoderKernels []KernelInfo
		for _, k := range timeline.Kernels {
			if k.Encoder == encoder.Index {
				encoderKernels = append(encoderKernels, k)
			}
		}

		isLastEncoder := (i == len(encoders)-1)
		encPrefix := "├─"
		pipePrefix := "│ "
		if isLastEncoder {
			encPrefix = "└─"
			pipePrefix = "  "
		}

		fmt.Fprintf(w, "%s %s [%.2fms, duration=%.2fms]\n", encPrefix, label, startMs, durationMs)

		for j, k := range encoderKernels {
			kStartMs := float64(k.StartTime-firstTimestamp) / 1e6
			kDurationMs := float64(k.Duration) / 1e6
			isLastKernel := (j == len(encoderKernels)-1)
			kPrefix := "├─"
			if isLastKernel {
				kPrefix = "└─"
			}
			fmt.Fprintf(w, "%s %s %.2fms: %s (%.2fms)\n", pipePrefix, kPrefix, kStartMs, k.Name, kDurationMs)
		}
	}
}

// exportTimelineBoth writes both measured clock domains without assigning a
// timestamp in either domain to data recorded only in the other.
func exportTimelineBoth(timeline *Timeline, format, outputPath string) error {
	return exportTimelineBothWithRawSamples(timeline, format, outputPath, false)
}

func exportTimelineBothWithRawSamples(timeline *Timeline, format, outputPath string, rawProfilerSamples bool) error {
	busy := timelineForClockWithRawSamples(timeline, timelineClockBusy, rawProfilerSamples)
	wall := timelineForClockWithRawSamples(timeline, timelineClockWall, rawProfilerSamples)

	switch format {
	case "text":
		return exportTextTimelineBoth(busy, wall, outputPath)
	case "json":
		return exportTimelineJSONBoth(busy, wall, outputPath)
	case "html":
		return exportHTMLBoth(busy, wall, outputPath)
	case "chrome", "perfetto":
		return fmt.Errorf("--clock both cannot be represented in one %s trace: it has one global time axis; use --format html or json, or export busy and wall separately", format)
	default:
		return validateTimelineFormat(format)
	}
}

func exportTextTimelineBoth(busy, wall *Timeline, outputPath string) error {
	w, closeOutput, err := createCommandOutput(outputPath)
	if err != nil {
		return err
	}
	if closeOutput != nil {
		defer closeOutput()
	}

	fmt.Fprintln(w, "GPU Timeline: two independent clock domains")
	fmt.Fprintln(w, "Busy time is cumulative GPU execution. Wall time is command-buffer scheduling.")
	fmt.Fprintln(w, "No timestamp mapping between them is present in this trace.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "=== GPU busy time ===")
	if err := writeTextTimeline(w, busy); err != nil {
		return err
	}
	fmt.Fprintln(w, "\n=== Wall-clock scheduling ===")
	return writeTextTimeline(w, wall)
}

type timelineBothJSON struct {
	ClockDomain  string    `json:"clock_domain"`
	ClockMapping string    `json:"clock_mapping"`
	Busy         *Timeline `json:"busy"`
	Wall         *Timeline `json:"wall"`
}

func exportTimelineJSONBoth(busy, wall *Timeline, outputPath string) error {
	f, closeOutput, err := createCommandOutput(outputPath)
	if err != nil {
		return err
	}
	if closeOutput != nil {
		defer closeOutput()
	}

	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")
	return encoder.Encode(timelineBothJSON{
		ClockDomain:  string(timelineClockBoth),
		ClockMapping: "none: busy and wall timestamps are independently measured and not aligned",
		Busy:         busy,
		Wall:         wall,
	})
}

// attributeEncodersToCBs groups encoders under the command buffer they ran in.
// Encoders that cannot be placed are returned separately so the report still
// accounts for them.
//
// streamData records no encoder-to-command-buffer link. Dispatch and command
// buffer timestamps do share one absolute GPU tick base, so a dispatch that
// starts inside a command buffer's tick window ran in that command buffer, and
// its encoder did too. A trace with a single command buffer needs no ticks:
// every encoder can only belong to it.
func attributeEncodersToCBs(timeline *Timeline, cbs []TimelineEvent) (map[int][]EncoderInfo, []EncoderInfo) {
	byCB := make(map[int][]EncoderInfo)
	var unattributed []EncoderInfo

	soleCB, hasSoleCB := -1, false
	if len(cbs) == 1 {
		soleCB, hasSoleCB = timelineEventArgInt(cbs[0].Args, "index")
	}

	for _, encoder := range timeline.Encoders {
		cbIndex, found := -1, false
		for _, k := range timeline.Kernels {
			if k.Encoder != encoder.Index {
				continue
			}
			if idx, ok := kernelCBIndex(cbs, k); ok {
				cbIndex, found = idx, true
				break
			}
		}
		if !found && hasSoleCB {
			cbIndex, found = soleCB, true
		}
		if found {
			byCB[cbIndex] = append(byCB[cbIndex], encoder)
		} else {
			unattributed = append(unattributed, encoder)
		}
	}
	return byCB, unattributed
}

// kernelCBIndex reports the command buffer whose tick window contains the
// kernel's start tick. Kernels synthesized from an encoder span carry no
// ticks and cannot be placed this way.
func kernelCBIndex(cbs []TimelineEvent, k KernelInfo) (int, bool) {
	start, ok := timelineEventArgUint64(k.Args, "start_ticks")
	if !ok || start == 0 {
		return -1, false
	}
	for _, cb := range cbs {
		cbStart, okStart := timelineEventArgUint64(cb.Args, "start_ticks")
		cbEnd, okEnd := timelineEventArgUint64(cb.Args, "end_ticks")
		if !okStart || !okEnd || cbEnd < cbStart {
			continue
		}
		if start < cbStart || start > cbEnd {
			continue
		}
		if idx, ok := timelineEventArgInt(cb.Args, "index"); ok {
			return idx, true
		}
	}
	return -1, false
}

// timelineEventArgUint64 reads a tick count from event args. Args round-trip
// through JSON, where every number decodes as float64, so both forms are
// accepted.
func timelineEventArgUint64(args map[string]interface{}, key string) (uint64, bool) {
	switch v := args[key].(type) {
	case uint64:
		return v, true
	case int:
		if v < 0 {
			return 0, false
		}
		return uint64(v), true
	case float64:
		if v < 0 {
			return 0, false
		}
		return uint64(v), true
	}
	return 0, false
}

func timelineEventArgInt(args map[string]interface{}, key string) (int, bool) {
	switch v := args[key].(type) {
	case int:
		return v, true
	case float64:
		return int(v), true
	}
	return 0, false
}

// Timeline represents the complete timeline data.
type Timeline struct {
	TracePath            string                      `json:"trace_path,omitempty"`
	ClockDomain          string                      `json:"clock_domain,omitempty"`
	RawProfilerSamples   bool                        `json:"raw_profiler_samples,omitempty"`
	StartTime            uint64                      `json:"start_time"`
	EndTime              uint64                      `json:"end_time"`
	Duration             uint64                      `json:"duration"`
	Events               []TimelineEvent             `json:"events"`
	Encoders             []EncoderInfo               `json:"encoders"`
	Kernels              []KernelInfo                `json:"kernels"`
	APICallseq           []APICall                   `json:"api_callseq"`
	CounterTracks        []CounterTrack              `json:"counter_tracks,omitempty"`
	UnattributedCounters []UnattributedCounterMetric `json:"unattributed_counters,omitempty"`
	UnavailableEvidence  []UnavailableEvidence       `json:"unavailable_evidence,omitempty"`
	Timing               *TimelineTiming             `json:"timing,omitempty"`
	XcodeMetrics         map[string]any              `json:"xcode_metrics,omitempty"`
	AbsoluteTime         uint64                      `json:"absolute_time"`
	ContinuousTime       uint64                      `json:"continuous_time,omitempty"`
	PState               *int                        `json:"pstate,omitempty"`
	TimebaseNumer        uint64                      `json:"timebase_numer"`
	TimebaseDenom        uint64                      `json:"timebase_denom"`
	MLXSemantics         *mlxsemantic.Sidecar        `json:"mlx_semantics,omitempty"`
	MLXSemanticReport    *mlxsemantic.Report         `json:"mlx_semantic_report,omitempty"`
	MLXSidecarDigest     string                      `json:"mlx_sidecar_digest,omitempty"`
	HostCorrelation      *hostCorrelationProjection  `json:"host_correlation,omitempty"`
	LiveTiming           *liveTimingProjection       `json:"live_timing,omitempty"`
	TraceUUID            string                      `json:"trace_uuid,omitempty"`
	DeviceID             int                         `json:"device_id,omitempty"`
	GPUGeneration        *uint32                     `json:"gpu_generation,omitempty"`
	MetalDeviceName      string                      `json:"metal_device_name,omitempty"`
	MetalPluginName      string                      `json:"metal_plugin_name,omitempty"`
	StreamMetadata       *counter.StreamDataMetadata `json:"stream_metadata,omitempty"`
	ObservedCSLabels     int                         `json:"observed_cs_labels,omitempty"`
	UniqueCSLabels       int                         `json:"unique_cs_labels,omitempty"`
	EvidenceInventory    *TimelineEvidenceInventory  `json:"evidence_inventory,omitempty"`
}

// TimelineEvidenceInventory counts source records before clock filtering.
// Projected event counts are reported separately by each exporter.
type TimelineEvidenceInventory struct {
	CommandBuffers    int `json:"command_buffers"`
	RestoreIntervals  int `json:"restore_intervals"`
	Encoders          int `json:"encoders"`
	Dispatches        int `json:"dispatches"`
	ProfilerStreams   int `json:"raw_profiler_streams"`
	ProfilerRecords   int `json:"raw_profiler_records"`
	UntimedDispatches int `json:"untimed_dispatches"`
}

func timelineEvidenceInventory(timeline *Timeline) TimelineEvidenceInventory {
	if timeline == nil {
		return TimelineEvidenceInventory{}
	}
	return TimelineEvidenceInventory{
		CommandBuffers:    timelineEventCount(timeline, "command_buffer"),
		RestoreIntervals:  timelineEventCount(timeline, "restore"),
		Encoders:          timelineEventCount(timeline, "encoder"),
		Dispatches:        timelineEventCount(timeline, "kernel") + timelineEventCount(timeline, "dispatch"),
		ProfilerStreams:   timelineEventCount(timeline, "profiler_stream"),
		ProfilerRecords:   timelineEventCount(timeline, "gprwcntr"),
		UntimedDispatches: timelineUntimedDispatchCount(timeline),
	}
}

// UnavailableEvidence records an evidence family that could not be projected
// without inventing an identity or clock relationship.
type UnavailableEvidence struct {
	Family string `json:"family"`
	Reason string `json:"reason"`
}

// TimelineTiming summarizes the timing sources that Xcode and gputrace expose.
type TimelineTiming struct {
	EncoderSpanNs            uint64  `json:"encoder_span_ns,omitempty"`
	DispatchSpanNs           uint64  `json:"dispatch_span_ns,omitempty"`
	EffectiveGPUTimeNs       *uint64 `json:"effective_gpu_time_ns,omitempty"`
	CommandBufferActiveNs    uint64  `json:"command_buffer_active_time_ns,omitempty"`
	CommandBufferWallNs      uint64  `json:"command_buffer_wall_time_ns,omitempty"`
	RestoreActiveNs          uint64  `json:"restore_active_time_ns,omitempty"`
	RestoreWallNs            uint64  `json:"restore_wall_time_ns,omitempty"`
	DisplayDurationNs        uint64  `json:"display_duration_ns,omitempty"`
	DisplayDurationSource    string  `json:"display_duration_source,omitempty"`
	TimingSource             string  `json:"timing_source,omitempty"`
	EncoderTimingSource      string  `json:"encoder_timing_source,omitempty"`
	EncoderTimingApproximate bool    `json:"encoder_timing_approximate"`
}

// TimelineEvent represents a single event in the timeline.
type TimelineEvent struct {
	Name        string                 `json:"name"`
	Category    string                 `json:"cat,omitempty"`
	Phase       string                 `json:"ph"` // B, E, X, i, M
	Timestamp   uint64                 `json:"ts"`
	Duration    uint64                 `json:"dur,omitempty"`
	TimestampNS uint64                 `json:"timestamp_ns,omitempty"`
	DurationNS  uint64                 `json:"duration_ns,omitempty"`
	ProcessID   int                    `json:"pid"`
	ThreadID    int                    `json:"tid"`
	Args        map[string]interface{} `json:"args,omitempty"`
}

// EncoderInfo contains information about an encoder.
type EncoderInfo struct {
	Index     int    `json:"index"`
	Label     string `json:"label"`
	Type      string `json:"type"`
	StartTime uint64 `json:"start_time"`
	EndTime   uint64 `json:"end_time"`
	Duration  uint64 `json:"duration"`
}

// KernelInfo contains information about a kernel execution.
type KernelInfo struct {
	Name      string                 `json:"name"`
	Encoder   int                    `json:"encoder"`
	StartTime uint64                 `json:"start_time"`
	EndTime   uint64                 `json:"end_time"`
	Duration  uint64                 `json:"duration"`
	Args      map[string]interface{} `json:"args,omitempty"`
}

// APICall represents an API call event.
type APICall struct {
	Name      string                 `json:"name"`
	Timestamp uint64                 `json:"timestamp"`
	Args      map[string]interface{} `json:"args,omitempty"`
}

// CounterTrack represents a performance counter track over time.
type CounterTrack struct {
	Name             string          `json:"name"`
	Unit             string          `json:"unit"` // %, GB/s, count, etc.
	Description      string          `json:"description,omitempty"`
	XcodeGroups      []string        `json:"xcode_groups,omitempty"`
	XcodeCatalogPath string          `json:"xcode_catalog_path,omitempty"`
	Samples          []CounterSample `json:"samples"`
	MinValue         float64         `json:"min_value"`
	MaxValue         float64         `json:"max_value"`
	AvgValue         float64         `json:"avg_value"`
}

// UnattributedCounterMetric is a pipeline-scoped counter row for which no
// capture-backed encoder identity exists.
type UnattributedCounterMetric struct {
	Label       string                 `json:"label,omitempty"`
	Attribution string                 `json:"attribution"`
	Source      string                 `json:"source"`
	Values      map[string]interface{} `json:"values,omitempty"`
}

// CounterSample represents a single counter measurement at a point in time.
type CounterSample struct {
	Timestamp uint64  `json:"ts"` // Timestamp in nanoseconds
	Value     float64 `json:"value"`
}

// generateTimeline creates timeline data from a trace.
func generateTimeline(trace *gputrace.Trace) (*Timeline, error) {
	timeline := &Timeline{
		TracePath:  trace.Path,
		Events:     make([]TimelineEvent, 0),
		Encoders:   make([]EncoderInfo, 0),
		Kernels:    make([]KernelInfo, 0),
		APICallseq: make([]APICall, 0),
	}

	var streamStats *counter.StreamDataStats
	var profilerDir string
	if stats, err := counter.ExtractPipelineStatsFromTraceStreamData(trace); err == nil {
		streamStats = stats
		applyStreamIdentity(timeline, streamStats)
		counter.CorrelateDispatchSamples(streamStats)
		profilerDir = findProfilerDir(trace.Path)
		if profilerDir != "" {
			annotateDispatchProfilingSampleShares(streamStats, profilerDir)
		}
	}

	// Capture-only bundles have no streamData, but Xcode archives the same
	// shader compilation statistics in the store sections.
	var storeStats *counter.StoreStats
	if streamStats == nil {
		if stats, err := counter.ExtractStoreStats(trace, 0); err == nil {
			storeStats = stats
		}
	}

	var perfStats *gputrace.PerfCounterStats
	if stats, err := gputrace.ParsePerfCounters(trace); err == nil {
		perfStats = stats
	}
	var encoderMetrics []counter.EncoderCounterMetrics
	if perfStats != nil {
		var err error
		encoderMetrics, err = counter.PopulateEncoderMetricsFromPerfCounterStats(perfStats)
		if err != nil {
			return nil, fmt.Errorf("populate counter attribution: %w", err)
		}
		recordUnattributedCounterMetrics(timeline, encoderMetrics)
	}
	var shaderReport *gputrace.ShaderMetricsReport
	if profilerDir != "" {
		if report, err := extractSIMDBasedMetrics(trace, profilerDir); err == nil {
			shaderReport = report
		}
	}
	dispatchCapture := timelineDispatchCaptureEvidence(trace, streamStats)
	sourceMapper := gputrace.NewShaderSourceMapper()
	_ = sourceMapper.IndexShaderSources()
	_ = sourceMapper.IndexTraceBundleSources(trace.Path)
	if storeStats != nil && storeStats.Source != "" {
		_ = sourceMapper.IndexSource(filepath.Join(trace.Path, "store0"), storeStats.Source)
	}

	// Get real encoder labels from ParseComputeEncoders (primary source for labels)
	computeEncoders := trace.ParseComputeEncoders()
	timeline.ObservedCSLabels = len(computeEncoders)
	uniqueCSLabels := make(map[string]bool)
	for _, encoder := range computeEncoders {
		if encoder.Label != "" {
			uniqueCSLabels[encoder.Label] = true
		}
	}
	timeline.UniqueCSLabels = len(uniqueCSLabels)

	// Extract timing metrics. This records whether encoder timings came from
	// measured profiler data or approximate extracted/synthetic fallback data.
	extractor := gputrace.NewTimingMetricsExtractor(trace)
	metrics, err := extractor.Extract()
	if err != nil {
		return nil, fmt.Errorf("extract timing: %w", err)
	}
	useProfilerTiming := timelineMetricsSource(metrics) == "profiler"

	// If TimingMetrics selected profiler timing, use it as measured timing.
	if useProfilerTiming {
		// Calculate total duration
		timeline.Duration = uint64(metrics.TotalDuration)
		timeline.StartTime = 0
		timeline.EndTime = timeline.Duration

		if streamStats != nil {
			timeline.Timing = timelineTimingFromStats(streamStats)
			if streamStats.Timeline != nil {
				timeline.AbsoluteTime = streamStats.Timeline.AbsoluteTime
				timeline.ContinuousTime = streamStats.Timeline.ContinuousTime
				if streamStats.Timeline.PState != nil {
					value := *streamStats.Timeline.PState
					timeline.PState = &value
				}
				timeline.TimebaseNumer = streamStats.Timeline.TimebaseNumer
				timeline.TimebaseDenom = streamStats.Timeline.TimebaseDenom
			}
		}

		// Build timeline from profiler timing selected by TimingMetrics.
		for i, et := range metrics.EncoderTimings {
			durationNs := et.DurationNs
			startTimeNs := et.StartTimestamp
			endTimeNs := et.EndTimestamp
			if endTimeNs <= startTimeNs {
				endTimeNs = startTimeNs + durationNs
			}

			label := et.Label
			if label == "" && i < len(computeEncoders) {
				label = computeEncoders[i].Label
			}
			if label == "" {
				label = fmt.Sprintf("Encoder_%d", i)
			}

			encoderInfo := EncoderInfo{
				Index:     i,
				Label:     label,
				Type:      "compute",
				StartTime: startTimeNs,
				EndTime:   endTimeNs,
				Duration:  durationNs,
			}
			timeline.Encoders = append(timeline.Encoders, encoderInfo)

			// Create timeline event for encoder
			event := TimelineEvent{
				Name:      label,
				Category:  "encoder",
				Phase:     "X",
				Timestamp: startTimeNs / 1000, // Convert to µs for Chrome format
				Duration:  durationNs / 1000,
				ProcessID: 1,
				ThreadID:  1,
				Args: map[string]interface{}{
					"index":       i,
					"duration_ms": float64(durationNs) / 1e6,
					"duration_us": float64(durationNs) / 1e3,
				},
			}
			addTimingMetricsEventArgs(event.Args, metrics)
			timeline.Events = append(timeline.Events, event)
		}
	} else {
		// Fall back to synthetic/heuristic timing
		// Build a map of timing by label for lookup
		timingByLabel := make(map[string]*gputrace.EncoderTiming)
		for _, et := range metrics.EncoderTimings {
			timingByLabel[et.Label] = et
		}

		// Calculate timeline bounds from timing metrics
		if len(metrics.EncoderTimings) > 0 {
			timeline.StartTime = metrics.EncoderTimings[0].StartTimestamp
			timeline.EndTime = metrics.EncoderTimings[0].EndTimestamp

			for _, encoder := range metrics.EncoderTimings {
				if encoder.StartTimestamp < timeline.StartTime {
					timeline.StartTime = encoder.StartTimestamp
				}
				if encoder.EndTimestamp > timeline.EndTime {
					timeline.EndTime = encoder.EndTimestamp
				}
			}
		}

		timeline.Duration = timeline.EndTime - timeline.StartTime

		// Use compute encoders as primary source for encoder info (better labels)
		if len(computeEncoders) > 0 && timelineMetricsSource(metrics) != "unavailable" {
			populateUnprofiledEncoderEvents(timeline, computeEncoders, timingByLabel, metrics)
		} else if timelineMetricsSource(metrics) != "unavailable" {
			// Fall back to timing metrics if no compute encoders found
			for i, encoder := range metrics.EncoderTimings {
				encoderInfo := EncoderInfo{
					Index:     i,
					Label:     encoder.Label,
					Type:      "compute",
					StartTime: encoder.StartTimestamp,
					EndTime:   encoder.EndTimestamp,
					Duration:  encoder.DurationNs,
				}
				timeline.Encoders = append(timeline.Encoders, encoderInfo)

				event := TimelineEvent{
					Name:      encoder.Label,
					Category:  "encoder",
					Phase:     "i",
					Timestamp: encoder.StartTimestamp / 1000,
					Duration:  0,
					ProcessID: 1,
					ThreadID:  1,
					Args: map[string]interface{}{
						"index":         i,
						"timing_source": "unprofiled (ordering/identity instant)",
					},
				}
				addTimingMetricsEventArgs(event.Args, metrics)
				timeline.Events = append(timeline.Events, event)
			}
		}
	}
	annotateTimelineWithTimingMetrics(timeline, metrics)

	// Add shader/kernel events. Prefer streamData dispatches so the Shaders lane
	// matches Xcode's pipeline table instead of duplicating whole encoder spans.
	if !addDispatchKernelEvents(timeline, streamStats, dispatchCapture, shaderReport, perfStats, encoderMetrics, sourceMapper) {
		if err := addCaptureDispatchEvents(timeline, trace, sourceMapper, storeStats); err != nil {
			return nil, fmt.Errorf("add capture dispatches: %w", err)
		}
	}

	// Add command buffer events - try to get real timing from APSTimelineData
	if streamStats != nil && streamStats.Timeline != nil && len(streamStats.Timeline.CommandBufferTimestamps) > 0 {
		// Use real CB timing from APSTimelineData
		ti := streamStats.Timeline
		timeline.AbsoluteTime = ti.AbsoluteTime
		timeline.ContinuousTime = ti.ContinuousTime
		if ti.PState != nil {
			value := *ti.PState
			timeline.PState = &value
		}
		timeline.TimebaseNumer = ti.TimebaseNumer
		timeline.TimebaseDenom = ti.TimebaseDenom
		addRestoreEvents(timeline, ti)

		// Command buffers are placed at their real offset from AbsoluteTime.
		// They used to be packed back to back with a running displayStartNs
		// accumulator, which erased every idle gap: on the 21-encoder capture
		// that compressed 2979 ms of wall time into 8.3 ms and drew a GPU that
		// is 0.28% busy as 99.9% busy. The args said "real_timing": true the
		// whole time.
		for _, cb := range ti.CommandBufferTimestamps {
			durationNs := cb.DurationNs(ti.TimebaseNumer, ti.TimebaseDenom)
			durationUs := durationNs / 1000
			var rawStartOffsetNs uint64
			if cb.StartTicks > ti.AbsoluteTime {
				rawStartOffsetNs = (cb.StartTicks - ti.AbsoluteTime) * ti.TimebaseNumer / ti.TimebaseDenom
			}

			event := TimelineEvent{
				Name:      fmt.Sprintf("CB#%d", cb.Index),
				Category:  "command_buffer",
				Phase:     timelineDurationPhase(durationUs),
				Timestamp: rawStartOffsetNs / 1000, // Convert to microseconds for Chrome format
				Duration:  durationUs,
				ProcessID: 1,
				ThreadID:  0,
				Args: map[string]interface{}{
					"index":               cb.Index,
					"start_ticks":         cb.StartTicks,
					"end_ticks":           cb.EndTicks,
					"raw_start_offset_ns": rawStartOffsetNs,
					"duration_us":         float64(durationNs) / 1000,
					"duration_ms":         float64(durationNs) / 1e6,
					"timing_source":       "APSTimelineData Command Buffer Timestamps",
					"real_timing":         true,
				},
			}
			timeline.Events = append(timeline.Events, event)
			if endNs := rawStartOffsetNs + durationNs; endNs > timeline.EndTime {
				timeline.EndTime = endNs
			}
		}

		// Preserve aggregates of GPRWCNTR records as raw profiler stream spans.
		// These are not established encoder intervals, so they are opt-in with
		// the raw records rather than appearing as encoders in the wall view.
		if len(ti.EncoderProfiles) > 0 {
			epLanes := newLanePacker(7, 8) // Raw profiler stream lanes 0..7
			for _, ep := range ti.EncoderProfiles {
				if ep.SampleCount == 0 || ep.StartTicks == 0 {
					continue
				}
				// Convert to nanoseconds relative to capture start
				startNs := (ep.StartTicks - ti.AbsoluteTime) * ti.TimebaseNumer / ti.TimebaseDenom

				event := TimelineEvent{
					Name:      fmt.Sprintf("Profiler stream %s #%d", ep.Source, ep.Index),
					Category:  "profiler_stream",
					Phase:     "X",
					Timestamp: startNs / 1000, // Convert to microseconds
					Duration:  ep.DurationNs / 1000,
					ProcessID: 1,
					ThreadID:  epLanes.assign(startNs/1000, ep.DurationNs/1000),
					Args: map[string]interface{}{
						"index":           ep.Index,
						"source":          ep.Source,
						"ring_buffer_idx": ep.RingBufferIndex,
						"sample_count":    ep.SampleCount,
						"duration_ns":     ep.DurationNs,
						"duration_us":     float64(ep.DurationNs) / 1000,
						"start_ticks":     ep.StartTicks,
						"end_ticks":       ep.EndTicks,
						"real_timing":     true,
					},
				}
				timeline.Events = append(timeline.Events, event)
			}
		}
	} else {
		// Fall back to ParseCommandBuffers for offset-only markers
		commandBuffers, err := trace.ParseCommandBuffers()
		if err == nil {
			for i, cb := range commandBuffers {
				event := TimelineEvent{
					Name:      fmt.Sprintf("CommandBuffer %d", i),
					Category:  "command_buffer",
					Phase:     "i",
					Timestamp: uint64(i),
					ProcessID: 1,
					ThreadID:  0,
					Args: map[string]interface{}{
						"offset":            cb.Offset,
						"index":             i,
						"coordinate_source": "capture byte offset",
						"real_timing":       false,
					},
				}
				timeline.Events = append(timeline.Events, event)
			}
		}
	}

	// Generate performance counter tracks
	timeline.CounterTracks = generateCounterTracks(trace, timeline)

	// Normalize timestamps to start at 0 (match Xcode visual baseline)
	// Find global minimum timestamp across all functional events (exclude metadata)
	var globalMinTs uint64 = ^uint64(0)
	foundAny := false

	for _, ev := range timeline.Events {
		if ev.Phase == "M" {
			continue
		}
		if ev.Timestamp < globalMinTs {
			globalMinTs = ev.Timestamp
			foundAny = true
		}
	}

	// Also check counter tracks for global minimum
	for _, track := range timeline.CounterTracks {
		for _, sample := range track.Samples {
			// Counter samples are in ns, ev.Timestamp is in us
			// Convert sample to us for comparison
			tsUs := sample.Timestamp / 1000
			if tsUs < globalMinTs {
				globalMinTs = tsUs
				foundAny = true
			}
		}
	}

	// Apply shift if we found any events
	if foundAny && globalMinTs > 0 {
		fmt.Fprintf(os.Stderr, "Normalizing timeline: shifting by -%d µs\n", globalMinTs)
		for i := range timeline.Events {
			ev := &timeline.Events[i]
			if ev.Phase == "M" {
				continue
			}
			if ev.Timestamp >= globalMinTs {
				ev.Timestamp -= globalMinTs
			} else {
				ev.Timestamp = 0
			}
		}

		// Shift counter tracks
		globalMinTsNs := globalMinTs * 1000
		for i := range timeline.CounterTracks {
			track := &timeline.CounterTracks[i]
			for j := range track.Samples {
				sample := &track.Samples[j]
				if sample.Timestamp >= globalMinTsNs {
					sample.Timestamp -= globalMinTsNs
				} else {
					sample.Timestamp = 0
				}
			}
		}

		// Also shift the nanosecond bounds.
		if timeline.StartTime >= globalMinTsNs {
			timeline.StartTime -= globalMinTsNs
		} else {
			timeline.StartTime = 0
		}
		if timeline.EndTime >= globalMinTsNs {
			timeline.EndTime -= globalMinTsNs
		}
		for i := range timeline.Encoders {
			enc := &timeline.Encoders[i]
			if enc.StartTime >= globalMinTsNs {
				enc.StartTime -= globalMinTsNs
			} else {
				enc.StartTime = 0
			}
			if enc.EndTime >= globalMinTsNs {
				enc.EndTime -= globalMinTsNs
			}
		}
		for i := range timeline.Kernels {
			k := &timeline.Kernels[i]
			if k.StartTime >= globalMinTsNs {
				k.StartTime -= globalMinTsNs
			} else {
				k.StartTime = 0
			}
			if k.EndTime >= globalMinTsNs {
				k.EndTime -= globalMinTsNs
			}
		}
	}

	timeline.XcodeMetrics = timelineXcodeMetricsArgs(timeline)
	return timeline, nil
}

func applyStreamIdentity(timeline *Timeline, stats *counter.StreamDataStats) {
	if timeline == nil || stats == nil {
		return
	}
	if stats.GPUGeneration != nil {
		generation := *stats.GPUGeneration
		timeline.GPUGeneration = &generation
	}
	timeline.MetalDeviceName = stats.MetalDeviceName
	timeline.MetalPluginName = stats.MetalPluginName
	if streamDataMetadataPresent(stats.Metadata) {
		metadata := cloneStreamDataMetadata(stats.Metadata)
		timeline.StreamMetadata = &metadata
	}
}

func streamDataMetadataPresent(metadata counter.StreamDataMetadata) bool {
	return metadata.Version != nil || metadata.UnixTimestamp != nil || metadata.TraceName != "" ||
		metadata.ProfiledExecutionMode != nil || metadata.ProfiledPerformanceState != nil ||
		metadata.ProfiledProfilerMode != nil || metadata.CaptureRangeLocation != nil ||
		metadata.CaptureRangeLength != nil || metadata.DataSourceHasUnusedResources != nil ||
		metadata.SupportsSeparateAPSData != nil || metadata.NumBlitCalls != nil ||
		streamDataTablesPresent(metadata.Tables) || streamDataFamiliesPresent(metadata.Families) ||
		streamDataDecodedFamiliesPresent(metadata.DecodedFamilies) || metadata.CounterDecode != nil
}

func streamDataTablesPresent(tables counter.StreamDataTables) bool {
	return tables.CommandBuffers != nil || tables.Encoders != nil || tables.GPUCommands != nil ||
		tables.Pipelines != nil || tables.Functions != nil
}

func streamDataFamiliesPresent(families counter.StreamDataFamilies) bool {
	return families.APSData != nil || families.APSTimelineData != nil || families.APSCounterData != nil ||
		families.ShaderProfilerData != nil || families.GPUTimelineData != nil ||
		families.BatchIDFilteredCountersData != nil
}

func streamDataDecodedFamiliesPresent(families counter.StreamDataDecodedFamilies) bool {
	return families.APSData != nil || families.APSTimelineData != nil || families.APSCounterData != nil ||
		families.ShaderProfilerData != nil || families.GPUTimelineData != nil ||
		families.BatchIDFilteredCountersData != nil
}

func cloneStreamDataMetadata(metadata counter.StreamDataMetadata) counter.StreamDataMetadata {
	result := metadata
	result.Version = cloneInt64(metadata.Version)
	result.UnixTimestamp = cloneInt64(metadata.UnixTimestamp)
	result.ProfiledExecutionMode = cloneInt64(metadata.ProfiledExecutionMode)
	result.ProfiledPerformanceState = cloneInt64(metadata.ProfiledPerformanceState)
	result.ProfiledProfilerMode = cloneInt64(metadata.ProfiledProfilerMode)
	result.CaptureRangeLocation = cloneInt64(metadata.CaptureRangeLocation)
	result.CaptureRangeLength = cloneInt64(metadata.CaptureRangeLength)
	result.DataSourceHasUnusedResources = cloneBool(metadata.DataSourceHasUnusedResources)
	result.SupportsSeparateAPSData = cloneBool(metadata.SupportsSeparateAPSData)
	result.NumBlitCalls = cloneInt64(metadata.NumBlitCalls)
	result.Tables.CommandBuffers = cloneStreamDataTable(metadata.Tables.CommandBuffers)
	result.Tables.Encoders = cloneStreamDataTable(metadata.Tables.Encoders)
	result.Tables.GPUCommands = cloneStreamDataTable(metadata.Tables.GPUCommands)
	result.Tables.Pipelines = cloneStreamDataTable(metadata.Tables.Pipelines)
	result.Tables.Functions = cloneStreamDataTable(metadata.Tables.Functions)
	result.Families.APSData = cloneInt64(metadata.Families.APSData)
	result.Families.APSTimelineData = cloneInt64(metadata.Families.APSTimelineData)
	result.Families.APSCounterData = cloneInt64(metadata.Families.APSCounterData)
	result.Families.ShaderProfilerData = cloneInt64(metadata.Families.ShaderProfilerData)
	result.Families.GPUTimelineData = cloneInt64(metadata.Families.GPUTimelineData)
	result.Families.BatchIDFilteredCountersData = cloneInt64(metadata.Families.BatchIDFilteredCountersData)
	result.DecodedFamilies.APSData = cloneInt64(metadata.DecodedFamilies.APSData)
	result.DecodedFamilies.APSTimelineData = cloneInt64(metadata.DecodedFamilies.APSTimelineData)
	result.DecodedFamilies.APSCounterData = cloneInt64(metadata.DecodedFamilies.APSCounterData)
	result.DecodedFamilies.ShaderProfilerData = cloneInt64(metadata.DecodedFamilies.ShaderProfilerData)
	result.DecodedFamilies.GPUTimelineData = cloneInt64(metadata.DecodedFamilies.GPUTimelineData)
	result.DecodedFamilies.BatchIDFilteredCountersData = cloneInt64(metadata.DecodedFamilies.BatchIDFilteredCountersData)
	if metadata.CounterDecode != nil {
		counterDecode := *metadata.CounterDecode
		result.CounterDecode = &counterDecode
	}
	return result
}

func cloneStreamDataTable(table *counter.StreamDataTable) *counter.StreamDataTable {
	if table == nil {
		return nil
	}
	result := *table
	result.RecordSize = cloneInt64(table.RecordSize)
	result.RecordCount = cloneInt64(table.RecordCount)
	result.RemainderBytes = cloneInt64(table.RemainderBytes)
	return &result
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func populateUnprofiledEncoderEvents(timeline *Timeline, computeEncoders []*tracepkg.ComputeEncoder, timingByLabel map[string]*gputrace.EncoderTiming, metrics *gputrace.TimingMetrics) {
	if timeline == nil || len(computeEncoders) == 0 {
		return
	}
	avgDuration := timeline.Duration / uint64(len(computeEncoders))
	if avgDuration == 0 {
		avgDuration = 1000000 // 1ms default
	}

	currentTime := timeline.StartTime
	for i, enc := range computeEncoders {
		var startTime, endTime, duration uint64
		if timing, ok := timingByLabel[enc.Label]; ok {
			startTime = timing.StartTimestamp
			endTime = timing.EndTimestamp
			duration = timing.DurationNs
		} else {
			startTime = currentTime
			duration = avgDuration
			endTime = startTime + duration
			currentTime = endTime + 10000
		}
		encoderInfo := EncoderInfo{
			Index:     i,
			Label:     enc.Label,
			Type:      "compute",
			StartTime: startTime,
			EndTime:   endTime,
			Duration:  duration,
		}
		timeline.Encoders = append(timeline.Encoders, encoderInfo)

		// Create timeline event for encoder in unprofiled fallback:
		// Emitted as Phase 'i' zero-duration instant. Synthetic/extracted timestamps
		// are retained only for ordering.
		event := TimelineEvent{
			Name:      enc.Label,
			Category:  "encoder",
			Phase:     "i",
			Timestamp: startTime / 1000, // Convert to microseconds
			Duration:  0,
			ProcessID: 1,
			ThreadID:  1,
			Args: map[string]interface{}{
				"index":         i,
				"address":       fmt.Sprintf("0x%x", enc.Address),
				"timing_source": "unprofiled (ordering/identity instant)",
			},
		}
		addTimingMetricsEventArgs(event.Args, metrics)
		timeline.Events = append(timeline.Events, event)
	}
}

// generateCounterTracks returns only counter series whose clock is established
// in the selected timeline domain. APSCounterData currently provides useful
// per-encoder aggregates, but its timestamps have no verified mapping to the
// cumulative busy clock, so those aggregates remain encoder details.
func generateCounterTracks(trace *gputrace.Trace, timeline *Timeline) []CounterTrack {
	streamStats, _ := gputrace.ExtractPipelineStats(trace)
	if streamStats == nil || streamStats.CounterArchive == nil {
		return nil
	}
	annotateEncoderCounterArchive(timeline, streamStats.CounterArchive)
	timeline.UnavailableEvidence = append(timeline.UnavailableEvidence, UnavailableEvidence{
		Family: "APSCounterData time series",
		Reason: "counter clock has no verified mapping to cumulative GPU-busy time",
	})
	return nil
}

func recordUnattributedCounterMetrics(timeline *Timeline, metrics []counter.EncoderCounterMetrics) {
	if timeline == nil {
		return
	}
	for _, metric := range metrics {
		if metric.Attribution == counter.CounterAttributionEncoder && metric.EncoderIndex >= 0 {
			continue
		}
		values := make(map[string]interface{})
		if metric.ALUUtilization != 0 {
			values["alu_utilization_pct"] = metric.ALUUtilization
		}
		if metric.MemoryBandwidth != 0 {
			values["memory_bandwidth_bytes"] = metric.MemoryBandwidth
		}
		if metric.DeviceMemoryBandwidthGBps != 0 {
			values["device_memory_bandwidth_gbps"] = metric.DeviceMemoryBandwidthGBps
		}
		if metric.BytesReadFromDeviceMemory != 0 {
			values["device_memory_read_bytes"] = metric.BytesReadFromDeviceMemory
		}
		if metric.BytesWrittenToDeviceMemory != 0 {
			values["device_memory_write_bytes"] = metric.BytesWrittenToDeviceMemory
		}
		if metric.InstructionThroughputUtil != 0 {
			values["instruction_throughput_utilization_pct"] = metric.InstructionThroughputUtil
		}
		if metric.ComputeShaderLaunchLimiter != 0 {
			values["compute_shader_launch_limiter_pct"] = metric.ComputeShaderLaunchLimiter
		}
		if metric.BufferL1MissRate != 0 {
			values["buffer_l1_miss_rate_pct"] = metric.BufferL1MissRate
		}
		timeline.UnattributedCounters = append(timeline.UnattributedCounters, UnattributedCounterMetric{
			Label:       metric.EncoderLabel,
			Attribution: string(counter.CounterAttributionUnknown),
			Source:      "PerfCounterStats pipeline row",
			Values:      values,
		})
	}
}

// annotateEncoderCounterArchive records capture-backed cycle aggregates on
// encoder events. Encoder Infos guarantees execution order, but does not expose
// a Metal encoder foreign key, so the relationship basis remains explicit.
func annotateEncoderCounterArchive(timeline *Timeline, archive *counter.CounterArchive) {
	if archive == nil || timeline == nil {
		return
	}
	costs := archive.EncoderCosts()
	if len(costs) == 0 {
		return
	}
	byOrdinal := make(map[int]counter.EncoderCost, len(costs))
	for _, c := range costs {
		byOrdinal[c.Ordinal] = c
	}
	for i := range timeline.Events {
		event := &timeline.Events[i]
		if event.Category != "encoder" {
			continue
		}
		index, ok := timelineEventArgInt(event.Args, "index")
		if !ok {
			continue
		}
		c, ok := byOrdinal[index]
		if !ok {
			continue
		}
		event.Args["gpu_cycles"] = c.GPUCycles
		event.Args["gpu_cycles_source"] = "APSCounterData GRC_GPU_CYCLES end records"
		event.Args["execution_cost_pct"] = c.CostPercent
		event.Args["execution_cost_formula"] = "100 * encoder GPU cycles / capture GPU cycles"
		event.Args["counter_attribution_basis"] = "Encoder Infos execution ordinal"
		event.Args["counter_end_records"] = c.EndRecords
		event.Args["counter_sample_count"] = c.SampleCount
		if c.Sparse() {
			event.Args["counter_coverage"] = "sparse: fewer than 16 end-counter reads"
		} else {
			event.Args["counter_coverage"] = "at least one end-counter read per replay group"
		}
	}
}

// calculateTrackStats calculates min, max, and average values for a counter track.
func calculateTrackStats(track *CounterTrack) {
	if len(track.Samples) == 0 {
		return
	}

	track.MinValue = track.Samples[0].Value
	track.MaxValue = track.Samples[0].Value
	sum := 0.0

	for _, sample := range track.Samples {
		if sample.Value < track.MinValue {
			track.MinValue = sample.Value
		}
		if sample.Value > track.MaxValue {
			track.MaxValue = sample.Value
		}
		sum += sample.Value
	}

	track.AvgValue = sum / float64(len(track.Samples))
}

func appendCounterTrackSample(track *CounterTrack, encoder EncoderInfo, value float64) {
	if track == nil || value <= 0 {
		return
	}
	appendCounterTrackSampleValue(track, encoder, value)
}

func appendCounterTrackSampleValue(track *CounterTrack, encoder EncoderInfo, value float64) {
	if track == nil {
		return
	}
	track.Samples = append(track.Samples,
		CounterSample{Timestamp: encoder.StartTime, Value: value},
		CounterSample{Timestamp: encoder.EndTime, Value: value})
}

// annotateDispatchProfilingSampleShares records the share of Profiling_f
// samples assigned to each pipeline. Sampling share is an estimate, not Xcode
// Execution Cost, so it is kept distinct from a validated cost measurement.
func annotateDispatchProfilingSampleShares(stats *counter.StreamDataStats, profilerDir string) {
	for i, share := range dispatchProfilingSampleShares(stats, profilerDir) {
		stats.Dispatches[i].ProfilingSampleSharePct = share
	}
}

func dispatchProfilingSampleShares(stats *counter.StreamDataStats, profilerDir string) map[int]float64 {
	if stats == nil || len(stats.Pipelines) == 0 || profilerDir == "" {
		return nil
	}
	pipelineIDs := make([]int, 0, len(stats.Pipelines))
	for _, p := range stats.Pipelines {
		pipelineIDs = append(pipelineIDs, p.PipelineID)
	}
	costs, err := counter.ParseExecutionCost(profilerDir, pipelineIDs)
	if err != nil {
		return nil
	}
	shares := make(map[int]float64, len(stats.Dispatches))
	for i, dispatch := range stats.Dispatches {
		if share, ok := costs.PipelineCosts[dispatch.PipelineID]; ok {
			shares[i] = share
		}
	}
	return shares
}

// annotateDispatchExecutionCosts is retained for the legacy Xcode-parity
// report. Its values are sampling-share estimates, not measured Xcode costs.
func annotateDispatchExecutionCosts(stats *counter.StreamDataStats, profilerDir string) {
	for i, share := range dispatchProfilingSampleShares(stats, profilerDir) {
		stats.Dispatches[i].ExecutionCostPct = share
	}
}

// addPipelineCompilerArgs records static shader compiler statistics. The
// caller supplies an already-attributed pipeline; this function does not infer
// pipeline identity or add dynamic counter measurements.
func addPipelineCompilerArgs(args map[string]interface{}, p *counter.PipelineStats, source string) {
	if p == nil {
		return
	}
	if p.FunctionName != "" {
		args["function_name"] = p.FunctionName
	}
	if p.PipelineID != 0 {
		if _, ok := args["pipeline_id"]; !ok {
			args["pipeline_id"] = p.PipelineID
		}
	}
	if p.PipelineAddress != 0 {
		args["pipeline_state"] = fmt.Sprintf("0x%x", p.PipelineAddress)
		args["pipeline_address"] = p.PipelineAddress
	}
	args["allocated_registers"] = p.TemporaryRegisterCount
	args["uniform_registers"] = p.UniformRegisterCount
	args["spilled_bytes"] = p.SpilledBytes
	args["thread_invariant_spilled"] = p.ThreadInvariantSpilled
	args["threadgroup_memory"] = p.ThreadgroupMemory
	args["instruction_count"] = p.InstructionCount
	args["alu_instruction_count"] = p.ALUInstructionCount
	args["fp32_instruction_count"] = p.FP32InstructionCount
	args["fp16_instruction_count"] = p.FP16InstructionCount
	args["int32_instruction_count"] = p.INT32InstructionCount
	args["int16_instruction_count"] = p.INT16InstructionCount
	args["branch_instruction_count"] = p.BranchInstructionCount
	args["device_load_instruction_count"] = p.DeviceLoadCount
	args["device_store_instruction_count"] = p.DeviceStoreCount
	args["device_atomic_instruction_count"] = p.DeviceAtomicCount
	args["texture_reads_instruction_count"] = p.TextureReadCount
	args["texture_writes_instruction_count"] = p.TextureWriteCount
	args["threadgroup_load_instruction_count"] = p.ThreadgroupLoadCount
	args["threadgroup_store_instruction_count"] = p.ThreadgroupStoreCount
	args["threadgroup_atomic_instruction_count"] = p.ThreadgroupAtomicCount
	args["wait_instruction_count"] = p.WaitInstructionCount
	args["constant_calculation_temporary_register_count"] = p.ConstantCalculationTemporaryRegisterCount
	args["constant_calculation_phase_present"] = p.ConstantCalculationPhasePresent
	args["compilation_time_ms"] = p.CompilationTimeMs
	args["metrics_source"] = source
}

func addEncoderKernelEvents(timeline *Timeline, trace *gputrace.Trace, sourceMapper *gputrace.ShaderSourceMapper, storeStats *counter.StoreStats) {
	computeEncoders := traceComputeEncoders(trace)
	lanes := newLanePacker(3, 4) // Kernels Lane 0..3

	for i, encoder := range timeline.Encoders {
		args := map[string]interface{}{
			"encoder_index": encoder.Index,
			"source":        "encoder span",
		}
		if encEvent, ok := timelineEncoderEvent(timeline, encoder.Index); !ok || encEvent.Phase != "i" {
			args["duration_us"] = float64(encoder.Duration) / 1e3
		}
		if len(computeEncoders) > 0 && i < len(computeEncoders) {
			dispatches := parseEncoderDispatches(trace, computeEncoders, i)
			if len(dispatches) > 0 {
				var simdGroups uint64
				for _, d := range dispatches {
					simdGroups += d.SIMDGroups()
				}
				if simdGroups > 0 {
					args["simd_groups"] = simdGroups
					args["dispatch_count"] = len(dispatches)
					args["source"] = "encoder span; dispatch geometry"
					d := dispatches[0]
					gridSize := fmt.Sprintf("%d,%d,%d", d.ThreadsX, d.ThreadsY, d.ThreadsZ)
					groupSize := fmt.Sprintf("%d,%d,%d", d.ThreadsPerGroupX, d.ThreadsPerGroupY, d.ThreadsPerGroupZ)
					if len(dispatches) > 1 {
						gridSize += fmt.Sprintf(" (+%d more)", len(dispatches)-1)
					}
					args["grid_size"] = gridSize
					args["threadgroup_size"] = groupSize
				}
			}
		}
		addPipelineCompilerArgs(args, storeStats.PipelineForLabel(encoder.Label), "capture bundle store sections")
		if sourceMapper != nil {
			if sourceFile, sourceLine := sourceMapper.SourceLocation(encoder.Label); sourceFile != "" {
				args["source_available"] = true
				args["source_file"] = sourceFile
				args["source_line"] = sourceLine
			}
		}
		kernelInfo := KernelInfo{
			Name:      encoder.Label,
			Encoder:   encoder.Index,
			StartTime: encoder.StartTime,
			EndTime:   encoder.EndTime,
			Duration:  encoder.Duration,
			Args:      args,
		}
		timeline.Kernels = append(timeline.Kernels, kernelInfo)

		threadID := lanes.assign(encoder.StartTime/1000, encoder.Duration/1000)
		if id, ok := timelineEncoderThreadID(timeline, encoder.Index); ok {
			threadID = id
		}

		phase := "X"
		eventDuration := encoder.Duration / 1000
		if encEvent, ok := timelineEncoderEvent(timeline, encoder.Index); ok && encEvent.Phase == "i" {
			phase = "i"
			eventDuration = 0
		}

		timeline.Events = append(timeline.Events, TimelineEvent{
			Name:      encoder.Label,
			Category:  "kernel",
			Phase:     phase,
			Timestamp: encoder.StartTime / 1000,
			Duration:  eventDuration,
			ProcessID: 1,
			ThreadID:  threadID,
			Args:      args,
		})
	}
}

func addCaptureDispatchEvents(timeline *Timeline, trace *gputrace.Trace, sourceMapper *gputrace.ShaderSourceMapper, storeStats *counter.StoreStats) error {
	if timeline == nil || trace == nil {
		return nil
	}
	dispatches, err := trace.ParseAttributedDispatches()
	if err != nil {
		return err
	}
	for _, dispatch := range dispatches {
		name := dispatch.FunctionName
		if name == "" {
			name = fmt.Sprintf("Dispatch #%d", dispatch.Index)
		}
		args := map[string]interface{}{
			"dispatch_index":           dispatch.Index,
			"command_buffer_index":     dispatch.CommandBuffer,
			"capture_offset":           dispatch.CaptureOffset,
			"pipeline_state":           fmt.Sprintf("0x%x", dispatch.PipelineAddr),
			"pipeline_address":         dispatch.PipelineAddr,
			"pipeline_identity_source": "capture dispatch record",
			"pipeline_identity_scope":  "capture-local",
			"source":                   "capture dispatch record; dispatch geometry",
			"coordinate_source":        "capture record order",
			"timing_source":            "unavailable",
			"function_attribution":     dispatch.AttributionBasis,
			"encoder_attribution":      "unavailable",
		}
		addDispatchGeometryArgs(args, dispatch.DispatchThreads)
		if dispatch.FunctionName != "" {
			addPipelineCompilerArgs(args, storeStats.PipelineForLabel(dispatch.FunctionName), "capture bundle store sections")
			if sourceMapper != nil {
				if sourceFile, sourceLine := sourceMapper.SourceLocation(dispatch.FunctionName); sourceFile != "" {
					args["source_available"] = true
					args["source_file"] = sourceFile
					args["source_line"] = sourceLine
				}
			}
		}
		timeline.Kernels = append(timeline.Kernels, KernelInfo{Name: name, Encoder: -1, Args: args})
		timeline.Events = append(timeline.Events, TimelineEvent{
			Name:      name,
			Category:  "dispatch",
			Phase:     "i",
			Timestamp: uint64(dispatch.Index),
			ProcessID: 1,
			ThreadID:  3,
			Args:      args,
		})
	}
	return nil
}

func timelineEncoderEvent(timeline *Timeline, index int) (TimelineEvent, bool) {
	if timeline == nil {
		return TimelineEvent{}, false
	}
	for _, event := range timeline.Events {
		eventIndex, ok := timelineEventArgInt(event.Args, "index")
		if event.Category == "encoder" && ok && eventIndex == index {
			return event, true
		}
	}
	return TimelineEvent{}, false
}

func traceComputeEncoders(trace *gputrace.Trace) []*tracepkg.ComputeEncoder {
	if trace == nil {
		return nil
	}
	return trace.ParseComputeEncoders()
}

func parseEncoderDispatches(trace *gputrace.Trace, encoders []*tracepkg.ComputeEncoder, index int) []tracepkg.DispatchThreads {
	if trace == nil || index < 0 || index >= len(encoders) || len(trace.CaptureData) == 0 {
		return nil
	}
	startOffset := encoders[index].Offset
	endOffset := int64(len(trace.CaptureData))
	if index < len(encoders)-1 {
		endOffset = encoders[index+1].Offset
	}
	captureLen := int64(len(trace.CaptureData))
	if startOffset < 0 || startOffset >= captureLen || endOffset > captureLen || startOffset >= endOffset {
		return nil
	}
	dispatches := trace.ParseDispatchInRegion(trace.CaptureData[startOffset:endOffset], startOffset)
	return dispatches
}

func addDispatchKernelEvents(timeline *Timeline, stats *counter.StreamDataStats, capture timelineDispatchCaptureStats, shaderReport *gputrace.ShaderMetricsReport, perfStats *gputrace.PerfCounterStats, encoderMetrics []counter.EncoderCounterMetrics, sourceMapper *gputrace.ShaderSourceMapper) bool {
	if timeline == nil || stats == nil || len(stats.Dispatches) == 0 {
		return false
	}
	// stats.Pipelines comes from pipelinePerformanceStatistics, an NSDictionary,
	// so its slice order is unrelated to the pipeline index that
	// gpuCommandInfoData records carry. Join on the pipeline ID instead; both
	// sides carry it.
	pipelineByID := make(map[int]*counter.PipelineStats, len(stats.Pipelines))
	for i := range stats.Pipelines {
		pipelineByID[stats.Pipelines[i].PipelineID] = &stats.Pipelines[i]
	}
	lanes := newLanePacker(3, 4) // Kernels Lane 0..3
	metrics := shaderMetricLookup(perfStats)
	shaderMetrics := timelineShaderReportLookup(shaderReport)
	encoderMetricByIndex := make(map[int]*counter.EncoderCounterMetrics)
	for i := range encoderMetrics {
		metric := &encoderMetrics[i]
		if metric.Attribution == counter.CounterAttributionEncoder && metric.EncoderIndex >= 0 {
			encoderMetricByIndex[metric.EncoderIndex] = metric
		}
	}
	encoderOffsets := make(map[int]uint64)
	var fallbackStartNs uint64

	for dispatchOrdinal, d := range stats.Dispatches {
		name := d.FunctionName
		if name == "" {
			name = fmt.Sprintf("(pipeline_%d)", d.PipelineID)
		}
		durationNs := uint64(d.DurationUs) * 1000

		var startNs uint64
		if d.EncoderIndex >= 0 && d.EncoderIndex < len(timeline.Encoders) {
			enc := timeline.Encoders[d.EncoderIndex]
			startNs = enc.StartTime + encoderOffsets[d.EncoderIndex]
			encoderOffsets[d.EncoderIndex] += durationNs
		} else {
			startNs = fallbackStartNs
			fallbackStartNs += durationNs
		}

		pipeline := pipelineByID[d.PipelineID]
		metric := metrics.find(name, pipeline)
		shaderMetric := shaderMetrics.find(name, pipeline)
		encoderMetric := encoderMetricByIndex[d.EncoderIndex]
		simdGroups, simdGroupSharePct := capture.work(name, dispatchOrdinal)
		args := dispatchKernelArgs(d, pipeline, simdGroups, simdGroupSharePct, shaderMetric, metric, encoderMetric, sourceMapper)
		if recorded, ok := capture.dispatch(dispatchOrdinal); ok {
			addDispatchGeometryArgs(args, recorded.DispatchThreads)
			args["geometry_source"] = "capture dispatch record matched by dispatch order after exact count check"
			args["command_buffer_index"] = recorded.CommandBuffer
			args["capture_offset"] = recorded.CaptureOffset
			args["capture_structure_source"] = "capture dispatch record matched by dispatch order after exact count check"
		}

		info := KernelInfo{
			Name:      name,
			Encoder:   d.EncoderIndex,
			StartTime: startNs,
			EndTime:   startNs + durationNs,
			Duration:  durationNs,
			Args:      args,
		}
		timeline.Kernels = append(timeline.Kernels, info)
		if info.EndTime > timeline.EndTime {
			timeline.EndTime = info.EndTime
		}

		threadID := lanes.assign(startNs/1000, durationNs/1000)
		contained := false
		if d.EncoderIndex >= 0 && d.EncoderIndex < len(timeline.Encoders) {
			encoder := timeline.Encoders[d.EncoderIndex]
			if startNs >= encoder.StartTime && info.EndTime <= encoder.EndTime {
				contained = true
				if id, ok := timelineEncoderThreadID(timeline, d.EncoderIndex); ok {
					threadID = id
				}
			}
		}
		if contained {
			args["encoder_containment"] = "strict"
		} else {
			// The cumulative-time bucketing can place a dispatch on either
			// side of an encoder boundary. Keep the inferred index in args,
			// but leave it on a separate track rather than asserting a
			// malformed parent/child relationship in Perfetto.
			args["encoder_containment"] = "not_strictly_contained"
		}
		timeline.Events = append(timeline.Events, TimelineEvent{
			Name:      name,
			Category:  "kernel",
			Phase:     "X",
			Timestamp: startNs / 1000,
			Duration:  durationNs / 1000,
			ProcessID: 1,
			ThreadID:  threadID,
			Args:      args,
		})
	}
	return true
}

// timelineEncoderThreadID returns the lane used by an encoder event. Dispatches
// in that encoder use the same lane so Perfetto shows them inside its span.
func timelineEncoderThreadID(timeline *Timeline, index int) (int, bool) {
	if timeline == nil {
		return 0, false
	}
	for _, event := range timeline.Events {
		eventIndex, ok := timelineEventArgInt(event.Args, "index")
		if event.Category != "encoder" || !ok || eventIndex != index {
			continue
		}
		return event.ThreadID, true
	}
	return 0, false
}

func dispatchKernelArgs(d counter.DispatchInfo, p *counter.PipelineStats, simdGroups uint64, simdGroupSharePct float64, shader *gputrace.ShaderMetrics, hardware *counter.ShaderHardwareMetrics, encoderMetric *counter.EncoderCounterMetrics, sourceMapper *gputrace.ShaderSourceMapper) map[string]interface{} {
	args := map[string]interface{}{
		"dispatch_index": d.Index,
		"duration_us":    float64(d.DurationUs),
		"duration_ms":    float64(d.DurationUs) / 1000,
		"cumulative_us":  d.CumulativeUs,
		"encoder_index":  d.EncoderIndex,
		"pipeline_idx":   d.PipelineIndex,
		"pipeline_id":    d.PipelineID,
		"xcode_type":     "Compute",
		"xcode_view":     "Shaders",
		"timing_source":  "streamData gpuCommandInfoData",
	}
	if d.ProfilingSampleSharePct > 0 {
		args["profiling_sample_share_estimate_pct"] = d.ProfilingSampleSharePct
	}
	if simdGroups > 0 {
		args["simd_groups"] = simdGroups
	}
	if simdGroupSharePct > 0 {
		args["simd_group_share_pct"] = simdGroupSharePct
		args["simd_group_share_source"] = "captured dispatch geometry"
	}
	if d.SampleCount > 0 {
		args["gprwcntr_sample_count"] = d.SampleCount
		args["sampling_density"] = d.SamplingDensity
		args["sample_attribution_basis"] = "GPRWCNTR samples in a scaled cumulative-dispatch window"
	}
	if d.StartTicks != 0 || d.EndTicks != 0 {
		args["start_ticks"] = d.StartTicks
		args["end_ticks"] = d.EndTicks
		args["sample_window_basis"] = "cumulative dispatch time scaled over the first APSTimelineData command buffer"
		args["sample_timestamp_domain"] = "mach absolute ticks"
	}
	addPipelineCompilerArgs(args, p, "streamData pipelinePerformanceStatistics")
	args["pipeline_identity_source"] = "streamData pipelineStateInfoData"
	args["pipeline_identity_scope"] = "capture-local"
	if sourceMapper != nil {
		sourceName := d.FunctionName
		if sourceName == "" && p != nil {
			sourceName = p.FunctionName
		}
		if sourceFile, sourceLine := sourceMapper.SourceLocation(sourceName); sourceFile != "" {
			args["source_available"] = true
			args["source_file"] = sourceFile
			args["source_line"] = sourceLine
		}
	}
	if shader != nil {
		if shader.PercentOfTotal > 0 && args["simd_group_share_pct"] == nil {
			args["shader_share_pct"] = shader.PercentOfTotal
			args["shader_share_source"] = "shader report"
		}
		if shader.TotalThreadgroups > 0 {
			args["function_simd_groups"] = shader.TotalThreadgroups
			args["function_simd_groups_source"] = "shader report"
		}
		if shader.TotalDurationNs > 0 {
			args["shader_duration_ns"] = shader.TotalDurationNs
		}
	}
	if hardware != nil {
		if hardware.SIMDGroups > 0 && args["function_simd_groups"] == nil {
			args["function_simd_groups"] = hardware.SIMDGroups
			args["function_simd_groups_source"] = "shader hardware metrics"
		}
		if hardware.AllocatedRegs > 0 {
			args["allocated_registers"] = hardware.AllocatedRegs
		}
		if hardware.HighRegister > 0 {
			args["high_register"] = hardware.HighRegister
		}
		if hardware.SpilledBytes > 0 {
			args["spilled_bytes"] = hardware.SpilledBytes
		}
		if hardware.ALUUtilization > 0 {
			args["alu_utilization_pct"] = hardware.ALUUtilization
		}
	}
	if encoderMetric != nil {
		// Only fall back to a value that was actually read. A zero here is not
		// a measurement of zero, it is the absence of one: Xcode reports ALU
		// utilization of 1.59 to 3.35 percent for encoders where this stamped
		// 0.00 on every dispatch.
		if args["alu_utilization_pct"] == nil && encoderMetric.ALUUtilization != 0 {
			args["alu_utilization_pct"] = encoderMetric.ALUUtilization
			args["alu_utilization_source"] = "encoder counter fallback"
		}
	}
	return args
}

type timelineDispatchCaptureStats struct {
	byIndex    []uint64
	dispatches []tracepkg.AttributedDispatch
	byName     map[string]uint64
	total      uint64
}

func timelineDispatchCaptureEvidence(t *gputrace.Trace, stats *counter.StreamDataStats) timelineDispatchCaptureStats {
	out := timelineDispatchCaptureStats{byName: make(map[string]uint64)}
	if t == nil || stats == nil || len(stats.Dispatches) == 0 || len(t.CaptureData) == 0 {
		return out
	}
	dispatches, err := t.ParseAttributedDispatches()
	if err != nil {
		return out
	}
	if len(dispatches) != len(stats.Dispatches) {
		return out
	}
	out.byIndex = make([]uint64, len(dispatches))
	out.dispatches = dispatches
	for i, d := range dispatches {
		groups := d.SIMDGroups()
		out.byIndex[i] = groups
		out.total += groups
		name := stats.Dispatches[i].FunctionName
		if name == "" {
			name = fmt.Sprintf("(pipeline_%d)", stats.Dispatches[i].PipelineID)
		}
		out.byName[name] += groups
	}
	return out
}

func (s timelineDispatchCaptureStats) dispatch(index int) (tracepkg.AttributedDispatch, bool) {
	if index < 0 || index >= len(s.dispatches) {
		return tracepkg.AttributedDispatch{}, false
	}
	return s.dispatches[index], true
}

func addDispatchGeometryArgs(args map[string]interface{}, dispatch tracepkg.DispatchThreads) {
	args["grid_size"] = fmt.Sprintf("%d,%d,%d", dispatch.ThreadsX, dispatch.ThreadsY, dispatch.ThreadsZ)
	args["threadgroup_size"] = fmt.Sprintf("%d,%d,%d", dispatch.ThreadsPerGroupX, dispatch.ThreadsPerGroupY, dispatch.ThreadsPerGroupZ)
	if _, ok := args["simd_groups"]; !ok {
		args["simd_groups"] = dispatch.SIMDGroups()
	}
}

func (s timelineDispatchCaptureStats) work(name string, index int) (uint64, float64) {
	var dispatchGroups uint64
	if index >= 0 && index < len(s.byIndex) {
		dispatchGroups = s.byIndex[index]
	}
	functionGroups := s.byName[name]
	if functionGroups == 0 || s.total == 0 {
		return dispatchGroups, 0
	}
	return dispatchGroups, float64(functionGroups) / float64(s.total) * 100
}

type timelineShaderReport struct {
	byName map[string]*gputrace.ShaderMetrics
}

func timelineShaderReportLookup(report *gputrace.ShaderMetricsReport) timelineShaderReport {
	out := timelineShaderReport{byName: make(map[string]*gputrace.ShaderMetrics)}
	if report == nil {
		return out
	}
	for _, m := range report.Shaders {
		if m != nil && m.Name != "" {
			out.byName[m.Name] = m
		}
	}
	return out
}

func (m timelineShaderReport) find(name string, pipeline *counter.PipelineStats) *gputrace.ShaderMetrics {
	if metric := m.byName[name]; metric != nil {
		return metric
	}
	if pipeline != nil && pipeline.FunctionName != "" {
		return m.byName[pipeline.FunctionName]
	}
	return nil
}

type timelineShaderMetrics struct {
	byName    map[string]*counter.ShaderHardwareMetrics
	byAddress map[uint64]*counter.ShaderHardwareMetrics
}

func shaderMetricLookup(stats *gputrace.PerfCounterStats) timelineShaderMetrics {
	out := timelineShaderMetrics{
		byName:    make(map[string]*counter.ShaderHardwareMetrics),
		byAddress: make(map[uint64]*counter.ShaderHardwareMetrics),
	}
	if stats == nil {
		return out
	}
	for i := range stats.ShaderMetrics {
		m := &stats.ShaderMetrics[i]
		if m.ShaderName != "" {
			out.byName[m.ShaderName] = m
		}
		if m.PipelineState != 0 {
			out.byAddress[m.PipelineState] = m
		}
	}
	return out
}

func (m timelineShaderMetrics) find(name string, pipeline *counter.PipelineStats) *counter.ShaderHardwareMetrics {
	if pipeline != nil && pipeline.PipelineAddress != 0 {
		if metric := m.byAddress[pipeline.PipelineAddress]; metric != nil {
			return metric
		}
	}
	if metric := m.byName[name]; metric != nil {
		return metric
	}
	if pipeline != nil && pipeline.FunctionName != "" {
		return m.byName[pipeline.FunctionName]
	}
	return nil
}

func timelineTimingFromStats(stats *counter.StreamDataStats) *TimelineTiming {
	if stats == nil {
		return nil
	}
	timing := &TimelineTiming{
		EncoderSpanNs:         uint64(stats.TotalEncoderTimeUs) * 1000,
		DispatchSpanNs:        uint64(stats.TotalDispatchTimeUs) * 1000,
		EffectiveGPUTimeNs:    stats.EffectiveGPUTimeNs,
		CommandBufferActiveNs: stats.CommandBufferActiveNs,
		CommandBufferWallNs:   stats.CommandBufferWallNs,
		TimingSource:          stats.TimingSource,
	}
	if stats.Timeline != nil {
		timing.RestoreActiveNs = stats.Timeline.RestoreActiveNs
		timing.RestoreWallNs = stats.Timeline.RestoreWallNs
	}
	switch {
	case stats.EffectiveGPUTimeNs != nil:
		timing.DisplayDurationNs = *stats.EffectiveGPUTimeNs
		timing.DisplayDurationSource = "APSTimelineData ReplayerGPUTime"
	case stats.CommandBufferActiveNs > 0:
		timing.DisplayDurationNs = stats.CommandBufferActiveNs
		timing.DisplayDurationSource = "APSTimelineData command buffer active time"
	case stats.TotalEncoderTimeUs > 0:
		timing.DisplayDurationNs = uint64(stats.TotalEncoderTimeUs) * 1000
		timing.DisplayDurationSource = "encoderInfoData cumulative encoder span"
	}
	return timing
}

// enrichTimelineWithXcodeGPUTime optionally reads the total Xcode displays in
// Overview. It annotates the export but does not align the busy and wall
// timeline clocks.
func enrichTimelineWithXcodeGPUTime(tracePath string, timeline *Timeline, enabled bool) error {
	if !enabled {
		return nil
	}
	gpuTime, err := readXcodeGPUTime(tracePath)
	if err != nil {
		return fmt.Errorf("read Xcode GPU time: %w", err)
	}
	if gpuTime == 0 || timeline == nil {
		return nil
	}
	applyXcodeGPUTime(timeline, gpuTime)
	return nil
}

func applyXcodeGPUTime(timeline *Timeline, gpuTime uint64) {
	if timeline == nil || gpuTime == 0 {
		return
	}
	if timeline.Timing == nil {
		timeline.Timing = &TimelineTiming{}
	}
	timeline.Timing.EffectiveGPUTimeNs = &gpuTime
	timeline.Timing.DisplayDurationNs = gpuTime
	timeline.Timing.DisplayDurationSource = "GTMioTraceData.gpuTime (Xcode Overview GPU Time)"
	if timeline.Timing.TimingSource == "" {
		timeline.Timing.TimingSource = "GTMioTraceData.gpuTime (Xcode Overview GPU Time)"
	} else {
		timeline.Timing.TimingSource += "; GTMioTraceData.gpuTime (Xcode Overview GPU Time)"
	}
}

func annotateTimelineWithTimingMetrics(timeline *Timeline, metrics *gputrace.TimingMetrics) {
	source := timelineMetricsSource(metrics)
	if timeline == nil || source == "" {
		return
	}
	if timeline.Timing == nil {
		timeline.Timing = &TimelineTiming{}
	}
	timeline.Timing.EncoderTimingSource = source
	timeline.Timing.EncoderTimingApproximate = metrics.TimingApproximate
	if timeline.Timing.EncoderSpanNs == 0 && metrics.TotalDuration > 0 {
		timeline.Timing.EncoderSpanNs = uint64(metrics.TotalDuration)
	}
}

func addTimingMetricsEventArgs(args map[string]interface{}, metrics *gputrace.TimingMetrics) {
	source := timelineMetricsSource(metrics)
	if args == nil || source == "" {
		return
	}
	args["timing_source"] = source
	args["timing_approximate"] = metrics.TimingApproximate
	args["real_timing"] = !metrics.TimingApproximate
}

func timelineMetricsSource(metrics *gputrace.TimingMetrics) string {
	if metrics == nil {
		return ""
	}
	return fmt.Sprint(metrics.TimingSource)
}

// timelineDurationPhase returns the Chrome trace phase for a duration expressed
// in microseconds. A zero duration has no dur field in JSON, so it is an
// instant marker rather than a malformed complete event.
func timelineDurationPhase(durationUs uint64) string {
	if durationUs == 0 {
		return "i"
	}
	return "X"
}

// addRestoreEvents retains APSTimelineData Restore Timestamps on the wall
// clock. They describe replay restore activity, not GPU execution, and stay on
// a separate track from command buffers.
func addRestoreEvents(timeline *Timeline, info *counter.TimelineInfo) {
	if timeline == nil || info == nil || info.TimebaseNumer == 0 || info.TimebaseDenom == 0 {
		return
	}
	for _, interval := range info.RestoreTimestamps {
		if interval.EndTicks < interval.StartTicks {
			continue
		}
		var startNS uint64
		if interval.StartTicks > info.AbsoluteTime {
			startNS = (interval.StartTicks - info.AbsoluteTime) * info.TimebaseNumer / info.TimebaseDenom
		}
		durationNS := interval.DurationNs(info.TimebaseNumer, info.TimebaseDenom)
		phase := "i"
		if durationNS != 0 {
			phase = "X"
		}
		timeline.Events = append(timeline.Events, TimelineEvent{
			Name:        fmt.Sprintf("Restore #%d", interval.Index),
			Category:    "restore",
			Phase:       phase,
			Timestamp:   startNS / 1000,
			Duration:    durationNS / 1000,
			TimestampNS: startNS,
			DurationNS:  durationNS,
			ProcessID:   1,
			ThreadID:    2,
			Args: map[string]interface{}{
				"index":               interval.Index,
				"start_ticks":         interval.StartTicks,
				"end_ticks":           interval.EndTicks,
				"raw_start_offset_ns": startNS,
				"duration_ns":         durationNS,
				"timing_source":       "APSTimelineData Restore Timestamps",
				"clock_domain":        "wall",
				"evidence_kind":       "replay_restore_interval",
			},
		})
	}
}

// exportChromeTracing exports timeline in Chrome tracing format.
func exportChromeTracing(timeline *Timeline, outputPath string) error {
	return exportChromeTracingForClock(timeline, outputPath, timelineClockBusy)
}

func attachMLXSidecar(timeline *Timeline, tracePath, uuid, sidecarPath string) error {
	if uuid == "" {
		return fmt.Errorf("attach MLX sidecar: trace UUID is unavailable")
	}
	sidecar, err := mlxsemantic.Read(sidecarPath)
	if err != nil {
		return err
	}
	digest, err := mlxsemantic.Digest(tracePath)
	if err != nil {
		return err
	}
	counts := map[string]int{
		"dispatch":       timelineEventCount(timeline, "kernel"),
		"encoder":        timelineEventCount(timeline, "encoder"),
		"command_buffer": timelineEventCount(timeline, "command_buffer"),
	}
	report, err := sidecar.Analyze(mlxsemantic.Identity{UUID: uuid, ContentDigest: digest}, counts)
	if err != nil {
		return err
	}
	sidecarDigest, err := mlxsemantic.Digest(sidecarPath)
	if err != nil {
		return err
	}
	timeline.MLXSemantics = sidecar
	timeline.MLXSemanticReport = &report
	timeline.MLXSidecarDigest = sidecarDigest
	return nil
}

func timelineEventCount(timeline *Timeline, category string) int {
	count := 0
	for _, event := range timeline.Events {
		if event.Category == category {
			count++
		}
	}
	return count
}

func timelineEventAt(timeline *Timeline, category string, index int) (TimelineEvent, bool) {
	for _, event := range timeline.Events {
		if event.Category != category {
			continue
		}
		if index == 0 {
			return event, true
		}
		index--
	}
	return TimelineEvent{}, false
}

// exportPerfettoForClock writes one measured clock domain as native Perfetto
// protobuf. Chrome JSON remains available through --format chrome.
func exportPerfettoForClock(timeline *Timeline, outputPath string, clock timelineClock) error {
	return exportPerfettoForClockWithBudget(timeline, outputPath, clock, 0)
}

func exportPerfettoForClockWithBudget(timeline *Timeline, outputPath string, clock timelineClock, maxBytes int64) error {
	if timeline == nil {
		return fmt.Errorf("write perfetto trace: nil timeline")
	}
	w, closeOutput, err := createCommandOutput(outputPath)
	if err != nil {
		return err
	}
	if closeOutput != nil {
		defer closeOutput()
	}

	gpuName := timeline.MetalDeviceName
	if gpuName == "" {
		gpuName = "Apple GPU"
	}
	trace := &perfetto.Trace{
		Identity:    timeline.TraceUUID,
		ClockDomain: string(clock),
		GPUName:     gpuName,
		GPUModel:    timeline.MetalPluginName,
		Metadata: map[string]any{
			"schema":                                      "gputrace.perfetto/v1",
			"exporter_version":                            buildinfo.EffectiveVersion(),
			"exporter_commit":                             buildinfo.Commit,
			"exporter_build_date":                         buildinfo.Date,
			"clock_domain":                                string(clock),
			"clock_mapping":                               "none",
			"timing_quality":                              "measured",
			"environment_schema":                          "gputrace.environment/v1",
			"environment_os":                              runtime.GOOS,
			"environment_arch":                            runtime.GOARCH,
			"environment_exporter_runtime":                runtime.Version(),
			"environment_source":                          "Go runtime and gputrace metadata",
			"environment_parser":                          "gputrace.perfetto/v1",
			"environment_driver_availability":             "unavailable",
			"environment_mlx_runtime_availability":        "unavailable",
			"environment_workload_availability":           "unavailable",
			"environment_capability_catalog_availability": "unavailable",
			"capture_mode_availability":                   "unavailable: capture provenance is not recorded in this trace schema",
			"replay_mode_availability":                    "unavailable: replay provenance is not recorded in this trace schema",
			"counter_catalog_availability":                "unavailable: no clock-aligned counter catalog identity is retained",
			"counter_decoder_availability":                "unavailable: no clock-aligned decoded hardware counter series is retained",
			"raw_counter_artifact_availability":           "unavailable: no separate raw artifact identity and digest were verified",
			"perfetto_schema_revision":                    perfetto.SchemaRevision,
			"packet_family_gpu_info":                      true,
			"packet_family_gpu_render_stage_event":        true,
			"packet_family_track_event":                   true,
			"packet_family_gpu_counter_event":             len(timeline.CounterTracks) > 0,
			"unavailable_cpu_scheduling":                  "Metal trace contains no CPU scheduling evidence",
			"unavailable_syscalls":                        "Metal trace contains no syscall evidence",
			"unavailable_cpu_frequency":                   "Metal trace contains no CPU frequency evidence",
			"unavailable_system_memory":                   "Metal trace contains no system-memory evidence",
		},
	}
	if timeline.TraceUUID != "" {
		trace.Metadata["input_uuid"] = timeline.TraceUUID
		trace.Metadata["input_uuid_availability"] = "available"
	} else {
		trace.Metadata["input_uuid_availability"] = "unavailable"
	}
	if timeline.MLXSemantics != nil && timeline.MLXSemantics.Trace.ContentDigest != "" {
		trace.Metadata["input_content_digest"] = timeline.MLXSemantics.Trace.ContentDigest
		trace.Metadata["input_content_digest_availability"] = "available: verified strict sidecar"
	} else {
		trace.Metadata["input_content_digest_availability"] = "unavailable: exact tree hashing is performed only for strict sidecar validation"
	}
	if timeline.DeviceID != 0 {
		trace.Metadata["environment_device_id"] = timeline.DeviceID
		trace.Metadata["environment_device_id_availability"] = "available"
	} else {
		trace.Metadata["environment_device_id_availability"] = "unavailable"
	}
	if timeline.MetalDeviceName != "" {
		trace.Metadata["environment_device_name"] = timeline.MetalDeviceName
		trace.Metadata["environment_device_name_source"] = "streamData metalDeviceName"
		trace.Metadata["environment_device_name_availability"] = "available"
		trace.Metadata["environment_device_availability"] = "available: streamData archive identity"
	} else {
		trace.Metadata["environment_device_name_availability"] = "unavailable: streamData metalDeviceName is absent"
		trace.Metadata["environment_device_availability"] = "unavailable: streamData metalDeviceName is absent"
	}
	if timeline.MetalPluginName != "" {
		trace.Metadata["environment_metal_plugin_name"] = timeline.MetalPluginName
		trace.Metadata["environment_metal_plugin_source"] = "streamData metalPluginName"
		trace.Metadata["environment_metal_plugin_availability"] = "available"
	} else {
		trace.Metadata["environment_metal_plugin_availability"] = "unavailable: streamData metalPluginName is absent"
	}
	if timeline.GPUGeneration != nil {
		trace.Metadata["environment_gpu_generation"] = *timeline.GPUGeneration
		trace.Metadata["environment_gpu_generation_source"] = "streamData gpuGeneration"
		trace.Metadata["environment_gpu_generation_availability"] = "available"
	} else {
		trace.Metadata["environment_gpu_generation_availability"] = "unavailable: streamData gpuGeneration is absent"
	}
	for key, value := range perfettoStreamMetadataArgs(timeline.StreamMetadata) {
		trace.Metadata[key] = value
	}
	if timeline != nil {
		for key, value := range perfettoClockConversionArgs(timeline) {
			trace.Metadata[key] = value
		}
		inventory := timeline.EvidenceInventory
		if inventory == nil {
			value := timelineEvidenceInventory(timeline)
			inventory = &value
		}
		trace.Metadata["source_command_buffer_count"] = inventory.CommandBuffers
		trace.Metadata["source_restore_interval_count"] = inventory.RestoreIntervals
		trace.Metadata["source_encoder_count"] = inventory.Encoders
		trace.Metadata["source_dispatch_count"] = inventory.Dispatches
		trace.Metadata["source_untimed_dispatch_count"] = inventory.UntimedDispatches
		trace.Metadata["source_raw_profiler_stream_count"] = inventory.ProfilerStreams
		trace.Metadata["source_raw_profiler_record_count"] = inventory.ProfilerRecords
		trace.Metadata["projected_command_buffer_count"] = timelineEventCount(timeline, "command_buffer")
		trace.Metadata["projected_restore_interval_count"] = timelineEventCount(timeline, "restore")
		trace.Metadata["projected_encoder_count"] = timelineEventCount(timeline, "encoder")
		trace.Metadata["projected_dispatch_count"] = timelineEventCount(timeline, "kernel") + timelineEventCount(timeline, "dispatch")
		trace.Metadata["projected_untimed_dispatch_count"] = timelineUntimedDispatchCount(timeline)
		trace.Metadata["projected_raw_profiler_stream_count"] = timelineEventCount(timeline, "profiler_stream")
		trace.Metadata["projected_raw_profiler_record_count"] = timelineEventCount(timeline, "gprwcntr")
		trace.Metadata["raw_profiler_samples"] = timeline.RawProfilerSamples
		trace.Metadata["dispatch_count"] = len(timeline.Kernels)
		trace.Metadata["untimed_dispatch_count"] = timelineUntimedDispatchCount(timeline)
		trace.Metadata["encoder_count"] = len(timeline.Encoders)
		trace.Metadata["observed_cs_label_count"] = timeline.ObservedCSLabels
		trace.Metadata["unique_cs_label_count"] = timeline.UniqueCSLabels
		trace.Metadata["cs_label_semantics"] = "observed capture annotations; not dispatch or encoder instances"
		trace.Metadata["command_buffer_count"] = timelineEventCount(timeline, "command_buffer")
		if timeline.Timing != nil {
			for key, value := range perfettoTimingSummaryArgs(timeline.Timing) {
				trace.Metadata[key] = value
			}
			trace.Metadata["timing_source"] = timeline.Timing.TimingSource
			trace.Metadata["timing_approximate"] = timeline.Timing.EncoderTimingApproximate
			if timeline.Timing.EncoderTimingApproximate {
				trace.Metadata["timing_quality"] = "approximate"
			}
		} else {
			trace.Metadata["timing_source"] = "unavailable"
			trace.Metadata["timing_quality"] = "unavailable"
		}
		if timeline.MLXSemantics != nil {
			trace.Metadata["mlx_semantic_schema"] = timeline.MLXSemantics.Schema
			trace.Metadata["mlx_semantic_producer_name"] = timeline.MLXSemantics.Producer.Name
			trace.Metadata["mlx_semantic_producer_version"] = timeline.MLXSemantics.Producer.Version
			trace.Metadata["mlx_semantic_nodes"] = len(timeline.MLXSemantics.Nodes)
			trace.Metadata["mlx_semantic_links"] = len(timeline.MLXSemantics.Links)
			trace.Metadata["mlx_sidecar_digest"] = timeline.MLXSidecarDigest
			if report := timeline.MLXSemanticReport; report != nil {
				trace.Metadata["mlx_semantic_used_nodes"] = report.UsedNodes
				trace.Metadata["mlx_semantic_unused_nodes"] = report.UnusedNodes
				for kind, count := range report.MatchedTargets {
					trace.Metadata["mlx_semantic_matched_"+kind] = count
				}
				for kind, count := range report.UnmatchedTargets {
					trace.Metadata["mlx_semantic_unmatched_"+kind] = count
				}
			}
			projected, unprojected := mlxSemanticProjectionCounts(timeline)
			for kind, count := range projected {
				trace.Metadata["mlx_semantic_projected_"+kind] = count
			}
			for kind, count := range unprojected {
				trace.Metadata["mlx_semantic_unprojected_"+kind] = count
				trace.Metadata["mlx_semantic_unprojected_"+kind+"_reason"] = "target is outside the selected clock domain"
			}
		}
		if correlation := timeline.HostCorrelation; correlation != nil {
			trace.Metadata["host_correlation_schema"] = correlation.Schema
			trace.Metadata["host_correlation_run_id"] = correlation.RunID
			trace.Metadata["host_correlation_host_digest"] = correlation.HostDigest
			trace.Metadata["host_correlation_trace_digest"] = correlation.TraceDigest
			trace.Metadata["host_correlation_host_clock"] = correlation.HostClock
			trace.Metadata["host_correlation_gpu_clock"] = correlation.GPUClock
			trace.Metadata["host_correlation_bridge_digest"] = correlation.BridgeDigest
			trace.Metadata["host_correlation_max_error_ns"] = correlation.MaxErrorNS
			trace.Metadata["host_correlation_event_count"] = len(correlation.Events)
		}
		if live := timeline.LiveTiming; live != nil {
			trace.Metadata["live_timing_run_id"] = live.RunID
			trace.Metadata["live_timing_digest"] = live.ContentDigest
			trace.Metadata["live_timing_clock_samples"] = live.ClockSamples
			trace.Metadata["live_timing_command_buffers"] = live.CommandBuffers
			trace.Metadata["live_timing_projected_command_buffers"] = live.Projected
			trace.Metadata["live_timing_unmatched_command_buffers"] = live.Unmatched
		}
		trace.Metadata["unavailable_evidence_count"] = len(timeline.UnavailableEvidence)
		trace.Metadata["unattributed_counter_rows"] = len(timeline.UnattributedCounters)
		if len(timeline.UnattributedCounters) > 0 {
			trace.Metadata["counter_attribution"] = string(counter.CounterAttributionUnknown)
			trace.Metadata["counter_attribution_reason"] = "no capture-backed encoder identity"
		}
		for i, gap := range timeline.UnavailableEvidence {
			trace.Metadata[fmt.Sprintf("unavailable_evidence_%d_family", i)] = gap.Family
			trace.Metadata[fmt.Sprintf("unavailable_evidence_%d_reason", i)] = gap.Reason
		}
	}

	trackNames := make(map[[2]int]string)
	if clock == timelineClockBusy {
		trackNames[[2]int{1, 1}] = "Compute encoders and dispatches (cumulative busy)"
		trackNames[[2]int{1, 3}] = "Unattributed compute dispatches (cumulative busy)"
	} else if clock == timelineClockLive {
		trackNames[[2]int{2, 0}] = "Command buffers (original live GPU clock)"
	} else {
		trackNames[[2]int{1, 0}] = "Command buffers (wall clock; APSTimelineData)"
		trackNames[[2]int{1, 2}] = "Replay restore intervals (wall clock; APSTimelineData)"
	}
	for _, event := range timeline.Events {
		if event.Phase != "M" || event.Name != "thread_name" {
			continue
		}
		if name, ok := event.Args["name"].(string); ok && name != "" {
			trackNames[[2]int{event.ProcessID, event.ThreadID}] = name
		}
	}

	trackIDs := make(map[[2]int]uint64)
	for _, event := range timeline.Events {
		if event.Phase == "M" || event.Category == "kernel" {
			continue
		}
		key := [2]int{event.ProcessID, event.ThreadID}
		if trackIDs[key] != 0 {
			continue
		}
		identity := fmt.Sprintf("%s/%d/%d", clock, key[0], key[1])
		id := perfetto.TrackUUID("gputrace.timeline", identity)
		trackIDs[key] = id
		name := trackNames[key]
		if name == "" {
			name = fmt.Sprintf("%s lane %d", event.Category, event.ThreadID)
		}
		track := perfetto.Track{
			UUID:        id,
			Name:        name,
			Description: fmt.Sprintf("gputrace %s-domain evidence", clock),
		}
		if clock == timelineClockBusy && key == [2]int{1, 1} {
			track.ChildOrder = perfetto.ChildTrackOrderChronological
		}
		trace.Tracks = append(trace.Tracks, track)
	}

	for index, event := range timeline.Events {
		if event.Phase == "M" {
			continue
		}
		converted := perfetto.Event{
			ID:         uint64(index + 1),
			Name:       event.Name,
			Category:   event.Category,
			StartNS:    event.Timestamp * 1000,
			DurationNS: event.Duration * 1000,
			Args:       perfettoEventArgs(timeline, event, clock),
			Required:   event.Category == "encoder" || event.Category == "command_buffer" || event.Category == "restore" || event.Category == "live_command_buffer",
		}
		if event.TimestampNS != 0 || event.DurationNS != 0 {
			converted.StartNS = event.TimestampNS
			converted.DurationNS = event.DurationNS
		}
		if event.Category == "kernel" {
			converted.Kind = perfetto.EventGPUCompute
		} else {
			converted.TrackUUID = trackIDs[[2]int{event.ProcessID, event.ThreadID}]
			if event.Phase == "i" || event.Duration == 0 {
				converted.Kind = perfetto.EventInstant
			} else {
				converted.Kind = perfetto.EventSlice
			}
		}
		trace.Events = append(trace.Events, converted)
	}
	if includeMetalDispatchDetailProjection(timeline, clock, maxBytes) {
		tracks, events := appendMetalDispatchDetailProjection(trace, timeline, trackIDs[[2]int{1, 1}])
		trace.Metadata["presentation_dispatch_tracks"] = tracks
		trace.Metadata["presentation_dispatch_events"] = events
		trace.Metadata["presentation_dispatch_accounting"] = "duplicate detail projection; aggregate GPU totals use native gpu_slice only"
	} else if clock == timelineClockBusy {
		reason := "omitted from constrained export"
		if maxBytes == 0 {
			reason = "omitted because dispatch timing is unavailable"
		}
		trace.Metadata["presentation_dispatch_accounting"] = reason + "; aggregate GPU totals use native gpu_slice only"
	}
	appendMLXSemanticEvents(trace, timeline)
	appendHostCorrelationEvents(trace, timeline)
	appendEvidenceDetailEvents(trace, timeline)

	counterTracks := append([]CounterTrack(nil), timeline.CounterTracks...)
	sort.SliceStable(counterTracks, func(i, j int) bool { return counterTracks[i].Name < counterTracks[j].Name })
	for _, track := range counterTracks {
		// Presence and measured zero are different. A native counter series with
		// source-backed samples is retained even when every value is zero.
		if len(track.Samples) == 0 {
			continue
		}
		counter := perfetto.Counter{
			ID:          uint32(len(trace.Counters) + 1),
			Name:        track.Name,
			Description: track.Description,
		}
		for _, sample := range track.Samples {
			counter.Samples = append(counter.Samples, perfetto.CounterSample{
				TimestampNS: sample.Timestamp,
				Value:       sample.Value,
			})
		}
		trace.Counters = append(trace.Counters, counter)
	}

	receipt, err := perfetto.WriteWithOptions(w, trace, perfetto.WriteOptions{MaxBytes: maxBytes})
	if err != nil {
		return err
	}
	if receipt.EventsDropped > 0 || receipt.SamplesDropped > 0 {
		fmt.Fprintf(os.Stderr, "Perfetto output sampled: retained %d/%d events and %d/%d counter samples within %d logical bytes\n",
			receipt.EventsRetained, receipt.EventsConsidered,
			receipt.SamplesRetained, receipt.SamplesConsidered, receipt.LogicalBytes)
	}
	return nil
}

func perfettoStreamMetadataArgs(metadata *counter.StreamDataMetadata) map[string]any {
	args := map[string]any{
		"stream_data_metadata_availability": "unavailable: streamData archive metadata is absent",
	}
	if metadata == nil {
		return args
	}
	args["stream_data_metadata_availability"] = "available: raw streamData archive root fields"
	args["stream_data_metadata_source"] = "streamData keyed archive root"
	args["stream_data_profile_mode_semantics"] = "raw private enum values; meanings unverified"
	args["stream_data_capture_range_semantics"] = "raw private scalar values; units and relationship unverified"
	if metadata.Version != nil {
		args["stream_data_version"] = *metadata.Version
	}
	if metadata.UnixTimestamp != nil {
		args["stream_data_unix_timestamp"] = *metadata.UnixTimestamp
	}
	if metadata.TraceName != "" {
		args["stream_data_trace_name"] = metadata.TraceName
	}
	if metadata.ProfiledExecutionMode != nil {
		args["stream_data_profiled_execution_mode"] = *metadata.ProfiledExecutionMode
	}
	if metadata.ProfiledPerformanceState != nil {
		args["stream_data_profiled_performance_state"] = *metadata.ProfiledPerformanceState
	}
	if metadata.ProfiledProfilerMode != nil {
		args["stream_data_profiled_profiler_mode"] = *metadata.ProfiledProfilerMode
	}
	if metadata.CaptureRangeLocation != nil {
		args["stream_data_capture_range_location"] = *metadata.CaptureRangeLocation
	}
	if metadata.CaptureRangeLength != nil {
		args["stream_data_capture_range_length"] = *metadata.CaptureRangeLength
	}
	if metadata.DataSourceHasUnusedResources != nil {
		args["stream_data_has_unused_resources"] = *metadata.DataSourceHasUnusedResources
	}
	if metadata.SupportsSeparateAPSData != nil {
		args["stream_data_supports_separate_aps_data"] = *metadata.SupportsSeparateAPSData
	}
	if metadata.NumBlitCalls != nil {
		args["stream_data_num_blit_calls"] = *metadata.NumBlitCalls
	}
	appendStreamDataTableArgs(args, "command_buffer", metadata.Tables.CommandBuffers)
	appendStreamDataTableArgs(args, "encoder", metadata.Tables.Encoders)
	appendStreamDataTableArgs(args, "gpu_command", metadata.Tables.GPUCommands)
	appendStreamDataTableArgs(args, "pipeline", metadata.Tables.Pipelines)
	appendStreamDataTableArgs(args, "function", metadata.Tables.Functions)
	args["stream_data_family_count_semantics"] = "top-level archive array entries; not decoded sample counts"
	appendStreamDataFamilyArgs(args, "aps_data", metadata.Families.APSData)
	appendStreamDataFamilyArgs(args, "aps_timeline_data", metadata.Families.APSTimelineData)
	appendStreamDataFamilyArgs(args, "aps_counter_data", metadata.Families.APSCounterData)
	appendStreamDataFamilyArgs(args, "shader_profiler_data", metadata.Families.ShaderProfilerData)
	appendStreamDataFamilyArgs(args, "gpu_timeline_data", metadata.Families.GPUTimelineData)
	appendStreamDataFamilyArgs(args, "batch_id_filtered_counters_data", metadata.Families.BatchIDFilteredCountersData)
	args["stream_data_decoded_family_count_semantics"] = "NSData payload blobs recovered from top-level archive arrays; not records or samples"
	appendDecodedStreamDataFamilyArgs(args, "aps_data", metadata.Families.APSData, metadata.DecodedFamilies.APSData)
	appendDecodedStreamDataFamilyArgs(args, "aps_timeline_data", metadata.Families.APSTimelineData, metadata.DecodedFamilies.APSTimelineData)
	appendDecodedStreamDataFamilyArgs(args, "aps_counter_data", metadata.Families.APSCounterData, metadata.DecodedFamilies.APSCounterData)
	appendDecodedStreamDataFamilyArgs(args, "shader_profiler_data", metadata.Families.ShaderProfilerData, metadata.DecodedFamilies.ShaderProfilerData)
	appendDecodedStreamDataFamilyArgs(args, "gpu_timeline_data", metadata.Families.GPUTimelineData, metadata.DecodedFamilies.GPUTimelineData)
	appendDecodedStreamDataFamilyArgs(args, "batch_id_filtered_counters_data", metadata.Families.BatchIDFilteredCountersData, metadata.DecodedFamilies.BatchIDFilteredCountersData)
	appendStreamDataCounterDecodeArgs(args, metadata.CounterDecode)
	return args
}

func appendStreamDataCounterDecodeArgs(args map[string]any, decode *counter.StreamDataCounterDecode) {
	const prefix = "stream_data_counter_decode_"
	if decode == nil {
		args[prefix+"availability"] = "unavailable: no APSCounterData counter archive was decoded"
		return
	}
	args[prefix+"availability"] = "available"
	args[prefix+"count_semantics"] = "GPRWCNTR records and archive identity tables; no timeline clock mapping"
	args[prefix+"gprwcntr_blobs"] = decode.GPRWCNTRBlobs
	args[prefix+"decoded_samples"] = decode.DecodedSamples
	args[prefix+"attributed_samples"] = decode.AttributedSamples
	args[prefix+"machine_wide_samples"] = decode.MachineWideSamples
	args[prefix+"unattributed_samples"] = decode.UnattributedSamples
	args[prefix+"known_encoder_ids"] = decode.KnownEncoderIDs
	args[prefix+"encoder_aggregates"] = decode.EncoderAggregates
	args[prefix+"pass_column_groups"] = decode.PassColumnGroups
	args[prefix+"trace_id_rows"] = decode.TraceIDRows
	args[prefix+"stride_mismatch_blobs"] = decode.StrideMismatchBlobs
}

func appendDecodedStreamDataFamilyArgs(args map[string]any, name string, entries, blobs *int64) {
	prefix := "stream_data_" + name + "_"
	if entries == nil || blobs == nil {
		args[prefix+"decode_availability"] = "unavailable: archive array key is absent or malformed"
		return
	}
	args[prefix+"decoded_blob_count"] = *blobs
	if *blobs > *entries {
		args[prefix+"decode_availability"] = "inconsistent: decoded blob count exceeds archive entry count"
		return
	}
	args[prefix+"non_blob_entry_count"] = *entries - *blobs
	args[prefix+"decode_availability"] = "available"
}

func appendStreamDataFamilyArgs(args map[string]any, name string, count *int64) {
	prefix := "stream_data_" + name + "_"
	if count == nil {
		args[prefix+"availability"] = "unavailable: archive array key is absent or malformed"
		return
	}
	args[prefix+"entry_count"] = *count
	args[prefix+"availability"] = "available"
}

func appendStreamDataTableArgs(args map[string]any, name string, table *counter.StreamDataTable) {
	prefix := "stream_data_" + name + "_table_"
	if table == nil {
		args[prefix+"availability"] = "unavailable: archive data key is absent"
		return
	}
	args[prefix+"availability"] = "available"
	args[prefix+"bytes"] = table.Bytes
	if table.RecordSize == nil || table.RecordCount == nil || table.RemainderBytes == nil {
		args[prefix+"integrity"] = "unknown: record size is absent or invalid"
		return
	}
	args[prefix+"record_size"] = *table.RecordSize
	args[prefix+"record_count"] = *table.RecordCount
	args[prefix+"remainder_bytes"] = *table.RemainderBytes
	if *table.RemainderBytes == 0 {
		args[prefix+"integrity"] = "complete: byte length is divisible by record size"
	} else {
		args[prefix+"integrity"] = "incomplete: trailing bytes do not form a complete record"
	}
}

func perfettoClockConversionArgs(timeline *Timeline) map[string]any {
	args := map[string]any{
		"clock_conversion_domain":       "wall",
		"clock_conversion_availability": "unavailable: APSTimelineData absolute time and timebase are incomplete",
		"continuous_time_availability":  "unavailable: APSTimelineData Continuous Time is absent or zero",
		"pstate_availability":           "unavailable: APSTimelineData PState field is absent",
	}
	if timeline != nil && timeline.ContinuousTime != 0 {
		args["continuous_time"] = timeline.ContinuousTime
		args["continuous_time_domain"] = "raw APSTimelineData field; relationship to exported clocks is unverified"
		args["continuous_time_availability"] = "available: retained without conversion or clock mapping"
	}
	if timeline != nil && timeline.PState != nil {
		args["pstate"] = *timeline.PState
		args["pstate_source"] = "APSTimelineData PState"
		args["pstate_semantics"] = "raw replay performance-state value; unit and operating-point mapping are unverified"
		args["pstate_availability"] = "available: retained without interpreting frequency or voltage"
	}
	if timeline == nil || timeline.AbsoluteTime == 0 || timeline.TimebaseNumer == 0 || timeline.TimebaseDenom == 0 {
		return args
	}
	args["absolute_time"] = timeline.AbsoluteTime
	args["timebase_numer"] = timeline.TimebaseNumer
	args["timebase_denom"] = timeline.TimebaseDenom
	args["clock_conversion_source"] = "APSTimelineData Absolute Time and Timebase"
	args["clock_conversion_formula"] = "wall nanoseconds = (ticks - absolute_time) * timebase_numer / timebase_denom"
	args["clock_conversion_availability"] = "available: raw wall-domain conversion inputs; no busy-to-wall clock mapping"
	return args
}

func includeMetalDispatchDetailProjection(timeline *Timeline, clock timelineClock, maxBytes int64) bool {
	if timeline == nil || clock != timelineClockBusy || maxBytes != 0 {
		return false
	}
	for _, event := range timeline.Events {
		if event.Category == "kernel" && event.Duration > 0 {
			return true
		}
	}
	return false
}

// appendMetalDispatchDetailProjection adds one generic child track per encoder
// so stock Perfetto exposes kernel names without requiring a deep zoom into the
// packed native GPU queue. The native GPU events remain the accounting source;
// these slices are an explicitly marked presentation duplicate.
func appendMetalDispatchDetailProjection(trace *perfetto.Trace, timeline *Timeline, parent uint64) (trackCount, eventCount int) {
	if trace == nil || timeline == nil || parent == 0 {
		return 0, 0
	}
	byEncoder := make(map[int][]TimelineEvent)
	for _, event := range timeline.Events {
		if event.Category != "kernel" {
			continue
		}
		index, ok := timelineEventArgInt(event.Args, "encoder_index")
		if !ok || index < 0 {
			continue
		}
		byEncoder[index] = append(byEncoder[index], event)
	}
	indices := make([]int, 0, len(byEncoder))
	for index := range byEncoder {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	nextID := nextPerfettoEventID(trace)
	for _, index := range indices {
		events := byEncoder[index]
		functions := make(map[string]bool)
		var duration uint64
		for _, event := range events {
			functions[event.Name] = true
			duration += event.Duration
		}
		name := fmt.Sprintf("Encoder %d · %d dispatches · %.3f ms · %d functions", index, len(events), float64(duration)/1000, len(functions))
		if index < len(timeline.Encoders) {
			label := timeline.Encoders[index].Label
			if label != "" && label != fmt.Sprintf("encoder_%d", index) {
				name += " — " + label
			}
		}
		trackID := perfetto.TrackUUID("gputrace.encoder-dispatch-detail", strconv.Itoa(index))
		trace.Tracks = append(trace.Tracks, perfetto.Track{
			UUID:        trackID,
			ParentUUID:  parent,
			Name:        name,
			Description: "Presentation duplicate of native GPU dispatch slices; do not add to gpu_slice totals",
		})
		trackCount++
		for _, event := range events {
			args := perfettoEventArgs(timeline, event, timelineClockBusy)
			args["presentation_projection"] = "encoder_dispatch_detail"
			args["accounting_source"] = "native gpu_slice"
			kind := perfetto.EventSlice
			if event.Duration == 0 {
				kind = perfetto.EventInstant
			}
			trace.Events = append(trace.Events, perfetto.Event{
				ID:         nextID,
				TrackUUID:  trackID,
				Name:       event.Name,
				Category:   "kernel_detail",
				StartNS:    event.Timestamp * 1000,
				DurationNS: event.Duration * 1000,
				Kind:       kind,
				Args:       args,
			})
			nextID++
			eventCount++
		}
	}
	return trackCount, eventCount
}

func nextPerfettoEventID(trace *perfetto.Trace) uint64 {
	next := uint64(1)
	for _, event := range trace.Events {
		if event.ID >= next {
			next = event.ID + 1
		}
	}
	return next
}

func mlxSemanticProjectionCounts(timeline *Timeline) (projected, unprojected map[string]int) {
	projected = make(map[string]int)
	unprojected = make(map[string]int)
	if timeline == nil || timeline.MLXSemantics == nil {
		return projected, unprojected
	}
	for _, link := range timeline.MLXSemantics.Links {
		if _, ok := timelineSemanticTargetEvent(timeline, link.Target.Kind, link.Target.Index); ok {
			projected[link.Target.Kind]++
		} else {
			unprojected[link.Target.Kind]++
		}
	}
	return projected, unprojected
}

func timelineUntimedDispatchCount(timeline *Timeline) int {
	count := 0
	for _, event := range timeline.Events {
		if (event.Category == "kernel" || event.Category == "dispatch") && (event.Phase == "i" || event.Duration == 0) {
			count++
		}
	}
	return count
}

func perfettoEventArgs(timeline *Timeline, event TimelineEvent, clock timelineClock) map[string]any {
	args := make(map[string]any, len(event.Args)+3)
	for key, value := range event.Args {
		args[key] = value
	}
	args["clock_domain"] = string(clock)
	args["timing_quality"] = perfettoTimingQualityForClock(timeline, clock)
	if _, ok := args["timing_source"]; !ok && timeline != nil && timeline.Timing != nil && timeline.Timing.TimingSource != "" {
		args["timing_source"] = timeline.Timing.TimingSource
	}
	return args
}

func perfettoTimingQualityForClock(timeline *Timeline, clock timelineClock) string {
	if clock == timelineClockLive && timelineHasMeasuredClock(timeline, clock) {
		return "measured"
	}
	return perfettoTimingQuality(timeline)
}

func perfettoTimingQuality(timeline *Timeline) string {
	if timeline == nil || timeline.Timing == nil || timeline.Timing.TimingSource == "" || timeline.Timing.TimingSource == "unavailable" {
		return "unavailable"
	}
	if timeline.Timing.EncoderTimingApproximate {
		return "approximate"
	}
	return "measured"
}

func appendMLXSemanticEvents(trace *perfetto.Trace, timeline *Timeline) {
	if timeline.MLXSemantics == nil {
		return
	}
	trackIDs := make(map[string]uint64)
	for _, node := range timeline.MLXSemantics.Nodes {
		trackIDs[node.ID] = perfetto.TrackUUID("gputrace.mlx", node.ID)
	}
	for _, node := range timeline.MLXSemantics.Nodes {
		trace.Tracks = append(trace.Tracks, perfetto.Track{
			UUID:        trackIDs[node.ID],
			ParentUUID:  trackIDs[node.ParentID],
			Name:        node.Name,
			Description: "MLX " + node.Kind + " semantic evidence",
		})
	}
	nextID := nextPerfettoEventID(trace)
	for _, node := range timeline.MLXSemantics.Nodes {
		args := make(map[string]any, len(node.Attrs)+5)
		for key, value := range node.Attrs {
			args[key] = value
		}
		args["semantic_id"] = node.ID
		args["semantic_parent_id"] = node.ParentID
		args["semantic_kind"] = node.Kind
		args["join_basis"] = "sidecar-declaration"
		args["clock_domain"] = "none"
		args["timing_source"] = "MLX semantic sidecar declaration"
		args["timing_quality"] = "unavailable"
		trace.Events = append(trace.Events, perfetto.Event{
			ID:        nextID,
			TrackUUID: trackIDs[node.ID],
			Name:      node.Name,
			Category:  "mlx_semantic_node",
			Kind:      perfetto.EventInstant,
			Required:  true,
			Args:      args,
		})
		nextID++
	}
	for _, link := range timeline.MLXSemantics.Links {
		target, ok := timelineSemanticTargetEvent(timeline, link.Target.Kind, link.Target.Index)
		if !ok {
			continue // The target belongs to another measured clock domain.
		}
		node := mlxSemanticNode(timeline.MLXSemantics, link.SemanticID)
		args := make(map[string]any, len(node.Attrs)+4)
		for key, value := range node.Attrs {
			args[key] = value
		}
		args["semantic_id"] = node.ID
		args["semantic_kind"] = node.Kind
		args["semantic_link_id"] = link.ID
		args["join_basis"] = "sidecar-explicit-id"
		args["target_kind"] = link.Target.Kind
		args["target_index"] = link.Target.Index
		args["clock_domain"] = timeline.ClockDomain
		args["timing_quality"] = perfettoTimingQuality(timeline)
		if target.Args != nil {
			if source, ok := target.Args["timing_source"]; ok {
				args["timing_source"] = source
			}
		}
		if _, ok := args["timing_source"]; !ok && timeline.Timing != nil {
			args["timing_source"] = timeline.Timing.TimingSource
		}
		kind := perfetto.EventSlice
		if target.Duration == 0 {
			kind = perfetto.EventInstant
		}
		trace.Events = append(trace.Events, perfetto.Event{
			ID:         nextID,
			TrackUUID:  trackIDs[node.ID],
			Name:       node.Name,
			Category:   "mlx_semantic",
			StartNS:    target.Timestamp * 1000,
			DurationNS: target.Duration * 1000,
			Kind:       kind,
			Required:   true,
			Args:       args,
		})
		nextID++
	}
}

func appendEvidenceDetailEvents(trace *perfetto.Trace, timeline *Timeline) {
	if len(timeline.UnattributedCounters) == 0 && len(timeline.UnavailableEvidence) == 0 {
		return
	}
	trackID := perfetto.TrackUUID("gputrace.evidence", "details")
	trace.Tracks = append(trace.Tracks, perfetto.Track{
		UUID:        trackID,
		Name:        "Evidence details (untimed)",
		Description: "Source-backed evidence without a verified timeline coordinate",
	})
	nextID := nextPerfettoEventID(trace)
	for _, metric := range timeline.UnattributedCounters {
		label := metric.Label
		if label == "" {
			label = "(pipeline unknown)"
		}
		args := make(map[string]any, len(metric.Values)+7)
		for key, value := range metric.Values {
			args[key] = value
		}
		args["pipeline_label"] = label
		args["attribution"] = metric.Attribution
		args["metric_scope"] = "pipeline"
		args["source"] = metric.Source
		args["clock_domain"] = "none"
		args["timing_quality"] = "unavailable"
		args["attribution_reason"] = "no capture-backed encoder identity"
		trace.Events = append(trace.Events, perfetto.Event{
			ID:        nextID,
			TrackUUID: trackID,
			Name:      "Unattributed counter metrics: " + label,
			Category:  "counter_attribution",
			Kind:      perfetto.EventInstant,
			Required:  true,
			Args:      args,
		})
		nextID++
	}
	for _, gap := range timeline.UnavailableEvidence {
		trace.Events = append(trace.Events, perfetto.Event{
			ID:        nextID,
			TrackUUID: trackID,
			Name:      "Unavailable evidence: " + gap.Family,
			Category:  "evidence_gap",
			Kind:      perfetto.EventInstant,
			Required:  true,
			Args: map[string]any{
				"family":         gap.Family,
				"reason":         gap.Reason,
				"clock_domain":   "none",
				"timing_quality": "unavailable",
			},
		})
		nextID++
	}
}

func timelineSemanticTargetEvent(timeline *Timeline, kind string, index int) (TimelineEvent, bool) {
	switch kind {
	case "dispatch":
		if event, ok := timelineEventAt(timeline, "kernel", index); ok {
			return event, true
		}
		return timelineEventAt(timeline, "dispatch", index)
	case "encoder":
		return timelineEventAt(timeline, "encoder", index)
	case "command_buffer":
		return timelineEventAt(timeline, "command_buffer", index)
	default:
		return TimelineEvent{}, false
	}
}

func mlxSemanticNode(sidecar *mlxsemantic.Sidecar, id string) mlxsemantic.Node {
	for _, node := range sidecar.Nodes {
		if node.ID == id {
			return node
		}
	}
	return mlxsemantic.Node{}
}

// exportChromeTracingForClock exports one measured timestamp domain. Perfetto
// has one global time axis, so callers must not combine wall-clock command
// buffers and cumulative GPU-busy execution in the same export.
func exportChromeTracingForClock(timeline *Timeline, outputPath string, clock timelineClock) error {
	f, closeOutput, err := createCommandOutput(outputPath)
	if err != nil {
		return err
	}
	if closeOutput != nil {
		defer closeOutput()
	}

	processName := "Compute GPU execution (cumulative busy; no wall-clock anchor)"
	if clock == timelineClockWall {
		processName = "Command buffers (wall clock; APSTimelineData)"
	}

	// Add process and thread name metadata events.
	metadataEvents := []TimelineEvent{
		{
			Name:      "process_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  0,
			Args: map[string]interface{}{
				"name":                  processName,
				"gputrace_clock_domain": string(clock),
			},
		},
		{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  0,
			Args: map[string]interface{}{
				"name": "Command Buffers (wall clock)",
			},
		},
		{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  1,
			Args: map[string]interface{}{
				"name": "Compute encoders and dispatches (cumulative busy)",
			},
		},
		{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  2,
			Args: map[string]interface{}{
				"name": "Compute encoders and dispatches lane 1 (cumulative busy)",
			},
		},
		{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  3,
			Args: map[string]interface{}{
				"name": "Unattributed compute dispatches (cumulative busy)",
			},
		},
		{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  4,
			Args: map[string]interface{}{
				"name": "Unattributed compute dispatches lane 1 (cumulative busy)",
			},
		},
		{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  5,
			Args: map[string]interface{}{
				"name": "Unattributed compute dispatches lane 2 (cumulative busy)",
			},
		},
		{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  6,
			Args: map[string]interface{}{
				"name": "Unattributed compute dispatches lane 3 (cumulative busy)",
			},
		},
		{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  7,
			Args: map[string]interface{}{
				"name": "Raw profiler stream lane 0 (wall clock)",
			},
		},
		{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  8,
			Args: map[string]interface{}{
				"name": "Raw profiler stream lane 1 (wall clock)",
			},
		},
		{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  9,
			Args: map[string]interface{}{
				"name": "Raw profiler stream lane 2 (wall clock)",
			},
		},
		{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  10,
			Args: map[string]interface{}{
				"name": "Raw profiler stream lane 3 (wall clock)",
			},
		},
		{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  11,
			Args: map[string]interface{}{
				"name": "Raw profiler stream lane 4 (wall clock)",
			},
		},
		{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  12,
			Args: map[string]interface{}{
				"name": "Raw profiler stream lane 5 (wall clock)",
			},
		},
		{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  13,
			Args: map[string]interface{}{
				"name": "Raw profiler stream lane 6 (wall clock)",
			},
		},
		{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  14,
			Args: map[string]interface{}{
				"name": "Raw profiler stream lane 7 (wall clock)",
			},
		},
	}

	metadataEvents = append(metadataEvents,
		TimelineEvent{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  15,
			Args: map[string]interface{}{
				"name": "Xcode Parity / Provenance",
			},
		},
		TimelineEvent{
			Name:      "Xcode Metrics Coverage",
			Category:  "xcode_metrics",
			Phase:     "i",
			ProcessID: 1,
			ThreadID:  15,
			Args:      timelineCoverageArgs(timeline, clock),
		},
	)
	for _, metric := range timeline.UnattributedCounters {
		label := metric.Label
		if label == "" {
			label = "(pipeline unknown)"
		}
		args := make(map[string]interface{}, len(metric.Values)+4)
		for key, value := range metric.Values {
			args[key] = value
		}
		args["attribution"] = metric.Attribution
		args["metric_scope"] = "pipeline"
		args["pipeline_label"] = label
		args["source"] = metric.Source
		metadataEvents = append(metadataEvents, TimelineEvent{
			Name:      "Unattributed counter metrics: " + label,
			Category:  "counter_attribution",
			Phase:     "i",
			ProcessID: 1,
			ThreadID:  15,
			Args:      args,
		})
	}
	for _, gap := range timeline.UnavailableEvidence {
		metadataEvents = append(metadataEvents, TimelineEvent{
			Name:      "Unavailable evidence: " + gap.Family,
			Category:  "evidence_gap",
			Phase:     "i",
			ProcessID: 1,
			ThreadID:  15,
			Args: map[string]interface{}{
				"family": gap.Family,
				"reason": gap.Reason,
			},
		})
	}

	if timeline.Timing != nil {
		metadataEvents = append(metadataEvents,
			TimelineEvent{
				Name:      "Xcode Timing Summary",
				Category:  "xcode_timing",
				Phase:     "i",
				ProcessID: 1,
				ThreadID:  15,
				Args:      timelineTimingArgs(timeline.Timing),
			},
		)
	}

	// Add counter track metadata and events, grouped by Xcode category group when available.
	threadID := 16 // Start after GPRWCNTR lanes (7-14) and provenance lane (15).
	counterEvents := make([]TimelineEvent, 0)
	groupPIDs := make(map[string]int)
	nextGroupPID := 10

	for _, track := range timeline.CounterTracks {
		if !counterTrackHasSignal(track) {
			continue
		}
		pid := 1
		if len(track.XcodeGroups) > 0 && track.XcodeGroups[0] != "" {
			groupName := track.XcodeGroups[0]
			if existingPID, exists := groupPIDs[groupName]; exists {
				pid = existingPID
			} else {
				pid = nextGroupPID
				groupPIDs[groupName] = pid
				nextGroupPID++
				metadataEvents = append(metadataEvents, TimelineEvent{
					Name:      "process_name",
					Category:  "__metadata",
					Phase:     "M",
					ProcessID: pid,
					ThreadID:  0,
					Args: map[string]interface{}{
						"name": fmt.Sprintf("Counters: %s", groupName),
					},
				})
			}
		}

		// Add thread name for this counter track
		metadataEvents = append(metadataEvents, TimelineEvent{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: pid,
			ThreadID:  threadID,
			Args:      counterTrackMetadataArgs(track),
		})

		// Add counter samples as events
		for _, sample := range track.Samples {
			// Use counter events (C phase) for Chrome tracing
			counterEvent := TimelineEvent{
				Name:      track.Name,
				Category:  "counter",
				Phase:     "C",                     // Counter event
				Timestamp: sample.Timestamp / 1000, // Convert to microseconds
				ProcessID: pid,
				ThreadID:  threadID,
				Args: map[string]interface{}{
					track.Name: sample.Value,
				},
			}
			counterEvents = append(counterEvents, counterEvent)
		}

		threadID++
	}

	// Kernel events retain the tids assigned during timeline construction.
	// Strictly contained dispatches therefore share their encoder track; the
	// pipeline state remains available in each dispatch's arguments.
	allEvents := append(metadataEvents, timeline.Events...)
	allEvents = append(allEvents, counterEvents...)
	allEvents = timelineMetadataForActiveTracks(allEvents)

	// Chrome tracing format
	// Standard format: { "traceEvents": [ ... ] }
	// We omit displayTimeUnit and other legacy fields to maximize Perfetto compatibility.
	tracing := map[string]interface{}{
		"traceEvents": allEvents,
	}

	// Provenance goes under otherData, the Trace Event Format's sanctioned slot
	// for producer-specific metadata. It used to sit in gputrace_timing and
	// gputrace_xcode_metrics keys at the root, which strict readers are free to
	// reject: the format defines the top level, and those names are not in it.
	//
	// The content is worth keeping rather than dropping. display_duration_source
	// says which clock a duration came from, and absent_kernel_arg_fields names
	// the metrics we deliberately do not emit, so a reader can tell an absent
	// field from one we forgot.
	other := map[string]interface{}{}
	if timeline.Timing != nil {
		other["gputrace_timing"] = timelineTimingArgs(timeline.Timing)
	}
	other["gputrace_xcode_metrics"] = timelineCoverageArgs(timeline, clock)
	rawProfilerSamples := timeline != nil && timeline.RawProfilerSamples
	other["gputrace_clock_domain"] = timelineClockProvenanceWithRawSamples(clock, rawProfilerSamples)
	tracing["otherData"] = other

	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")
	return encoder.Encode(tracing)
}

// timelineMetadataForActiveTracks omits names for tracks absent from this
// clock-domain export. Perfetto otherwise renders empty tracks, which makes a
// busy-only trace look as though it also contains wall-clock data.
func timelineMetadataForActiveTracks(events []TimelineEvent) []TimelineEvent {
	active := make(map[[2]int]bool)
	for _, event := range events {
		if event.Phase != "M" {
			active[[2]int{event.ProcessID, event.ThreadID}] = true
		}
	}

	result := events[:0]
	for _, event := range events {
		if event.Phase == "M" && event.Name == "thread_name" && !active[[2]int{event.ProcessID, event.ThreadID}] {
			continue
		}
		result = append(result, event)
	}
	return result
}

func counterTrackMetadataArgs(track CounterTrack) map[string]interface{} {
	args := map[string]interface{}{
		"name": fmt.Sprintf("%s (%s)", track.Name, track.Unit),
	}
	if track.Description != "" {
		args["description"] = track.Description
		args["xcode_tooltip"] = track.Description
	}
	if len(track.XcodeGroups) > 0 {
		args["xcode_groups"] = track.XcodeGroups
	}
	if track.XcodeCatalogPath != "" {
		args["xcode_catalog_path"] = track.XcodeCatalogPath
	}
	return args
}

func timelineCoverageArgs(timeline *Timeline, clock timelineClock) map[string]interface{} {
	args := timelineXcodeMetricsArgs(timeline)
	rawProfilerSamples := timeline != nil && timeline.RawProfilerSamples
	for key, value := range timelineClockProvenanceWithRawSamples(clock, rawProfilerSamples) {
		args[key] = value
	}
	return args
}

func timelineClockProvenance(clock timelineClock) map[string]interface{} {
	return timelineClockProvenanceWithRawSamples(clock, true)
}

func timelineClockProvenanceWithRawSamples(clock timelineClock, rawProfilerSamples bool) map[string]interface{} {
	args := map[string]interface{}{
		"clock_domain":  string(clock),
		"clock_mapping": "none: trace records no measured correspondence between cumulative GPU-busy offsets and command-buffer wall time",
	}
	switch clock {
	case timelineClockBusy:
		args["included_categories"] = []string{"encoder", "kernel", "counter"}
		args["excluded_categories"] = []string{"command_buffer", "restore", "profiler_stream", "gprwcntr"}
		args["excluded_counter_series"] = "memory-side GTMioCounterData has scope=2/index=0 and a separate tick domain; it is not encoder-attributed or clock-aligned"
	case timelineClockWall:
		args["included_categories"] = []string{"command_buffer", "restore"}
		args["excluded_categories"] = []string{"encoder", "kernel", "counter"}
		if rawProfilerSamples {
			args["included_categories"] = append(args["included_categories"].([]string), "profiler_stream", "gprwcntr")
		} else {
			args["excluded_categories"] = append(args["excluded_categories"].([]string), "profiler_stream", "gprwcntr")
			args["raw_profiler_samples"] = "excluded by default: GPRWCNTR records and their aggregate profiler streams are not decoded counters or encoder intervals; use --include-raw-samples to inspect them"
		}
	case timelineClockLive:
		args["included_categories"] = []string{"live_command_buffer"}
		args["excluded_categories"] = []string{"encoder", "kernel", "counter", "command_buffer", "profiler_stream", "gprwcntr"}
		args["clock_mapping"] = "MTLDevice sampled CPU/GPU timestamp pairs retained by the original-execution timing sidecar"
	}
	return args
}

// zeroIsNotAReading names the kernel-event fields that a fallback may stamp
// with zero when nothing was read. For these, only a nonzero value counts as
// evidence that gputrace can produce the field. Every other field in the
// parity list comes from pipeline statistics, which are attached only when
// they were joined, so a zero there is a genuine measurement.
var zeroIsNotAReading = map[string]bool{
	"alu_utilization_pct": true,
}

func timelineXcodeMetricsArgs(timeline *Timeline) map[string]interface{} {
	args := map[string]interface{}{
		"kernel_events": 0,
	}
	if timeline == nil {
		return args
	}

	presentFields := make(map[string]bool)
	for _, ev := range timeline.Events {
		if (ev.Category != "kernel" && ev.Category != "dispatch") || ev.Args == nil {
			continue
		}
		args["kernel_events"] = args["kernel_events"].(int) + 1
		for _, field := range []string{
			"simd_groups",
			"simd_group_share_pct",
			"allocated_registers",
			"uniform_registers",
			"high_register",
			"spilled_bytes",
			"threadgroup_memory",
			"instruction_count",
			"alu_utilization_pct",
			"pipeline_id",
			"pipeline_state",
		} {
			// The counter-derived fields are present only if they carry a
			// nonzero value: a zero written by a fallback is not evidence
			// that gputrace read the counter, and counting it as presence
			// is how alu_utilization_pct came to be reported as a closed
			// gap while Xcode reported 1.59% for the same encoder.
			//
			// The compiler statistics are different. They are set only when
			// the pipeline stats were joined, and zero is a real reading
			// there: a kernel that spills nothing has spilled_bytes 0.
			v, ok := ev.Args[field]
			if !ok {
				continue
			}
			if zeroIsNotAReading[field] && isZeroMetricValue(v) {
				continue
			}
			presentFields[field] = true
		}
	}

	var present, absent []string
	for _, field := range []string{
		"simd_groups",
		"simd_group_share_pct",
		"allocated_registers",
		"uniform_registers",
		"high_register",
		"spilled_bytes",
		"threadgroup_memory",
		"instruction_count",
		"alu_utilization_pct",
		"pipeline_id",
		"pipeline_state",
	} {
		if presentFields[field] {
			present = append(present, field)
		} else {
			absent = append(absent, field)
		}
	}

	var tracks, emptyTracks []string
	for _, track := range timeline.CounterTracks {
		name := fmt.Sprintf("%s (%s)", track.Name, track.Unit)
		if len(track.Samples) == 0 || track.MaxValue == 0 {
			// An all-zero track carries no information about whether the
			// counter was read. Report it alongside the empty ones.
			emptyTracks = append(emptyTracks, name)
		} else {
			tracks = append(tracks, name)
		}
	}
	sort.Strings(tracks)
	sort.Strings(emptyTracks)

	args["kernel_arg_fields"] = present
	args["absent_kernel_arg_fields"] = absent
	args["binding_candidates"] = xcodeMetricBindingCandidates(absent)
	args["counter_tracks"] = tracks
	args["empty_counter_tracks"] = emptyTracks
	if len(timeline.UnattributedCounters) > 0 {
		labels := make([]string, 0, len(timeline.UnattributedCounters))
		for _, metric := range timeline.UnattributedCounters {
			if metric.Label != "" {
				labels = append(labels, metric.Label)
			}
		}
		sort.Strings(labels)
		args["counter_attribution"] = string(counter.CounterAttributionUnknown)
		args["unattributed_counter_rows"] = len(timeline.UnattributedCounters)
		args["unattributed_counter_labels"] = labels
		args["counter_attribution_reason"] = "no capture-backed encoder identity"
	}
	if len(timeline.UnavailableEvidence) > 0 {
		families := make([]string, 0, len(timeline.UnavailableEvidence))
		for _, gap := range timeline.UnavailableEvidence {
			families = append(families, gap.Family+": "+gap.Reason)
		}
		sort.Strings(families)
		args["unavailable_evidence"] = families
	}
	if report := timeline.MLXSemanticReport; report != nil {
		args["mlx_semantic_used_nodes"] = report.UsedNodes
		args["mlx_semantic_unused_nodes"] = report.UnusedNodes
		args["mlx_semantic_matched_targets"] = report.MatchedTargets
		args["mlx_semantic_unmatched_targets"] = report.UnmatchedTargets
	}
	if timeline.Timing != nil {
		args["display_duration_source"] = timeline.Timing.DisplayDurationSource
		args["timing_source"] = timeline.Timing.TimingSource
		if timeline.Timing.EncoderTimingSource != "" {
			args["encoder_timing_source"] = timeline.Timing.EncoderTimingSource
			args["encoder_timing_approximate"] = timeline.Timing.EncoderTimingApproximate
		}
		args["has_effective_gpu_time"] = timeline.Timing.EffectiveGPUTimeNs != nil
	} else {
		args["has_effective_gpu_time"] = false
	}
	return args
}

// isZeroMetricValue reports whether v is a numeric zero. Parity accounting uses
// it to tell "gputrace read this counter and it was zero" apart from "gputrace
// wrote a zero because it had nothing to write".
func isZeroMetricValue(v interface{}) bool {
	switch n := v.(type) {
	case float64:
		return n == 0
	case float32:
		return n == 0
	case int:
		return n == 0
	case int64:
		return n == 0
	case uint64:
		return n == 0
	default:
		return false
	}
}

func xcodeMetricBindingCandidates(fields []string) map[string]string {
	candidates := map[string]string{
		"high_register":       "GTMioShaderBinaryData.LiveRegisterForInstructionAtIndex",
		"alu_utilization_pct": "XRGPUAPSDataProcessor derived counters",
	}
	result := make(map[string]string)
	for _, field := range fields {
		if candidate := candidates[field]; candidate != "" {
			result[field] = candidate
		}
	}
	return result
}

func timelineTimingArgs(timing *TimelineTiming) map[string]interface{} {
	args := map[string]interface{}{
		"encoder_span_ns":               timing.EncoderSpanNs,
		"dispatch_span_ns":              timing.DispatchSpanNs,
		"command_buffer_active_time_ns": timing.CommandBufferActiveNs,
		"command_buffer_wall_time_ns":   timing.CommandBufferWallNs,
		"restore_active_time_ns":        timing.RestoreActiveNs,
		"restore_wall_time_ns":          timing.RestoreWallNs,
		"display_duration_ns":           timing.DisplayDurationNs,
		"display_duration_source":       timing.DisplayDurationSource,
		"timing_source":                 timing.TimingSource,
	}
	if timing.EncoderTimingSource != "" {
		args["encoder_timing_source"] = timing.EncoderTimingSource
		args["encoder_timing_approximate"] = timing.EncoderTimingApproximate
	}
	if timing.EffectiveGPUTimeNs != nil {
		args["effective_gpu_time_ns"] = *timing.EffectiveGPUTimeNs
	}
	return args
}

func perfettoTimingSummaryArgs(timing *TimelineTiming) map[string]interface{} {
	args := make(map[string]interface{})
	if timing == nil {
		return args
	}
	for key, value := range map[string]uint64{
		"encoder_span_ns":               timing.EncoderSpanNs,
		"dispatch_span_ns":              timing.DispatchSpanNs,
		"command_buffer_active_time_ns": timing.CommandBufferActiveNs,
		"command_buffer_wall_time_ns":   timing.CommandBufferWallNs,
		"restore_active_time_ns":        timing.RestoreActiveNs,
		"restore_wall_time_ns":          timing.RestoreWallNs,
		"display_duration_ns":           timing.DisplayDurationNs,
	} {
		if value != 0 {
			args[key] = value
		}
	}
	if timing.EffectiveGPUTimeNs != nil {
		args["effective_gpu_time_ns"] = *timing.EffectiveGPUTimeNs
	}
	if timing.DisplayDurationSource != "" {
		args["display_duration_source"] = timing.DisplayDurationSource
	}
	if timing.TimingSource != "" {
		args["timing_source"] = timing.TimingSource
	}
	if timing.EncoderTimingSource != "" {
		args["encoder_timing_source"] = timing.EncoderTimingSource
		args["encoder_timing_approximate"] = timing.EncoderTimingApproximate
	}
	return args
}

// exportTimelineJSON exports raw timeline data as JSON.
func exportTimelineJSON(timeline *Timeline, outputPath string) error {
	f, closeOutput, err := createCommandOutput(outputPath)
	if err != nil {
		return err
	}
	if closeOutput != nil {
		defer closeOutput()
	}

	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")
	return encoder.Encode(timeline)
}

// buildPerfettoTraceForHTML constructs the full trace object (traceEvents + otherData) for embedding in HTML viewer.
func buildPerfettoTraceForHTML(timeline *Timeline) map[string]interface{} {
	clock := timelineClockBusy
	if timeline != nil && timeline.ClockDomain != "" {
		clock = timelineClock(timeline.ClockDomain)
	}

	processName := "Compute GPU execution (cumulative busy; no wall-clock anchor)"
	if clock == timelineClockWall {
		processName = "Command buffers (wall clock; APSTimelineData)"
	}

	metadataEvents := []TimelineEvent{
		{
			Name:      "process_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  0,
			Args: map[string]interface{}{
				"name":                  processName,
				"gputrace_clock_domain": string(clock),
			},
		},
		{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  0,
			Args: map[string]interface{}{
				"name": "Command Buffers (wall clock)",
			},
		},
		{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  1,
			Args: map[string]interface{}{
				"name": "Compute encoders and dispatches (cumulative busy)",
			},
		},
		{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  2,
			Args: map[string]interface{}{
				"name": "Compute encoders and dispatches lane 1 (cumulative busy)",
			},
		},
		{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  3,
			Args: map[string]interface{}{
				"name": "Unattributed compute dispatches (cumulative busy)",
			},
		},
		{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  4,
			Args: map[string]interface{}{
				"name": "Unattributed compute dispatches lane 1 (cumulative busy)",
			},
		},
		{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  5,
			Args: map[string]interface{}{
				"name": "Unattributed compute dispatches lane 2 (cumulative busy)",
			},
		},
		{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  6,
			Args: map[string]interface{}{
				"name": "Unattributed compute dispatches lane 3 (cumulative busy)",
			},
		},
		{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  7,
			Args: map[string]interface{}{
				"name": "Raw profiler stream lane 0 (wall clock)",
			},
		},
		{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  8,
			Args: map[string]interface{}{
				"name": "Raw profiler stream lane 1 (wall clock)",
			},
		},
		{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  9,
			Args: map[string]interface{}{
				"name": "Raw profiler stream lane 2 (wall clock)",
			},
		},
		{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  10,
			Args: map[string]interface{}{
				"name": "Raw profiler stream lane 3 (wall clock)",
			},
		},
		{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  11,
			Args: map[string]interface{}{
				"name": "Raw profiler stream lane 4 (wall clock)",
			},
		},
		{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  12,
			Args: map[string]interface{}{
				"name": "Raw profiler stream lane 5 (wall clock)",
			},
		},
		{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  13,
			Args: map[string]interface{}{
				"name": "Raw profiler stream lane 6 (wall clock)",
			},
		},
		{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  14,
			Args: map[string]interface{}{
				"name": "Raw profiler stream lane 7 (wall clock)",
			},
		},
	}

	metadataEvents = append(metadataEvents,
		TimelineEvent{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  15,
			Args: map[string]interface{}{
				"name": "Xcode Parity / Provenance",
			},
		},
		TimelineEvent{
			Name:      "Xcode Metrics Coverage",
			Category:  "xcode_metrics",
			Phase:     "i",
			ProcessID: 1,
			ThreadID:  15,
			Args:      timelineCoverageArgs(timeline, clock),
		},
	)

	if timeline != nil && timeline.Timing != nil {
		metadataEvents = append(metadataEvents,
			TimelineEvent{
				Name:      "Xcode Timing Summary",
				Category:  "xcode_timing",
				Phase:     "i",
				ProcessID: 1,
				ThreadID:  15,
				Args:      timelineTimingArgs(timeline.Timing),
			},
		)
	}

	threadID := 16
	counterEvents := make([]TimelineEvent, 0)
	groupPIDs := make(map[string]int)
	nextGroupPID := 10

	if timeline != nil {
		for _, track := range timeline.CounterTracks {
			if !counterTrackHasSignal(track) {
				continue
			}
			pid := 1
			if len(track.XcodeGroups) > 0 && track.XcodeGroups[0] != "" {
				groupName := track.XcodeGroups[0]
				if existingPID, exists := groupPIDs[groupName]; exists {
					pid = existingPID
				} else {
					pid = nextGroupPID
					groupPIDs[groupName] = pid
					nextGroupPID++
					metadataEvents = append(metadataEvents, TimelineEvent{
						Name:      "process_name",
						Category:  "__metadata",
						Phase:     "M",
						ProcessID: pid,
						ThreadID:  0,
						Args: map[string]interface{}{
							"name": fmt.Sprintf("Counters: %s", groupName),
						},
					})
				}
			}

			metadataEvents = append(metadataEvents, TimelineEvent{
				Name:      "thread_name",
				Category:  "__metadata",
				Phase:     "M",
				ProcessID: pid,
				ThreadID:  threadID,
				Args:      counterTrackMetadataArgs(track),
			})

			for _, sample := range track.Samples {
				counterEvent := TimelineEvent{
					Name:      track.Name,
					Category:  "counter",
					Phase:     "C",
					Timestamp: sample.Timestamp / 1000,
					ProcessID: pid,
					ThreadID:  threadID,
					Args: map[string]interface{}{
						track.Name: sample.Value,
					},
				}
				counterEvents = append(counterEvents, counterEvent)
			}
			threadID++
		}
	}

	events := []TimelineEvent{}
	if timeline != nil {
		events = timeline.Events
	}
	allEvents := append(metadataEvents, events...)
	allEvents = append(allEvents, counterEvents...)
	allEvents = timelineMetadataForActiveTracks(allEvents)

	tracing := map[string]interface{}{
		"traceEvents": allEvents,
	}

	other := map[string]interface{}{}
	if timeline != nil && timeline.Timing != nil {
		other["gputrace_timing"] = timelineTimingArgs(timeline.Timing)
	}
	other["gputrace_xcode_metrics"] = timelineCoverageArgs(timeline, clock)
	rawProfilerSamples := timeline != nil && timeline.RawProfilerSamples
	other["gputrace_clock_domain"] = timelineClockProvenanceWithRawSamples(clock, rawProfilerSamples)
	tracing["otherData"] = other

	return tracing
}

// exportHTML exports an interactive standalone HTML timeline viewer.
func exportHTML(timeline *Timeline, outputPath string) error {
	f, closeOutput, err := createCommandOutput(outputPath)
	if err != nil {
		return err
	}
	if closeOutput != nil {
		defer closeOutput()
	}
	html, err := timelineHTML(timeline)
	if err != nil {
		return err
	}
	_, err = io.WriteString(f, html)
	return err
}

func timelineHTML(timeline *Timeline) (string, error) {
	timelineJSON, err := json.Marshal(timeline)
	if err != nil {
		return "", fmt.Errorf("marshal timeline: %w", err)
	}

	perfettoTrace := buildPerfettoTraceForHTML(timeline)
	perfettoJSON, err := json.Marshal(perfettoTrace)
	if err != nil {
		return "", fmt.Errorf("marshal perfetto trace: %w", err)
	}
	return generateInteractiveHTML(string(timelineJSON), string(perfettoJSON)), nil
}

func exportHTMLBoth(busy, wall *Timeline, outputPath string) error {
	busyHTML, err := timelineHTML(busy)
	if err != nil {
		return err
	}
	wallHTML, err := timelineHTML(wall)
	if err != nil {
		return err
	}
	busyJSON, err := json.Marshal(busyHTML)
	if err != nil {
		return fmt.Errorf("marshal busy viewer: %w", err)
	}
	wallJSON, err := json.Marshal(wallHTML)
	if err != nil {
		return fmt.Errorf("marshal wall viewer: %w", err)
	}

	f, closeOutput, err := createCommandOutput(outputPath)
	if err != nil {
		return err
	}
	if closeOutput != nil {
		defer closeOutput()
	}
	wallHeading := "Wall-clock scheduling — command buffers and encoder profiles"
	if wall != nil && wall.RawProfilerSamples {
		wallHeading += " plus raw profiler records"
	}

	html := fmt.Sprintf(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>GPU Timeline: busy and wall time</title>
  <style>
    * { box-sizing: border-box; }
    body { margin: 0; background: #1e1e1e; color: #d4d4d4; font: 14px -apple-system, BlinkMacSystemFont, sans-serif; }
    header { padding: 12px 16px; background: #252526; border-bottom: 1px solid #3e3e42; }
    h1 { margin: 0 0 6px; font-size: 18px; }
    p { margin: 0; color: #d7ba7d; }
    main { height: calc(100vh - 73px); display: grid; grid-template-columns: 1fr 1fr; gap: 1px; background: #3e3e42; }
    section { min-width: 0; display: flex; flex-direction: column; }
    h2 { margin: 0; padding: 8px 12px; background: #252526; font-size: 14px; }
    iframe { border: 0; flex: 1; width: 100%%; background: #1e1e1e; }
    @media (max-width: 1000px) { main { height: auto; min-height: calc(100vh - 73px); grid-template-columns: 1fr; grid-template-rows: 70vh 70vh; } }
  </style>
</head>
<body>
  <header>
    <h1>GPU Timeline: total information view</h1>
    <p>Busy and wall clocks are independently measured. They are displayed separately because this trace has no measured mapping between them.</p>
  </header>
  <main>
    <section><h2>GPU busy time — encoders, dispatches, and archive-backed counters</h2><iframe id="busy" title="GPU busy time"></iframe></section>
    <section><h2>%s</h2><iframe id="wall" title="Wall-clock scheduling"></iframe></section>
  </main>
  <script>
    document.getElementById("busy").srcdoc = %s;
    document.getElementById("wall").srcdoc = %s;
  </script>
</body>
</html>`, wallHeading, busyJSON, wallJSON)
	_, err = io.WriteString(f, html)
	return err
}

// runTimelineFromProfiler generates timeline from profiler-only traces (.gpuprofiler_raw without unsorted-capture).
func runTimelineFromProfiler(cmd *cobra.Command, tracePath string, opts *timelineOptions) error {
	if err := validateTimelineFormat(opts.format); err != nil {
		return err
	}
	if err := validateTimelineClock(opts.clock); err != nil {
		return err
	}
	if err := validateTimelineSQLOutput(opts); err != nil {
		return err
	}

	// Find .gpuprofiler_raw directory
	profilerDir := profilerraw.FindDir(tracePath)

	if profilerDir == "" {
		fmt.Fprint(os.Stderr, profileReplayHint(tracePath))
		return fmt.Errorf("no .gpuprofiler_raw directory found in %s (and unsorted-capture is missing)", tracePath)
	}

	// Parse streamData for timing info
	stats, err := counter.ParseStreamData(profilerDir, nil)
	if err != nil {
		return fmt.Errorf("parse streamData: %w", err)
	}
	counter.CorrelateDispatchSamples(stats)
	annotateDispatchProfilingSampleShares(stats, profilerDir)

	// Build timeline from profiler data
	timeline := buildTimelineFromProfilerData(tracePath, stats)
	if err := enrichTimelineWithXcodeGPUTime(tracePath, timeline, opts.xcodeGPUTime); err != nil {
		return err
	}
	if opts.sidecar != "" {
		return fmt.Errorf("attach MLX sidecar: profiler-only input has no capture UUID; use the self-contained profiled .gputrace")
	}
	if opts.hostCorrelation != "" {
		return fmt.Errorf("attach host correlation: profiler-only input has no trace tree for content identity")
	}
	if timeline.Timing == nil || timeline.Timing.EncoderTimingApproximate || timeline.Timing.TimingSource == "" || timeline.Timing.TimingSource == "unavailable" {
		fmt.Fprintln(os.Stderr, "Warning: trace lacks precise hardware timing data; encoder/dispatch durations are estimated.")
	}
	outputPath := timelineOutputPath(opts.format, opts.output)
	if err := validateTimelineViewerOptions(opts, outputPath); err != nil {
		return err
	}
	if opts.clock == timelineClockBoth {
		if err := exportTimelineBothWithRawSamples(timeline, opts.format, outputPath, opts.rawProfilerSamples); err != nil {
			return err
		}
		if opts.format != "text" || (outputPath != "" && !commandOutputPathIsStdout(outputPath)) {
			printTimelineExportStatus(outputPath, opts.format, true)
		}
		return nil
	}
	timeline = timelineForClockWithRawSamples(timeline, opts.clock, opts.rawProfilerSamples)
	if err := resolveTimelineNavigation(timeline, opts); err != nil {
		return err
	}

	// Export based on format
	switch opts.format {
	case "chrome":
		if err := exportChromeTracingForClock(timeline, outputPath, opts.clock); err != nil {
			return fmt.Errorf("failed to export Chrome tracing: %w", err)
		}
	case "perfetto":
		if err := exportPerfettoForClockWithBudget(timeline, outputPath, opts.clock, opts.maxOutputBytes); err != nil {
			return fmt.Errorf("failed to export Perfetto tracing: %w", err)
		}
		if err := writeTimelinePerfettoSQL(opts.sqlOutput); err != nil {
			return err
		}
	case "html":
		if err := exportHTML(timeline, outputPath); err != nil {
			return fmt.Errorf("failed to export HTML: %w", err)
		}
	case "json":
		if err := exportTimelineJSON(timeline, outputPath); err != nil {
			return fmt.Errorf("failed to export JSON: %w", err)
		}
	case "text":
		if err := exportTextTimeline(timeline, nil, outputPath); err != nil {
			return fmt.Errorf("failed to export text: %w", err)
		}
		if outputPath != "" && !commandOutputPathIsStdout(outputPath) {
			printTimelineExportStatus(outputPath, opts.format, true)
		}
		return nil
	default:
		return validateTimelineFormat(opts.format)
	}

	printTimelineExportStatus(outputPath, opts.format, true)
	return serveTimelinePerfetto(cmd, tracePath, outputPath, opts)
}

// buildTimelineFromProfilerData creates a Timeline from StreamDataStats.
func buildTimelineFromProfilerData(tracePath string, stats *counter.StreamDataStats) *Timeline {
	timeline := &Timeline{
		TracePath:  tracePath,
		Events:     make([]TimelineEvent, 0),
		Encoders:   make([]EncoderInfo, 0),
		Kernels:    make([]KernelInfo, 0),
		APICallseq: make([]APICall, 0),
		Timing:     timelineTimingFromStats(stats),
	}
	applyStreamIdentity(timeline, stats)
	if timeline.Timing == nil {
		timeline.Timing = &TimelineTiming{}
	}
	timeline.Timing.EncoderTimingSource = "profiler"
	timeline.Timing.EncoderTimingApproximate = false

	// Get timebase from timeline info
	var timebaseNumer, timebaseDenom uint64 = 125, 3 // Default
	var absoluteTime uint64
	if stats.Timeline != nil {
		timebaseNumer = stats.Timeline.TimebaseNumer
		timebaseDenom = stats.Timeline.TimebaseDenom
		absoluteTime = stats.Timeline.AbsoluteTime
		timeline.ContinuousTime = stats.Timeline.ContinuousTime
		if stats.Timeline.PState != nil {
			value := *stats.Timeline.PState
			timeline.PState = &value
		}
	}

	timeline.TimebaseNumer = timebaseNumer
	timeline.TimebaseDenom = timebaseDenom
	timeline.AbsoluteTime = absoluteTime
	addRestoreEvents(timeline, stats.Timeline)

	// Add command buffer events with real timing from APSTimelineData
	if stats.Timeline != nil && len(stats.Timeline.CommandBufferTimestamps) > 0 {
		// Real offsets, not a back-to-back accumulator. See the note on the
		// other command-buffer emitter: packing erased all idle time.
		for _, cb := range stats.Timeline.CommandBufferTimestamps {
			durationNs := cb.DurationNs(timebaseNumer, timebaseDenom)
			durationUs := durationNs / 1000
			var rawStartOffsetNs uint64
			if cb.StartTicks > absoluteTime {
				rawStartOffsetNs = (cb.StartTicks - absoluteTime) * timebaseNumer / timebaseDenom
			}

			event := TimelineEvent{
				Name:      fmt.Sprintf("CB#%d", cb.Index),
				Category:  "command_buffer",
				Phase:     timelineDurationPhase(durationUs),
				Timestamp: rawStartOffsetNs / 1000, // Convert to µs for Chrome format
				Duration:  durationUs,
				ProcessID: 1,
				ThreadID:  0,
				Args: map[string]interface{}{
					"index":               cb.Index,
					"start_ticks":         cb.StartTicks,
					"end_ticks":           cb.EndTicks,
					"raw_start_offset_ns": rawStartOffsetNs,
					"duration_us":         float64(durationNs) / 1000,
					"duration_ms":         float64(durationNs) / 1e6,
					"timing_source":       "APSTimelineData Command Buffer Timestamps",
					"real_timing":         true,
				},
			}
			timeline.Events = append(timeline.Events, event)
			if endNs := rawStartOffsetNs + durationNs; endNs > timeline.EndTime {
				timeline.EndTime = endNs
			}
		}
	}

	// Add encoder events from duration-only profiler timing.
	var currentTimeNs uint64
	for i, et := range stats.EncoderTimings {
		durationNs := uint64(et.DurationMicros) * 1000
		startTimeNs := currentTimeNs
		endTimeNs := startTimeNs + durationNs

		label := et.Label
		if label == "" {
			label = fmt.Sprintf("Encoder_%d", i)
		}

		encoderInfo := EncoderInfo{
			Index:     i,
			Label:     label,
			Type:      "compute",
			StartTime: startTimeNs,
			EndTime:   endTimeNs,
			Duration:  durationNs,
		}
		timeline.Encoders = append(timeline.Encoders, encoderInfo)
		if endTimeNs > timeline.EndTime {
			timeline.EndTime = endTimeNs
		}

		event := TimelineEvent{
			Name:      label,
			Category:  "encoder",
			Phase:     "X",
			Timestamp: startTimeNs / 1000,
			Duration:  durationNs / 1000,
			ProcessID: 1,
			ThreadID:  1 + (i % 2),
			Args: map[string]interface{}{
				"index":              i,
				"duration_ms":        float64(durationNs) / 1e6,
				"duration_us":        float64(durationNs) / 1e3,
				"timing_source":      "profiler",
				"timing_approximate": false,
				"real_timing":        true,
			},
		}
		timeline.Events = append(timeline.Events, event)

		currentTimeNs = endTimeNs
	}

	// Add raw profiler stream spans. These aggregates describe profiler input,
	// not the GPU encoder hierarchy.
	if stats.Timeline != nil && len(stats.Timeline.EncoderProfiles) > 0 {
		for _, ep := range stats.Timeline.EncoderProfiles {
			if ep.SampleCount == 0 || ep.StartTicks == 0 {
				continue
			}
			// Convert to nanoseconds relative to capture start
			var startNs uint64
			if ep.StartTicks > absoluteTime {
				startNs = (ep.StartTicks - absoluteTime) * timebaseNumer / timebaseDenom
			}

			event := TimelineEvent{
				Name:      fmt.Sprintf("Profiler stream %s #%d", ep.Source, ep.Index),
				Category:  "profiler_stream",
				Phase:     "X",
				Timestamp: startNs / 1000,
				Duration:  ep.DurationNs / 1000,
				ProcessID: 1,
				ThreadID:  3, // Separate track for encoder profiles
				Args: map[string]interface{}{
					"index":           ep.Index,
					"source":          ep.Source,
					"ring_buffer_idx": ep.RingBufferIndex,
					"sample_count":    ep.SampleCount,
					"duration_ns":     ep.DurationNs,
					"duration_us":     float64(ep.DurationNs) / 1000,
					"start_ticks":     ep.StartTicks,
					"end_ticks":       ep.EndTicks,
					"real_timing":     true,
				},
			}
			timeline.Events = append(timeline.Events, event)
		}
	}

	// Add kernel events from streamData dispatches.
	if !addDispatchKernelEvents(timeline, stats, timelineDispatchCaptureStats{}, nil, nil, nil, nil) {
		addEncoderKernelEvents(timeline, nil, nil, nil)
	}

	// Set timeline duration
	if timeline.EndTime > timeline.StartTime {
		timeline.Duration = timeline.EndTime - timeline.StartTime
	} else {
		timeline.Duration = uint64(stats.TotalTimeUs) * 1000
	}

	// Normalize timestamps to start at 0 (match Xcode visual baseline)
	// Find global minimum timestamp across all functional events (exclude metadata)
	var globalMinTs uint64 = ^uint64(0)
	foundAny := false

	for _, ev := range timeline.Events {
		if ev.Phase == "M" {
			continue
		}
		if ev.Timestamp < globalMinTs {
			globalMinTs = ev.Timestamp
			foundAny = true
		}
	}

	// Apply shift if we found any events
	if foundAny && globalMinTs > 0 {
		fmt.Fprintf(os.Stderr, "Normalizing timeline: shifting by -%d µs\n", globalMinTs)
		for i := range timeline.Events {
			ev := &timeline.Events[i]
			if ev.Phase == "M" {
				continue
			}
			if ev.Timestamp >= globalMinTs {
				ev.Timestamp -= globalMinTs
			} else {
				ev.Timestamp = 0
			}
		}
		// Also shift the bounds
		globalMinNs := globalMinTs * 1000
		if timeline.StartTime >= globalMinNs {
			timeline.StartTime -= globalMinNs
		} else {
			timeline.StartTime = 0
		}
		if timeline.EndTime >= globalMinNs {
			timeline.EndTime -= globalMinNs
		}
		for i := range timeline.Encoders {
			enc := &timeline.Encoders[i]
			if enc.StartTime >= globalMinNs {
				enc.StartTime -= globalMinNs
			} else {
				enc.StartTime = 0
			}
			if enc.EndTime >= globalMinNs {
				enc.EndTime -= globalMinNs
			}
		}
		for i := range timeline.Kernels {
			k := &timeline.Kernels[i]
			if k.StartTime >= globalMinNs {
				k.StartTime -= globalMinNs
			} else {
				k.StartTime = 0
			}
			if k.EndTime >= globalMinNs {
				k.EndTime -= globalMinNs
			}
		}
	}

	timeline.XcodeMetrics = timelineXcodeMetricsArgs(timeline)
	return timeline
}

// generateInteractiveHTML creates a standalone interactive HTML timeline viewer.
func generateInteractiveHTML(timelineJSON string, perfettoJSON ...string) string {
	perfJSON := "{}"
	if len(perfettoJSON) > 0 && perfettoJSON[0] != "" {
		perfJSON = perfettoJSON[0]
	}
	return `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>GPU Timeline Viewer</title>
    <style>
        * {
            margin: 0;
            padding: 0;
            box-sizing: border-box;
        }

        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
            background: #1e1e1e;
            color: #d4d4d4;
            overflow: hidden;
        }

        #container {
            width: 100vw;
            height: 100vh;
            display: flex;
            flex-direction: column;
        }

        #header {
            background: #252526;
            padding: 12px 20px;
            border-bottom: 1px solid #3e3e42;
            display: flex;
            justify-content: space-between;
            align-items: center;
        }

        #header h1 {
            font-size: 18px;
            font-weight: 600;
            color: #cccccc;
        }

        #controls {
            display: flex;
            gap: 15px;
            align-items: center;
        }

        .control-group {
            display: flex;
            gap: 8px;
            align-items: center;
        }

        button {
            background: #0e639c;
            color: white;
            border: none;
            padding: 6px 12px;
            border-radius: 3px;
            cursor: pointer;
            font-size: 13px;
            transition: background 0.2s;
        }

        button:hover {
            background: #1177bb;
        }

        button:active {
            background: #0d5a8f;
        }

        #stats {
            font-size: 12px;
            color: #858585;
            max-width: 620px;
            white-space: nowrap;
            overflow: hidden;
            text-overflow: ellipsis;
        }

        #main {
            flex: 1;
            display: flex;
            overflow: hidden;
        }

        #sidebar {
            width: 250px;
            background: #252526;
            border-right: 1px solid #3e3e42;
            overflow-y: auto;
            padding: 15px;
        }

        #timeline-container {
            flex: 1;
            position: relative;
            overflow: hidden;
        }

        #timeline-canvas {
            width: 100%;
            height: 100%;
            cursor: grab;
        }

        #timeline-canvas:active {
            cursor: grabbing;
        }

        .section-title {
            font-size: 13px;
            font-weight: 600;
            color: #cccccc;
            margin-bottom: 10px;
            text-transform: uppercase;
            letter-spacing: 0.5px;
        }

        .encoder-item {
            padding: 8px 10px;
            margin-bottom: 6px;
            background: #2d2d30;
            border-radius: 3px;
            cursor: pointer;
            border-left: 3px solid transparent;
            transition: all 0.2s;
        }

        .encoder-item:hover {
            background: #37373d;
        }

        .encoder-item.selected {
            background: #37373d;
            border-left-color: #0e639c;
        }

        .encoder-name {
            font-size: 13px;
            font-weight: 500;
            margin-bottom: 4px;
        }

        .encoder-stats {
            font-size: 11px;
            color: #858585;
        }

        .counter-group {
            margin: 10px 0 4px;
            font-size: 11px;
            text-transform: uppercase;
            letter-spacing: 0.5px;
            color: #8c8c8c;
        }

        .counter-track {
            padding: 6px 10px;
            margin-bottom: 4px;
            background: #2d2d30;
            border-radius: 3px;
            font-size: 12px;
        }

        .counter-name {
            font-weight: 500;
        }

        .counter-unit {
            color: #858585;
            margin-left: 4px;
        }

        #detail-panel {
            background: #2d2d30;
            border-radius: 3px;
            padding: 8px 10px;
            font-size: 12px;
            color: #d4d4d4;
        }

        .detail-row {
            display: flex;
            justify-content: space-between;
            gap: 12px;
            margin-bottom: 5px;
        }

        .detail-label {
            color: #858585;
        }

        .detail-value {
            color: #d4d4d4;
            text-align: right;
            overflow-wrap: anywhere;
        }

        #tooltip {
            position: absolute;
            background: rgba(30, 30, 30, 0.95);
            border: 1px solid #3e3e42;
            border-radius: 4px;
            padding: 10px 12px;
            font-size: 12px;
            pointer-events: none;
            z-index: 1000;
            display: none;
            box-shadow: 0 4px 12px rgba(0, 0, 0, 0.5);
            max-width: 300px;
        }

        #tooltip.visible {
            display: block;
        }

        .tooltip-title {
            font-weight: 600;
            margin-bottom: 6px;
            color: #cccccc;
            font-size: 13px;
        }

        .tooltip-row {
            display: flex;
            justify-content: space-between;
            margin-bottom: 3px;
            gap: 15px;
        }

        .tooltip-label {
            color: #858585;
        }

        .tooltip-value {
            color: #d4d4d4;
            font-weight: 500;
        }

        #cursor-overlay {
            position: absolute;
            top: 0;
            bottom: 0;
            width: 1px;
            background: rgba(255, 255, 255, 0.5);
            pointer-events: none;
            display: none;
        }

        #cursor-overlay.visible {
            display: block;
        }

        .counter-value-overlay {
            position: absolute;
            background: rgba(14, 99, 156, 0.9);
            color: white;
            padding: 3px 6px;
            border-radius: 3px;
            font-size: 11px;
            font-weight: 600;
            pointer-events: none;
            white-space: nowrap;
        }

        #warning-banner {
            background: #6a4f00;
            color: #fff8dc;
            padding: 6px 20px;
            font-size: 12px;
            display: none;
            align-items: center;
            gap: 8px;
            border-bottom: 1px solid #8b6b00;
        }

        #warning-banner.visible {
            display: flex;
        }

        .badge-estimated {
            background: #d7ba7d;
            color: #1e1e1e;
            font-size: 11px;
            font-weight: 600;
            padding: 2px 6px;
            border-radius: 3px;
            margin-left: 8px;
        }
    </style>
</head>
<body>
    <div id="container">
        <div id="header">
            <h1>GPU Timeline Viewer<span id="estimated-badge" class="badge-estimated" style="display:none;">Estimated Timing</span></h1>
            <div id="controls">
                <div class="control-group">
                    <button id="zoom-in">Zoom In (+)</button>
                    <button id="zoom-out">Zoom Out (-)</button>
                    <button id="reset-view">Reset View</button>
                </div>
                <div id="stats"></div>
            </div>
        </div>
        <div id="warning-banner">
            <strong>Warning:</strong> Precise hardware timing data is unavailable for this trace. Durations and execution spans are estimated.
        </div>

        <div id="main">
            <div id="sidebar">
                <div class="section-title">Encoders</div>
                <div id="encoder-list"></div>

                <div class="section-title" style="margin-top: 20px;">Counter Tracks</div>
                <div id="counter-list"></div>

                <div class="section-title" style="margin-top: 20px;">Selection</div>
                <div id="detail-panel"></div>
            </div>

            <div id="timeline-container">
                <canvas id="timeline-canvas"></canvas>
                <div id="cursor-overlay"></div>
                <div id="tooltip"></div>
            </div>
        </div>
    </div>

    <script>
        // Embedded timeline data
        const timelineData = ` + timelineJSON + `;
        const perfettoData = ` + perfJSON + `;

        // Timeline viewer state
        const state = {
            timeline: timelineData,
            zoom: 1.0,
            panX: 0,
            panY: 0,
            selectedEncoder: null,
            hoveredItem: null,
            isDragging: false,
            dragStartX: 0,
            dragStartY: 0,
            dragStartPanX: 0,
            dragStartPanY: 0,
        };

        // Constants
        const COLORS = {
            commandBuffer: '#d7ba7d',
            encoder: '#0e639c',
            encoderSelected: '#1177bb',
            kernel: '#6a9955',
            counter: '#ce9178',
            grid: '#3e3e42',
            text: '#d4d4d4',
            textDim: '#858585',
        };

        const LAYOUT = {
            headerHeight: 40,
            trackHeight: 60,
            trackPadding: 10,
            minBarHeight: 20,
            counterTrackHeight: 40,
        };

        // Get DOM elements
        const canvas = document.getElementById('timeline-canvas');
        const ctx = canvas.getContext('2d');
        const tooltip = document.getElementById('tooltip');
        const cursorOverlay = document.getElementById('cursor-overlay');
        const statsEl = document.getElementById('stats');
        const encoderList = document.getElementById('encoder-list');
        const counterList = document.getElementById('counter-list');
        const detailPanel = document.getElementById('detail-panel');

        // Initialize canvas
        function resizeCanvas() {
            const container = canvas.parentElement;
            canvas.width = container.clientWidth * window.devicePixelRatio;
            canvas.height = container.clientHeight * window.devicePixelRatio;
            canvas.style.width = container.clientWidth + 'px';
            canvas.style.height = container.clientHeight + 'px';
            ctx.scale(window.devicePixelRatio, window.devicePixelRatio);
            render();
        }

        window.addEventListener('resize', resizeCanvas);
        resizeCanvas();

        // Initialize sidebar
        function initSidebar() {
            // Populate encoder list
            encoderList.innerHTML = '';
            state.timeline.encoders.forEach((encoder, idx) => {
                const item = document.createElement('div');
                item.className = 'encoder-item';
                item.innerHTML = ` + "`" + `
                    <div class="encoder-name">${encoder.label || 'Encoder ' + idx}</div>
                    <div class="encoder-stats">
                        ${(encoder.duration / 1000000).toFixed(2)} ms
                    </div>
                ` + "`" + `;
                item.addEventListener('click', () => selectEncoder(idx));
                encoderList.appendChild(item);
            });

            // Populate counter list
            counterList.innerHTML = '';
            if (state.timeline.counter_tracks) {
                let previousGroup = '';
                state.timeline.counter_tracks.forEach(track => {
                    const group = (track.xcode_groups || []).join(' / ');
                    if (group && group !== previousGroup) {
                        const heading = document.createElement('div');
                        heading.className = 'counter-group';
                        heading.textContent = group;
                        counterList.appendChild(heading);
                        previousGroup = group;
                    }
                    const item = document.createElement('div');
                    item.className = 'counter-track';
                    item.innerHTML = ` + "`" + `
                        <span class="counter-name">${track.name}</span>
                        <span class="counter-unit">(${track.unit})</span>
                    ` + "`" + `;
                    counterList.appendChild(item);
                });
            }

            updateStats();
            updateDetails();

            // Check if timing data is estimated or unavailable
            const timing = state.timeline.timing || {};
            const isEstimated = timing.encoder_timing_approximate || !timing.timing_source || timing.timing_source === 'unavailable';
            if (isEstimated) {
                const banner = document.getElementById('warning-banner');
                if (banner) banner.classList.add('visible');
                const badge = document.getElementById('estimated-badge');
                if (badge) badge.style.display = 'inline-block';
            }
        }

        function updateStats() {
            const timing = state.timeline.timing || {};
            const clock = state.timeline.clock_domain || 'unclassified clock';
            statsEl.textContent = ` + "`" + `${clock} | ${state.timeline.encoders.length} encoders | Range ${formatNs(state.timeline.duration)} | Zoom ${(state.zoom * 100).toFixed(0)}%` + "`" + `;
            statsEl.title = timing.timing_source || 'selected timeline clock';
        }

        function formatNs(ns) {
            if (!ns) return '0 ns';
            if (ns >= 1000000000) return (ns / 1000000000).toFixed(2) + ' s';
            if (ns >= 1000000) return (ns / 1000000).toFixed(2) + ' ms';
            if (ns >= 1000) return (ns / 1000).toFixed(2) + ' µs';
            return ns + ' ns';
        }

        function selectEncoder(idx) {
            state.selectedEncoder = idx === state.selectedEncoder ? null : idx;

            // Update UI
            document.querySelectorAll('.encoder-item').forEach((item, i) => {
                item.classList.toggle('selected', i === state.selectedEncoder);
            });

            updateDetails();
            render();
        }

        function updateDetails() {
            const timing = state.timeline.timing || {};
            const metrics = state.timeline.xcode_metrics || {};
            const present = (metrics.kernel_arg_fields || []).join(', ') || 'none';
            const absent = (metrics.absent_kernel_arg_fields || []).join(', ') || 'none';
            const counters = (metrics.counter_tracks || []).join(', ') || 'none';
            const emptyCounters = (metrics.empty_counter_tracks || []).join(', ') || 'none';
            const bindings = Object.entries(metrics.binding_candidates || {}).map(([field, binding]) => field + ': ' + binding).join('<br>') || 'none';
            if (state.selectedEncoder !== null) {
                const encoder = state.timeline.encoders[state.selectedEncoder];
                detailPanel.innerHTML = ` + "`" + `
                    <div class="detail-row"><span class="detail-label">Name</span><span class="detail-value">${encoder.label || 'Encoder ' + state.selectedEncoder}</span></div>
                    <div class="detail-row"><span class="detail-label">Type</span><span class="detail-value">${encoder.type || 'compute'}</span></div>
                    <div class="detail-row"><span class="detail-label">Start</span><span class="detail-value">${formatNs(encoder.start_time)}</span></div>
                    <div class="detail-row"><span class="detail-label">Duration</span><span class="detail-value">${formatNs(encoder.duration)}</span></div>
                    <div class="detail-row"><span class="detail-label">Index</span><span class="detail-value">${encoder.index}</span></div>
                ` + "`" + `;
                return;
            }
            const timingStatus = (timing.encoder_timing_approximate || !timing.timing_source || timing.timing_source === 'unavailable')
                ? 'Estimated (approximate)'
                : 'Precise (hardware)';
            detailPanel.innerHTML = ` + "`" + `
                <div class="detail-row"><span class="detail-label">Timing Mode</span><span class="detail-value">${timingStatus}</span></div>
                <div class="detail-row"><span class="detail-label">Timing Source</span><span class="detail-value">${timing.timing_source || 'not available'}</span></div>
                <div class="detail-row"><span class="detail-label">CB active</span><span class="detail-value">${formatNs(timing.command_buffer_active_time_ns || 0)}</span></div>
                <div class="detail-row"><span class="detail-label">CB wall</span><span class="detail-value">${formatNs(timing.command_buffer_wall_time_ns || 0)}</span></div>
                <div class="detail-row"><span class="detail-label">Dispatch span</span><span class="detail-value">${formatNs(timing.dispatch_span_ns || 0)}</span></div>
                <div class="detail-row"><span class="detail-label">Effective GPU time</span><span class="detail-value">${metrics.has_effective_gpu_time ? 'available' : 'not available'}</span></div>
                <div class="detail-row"><span class="detail-label">Kernel fields</span><span class="detail-value">${present}</span></div>
                <div class="detail-row"><span class="detail-label">Absent fields</span><span class="detail-value">${absent}</span></div>
                <div class="detail-row"><span class="detail-label">Binding candidates</span><span class="detail-value">${bindings}</span></div>
                <div class="detail-row"><span class="detail-label">Counter tracks</span><span class="detail-value">${counters}</span></div>
                <div class="detail-row"><span class="detail-label">Empty tracks</span><span class="detail-value">${emptyCounters}</span></div>
            ` + "`" + `;
        }

        // Render timeline
        function render() {
            const w = canvas.width / window.devicePixelRatio;
            const h = canvas.height / window.devicePixelRatio;

            // Clear
            ctx.fillStyle = '#1e1e1e';
            ctx.fillRect(0, 0, w, h);

            // Calculate timeline scale
            const duration = state.timeline.duration;
            const startTime = state.timeline.start_time;
            const timeScale = (w - 100) / (duration / 1000000); // pixels per millisecond
            const scaledTimeScale = timeScale * state.zoom;

            // Draw time grid
            drawTimeGrid(w, h, scaledTimeScale, startTime, duration);

            let y = LAYOUT.headerHeight;
            const commandBuffers = state.timeline.events.filter(ev => ev.cat === 'command_buffer');
            if (commandBuffers.length) {
                drawEventLane('Command Buffers', commandBuffers, y, scaledTimeScale, startTime, COLORS.commandBuffer);
                y += LAYOUT.trackHeight;
            }

            const kernels = state.timeline.events.filter(ev => ev.cat === 'kernel');
            if (kernels.length) {
                drawEventLane('Shaders', kernels, y, scaledTimeScale, startTime, COLORS.kernel);
                y += LAYOUT.trackHeight;
            }

            // Draw encoder tracks
            state.timeline.encoders.forEach((encoder, idx) => {
                const isSelected = idx === state.selectedEncoder;
                const isHovered = state.hoveredItem?.type === 'encoder' && state.hoveredItem?.index === idx;

                drawEncoderTrack(encoder, idx, y, scaledTimeScale, startTime, isSelected, isHovered);
                y += LAYOUT.trackHeight;
            });

            // Draw counter tracks
            if (state.timeline.counter_tracks) {
                state.timeline.counter_tracks.forEach((track, idx) => {
                    drawCounterTrack(track, idx, y, scaledTimeScale, startTime);
                    y += LAYOUT.counterTrackHeight;
                });
            }
        }

        function drawTimeGrid(w, h, timeScale, startTime, duration) {
            ctx.strokeStyle = COLORS.grid;
            ctx.lineWidth = 1;
            ctx.font = '11px -apple-system, sans-serif';
            ctx.fillStyle = COLORS.textDim;

            // Calculate tick interval (aim for ticks every ~100px)
            const msPerPixel = 1 / timeScale;
            const msPerTick = Math.pow(10, Math.floor(Math.log10(msPerPixel * 100)));
            const pixelsPerTick = msPerTick * timeScale;

            // Draw vertical grid lines
            for (let ms = 0; ms < duration / 1000000; ms += msPerTick) {
                const x = 50 + ms * timeScale + state.panX;
                if (x < 50 || x > w) continue;

                ctx.beginPath();
                ctx.moveTo(x, 0);
                ctx.lineTo(x, h);
                ctx.stroke();

                ctx.fillText(ms.toFixed(1) + ' ms', x + 3, 15);
            }

            // Draw left margin
            ctx.fillStyle = '#252526';
            ctx.fillRect(0, 0, 50, h);
        }

        function drawEncoderTrack(encoder, idx, y, timeScale, startTime, isSelected, isHovered) {
            const w = canvas.width / window.devicePixelRatio;
            const relStart = (encoder.start_time - startTime) / 1000000;
            const duration = encoder.duration / 1000000;

            const x = 50 + relStart * timeScale + state.panX;
            const width = duration * timeScale;

            // Draw track background
            ctx.fillStyle = isHovered ? '#2d2d30' : '#252526';
            ctx.fillRect(50, y, w - 50, LAYOUT.trackHeight);

            // Draw encoder bar
            const barHeight = LAYOUT.minBarHeight;
            const barY = y + (LAYOUT.trackHeight - barHeight) / 2;

            ctx.fillStyle = isSelected ? COLORS.encoderSelected : COLORS.encoder;
            if (isHovered) {
                ctx.fillStyle = '#1a7fc1';
            }
            ctx.fillRect(x, barY, Math.max(width, 2), barHeight);

            // Draw label
            ctx.fillStyle = COLORS.text;
            ctx.font = '12px -apple-system, sans-serif';
            ctx.fillText(encoder.label || 'Encoder ' + idx, 5, y + LAYOUT.trackHeight / 2 + 4);

            // Draw duration text on bar if wide enough
            if (width > 60) {
                ctx.fillStyle = 'white';
                ctx.font = '11px -apple-system, sans-serif';
                const durationText = duration.toFixed(2) + ' ms';
                const textWidth = ctx.measureText(durationText).width;
                ctx.fillText(durationText, x + (width - textWidth) / 2, barY + barHeight / 2 + 4);
            }

            // Draw selection indicator
            if (isSelected) {
                ctx.strokeStyle = COLORS.encoderSelected;
                ctx.lineWidth = 2;
                ctx.strokeRect(x - 1, barY - 1, width + 2, barHeight + 2);
            }
        }

        function drawEventLane(label, events, y, timeScale, startTime, color) {
            const w = canvas.width / window.devicePixelRatio;
            ctx.fillStyle = '#252526';
            ctx.fillRect(50, y, w - 50, LAYOUT.trackHeight);

            ctx.fillStyle = COLORS.text;
            ctx.font = '12px -apple-system, sans-serif';
            ctx.fillText(label, 5, y + LAYOUT.trackHeight / 2 + 4);

            const barHeight = LAYOUT.minBarHeight;
            const barY = y + (LAYOUT.trackHeight - barHeight) / 2;
            ctx.fillStyle = color;
            events.forEach(event => {
                const relStart = ((event.ts * 1000) - startTime) / 1000000;
                const duration = (event.dur || 1) / 1000;
                const x = 50 + relStart * timeScale + state.panX;
                const width = Math.max(duration * timeScale, 2);
                if (x + width < 50 || x > w) return;
                ctx.fillRect(x, barY, width, barHeight);
            });
        }

        function drawCounterTrack(track, idx, y, timeScale, startTime) {
            const w = canvas.width / window.devicePixelRatio;

            // Draw track background
            ctx.fillStyle = '#1a1a1a';
            ctx.fillRect(50, y, w - 50, LAYOUT.counterTrackHeight);

            // Draw track label
            ctx.fillStyle = COLORS.textDim;
            ctx.font = '11px -apple-system, sans-serif';
            const group = (track.xcode_groups || []).join(' / ');
            ctx.fillText(group ? group + ': ' + track.name : track.name, 5, y + 12);

            if (!track.samples || track.samples.length === 0) return;

            // Draw counter line
            ctx.strokeStyle = COLORS.counter;
            ctx.lineWidth = 2;
            ctx.beginPath();

            const range = track.max_value - track.min_value;
            const heightScale = (LAYOUT.counterTrackHeight - 10) / (range || 1);

            let firstPoint = true;
            track.samples.forEach(sample => {
                const relTime = (sample.ts - startTime) / 1000000;
                const x = 50 + relTime * timeScale + state.panX;
                const normalizedValue = (sample.value - track.min_value) / (range || 1);
                const plotY = y + LAYOUT.counterTrackHeight - 5 - (normalizedValue * heightScale);

                if (firstPoint) {
                    ctx.moveTo(x, plotY);
                    firstPoint = false;
                } else {
                    ctx.lineTo(x, plotY);
                }
            });

            ctx.stroke();

            // Fill area under curve
            const firstSample = track.samples[0];
            const lastSample = track.samples[track.samples.length - 1];
            const firstX = 50 + ((firstSample.ts - startTime) / 1000000) * timeScale + state.panX;
            const lastX = 50 + ((lastSample.ts - startTime) / 1000000) * timeScale + state.panX;

            ctx.lineTo(lastX, y + LAYOUT.counterTrackHeight - 5);
            ctx.lineTo(firstX, y + LAYOUT.counterTrackHeight - 5);
            ctx.closePath();

            ctx.fillStyle = COLORS.counter + '20';
            ctx.fill();
        }

        // Hit testing
        function hitTest(x, y) {
            const timeScale = ((canvas.width / window.devicePixelRatio - 100) / (state.timeline.duration / 1000000)) * state.zoom;
            const startTime = state.timeline.start_time;

            let trackY = LAYOUT.headerHeight;
            const commandBuffers = state.timeline.events.filter(ev => ev.cat === 'command_buffer');
            if (commandBuffers.length) {
                const hit = hitTestEventLane('command_buffer', commandBuffers, x, y, trackY, timeScale, startTime);
                if (hit) return hit;
                trackY += LAYOUT.trackHeight;
            }
            const kernels = state.timeline.events.filter(ev => ev.cat === 'kernel');
            if (kernels.length) {
                const hit = hitTestEventLane('kernel', kernels, x, y, trackY, timeScale, startTime);
                if (hit) return hit;
                trackY += LAYOUT.trackHeight;
            }

            // Test encoders
            for (let i = 0; i < state.timeline.encoders.length; i++) {
                const encoder = state.timeline.encoders[i];
                const relStart = (encoder.start_time - startTime) / 1000000;
                const duration = encoder.duration / 1000000;
                const barX = 50 + relStart * timeScale + state.panX;
                const barWidth = duration * timeScale;
                const barHeight = LAYOUT.minBarHeight;
                const barY = trackY + (LAYOUT.trackHeight - barHeight) / 2;

                if (x >= barX && x <= barX + barWidth && y >= barY && y <= barY + barHeight) {
                    return { type: 'encoder', index: i, data: encoder };
                }

                trackY += LAYOUT.trackHeight;
            }

            return null;
        }

        function hitTestEventLane(type, events, x, y, trackY, timeScale, startTime) {
            const barHeight = LAYOUT.minBarHeight;
            const barY = trackY + (LAYOUT.trackHeight - barHeight) / 2;
            for (let i = events.length - 1; i >= 0; i--) {
                const event = events[i];
                const relStart = ((event.ts * 1000) - startTime) / 1000000;
                const duration = (event.dur || 1) / 1000;
                const barX = 50 + relStart * timeScale + state.panX;
                const barWidth = Math.max(duration * timeScale, 2);
                if (x >= barX && x <= barX + barWidth && y >= barY && y <= barY + barHeight) {
                    return { type, index: i, data: event };
                }
            }
            return null;
        }

        // Event handlers
        canvas.addEventListener('mousedown', (e) => {
            state.isDragging = true;
            state.dragStartX = e.clientX;
            state.dragStartY = e.clientY;
            state.dragStartPanX = state.panX;
            state.dragStartPanY = state.panY;
        });

        canvas.addEventListener('mousemove', (e) => {
            const rect = canvas.getBoundingClientRect();
            const x = e.clientX - rect.left;
            const y = e.clientY - rect.top;

            if (state.isDragging) {
                state.panX = state.dragStartPanX + (e.clientX - state.dragStartX);
                state.panY = state.dragStartPanY + (e.clientY - state.dragStartY);
                render();
            } else {
                // Update hover
                const hit = hitTest(x, y);
                state.hoveredItem = hit;

                if (hit) {
                    showTooltip(e.clientX, e.clientY, hit.data, hit.index);
                    cursorOverlay.style.left = x + 'px';
                    cursorOverlay.classList.add('visible');
                } else {
                    hideTooltip();
                    cursorOverlay.classList.remove('visible');
                }

                render();
            }
        });

        canvas.addEventListener('mouseup', () => {
            state.isDragging = false;
        });

        canvas.addEventListener('mouseleave', () => {
            state.isDragging = false;
            state.hoveredItem = null;
            hideTooltip();
            cursorOverlay.classList.remove('visible');
            render();
        });

        canvas.addEventListener('wheel', (e) => {
            e.preventDefault();
            const delta = e.deltaY > 0 ? 0.9 : 1.1;
            state.zoom = Math.max(0.1, Math.min(100, state.zoom * delta));
            updateStats();
            render();
        });

        canvas.addEventListener('click', (e) => {
            const rect = canvas.getBoundingClientRect();
            const x = e.clientX - rect.left;
            const y = e.clientY - rect.top;
            const hit = hitTest(x, y);

            if (hit && hit.type === 'encoder') {
                selectEncoder(hit.index);
            }
        });

        // Tooltip
        function showTooltip(x, y, data, index) {
            const isEvent = data.ts !== undefined;
            const title = data.label || data.name || ('Encoder ' + index);
            const duration = isEvent ? ((data.dur || 0) / 1000).toFixed(3) : (data.duration / 1000000).toFixed(3);
            const startTime = isEvent ? (data.ts / 1000).toFixed(3) : (data.start_time / 1000000).toFixed(3);
            const args = data.args || {};

            const timingMode = (args.timing_approximate || args.real_timing === false) ? 'Estimated' : (args.real_timing ? 'Precise (hardware)' : undefined);
            let html = '<div class="tooltip-title">' + escapeHTML(title) + '</div>' +
                tooltipRow('Duration', duration + ' ms') +
                tooltipRow('Start', startTime + ' ms') +
                tooltipRow('Type', args.xcode_type || data.type || data.cat || 'compute');

            const fields = [
                ['Timing Mode', timingMode],
                ['SIMD Group Share', args.simd_group_share_pct !== undefined ? args.simd_group_share_pct.toFixed(2) + '%' : undefined],
                ['Profiling Sample Share (estimate)', args.profiling_sample_share_estimate_pct !== undefined ? args.profiling_sample_share_estimate_pct.toFixed(2) + '%' : undefined],
                ['Pipeline', args.pipeline_state],
                ['Pipeline ID', args.pipeline_id],
                ['SIMD Groups', args.simd_groups],
                ['Registers', args.allocated_registers],
                ['High Register', args.high_register],
                ['Spilled Bytes', args.spilled_bytes],
                ['Instructions', args.instruction_count],
                ['ALU Instructions', args.alu_instruction_count],
                ['FP32 Instructions', args.fp32_instruction_count],
                ['FP16 Instructions', args.fp16_instruction_count],
                ['Samples', args.gprwcntr_sample_count],
                ['Source', args.timing_source],
            ];
            fields.forEach(([label, value]) => {
                if (value !== undefined && value !== null && value !== '') {
                    html += tooltipRow(label, value);
                }
            });
            tooltip.innerHTML = html;

            tooltip.style.left = (x + 15) + 'px';
            tooltip.style.top = (y + 15) + 'px';
            tooltip.classList.add('visible');
        }

        function tooltipRow(label, value) {
            return '<div class="tooltip-row"><span class="tooltip-label">' + escapeHTML(label) +
                ':</span><span class="tooltip-value">' + escapeHTML(String(value)) + '</span></div>';
        }

        function escapeHTML(value) {
            return String(value).replace(/[&<>"']/g, c => ({
                '&': '&amp;',
                '<': '&lt;',
                '>': '&gt;',
                '"': '&quot;',
                "'": '&#39;',
            }[c]));
        }

        function hideTooltip() {
            tooltip.classList.remove('visible');
        }

        // Controls
        document.getElementById('zoom-in').addEventListener('click', () => {
            state.zoom *= 1.5;
            updateStats();
            render();
        });

        document.getElementById('zoom-out').addEventListener('click', () => {
            state.zoom /= 1.5;
            updateStats();
            render();
        });

        document.getElementById('reset-view').addEventListener('click', () => {
            state.zoom = 1.0;
            state.panX = 0;
            state.panY = 0;
            updateStats();
            render();
        });

        // Keyboard shortcuts
        document.addEventListener('keydown', (e) => {
            if (e.key === '+' || e.key === '=') {
                state.zoom *= 1.2;
                updateStats();
                render();
            } else if (e.key === '-' || e.key === '_') {
                state.zoom /= 1.2;
                updateStats();
                render();
            } else if (e.key === 'r' || e.key === 'R') {
                state.zoom = 1.0;
                state.panX = 0;
                state.panY = 0;
                updateStats();
                render();
            }
        });

        // Initialize
        initSidebar();
        render();
    </script>
</body>
</html>
`
}

// lanePacker assigns overlapping slices to distinct horizontal lanes, the way
// Xcode's TrackLane does.
//
// The obvious alternative, spreading slices across lanes by index modulo the
// lane count, is what this replaces. It is worse than untidy: GPU dispatches
// run back to back, so scattering consecutive non-overlapping slices across
// four lanes renders four-way concurrency that never happened. A reader cannot
// tell that apart from real parallelism, which makes the picture wrong rather
// than merely ugly.
//
// Slices must be offered in nondecreasing start order; callers walk the
// dispatch and encoder lists, which are already ordered by cumulative time.
type lanePacker struct {
	base int      // ThreadID of lane 0
	ends []uint64 // end timestamp of the last slice placed in each lane
}

// newLanePacker returns a packer over n lanes numbered base..base+n-1.
func newLanePacker(base, n int) *lanePacker {
	return &lanePacker{base: base, ends: make([]uint64, n)}
}

// assign places a slice and returns its ThreadID. It picks the first lane free
// at start. When every lane is busy the slice goes to the lane that frees
// soonest: the legend names a fixed set of lanes, so growing past it would emit
// slices onto unnamed threads. Overlap beyond the lane count is therefore drawn
// stacked rather than dropped or hidden.
func (p *lanePacker) assign(start, duration uint64) int {
	if len(p.ends) == 0 {
		return p.base
	}
	end := start + duration
	best := 0
	for i, e := range p.ends {
		if e <= start {
			p.ends[i] = end
			return p.base + i
		}
		if e < p.ends[best] {
			best = i
		}
	}
	if end > p.ends[best] {
		p.ends[best] = end
	}
	return p.base + best
}

// counterTrackHasSignal reports whether a counter track carries any nonzero
// sample.
//
// The distinction that matters is between a counter that measured zero and a
// counter we never decoded. Nothing in the archive marks which is which, so a
// track that is zero throughout is treated as undecoded and dropped rather than
// published as a flat zero line.
func counterTrackHasSignal(track CounterTrack) bool {
	for _, s := range track.Samples {
		if s.Value != 0 {
			return true
		}
	}
	return false
}
