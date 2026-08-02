package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tmc/gputrace"
	"github.com/tmc/gputrace/internal/counter"
	"github.com/tmc/gputrace/internal/profilerraw"
	tracepkg "github.com/tmc/gputrace/internal/trace"
)

var timelineCmd = newTimelineCommand(&timelineOptions{
	format: "text",
	clock:  timelineClockBusy,
})

type timelineOptions struct {
	output             string
	format             string
	clock              timelineClock
	rawProfilerSamples bool
	xcodeGPUTime       bool
}

// timelineClock selects one measured timestamp domain. The profiler records
// command buffers in wall-clock ticks and encoders in cumulative GPU-busy
// offsets. Those domains have no measured correspondence.
type timelineClock string

const (
	timelineClockBusy timelineClock = "busy"
	timelineClockWall timelineClock = "wall"
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
  - perfetto: Perfetto format (ui.perfetto.dev) - same as chrome
  - html: Interactive standalone HTML timeline viewer
  - json: Raw timeline data in JSON format

Clock domains:
  - busy (default): cumulative GPU execution offsets for encoders, dispatches,
    and archive-backed counter tracks
  - wall: APSTimelineData command-buffer scheduling and encoder profiles
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
  gputrace timeline trace.gputrace --format perfetto --clock wall -o command-buffers.json

  # Add Xcode Overview GPU Time without aligning the two timeline clocks
  gputrace timeline trace.gputrace --format perfetto --xcode-gpu-time -o timeline.json

  # Inspect both domains without inventing a clock mapping
  gputrace timeline trace.gputrace --format html --clock both -o timeline.html

  # View in Chrome
  # 1. Open chrome://tracing in Chrome
  # 2. Click "Load" and select timeline.json
  # 3. Use WASD keys to navigate, mouse wheel to zoom

  # View in Perfetto UI (recommended)
  # 1. Open https://ui.perfetto.dev
  # 2. Drag and drop timeline.json or click "Open trace file"
  # 3. Use keyboard shortcuts: W/S zoom, A/D pan, F fit

  # Generate raw JSON for custom processing
  gputrace timeline trace.gputrace -o timeline.json --format json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTimeline(cmd, args, opts)
		},
	}

	cmd.Flags().StringVarP(&opts.output, "output", "o", opts.output, "Output file path (default: stdout for text, timeline.html for html, timeline.json otherwise)")
	cmd.Flags().StringVar(&opts.format, "format", opts.format, "Output format: chrome, perfetto, html, json, text")
	cmd.Flags().Var(&opts.clock, "clock", "Timeline clock domain: busy (default), wall, or both (separate views; no clock mapping)")
	cmd.Flags().BoolVar(&opts.rawProfilerSamples, "include-raw-samples", opts.rawProfilerSamples, "Include raw GPRWCNTR profiler records in wall-clock output (they are not decoded hardware counters)")
	cmd.Flags().BoolVar(&opts.xcodeGPUTime, "xcode-gpu-time", opts.xcodeGPUTime, "Read Xcode Overview GPU Time through GTShaderProfiler (Darwin only; runs a private-framework model pass)")
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
		return runTimelineFromProfiler(tracePath, opts)
	}

	// Generate timeline data
	timeline, err := generateTimeline(trace)
	if err != nil {
		return fmt.Errorf("failed to generate timeline: %w", err)
	}
	if err := enrichTimelineWithXcodeGPUTime(tracePath, timeline, opts.xcodeGPUTime); err != nil {
		return err
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
	if timeline.Timing == nil || timeline.Timing.EncoderTimingApproximate || timeline.Timing.TimingSource == "" || timeline.Timing.TimingSource == "unavailable" {
		fmt.Fprintf(cmd.ErrOrStderr(), "Warning: trace lacks precise hardware timing data; encoder/dispatch durations are estimated.\n")
	}

	outputPath := timelineOutputPath(opts.format, opts.output)
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

	// Export based on format
	switch opts.format {
	case "chrome", "perfetto":
		if err := exportChromeTracingForClock(timeline, outputPath, opts.clock); err != nil {
			return fmt.Errorf("failed to export Chrome/Perfetto tracing: %w", err)
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
	return nil
}

func validateTimelineClock(clock timelineClock) error {
	switch clock {
	case timelineClockBusy, timelineClockWall, timelineClockBoth:
		return nil
	default:
		return fmt.Errorf("invalid timeline clock %q (supported: busy, wall, both)", clock)
	}
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
	selected.ClockDomain = string(clock)
	selected.RawProfilerSamples = clock == timelineClockWall && rawProfilerSamples
	selected.Events = make([]TimelineEvent, 0, len(timeline.Events))
	for _, event := range timeline.Events {
		if timelineEventInClockWithRawSamples(event, clock, rawProfilerSamples) {
			selected.Events = append(selected.Events, event)
		}
	}
	if clock == timelineClockWall {
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
		return event.Category == "encoder" || event.Category == "kernel"
	case timelineClockWall:
		return event.Category == "command_buffer" || (rawProfilerSamples && (event.Category == "profiler_stream" || event.Category == "gprwcntr"))
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
	TracePath          string          `json:"trace_path,omitempty"`
	ClockDomain        string          `json:"clock_domain,omitempty"`
	RawProfilerSamples bool            `json:"raw_profiler_samples,omitempty"`
	StartTime          uint64          `json:"start_time"`
	EndTime            uint64          `json:"end_time"`
	Duration           uint64          `json:"duration"`
	Events             []TimelineEvent `json:"events"`
	Encoders           []EncoderInfo   `json:"encoders"`
	Kernels            []KernelInfo    `json:"kernels"`
	APICallseq         []APICall       `json:"api_callseq"`
	CounterTracks      []CounterTrack  `json:"counter_tracks,omitempty"`
	Timing             *TimelineTiming `json:"timing,omitempty"`
	XcodeMetrics       map[string]any  `json:"xcode_metrics,omitempty"`
	AbsoluteTime       uint64          `json:"absolute_time"`
	TimebaseNumer      uint64          `json:"timebase_numer"`
	TimebaseDenom      uint64          `json:"timebase_denom"`
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
	Name      string                 `json:"name"`
	Category  string                 `json:"cat,omitempty"`
	Phase     string                 `json:"ph"` // B, E, X, i, M
	Timestamp uint64                 `json:"ts"`
	Duration  uint64                 `json:"dur,omitempty"`
	ProcessID int                    `json:"pid"`
	ThreadID  int                    `json:"tid"`
	Args      map[string]interface{} `json:"args,omitempty"`
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
		encoderMetrics, _ = counter.PopulateEncoderMetricsFromPerfCounterStats(perfStats)
	}
	var shaderReport *gputrace.ShaderMetricsReport
	if profilerDir != "" {
		if report, err := extractSIMDBasedMetrics(trace, profilerDir); err == nil {
			shaderReport = report
		}
	}
	dispatchSIMD := timelineDispatchSIMDGroups(trace, streamStats)
	sourceMapper := gputrace.NewShaderSourceMapper()
	_ = sourceMapper.IndexShaderSources()
	_ = sourceMapper.IndexTraceBundleSources(trace.Path)
	if storeStats != nil && storeStats.Source != "" {
		_ = sourceMapper.IndexSource(filepath.Join(trace.Path, "store0"), storeStats.Source)
	}

	// Get real encoder labels from ParseComputeEncoders (primary source for labels)
	computeEncoders := trace.ParseComputeEncoders()

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
		if len(computeEncoders) > 0 {
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

				// Create timeline event for encoder
				event := TimelineEvent{
					Name:      enc.Label,
					Category:  "encoder",
					Phase:     "X",
					Timestamp: startTime / 1000, // Convert to microseconds
					Duration:  duration / 1000,
					ProcessID: 1,
					ThreadID:  1,
					Args: map[string]interface{}{
						"index":       i,
						"address":     fmt.Sprintf("0x%x", enc.Address),
						"duration_ms": float64(duration) / 1e6,
						"duration_us": float64(duration) / 1e3,
					},
				}
				addTimingMetricsEventArgs(event.Args, metrics)
				timeline.Events = append(timeline.Events, event)
			}
		} else {
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
					Phase:     "X",
					Timestamp: encoder.StartTimestamp / 1000,
					Duration:  encoder.DurationNs / 1000,
					ProcessID: 1,
					ThreadID:  1,
					Args: map[string]interface{}{
						"index":       i,
						"duration_ms": float64(encoder.DurationNs) / 1e6,
						"duration_us": float64(encoder.DurationNs) / 1e3,
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
	if !addDispatchKernelEvents(timeline, streamStats, dispatchSIMD, shaderReport, perfStats, encoderMetrics, sourceMapper) {
		addEncoderKernelEvents(timeline, trace, sourceMapper, storeStats)
	}

	// Add command buffer events - try to get real timing from APSTimelineData
	if streamStats != nil && streamStats.Timeline != nil && len(streamStats.Timeline.CommandBufferTimestamps) > 0 {
		// Use real CB timing from APSTimelineData
		ti := streamStats.Timeline
		timeline.AbsoluteTime = ti.AbsoluteTime
		timeline.TimebaseNumer = ti.TimebaseNumer
		timeline.TimebaseDenom = ti.TimebaseDenom

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

// generateCounterTracks creates the measured per-encoder counter tracks for
// the timeline. Pipeline instruction and register statistics remain event
// metadata: plotting a static compiler property as a stepped time series
// makes it look like a sampled hardware counter.
func generateCounterTracks(trace *gputrace.Trace, timeline *Timeline) []CounterTrack {
	tracks := make([]CounterTrack, 0)

	// Skip if no encoders (can't generate meaningful counter data)
	if len(timeline.Encoders) == 0 {
		return tracks
	}

	streamStats, _ := gputrace.ExtractPipelineStats(trace)
	if streamStats != nil {
		tracks = append(tracks, generateCounterTracksFromCounterArchive(streamStats.CounterArchive, timeline)...)
	}

	return applyXcodeCounterMetadata(tracks)
}

// generateCounterTracksFromCounterArchive records measured per-encoder GPU
// cycles and their archive-derived execution-cost share.
func generateCounterTracksFromCounterArchive(archive *counter.CounterArchive, timeline *Timeline) []CounterTrack {
	if archive == nil || timeline == nil {
		return nil
	}
	costs := archive.EncoderCosts()
	if len(costs) == 0 {
		return nil
	}
	cycles := CounterTrack{
		Name:        "GPU Cycles",
		Unit:        "cycles",
		Description: "Measured per encoder from APSCounterData GRC_GPU_CYCLES.",
	}
	cost := CounterTrack{
		Name:        "Execution Cost",
		Unit:        "%",
		Description: "Derived per encoder from APSCounterData GRC_GPU_CYCLES; not Xcode's exact Execution Cost column.",
	}
	var sparse int
	for _, c := range costs {
		if c.Ordinal < 0 || c.Ordinal >= len(timeline.Encoders) {
			continue
		}
		if c.Sparse() {
			sparse++
		}
		encoder := timeline.Encoders[c.Ordinal]
		appendCounterTrackSampleValue(&cycles, encoder, float64(c.GPUCycles))
		appendCounterTrackSampleValue(&cost, encoder, c.CostPercent)
	}
	if sparse > 0 {
		caveat := fmt.Sprintf(" %d encoder value(s) have fewer than 16 end-counter reads, the minimum for the archive's 16 replay groups; treat those values as low confidence.", sparse)
		cycles.Description += caveat
		cost.Description += caveat
	}
	calculateTrackStats(&cycles)
	calculateTrackStats(&cost)
	if len(cycles.Samples) == 0 || len(cost.Samples) == 0 {
		return nil
	}
	return []CounterTrack{cycles, cost}
}

// generateCounterTracksFromPerfData creates counter tracks from real performance counter data.
func generateCounterTracksFromPerfData(perfStats *gputrace.PerfCounterStats, streamStats *gputrace.StreamDataStats, encoderMetrics []counter.EncoderCounterMetrics, timeline *Timeline) []CounterTrack {
	tracks := make([]CounterTrack, 0)

	// Initialize counter tracks
	activeCoresTrack := CounterTrack{
		Name:    "Active Cores",
		Unit:    "count",
		Samples: make([]CounterSample, 0),
	}

	aluTrack := CounterTrack{
		Name:    "ALU Utilization",
		Unit:    "%",
		Samples: make([]CounterSample, 0),
	}

	bandwidthTrack := CounterTrack{
		Name:    "Bandwidth",
		Unit:    "GB/s",
		Samples: make([]CounterSample, 0),
	}

	throughputTrack := CounterTrack{
		Name:    "Instruction Throughput",
		Unit:    "%",
		Samples: make([]CounterSample, 0),
	}

	shaderLaunchLimiterTrack := CounterTrack{
		Name:    "Shader Launch Limiter",
		Unit:    "%",
		Samples: make([]CounterSample, 0),
	}

	// Create a map of shader name to hardware metrics
	shaderMetricsMap := make(map[string]*gputrace.ShaderHardwareMetrics)
	for i := range perfStats.ShaderMetrics {
		metric := &perfStats.ShaderMetrics[i]
		if metric.ShaderName != "" {
			shaderMetricsMap[metric.ShaderName] = metric
		}
	}
	encoderMetricsByIndex := make(map[int]*counter.EncoderCounterMetrics)
	encoderMetricsByLabel := make(map[string]*counter.EncoderCounterMetrics)
	for i := range encoderMetrics {
		m := &encoderMetrics[i]
		encoderMetricsByIndex[m.EncoderIndex] = m
		if m.EncoderLabel != "" {
			encoderMetricsByLabel[m.EncoderLabel] = m
		}
	}

	// Build map of function name to PipelineStats for instruction counts
	// This provides instruction counts by kernel name directly
	pipelineByName := make(map[string]*gputrace.PipelineStats)
	if streamStats != nil {
		// Index by function name for fuzzy matching
		for i, funcName := range streamStats.FunctionNames {
			if i < len(streamStats.Pipelines) {
				p := &streamStats.Pipelines[i]
				pipelineByName[funcName] = p
			}
		}
	}

	// Generate samples for each encoder period using actual hardware metrics
	for _, encoder := range timeline.Encoders {
		// Look up hardware metrics for this encoder
		var metrics *gputrace.ShaderHardwareMetrics
		if m, exists := shaderMetricsMap[encoder.Label]; exists {
			metrics = m
		}
		var encoderMetric *counter.EncoderCounterMetrics
		if m, exists := encoderMetricsByLabel[encoder.Label]; exists {
			encoderMetric = m
		} else if m, exists := encoderMetricsByIndex[encoder.Index]; exists {
			encoderMetric = m
		}

		// Calculate values from real hardware data.
		var activeCores float64
		var aluUtil float64
		var bandwidth float64
		var throughput float64
		var shaderLaunchLimiter float64

		if metrics != nil {
			// Use real hardware metrics
			aluUtil = metrics.ALUUtilization

			// Calculate bandwidth from memory bandwidth counter (convert bytes to GB/s)
			if metrics.MemoryBandwidth > 0 && encoder.Duration > 0 {
				durationSec := float64(encoder.Duration) / 1e9
				bandwidth = float64(metrics.MemoryBandwidth) / 1e9 / durationSec
			}

		}
		if encoderMetric != nil {
			if aluUtil == 0 {
				aluUtil = encoderMetric.ALUUtilization
			}
			if bandwidth == 0 {
				switch {
				case encoderMetric.DeviceMemoryBandwidthGBps > 0:
					bandwidth = encoderMetric.DeviceMemoryBandwidthGBps
				case encoderMetric.MemoryBandwidth > 0 && encoder.Duration > 0:
					durationSec := float64(encoder.Duration) / 1e9
					bandwidth = float64(encoderMetric.MemoryBandwidth) / 1e9 / durationSec
				}
			}
			if throughput == 0 {
				throughput = encoderMetric.InstructionThroughputUtil
			}
			if shaderLaunchLimiter == 0 {
				shaderLaunchLimiter = encoderMetric.ComputeShaderLaunchLimiter
			}
		}
		if metrics == nil && encoderMetric == nil {
			// No real data for this encoder - skip it (no synthetic data)
			continue
		}

		// Add samples at start and end of encoder execution. For source-backed
		// Xcode counters, zero is a meaningful value and should appear as a
		// flat track instead of being reported as unavailable.
		appendCounterTrackSample(&activeCoresTrack, encoder, activeCores)
		appendCounterTrackSampleValue(&aluTrack, encoder, aluUtil)
		appendCounterTrackSampleValue(&bandwidthTrack, encoder, bandwidth)
		appendCounterTrackSampleValue(&throughputTrack, encoder, throughput)
		appendCounterTrackSampleValue(&shaderLaunchLimiterTrack, encoder, shaderLaunchLimiter)
	}

	// Calculate statistics for each track
	calculateTrackStats(&activeCoresTrack)
	calculateTrackStats(&aluTrack)
	calculateTrackStats(&bandwidthTrack)
	calculateTrackStats(&throughputTrack)
	calculateTrackStats(&shaderLaunchLimiterTrack)

	tracks = append(tracks, activeCoresTrack, aluTrack, bandwidthTrack, throughputTrack, shaderLaunchLimiterTrack)

	// Add L1 Cache Miss Rate Track
	l1MissTrack := CounterTrack{
		Name:    "L1 Cache Miss Rate",
		Unit:    "%",
		Samples: make([]CounterSample, 0),
	}

	// Add Memory Read/Write Bandwidth Tracks
	memReadTrack := CounterTrack{
		Name:    "Memory Read BW",
		Unit:    "GB/s",
		Samples: make([]CounterSample, 0),
	}
	memWriteTrack := CounterTrack{
		Name:    "Memory Write BW",
		Unit:    "GB/s",
		Samples: make([]CounterSample, 0),
	}

	// Add Bottleneck Limiter Tracks
	computeLimiterTrack := CounterTrack{
		Name:    "Limiter: Compute",
		Unit:    "%",
		Samples: make([]CounterSample, 0),
	}
	memoryLimiterTrack := CounterTrack{
		Name:    "Limiter: Memory",
		Unit:    "%",
		Samples: make([]CounterSample, 0),
	}

	// Generate samples for new tracks - only for encoders with real data
	for _, encoder := range timeline.Encoders {
		metrics := shaderMetricsMap[encoder.Label]
		var encoderMetric *counter.EncoderCounterMetrics
		if m, exists := encoderMetricsByLabel[encoder.Label]; exists {
			encoderMetric = m
		} else if m, exists := encoderMetricsByIndex[encoder.Index]; exists {
			encoderMetric = m
		}
		if metrics == nil && encoderMetric == nil {
			// No real data for this encoder - skip it (no synthetic data)
			continue
		}

		var l1Miss float64
		var memRead, memWrite float64
		var compLimit, memLimit float64

		if metrics != nil {
			l1Miss = metrics.BufferL1MissRate
			durationSec := float64(encoder.Duration) / 1e9
			if durationSec > 0 {
				memRead = float64(metrics.BytesReadFromDeviceMemory) / 1e9 / durationSec
				memWrite = float64(metrics.BytesWrittenToDeviceMemory) / 1e9 / durationSec
			}
			compLimit = metrics.ComputeShaderLaunchLimiter
			memLimit = metrics.L1CacheLimiter + metrics.LastLevelCacheLimiter + metrics.TextureReadLimiter
		}
		if encoderMetric != nil {
			if l1Miss == 0 {
				l1Miss = encoderMetric.BufferL1MissRate
			}
			if memRead == 0 {
				if encoderMetric.GPUReadBandwidthGBps > 0 {
					memRead = encoderMetric.GPUReadBandwidthGBps
				} else if encoderMetric.BytesReadFromDeviceMemory > 0 && encoder.Duration > 0 {
					durationSec := float64(encoder.Duration) / 1e9
					memRead = float64(encoderMetric.BytesReadFromDeviceMemory) / 1e9 / durationSec
				}
			}
			if memWrite == 0 {
				if encoderMetric.GPUWriteBandwidthGBps > 0 {
					memWrite = encoderMetric.GPUWriteBandwidthGBps
				} else if encoderMetric.BytesWrittenToDeviceMemory > 0 && encoder.Duration > 0 {
					durationSec := float64(encoder.Duration) / 1e9
					memWrite = float64(encoderMetric.BytesWrittenToDeviceMemory) / 1e9 / durationSec
				}
			}
			if compLimit == 0 {
				compLimit = encoderMetric.ComputeShaderLaunchLimiter
			}
			if memLimit == 0 {
				memLimit = encoderMetric.L1CacheLimiter + encoderMetric.LastLevelCacheLimiter + encoderMetric.TextureReadLimiter
			}
		}

		appendCounterTrackSampleValue(&l1MissTrack, encoder, l1Miss)
		appendCounterTrackSampleValue(&memReadTrack, encoder, memRead)
		appendCounterTrackSampleValue(&memWriteTrack, encoder, memWrite)
		appendCounterTrackSampleValue(&computeLimiterTrack, encoder, compLimit)
		appendCounterTrackSampleValue(&memoryLimiterTrack, encoder, memLimit)
	}

	calculateTrackStats(&l1MissTrack)
	calculateTrackStats(&memReadTrack)
	calculateTrackStats(&memWriteTrack)
	calculateTrackStats(&computeLimiterTrack)
	calculateTrackStats(&memoryLimiterTrack)

	tracks = append(tracks, l1MissTrack, memReadTrack, memWriteTrack, computeLimiterTrack, memoryLimiterTrack)

	// Add Instruction Count Tracks from PipelineStats/streamData
	instructionTrack := CounterTrack{
		Name:    "Total Instructions",
		Unit:    "count",
		Samples: make([]CounterSample, 0),
	}
	aluInstrTrack := CounterTrack{
		Name:    "ALU Instructions",
		Unit:    "count",
		Samples: make([]CounterSample, 0),
	}
	fp32InstrTrack := CounterTrack{
		Name:    "FP32 Instructions",
		Unit:    "count",
		Samples: make([]CounterSample, 0),
	}
	fp16InstrTrack := CounterTrack{
		Name:    "FP16 Instructions",
		Unit:    "count",
		Samples: make([]CounterSample, 0),
	}
	int32InstrTrack := CounterTrack{
		Name:    "INT32 Instructions",
		Unit:    "count",
		Samples: make([]CounterSample, 0),
	}
	int16InstrTrack := CounterTrack{
		Name:    "INT16 Instructions",
		Unit:    "count",
		Samples: make([]CounterSample, 0),
	}
	branchInstrTrack := CounterTrack{
		Name:    "Branch Instructions",
		Unit:    "count",
		Samples: make([]CounterSample, 0),
	}
	threadgroupMemTrack := CounterTrack{
		Name:    "Threadgroup Memory",
		Unit:    "bytes",
		Samples: make([]CounterSample, 0),
	}
	allocatedRegsTrack := CounterTrack{
		Name:    "Allocated Registers",
		Unit:    "count",
		Samples: make([]CounterSample, 0),
	}
	uniformRegsTrack := CounterTrack{
		Name:    "Uniform Registers",
		Unit:    "count",
		Samples: make([]CounterSample, 0),
	}
	spilledBytesTrack := CounterTrack{
		Name:    "Spilled Bytes",
		Unit:    "bytes",
		Samples: make([]CounterSample, 0),
	}

	// Generate samples for instruction tracks - use PipelineStats from streamData
	// Match by encoder label (which is the kernel/function name)
	for _, encoder := range timeline.Encoders {
		// Try to find matching PipelineStats by exact or fuzzy match
		var pipeline *gputrace.PipelineStats
		if p, exists := pipelineByName[encoder.Label]; exists {
			pipeline = p
		} else {
			// Try fuzzy match - encoder label may contain or be contained in
			// function name. An empty name must not match everything.
			for funcName, p := range pipelineByName {
				if encoder.Label == "" || funcName == "" {
					continue
				}
				if strings.Contains(encoder.Label, funcName) || strings.Contains(funcName, encoder.Label) {
					pipeline = p
					break
				}
			}
		}

		if pipeline == nil {
			continue
		}

		// Add instruction count samples
		if pipeline.InstructionCount > 0 {
			instructionTrack.Samples = append(instructionTrack.Samples,
				CounterSample{Timestamp: encoder.StartTime, Value: float64(pipeline.InstructionCount)},
				CounterSample{Timestamp: encoder.EndTime, Value: float64(pipeline.InstructionCount)})
		}
		if pipeline.ALUInstructionCount > 0 {
			aluInstrTrack.Samples = append(aluInstrTrack.Samples,
				CounterSample{Timestamp: encoder.StartTime, Value: float64(pipeline.ALUInstructionCount)},
				CounterSample{Timestamp: encoder.EndTime, Value: float64(pipeline.ALUInstructionCount)})
		}
		if pipeline.FP32InstructionCount > 0 {
			fp32InstrTrack.Samples = append(fp32InstrTrack.Samples,
				CounterSample{Timestamp: encoder.StartTime, Value: float64(pipeline.FP32InstructionCount)},
				CounterSample{Timestamp: encoder.EndTime, Value: float64(pipeline.FP32InstructionCount)})
		}
		if pipeline.FP16InstructionCount > 0 {
			fp16InstrTrack.Samples = append(fp16InstrTrack.Samples,
				CounterSample{Timestamp: encoder.StartTime, Value: float64(pipeline.FP16InstructionCount)},
				CounterSample{Timestamp: encoder.EndTime, Value: float64(pipeline.FP16InstructionCount)})
		}
		if pipeline.INT32InstructionCount > 0 {
			int32InstrTrack.Samples = append(int32InstrTrack.Samples,
				CounterSample{Timestamp: encoder.StartTime, Value: float64(pipeline.INT32InstructionCount)},
				CounterSample{Timestamp: encoder.EndTime, Value: float64(pipeline.INT32InstructionCount)})
		}
		if pipeline.INT16InstructionCount > 0 {
			int16InstrTrack.Samples = append(int16InstrTrack.Samples,
				CounterSample{Timestamp: encoder.StartTime, Value: float64(pipeline.INT16InstructionCount)},
				CounterSample{Timestamp: encoder.EndTime, Value: float64(pipeline.INT16InstructionCount)})
		}
		if pipeline.BranchInstructionCount > 0 {
			branchInstrTrack.Samples = append(branchInstrTrack.Samples,
				CounterSample{Timestamp: encoder.StartTime, Value: float64(pipeline.BranchInstructionCount)},
				CounterSample{Timestamp: encoder.EndTime, Value: float64(pipeline.BranchInstructionCount)})
		}
		if pipeline.ThreadgroupMemory > 0 {
			threadgroupMemTrack.Samples = append(threadgroupMemTrack.Samples,
				CounterSample{Timestamp: encoder.StartTime, Value: float64(pipeline.ThreadgroupMemory)},
				CounterSample{Timestamp: encoder.EndTime, Value: float64(pipeline.ThreadgroupMemory)})
		}
		appendCounterTrackSample(&allocatedRegsTrack, encoder, float64(pipeline.TemporaryRegisterCount))
		appendCounterTrackSample(&uniformRegsTrack, encoder, float64(pipeline.UniformRegisterCount))
		appendCounterTrackSample(&spilledBytesTrack, encoder, float64(pipeline.SpilledBytes))
	}

	// Calculate stats and append tracks that have data
	calculateTrackStats(&instructionTrack)
	calculateTrackStats(&aluInstrTrack)
	calculateTrackStats(&fp32InstrTrack)
	calculateTrackStats(&fp16InstrTrack)
	calculateTrackStats(&int32InstrTrack)
	calculateTrackStats(&int16InstrTrack)
	calculateTrackStats(&branchInstrTrack)
	calculateTrackStats(&threadgroupMemTrack)
	calculateTrackStats(&allocatedRegsTrack)
	calculateTrackStats(&uniformRegsTrack)
	calculateTrackStats(&spilledBytesTrack)

	// Only add tracks that have samples
	if len(instructionTrack.Samples) > 0 {
		tracks = append(tracks, instructionTrack)
	}
	if len(aluInstrTrack.Samples) > 0 {
		tracks = append(tracks, aluInstrTrack)
	}
	if len(fp32InstrTrack.Samples) > 0 {
		tracks = append(tracks, fp32InstrTrack)
	}
	if len(fp16InstrTrack.Samples) > 0 {
		tracks = append(tracks, fp16InstrTrack)
	}
	if len(int32InstrTrack.Samples) > 0 {
		tracks = append(tracks, int32InstrTrack)
	}
	if len(int16InstrTrack.Samples) > 0 {
		tracks = append(tracks, int16InstrTrack)
	}
	if len(branchInstrTrack.Samples) > 0 {
		tracks = append(tracks, branchInstrTrack)
	}
	if len(threadgroupMemTrack.Samples) > 0 {
		tracks = append(tracks, threadgroupMemTrack)
	}
	if len(allocatedRegsTrack.Samples) > 0 {
		tracks = append(tracks, allocatedRegsTrack)
	}
	if len(uniformRegsTrack.Samples) > 0 {
		tracks = append(tracks, uniformRegsTrack)
	}
	if len(spilledBytesTrack.Samples) > 0 {
		tracks = append(tracks, spilledBytesTrack)
	}

	return tracks
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

// addStorePipelineArgs records the shader statistics archived in the capture
// bundle. Only fields the store actually carries are set; high_register,
// occupancy and ALU utilization are not archived here and stay absent.
func addStorePipelineArgs(args map[string]interface{}, p *counter.PipelineStats) {
	if p == nil {
		return
	}
	args["function_name"] = p.FunctionName
	args["allocated_registers"] = p.TemporaryRegisterCount
	args["uniform_registers"] = p.UniformRegisterCount
	args["spilled_bytes"] = p.SpilledBytes
	args["threadgroup_memory"] = p.ThreadgroupMemory
	args["instruction_count"] = p.InstructionCount
	args["metrics_source"] = "capture bundle store sections"
}

func addEncoderKernelEvents(timeline *Timeline, trace *gputrace.Trace, sourceMapper *gputrace.ShaderSourceMapper, storeStats *counter.StoreStats) {
	computeEncoders := traceComputeEncoders(trace)
	lanes := newLanePacker(3, 4) // Kernels Lane 0..3

	for i, encoder := range timeline.Encoders {
		args := map[string]interface{}{
			"encoder_index": encoder.Index,
			"duration_us":   float64(encoder.Duration) / 1e3,
			"source":        "encoder span",
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
		addStorePipelineArgs(args, storeStats.PipelineForLabel(encoder.Label))
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
		timeline.Events = append(timeline.Events, TimelineEvent{
			Name:      encoder.Label,
			Category:  "kernel",
			Phase:     "X",
			Timestamp: encoder.StartTime / 1000,
			Duration:  encoder.Duration / 1000,
			ProcessID: 1,
			ThreadID:  threadID,
			Args:      args,
		})
	}
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

func addDispatchKernelEvents(timeline *Timeline, stats *counter.StreamDataStats, simd timelineDispatchSIMDStats, shaderReport *gputrace.ShaderMetricsReport, perfStats *gputrace.PerfCounterStats, encoderMetrics []counter.EncoderCounterMetrics, sourceMapper *gputrace.ShaderSourceMapper) bool {
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
		encoderMetricByIndex[encoderMetrics[i].EncoderIndex] = &encoderMetrics[i]
	}
	encoderOffsets := make(map[int]uint64)
	var fallbackStartNs uint64

	for _, d := range stats.Dispatches {
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
		simdGroups, simdGroupSharePct := simd.cost(name, d.Index)
		args := dispatchKernelArgs(d, pipeline, simdGroups, simdGroupSharePct, shaderMetric, metric, encoderMetric, sourceMapper)

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
	}
	if d.StartTicks != 0 || d.EndTicks != 0 {
		args["start_ticks"] = d.StartTicks
		args["end_ticks"] = d.EndTicks
	}
	if p != nil {
		if p.FunctionName != "" {
			args["function_name"] = p.FunctionName
		}
		if p.PipelineAddress != 0 {
			args["pipeline_state"] = fmt.Sprintf("0x%x", p.PipelineAddress)
		}
		args["allocated_registers"] = p.TemporaryRegisterCount
		args["uniform_registers"] = p.UniformRegisterCount
		args["spilled_bytes"] = p.SpilledBytes
		args["threadgroup_memory"] = p.ThreadgroupMemory
		args["instruction_count"] = p.InstructionCount
		args["alu_instruction_count"] = p.ALUInstructionCount
		args["fp32_instruction_count"] = p.FP32InstructionCount
		args["fp16_instruction_count"] = p.FP16InstructionCount
	}
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
		if shader.TotalThreadgroups > 0 && args["simd_groups"] == nil {
			args["simd_groups"] = shader.TotalThreadgroups
		}
		if shader.TotalDurationNs > 0 {
			args["shader_duration_ns"] = shader.TotalDurationNs
		}
	}
	if hardware != nil {
		if hardware.SIMDGroups > 0 && args["simd_groups"] == nil {
			args["simd_groups"] = hardware.SIMDGroups
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

type timelineDispatchSIMDStats struct {
	byIndex []uint64
	byName  map[string]uint64
	total   uint64
}

func timelineDispatchSIMDGroups(t *gputrace.Trace, stats *counter.StreamDataStats) timelineDispatchSIMDStats {
	out := timelineDispatchSIMDStats{byName: make(map[string]uint64)}
	if t == nil || stats == nil || len(stats.Dispatches) == 0 || len(t.CaptureData) == 0 {
		return out
	}
	dispatches := t.ParseDispatchInRegion(t.CaptureData, 0)
	if len(dispatches) != len(stats.Dispatches) {
		return out
	}
	out.byIndex = make([]uint64, len(dispatches))
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

func (s timelineDispatchSIMDStats) cost(name string, index int) (uint64, float64) {
	groups := s.byName[name]
	if groups == 0 && index >= 0 && index < len(s.byIndex) {
		groups = s.byIndex[index]
	}
	if groups == 0 || s.total == 0 {
		return groups, 0
	}
	return groups, float64(groups) / float64(s.total) * 100
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

// exportChromeTracing exports timeline in Chrome tracing format.
func exportChromeTracing(timeline *Timeline, outputPath string) error {
	return exportChromeTracingForClock(timeline, outputPath, timelineClockBusy)
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
		args["excluded_categories"] = []string{"command_buffer", "profiler_stream", "gprwcntr"}
		args["excluded_counter_series"] = "memory-side GTMioCounterData has scope=2/index=0 and a separate tick domain; it is not encoder-attributed or clock-aligned"
	case timelineClockWall:
		args["included_categories"] = []string{"command_buffer"}
		args["excluded_categories"] = []string{"encoder", "kernel", "counter"}
		if rawProfilerSamples {
			args["included_categories"] = append(args["included_categories"].([]string), "profiler_stream", "gprwcntr")
		} else {
			args["excluded_categories"] = append(args["excluded_categories"].([]string), "profiler_stream", "gprwcntr")
			args["raw_profiler_samples"] = "excluded by default: GPRWCNTR records and their aggregate profiler streams are not decoded counters or encoder intervals; use --include-raw-samples to inspect them"
		}
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
		if ev.Category != "kernel" || ev.Args == nil {
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
func runTimelineFromProfiler(tracePath string, opts *timelineOptions) error {
	if err := validateTimelineFormat(opts.format); err != nil {
		return err
	}
	if err := validateTimelineClock(opts.clock); err != nil {
		return err
	}

	// Find .gpuprofiler_raw directory
	profilerDir := profilerraw.FindDir(tracePath)

	if profilerDir == "" {
		fmt.Fprintf(os.Stderr, "Hint: To generate performance data, run:\n")
		fmt.Fprintf(os.Stderr, "  gputrace xcode-profile run %s\n\n", tracePath)
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
	if timeline.Timing == nil || timeline.Timing.EncoderTimingApproximate || timeline.Timing.TimingSource == "" || timeline.Timing.TimingSource == "unavailable" {
		fmt.Fprintln(os.Stderr, "Warning: trace lacks precise hardware timing data; encoder/dispatch durations are estimated.")
	}
	outputPath := timelineOutputPath(opts.format, opts.output)
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

	// Export based on format
	switch opts.format {
	case "chrome", "perfetto":
		if err := exportChromeTracingForClock(timeline, outputPath, opts.clock); err != nil {
			return fmt.Errorf("failed to export Chrome/Perfetto tracing: %w", err)
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

	return nil
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
	}

	timeline.TimebaseNumer = timebaseNumer
	timeline.TimebaseDenom = timebaseDenom
	timeline.AbsoluteTime = absoluteTime

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
	if !addDispatchKernelEvents(timeline, stats, timelineDispatchSIMDStats{}, nil, nil, nil, nil) {
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
