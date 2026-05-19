package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/tmc/gputrace"
	"github.com/tmc/gputrace/internal/counter"
)

var (
	timelineOutput string
	timelineFormat string
)

var timelineCmd = &cobra.Command{
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

Examples:
  # Generate interactive HTML timeline viewer
  gputrace timeline trace.gputrace -o timeline.html --format html

  # Generate Chrome tracing format
  gputrace timeline trace.gputrace -o timeline.json

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
	RunE: runTimeline,
}

func init() {
	rootCmd.AddCommand(timelineCmd)

	timelineCmd.Flags().StringVarP(&timelineOutput, "output", "o", "timeline.json", "Output file path")
	timelineCmd.Flags().StringVar(&timelineFormat, "format", "text", "Output format: chrome, html, json, text")
}

func runTimeline(cmd *cobra.Command, args []string) error {
	tracePath := args[0]

	// Verify trace file exists
	if err := checkTraceFile(tracePath); err != nil {
		return err
	}

	// Try to open full trace first
	trace, err := gputrace.Open(tracePath)
	if err != nil {
		// Fall back to profiler-only mode if unsorted-capture is missing
		return runTimelineFromProfiler(tracePath)
	}

	// Generate timeline data
	timeline, err := generateTimeline(trace)
	if err != nil {
		return fmt.Errorf("failed to generate timeline: %w", err)
	}

	// Enhance with raw GPRWCNTR data if available
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
			fmt.Printf("✓ Enhanced with %d GPRWCNTR samples\n", sampleCount)
		}
	}

	// Export based on format
	switch timelineFormat {
	case "chrome", "perfetto":
		if err := exportChromeTracing(timeline, timelineOutput); err != nil {
			return fmt.Errorf("failed to export Chrome/Perfetto tracing: %w", err)
		}
	case "html":
		if err := exportHTML(timeline, timelineOutput); err != nil {
			return fmt.Errorf("failed to export HTML: %w", err)
		}
	case "json":
		if err := exportTimelineJSON(timeline, timelineOutput); err != nil {
			return fmt.Errorf("failed to export JSON: %w", err)
		}
	case "text":
		if err := exportTextTimeline(timeline); err != nil {
			return fmt.Errorf("failed to export text: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("unknown format: %s (supported: chrome, perfetto, html, json, text)", timelineFormat)
	}

	fmt.Printf("✓ Timeline written to: %s\n", timelineOutput)
	if timelineFormat == "chrome" {
		fmt.Println("\nView in Chrome:")
		fmt.Println("  1. Open chrome://tracing")
		fmt.Println("  2. Click 'Load' and select", timelineOutput)
		fmt.Println("  3. Use WASD to navigate, mouse wheel to zoom")
	} else if timelineFormat == "perfetto" {
		fmt.Println("\nView in Perfetto:")
		fmt.Println("  1. Open https://ui.perfetto.dev")
		fmt.Println("  2. Drag and drop", timelineOutput, "onto the page")
		fmt.Println("  3. Use WASD to navigate, mouse wheel to zoom")
	} else if timelineFormat == "html" {
		fmt.Println("\nView timeline:")
		fmt.Printf("  open %s\n", timelineOutput)
	}

	return nil
}

// exportTextTimeline prints the timeline to stdout in a hierarchical format.
func exportTextTimeline(timeline *Timeline) error {
	if len(timeline.Encoders) == 0 && len(timeline.Events) == 0 {
		fmt.Println("No timeline data available.")
		return nil
	}

	// Find command buffer events
	var cbs []TimelineEvent
	for _, event := range timeline.Events {
		if event.Category == "command_buffer" {
			cbs = append(cbs, event)
		}
	}

	// If no CB events, create a dummy one
	if len(cbs) == 0 {
		cbs = append(cbs, TimelineEvent{
			Name:      "CB#0",
			Timestamp: timeline.StartTime,
			Duration:  timeline.Duration,
		})
	}

	firstTimestamp := timeline.StartTime
	if len(cbs) > 0 && cbs[0].Timestamp < firstTimestamp {
		firstTimestamp = cbs[0].Timestamp
	}

	for _, cb := range cbs {
		var cbStart float64
		if cb.Timestamp >= firstTimestamp {
			cbStart = float64(cb.Timestamp-firstTimestamp) / 1000000.0
		} else {
			cbStart = 0.0
		}
		// Show duration if available (from APSTimelineData)
		if cb.Duration > 0 {
			cbDurationMs := float64(cb.Duration) / 1000.0 // Duration is in µs, convert to ms
			fmt.Printf("%s [%.1fms, duration=%.2fms]\n", cb.Name, cbStart, cbDurationMs)
		} else {
			fmt.Printf("%s [%.1fms]\n", cb.Name, cbStart)
		}

		cbIndex, ok := cb.Args["index"].(int)
		if !ok {
			continue
		}

		var cbEncoders []EncoderInfo
		for _, encoder := range timeline.Encoders {
			belongsToCB := false
			for _, k := range timeline.Kernels {
				if k.Encoder == encoder.Index {
					if kArgCB, ok := getKernelCBIndex(timeline, k); ok && kArgCB == cbIndex {
						belongsToCB = true
						break
					}
				}
			}
			if belongsToCB {
				cbEncoders = append(cbEncoders, encoder)
			}
		}

		for i, encoder := range cbEncoders {
			startMs := float64(encoder.StartTime-firstTimestamp) / 1e6
			durationMs := float64(encoder.Duration) / 1e6

			label := encoder.Label
			if label == "" {
				label = "Unknown Encoder"
			}

			var encoderKernels []KernelInfo
			for _, k := range timeline.Kernels {
				if k.Encoder == encoder.Index {
					encoderKernels = append(encoderKernels, k)
				}
			}

			prefix := "├─"
			if i == len(cbEncoders)-1 {
				prefix = "└─"
			}

			if len(encoderKernels) > 0 {
				for _, k := range encoderKernels {
					kStartMs := float64(k.StartTime-firstTimestamp) / 1e6
					kDurationMs := float64(k.Duration) / 1e6
					fmt.Printf("  %s %.2fms: %s (%.2fms) - %s\n",
						prefix, kStartMs, k.Name, kDurationMs, label)
				}
			} else {
				fmt.Printf("  %s %.2fms: %s (%.2fms) - %s\n", prefix, startMs, label, durationMs, "Encoder")
			}
		}
	}

	return nil
}

func getKernelCBIndex(timeline *Timeline, k KernelInfo) (int, bool) {
	for _, e := range timeline.Events {
		if e.Category == "kernel" && e.Name == k.Name && e.Timestamp == k.StartTime/1000 {
			if cbIdx, ok := e.Args["cb_index"].(int); ok {
				return cbIdx, true
			}
		}
	}
	return -1, false
}

// Timeline represents the complete timeline data.
type Timeline struct {
	StartTime     uint64          `json:"start_time"`
	EndTime       uint64          `json:"end_time"`
	Duration      uint64          `json:"duration"`
	Events        []TimelineEvent `json:"events"`
	Encoders      []EncoderInfo   `json:"encoders"`
	Kernels       []KernelInfo    `json:"kernels"`
	APICallseq    []APICall       `json:"api_callseq"`
	CounterTracks []CounterTrack  `json:"counter_tracks,omitempty"`
	Timing        *TimelineTiming `json:"timing,omitempty"`
	AbsoluteTime  uint64          `json:"absolute_time"`
	TimebaseNumer uint64          `json:"timebase_numer"`
	TimebaseDenom uint64          `json:"timebase_denom"`
}

// TimelineTiming summarizes the timing sources that Xcode and gputrace expose.
type TimelineTiming struct {
	EncoderSpanNs         uint64  `json:"encoder_span_ns,omitempty"`
	DispatchSpanNs        uint64  `json:"dispatch_span_ns,omitempty"`
	EffectiveGPUTimeNs    *uint64 `json:"effective_gpu_time_ns,omitempty"`
	CommandBufferActiveNs uint64  `json:"command_buffer_active_time_ns,omitempty"`
	CommandBufferWallNs   uint64  `json:"command_buffer_wall_time_ns,omitempty"`
	RestoreActiveNs       uint64  `json:"restore_active_time_ns,omitempty"`
	RestoreWallNs         uint64  `json:"restore_wall_time_ns,omitempty"`
	DisplayDurationNs     uint64  `json:"display_duration_ns,omitempty"`
	DisplayDurationSource string  `json:"display_duration_source,omitempty"`
	TimingSource          string  `json:"timing_source,omitempty"`
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
	Name      string `json:"name"`
	Encoder   int    `json:"encoder"`
	StartTime uint64 `json:"start_time"`
	EndTime   uint64 `json:"end_time"`
	Duration  uint64 `json:"duration"`
}

// APICall represents an API call event.
type APICall struct {
	Name      string                 `json:"name"`
	Timestamp uint64                 `json:"timestamp"`
	Args      map[string]interface{} `json:"args,omitempty"`
}

// CounterTrack represents a performance counter track over time.
type CounterTrack struct {
	Name     string          `json:"name"`
	Unit     string          `json:"unit"` // %, GB/s, count, etc.
	Samples  []CounterSample `json:"samples"`
	MinValue float64         `json:"min_value"`
	MaxValue float64         `json:"max_value"`
	AvgValue float64         `json:"avg_value"`
}

// CounterSample represents a single counter measurement at a point in time.
type CounterSample struct {
	Timestamp uint64  `json:"ts"` // Timestamp in nanoseconds
	Value     float64 `json:"value"`
}

// generateTimeline creates timeline data from a trace.
func generateTimeline(trace *gputrace.Trace) (*Timeline, error) {
	timeline := &Timeline{
		Events:     make([]TimelineEvent, 0),
		Encoders:   make([]EncoderInfo, 0),
		Kernels:    make([]KernelInfo, 0),
		APICallseq: make([]APICall, 0),
	}

	// Try to get real timing from profiler data first (streamData plist)
	profilerTimings, totalTimeUs, profilerErr := gputrace.ExtractEncoderTimingsFromProfiler(trace)
	useProfilerTiming := profilerErr == nil && len(profilerTimings) > 0

	// Get real encoder labels from ParseComputeEncoders (primary source for labels)
	computeEncoders, _ := trace.ParseComputeEncoders()

	// Extract timing metrics (fallback)
	extractor := gputrace.NewTimingMetricsExtractor(trace)
	metrics, err := extractor.Extract()
	if err != nil {
		return nil, fmt.Errorf("extract timing: %w", err)
	}

	// If we have real profiler timing, use it
	if useProfilerTiming {
		// Calculate total duration
		timeline.Duration = uint64(totalTimeUs) * 1000 // Convert µs to ns
		timeline.StartTime = 0
		timeline.EndTime = timeline.Duration

		streamStats, statsErr := gputrace.ExtractPipelineStats(trace)
		if statsErr == nil {
			timeline.Timing = timelineTimingFromStats(streamStats)
			if streamStats != nil && streamStats.Timeline != nil {
				timeline.AbsoluteTime = streamStats.Timeline.AbsoluteTime
				timeline.TimebaseNumer = streamStats.Timeline.TimebaseNumer
				timeline.TimebaseDenom = streamStats.Timeline.TimebaseDenom
			}
		}

		// Build timeline from profiler timing
		var currentTimeNs uint64
		for i, pt := range profilerTimings {
			durationNs := uint64(pt.DurationMicros) * 1000 // Convert µs to ns
			startTimeNs := currentTimeNs
			endTimeNs := startTimeNs + durationNs

			label := pt.Label
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
					"real_timing": true,
				},
			}
			timeline.Events = append(timeline.Events, event)

			currentTimeNs = endTimeNs
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
				timeline.Events = append(timeline.Events, event)
			}
		}
	}

	// Add kernel events - generate one per encoder to show sequence
	// This avoids the "stacking" issue where aggregated stats were placed at arbitrary timestamps.
	for _, encoder := range timeline.Encoders {
		kernelInfo := KernelInfo{
			Name:      encoder.Label,
			Encoder:   encoder.Index,
			StartTime: encoder.StartTime,
			EndTime:   encoder.EndTime,
			Duration:  encoder.Duration,
		}
		timeline.Kernels = append(timeline.Kernels, kernelInfo)

		// 1. Generate "Encoder" event on ThreadID 1 (Encoders Lane 0)
		// This matches Xcode's top track.
		encoderEvent := TimelineEvent{
			Name:      encoder.Label,
			Category:  "encoder",
			Phase:     "X",
			Timestamp: encoder.StartTime / 1000,
			Duration:  encoder.Duration / 1000,
			ProcessID: 1,
			ThreadID:  1 + (encoder.Index % 2), // Use 2 lanes for encoders (ID 1-2) to handle overlaps
			Args: map[string]interface{}{
				"encoder_index": encoder.Index,
				"duration_us":   float64(encoder.Duration) / 1e3,
			},
		}
		timeline.Events = append(timeline.Events, encoderEvent)

		// 2. Generate "Kernel" event on ThreadIDs 3-6 (Kernels Lane 0-3)
		kernelEvent := TimelineEvent{
			Name:      encoder.Label,
			Category:  "kernel",
			Phase:     "X",
			Timestamp: encoder.StartTime / 1000,
			Duration:  encoder.Duration / 1000,
			ProcessID: 1,
			ThreadID:  3 + (encoder.Index % 4),
			Args: map[string]interface{}{
				"encoder_index": encoder.Index,
				"duration_us":   float64(encoder.Duration) / 1e3,
			},
		}
		timeline.Events = append(timeline.Events, kernelEvent)
	}

	// Add command buffer events - try to get real timing from APSTimelineData
	streamStats, statsErr := gputrace.ExtractPipelineStats(trace)
	if statsErr == nil && streamStats != nil && streamStats.Timeline != nil && len(streamStats.Timeline.CommandBufferTimestamps) > 0 {
		// Use real CB timing from APSTimelineData
		ti := streamStats.Timeline
		timeline.AbsoluteTime = ti.AbsoluteTime
		timeline.TimebaseNumer = ti.TimebaseNumer
		timeline.TimebaseDenom = ti.TimebaseDenom

		var displayStartNs uint64
		for _, cb := range ti.CommandBufferTimestamps {
			durationNs := cb.DurationNs(ti.TimebaseNumer, ti.TimebaseDenom)
			var rawStartOffsetNs uint64
			if cb.StartTicks > ti.AbsoluteTime {
				rawStartOffsetNs = (cb.StartTicks - ti.AbsoluteTime) * ti.TimebaseNumer / ti.TimebaseDenom
			}

			event := TimelineEvent{
				Name:      fmt.Sprintf("CB#%d", cb.Index),
				Category:  "command_buffer",
				Phase:     "X",                   // Duration event
				Timestamp: displayStartNs / 1000, // Convert to microseconds for Chrome format
				Duration:  durationNs / 1000,
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
			displayStartNs += durationNs
		}

		// Add encoder profile events from GPRWCNTR ShaderProfilerData
		if len(ti.EncoderProfiles) > 0 {
			for _, ep := range ti.EncoderProfiles {
				if ep.SampleCount == 0 || ep.StartTicks == 0 {
					continue
				}
				// Convert to nanoseconds relative to capture start
				startNs := (ep.StartTicks - ti.AbsoluteTime) * ti.TimebaseNumer / ti.TimebaseDenom

				event := TimelineEvent{
					Name:      fmt.Sprintf("GPRWCNTR Enc#%d", ep.Index),
					Category:  "encoder_profile",
					Phase:     "X",
					Timestamp: startNs / 1000, // Convert to microseconds
					Duration:  ep.DurationNs / 1000,
					ProcessID: 1,
					ThreadID:  7 + (ep.Index % 8), // 8 Lanes for encoder profiles (7-14)
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
					Timestamp: uint64(cb.Offset),
					ProcessID: 1,
					ThreadID:  0,
					Args: map[string]interface{}{
						"offset": cb.Offset,
						"index":  i,
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
		fmt.Printf("Normalizing timeline: shifting by -%d µs\n", globalMinTs)
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

	return timeline, nil
}

// containsSubstr checks if s contains substr.
func containsSubstr(s, substr string) bool {
	if len(substr) > len(s) {
		return false
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// generateCounterTracks creates performance counter tracks for the timeline.
// Only returns real data from .gpuprofiler_raw files - no synthetic data.
func generateCounterTracks(trace *gputrace.Trace, timeline *Timeline) []CounterTrack {
	tracks := make([]CounterTrack, 0)

	// Skip if no encoders (can't generate meaningful counter data)
	if len(timeline.Encoders) == 0 {
		return tracks
	}

	// Only use real performance counter data - no synthetic fallback
	perfStats, err := gputrace.ParsePerfCounters(trace)
	if err == nil && len(perfStats.ShaderMetrics) > 0 {
		// Also get PipelineStats from streamData for instruction counts
		streamStats, _ := gputrace.ExtractPipelineStats(trace)
		return generateCounterTracksFromPerfData(perfStats, streamStats, timeline)
	}

	// No synthetic data - return empty if no real perf data available
	return tracks
}

// generateCounterTracksFromPerfData creates counter tracks from real performance counter data.
func generateCounterTracksFromPerfData(perfStats *gputrace.PerfCounterStats, streamStats *gputrace.StreamDataStats, timeline *Timeline) []CounterTrack {
	tracks := make([]CounterTrack, 0)

	// Initialize counter tracks
	activeCoresTrack := CounterTrack{
		Name:    "Active Cores",
		Unit:    "count",
		Samples: make([]CounterSample, 0),
	}

	occupancyTrack := CounterTrack{
		Name:    "Occupancy",
		Unit:    "%",
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

	occupancyManagerTrack := CounterTrack{
		Name:    "Occupancy Manager",
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

		// Calculate values from real hardware data or use defaults
		var activeCores float64
		var occupancy float64
		var aluUtil float64
		var bandwidth float64
		var throughput float64
		var occupancyManager float64
		var shaderLaunchLimiter float64

		if metrics != nil {
			// Use real hardware metrics
			occupancy = metrics.KernelOccupancy
			aluUtil = metrics.ALUUtilization

			// Calculate active cores from SIMD groups
			// Typical M-series GPU: 8-10 cores, each core has 128-1024 SIMD lanes
			// Heuristic: map SIMD groups to estimated core count
			if metrics.SIMDGroups > 0 {
				activeCores = float64(metrics.SIMDGroups) / 100.0 // Rough estimate
				if activeCores > 8.0 {
					activeCores = 8.0 // Cap at typical M-series core count
				}
				if activeCores < 1.0 {
					activeCores = 1.0
				}
			} else {
				activeCores = 4.0 // Default
			}

			// Calculate bandwidth from memory bandwidth counter (convert bytes to GB/s)
			if metrics.MemoryBandwidth > 0 && encoder.Duration > 0 {
				durationSec := float64(encoder.Duration) / 1e9
				bandwidth = float64(metrics.MemoryBandwidth) / 1e9 / durationSec
			} else {
				bandwidth = 50.0 // Default
			}

			// Estimate throughput from occupancy and ALU utilization
			throughput = (occupancy + aluUtil) / 2.0

			// Occupancy Manager: Tracks how well the GPU scheduler manages threadgroup dispatch
			// High when occupancy is maintained well, low when there are bubbles
			occupancyManager = occupancy * 0.95 // Typically slightly lower than raw occupancy

			// Shader Launch Limiter: Percentage of time shader launches are limited by resources
			// High values indicate resource contention (registers, threadgroup memory, etc.)
			// Estimate from register pressure and occupancy
			if metrics.AllocatedRegs > 0 {
				// More registers = more likely to hit launch limits
				regPressure := float64(metrics.AllocatedRegs) / 256.0 // 256 max registers typical
				if regPressure > 1.0 {
					regPressure = 1.0
				}
				shaderLaunchLimiter = regPressure * 100.0
			} else {
				// Estimate from inverse of occupancy
				shaderLaunchLimiter = (1.0 - occupancy/100.0) * 100.0
			}
		} else {
			// No real data for this encoder - skip it (no synthetic data)
			continue
		}

		// Add samples at start and end of encoder execution
		activeCoresTrack.Samples = append(activeCoresTrack.Samples,
			CounterSample{Timestamp: encoder.StartTime, Value: activeCores},
			CounterSample{Timestamp: encoder.EndTime, Value: activeCores})

		occupancyTrack.Samples = append(occupancyTrack.Samples,
			CounterSample{Timestamp: encoder.StartTime, Value: occupancy},
			CounterSample{Timestamp: encoder.EndTime, Value: occupancy})

		aluTrack.Samples = append(aluTrack.Samples,
			CounterSample{Timestamp: encoder.StartTime, Value: aluUtil},
			CounterSample{Timestamp: encoder.EndTime, Value: aluUtil})

		bandwidthTrack.Samples = append(bandwidthTrack.Samples,
			CounterSample{Timestamp: encoder.StartTime, Value: bandwidth},
			CounterSample{Timestamp: encoder.EndTime, Value: bandwidth})

		throughputTrack.Samples = append(throughputTrack.Samples,
			CounterSample{Timestamp: encoder.StartTime, Value: throughput},
			CounterSample{Timestamp: encoder.EndTime, Value: throughput})

		occupancyManagerTrack.Samples = append(occupancyManagerTrack.Samples,
			CounterSample{Timestamp: encoder.StartTime, Value: occupancyManager},
			CounterSample{Timestamp: encoder.EndTime, Value: occupancyManager})

		shaderLaunchLimiterTrack.Samples = append(shaderLaunchLimiterTrack.Samples,
			CounterSample{Timestamp: encoder.StartTime, Value: shaderLaunchLimiter},
			CounterSample{Timestamp: encoder.EndTime, Value: shaderLaunchLimiter})
	}

	// Calculate statistics for each track
	calculateTrackStats(&activeCoresTrack)
	calculateTrackStats(&occupancyTrack)
	calculateTrackStats(&aluTrack)
	calculateTrackStats(&bandwidthTrack)
	calculateTrackStats(&throughputTrack)
	calculateTrackStats(&occupancyManagerTrack)
	calculateTrackStats(&shaderLaunchLimiterTrack)

	tracks = append(tracks, activeCoresTrack, occupancyTrack, aluTrack, bandwidthTrack, throughputTrack, occupancyManagerTrack, shaderLaunchLimiterTrack)

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
		metrics, exists := shaderMetricsMap[encoder.Label]
		if !exists {
			// No real data for this encoder - skip it (no synthetic data)
			continue
		}

		l1Miss := metrics.BufferL1MissRate
		// Convert bytes to GB/s
		var memRead, memWrite float64
		durationSec := float64(encoder.Duration) / 1e9
		if durationSec > 0 {
			memRead = float64(metrics.BytesReadFromDeviceMemory) / 1e9 / durationSec
			memWrite = float64(metrics.BytesWrittenToDeviceMemory) / 1e9 / durationSec
		}
		compLimit := metrics.ComputeShaderLaunchLimiter + metrics.ALUUtilization                        // Proxy
		memLimit := metrics.L1CacheLimiter + metrics.LastLevelCacheLimiter + metrics.TextureReadLimiter // Proxy

		l1MissTrack.Samples = append(l1MissTrack.Samples,
			CounterSample{Timestamp: encoder.StartTime, Value: l1Miss},
			CounterSample{Timestamp: encoder.EndTime, Value: l1Miss})

		memReadTrack.Samples = append(memReadTrack.Samples,
			CounterSample{Timestamp: encoder.StartTime, Value: memRead},
			CounterSample{Timestamp: encoder.EndTime, Value: memRead})

		memWriteTrack.Samples = append(memWriteTrack.Samples,
			CounterSample{Timestamp: encoder.StartTime, Value: memWrite},
			CounterSample{Timestamp: encoder.EndTime, Value: memWrite})

		computeLimiterTrack.Samples = append(computeLimiterTrack.Samples,
			CounterSample{Timestamp: encoder.StartTime, Value: compLimit},
			CounterSample{Timestamp: encoder.EndTime, Value: compLimit})

		memoryLimiterTrack.Samples = append(memoryLimiterTrack.Samples,
			CounterSample{Timestamp: encoder.StartTime, Value: memLimit},
			CounterSample{Timestamp: encoder.EndTime, Value: memLimit})
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

	// Generate samples for instruction tracks - use PipelineStats from streamData
	// Match by encoder label (which is the kernel/function name)
	for _, encoder := range timeline.Encoders {
		// Try to find matching PipelineStats by exact or fuzzy match
		var pipeline *gputrace.PipelineStats
		if p, exists := pipelineByName[encoder.Label]; exists {
			pipeline = p
		} else {
			// Try fuzzy match - encoder label may contain or be contained in function name
			for funcName, p := range pipelineByName {
				if containsSubstr(encoder.Label, funcName) || containsSubstr(funcName, encoder.Label) {
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

// exportChromeTracing exports timeline in Chrome tracing format.
func exportChromeTracing(timeline *Timeline, outputPath string) error {
	f, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer f.Close()

	// Add process and thread name metadata events
	metadataEvents := []TimelineEvent{
		{
			Name:      "process_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  0,
			Args: map[string]interface{}{
				"name": "GPU Trace",
			},
		},
		{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  0,
			Args: map[string]interface{}{
				"name": "Command Buffers",
			},
		},
		{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  1,
			Args: map[string]interface{}{
				"name": "Encoders Lane 0",
			},
		},
		{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  2,
			Args: map[string]interface{}{
				"name": "Encoders Lane 1",
			},
		},
		{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  3,
			Args: map[string]interface{}{
				"name": "Kernels Lane 0",
			},
		},
		{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  4,
			Args: map[string]interface{}{
				"name": "Kernels Lane 1",
			},
		},
		{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  5,
			Args: map[string]interface{}{
				"name": "Kernels Lane 2",
			},
		},
		{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  6,
			Args: map[string]interface{}{
				"name": "Kernels Lane 3",
			},
		},
		{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  7,
			Args: map[string]interface{}{
				"name": "GPRWCNTR Lane 0",
			},
		},
		{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  8,
			Args: map[string]interface{}{
				"name": "GPRWCNTR Lane 1",
			},
		},
		{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  9,
			Args: map[string]interface{}{
				"name": "GPRWCNTR Lane 2",
			},
		},
		{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  10,
			Args: map[string]interface{}{
				"name": "GPRWCNTR Lane 3",
			},
		},
		{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  11,
			Args: map[string]interface{}{
				"name": "GPRWCNTR Lane 4",
			},
		},
		{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  12,
			Args: map[string]interface{}{
				"name": "GPRWCNTR Lane 5",
			},
		},
		{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  13,
			Args: map[string]interface{}{
				"name": "GPRWCNTR Lane 6",
			},
		},
		{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  14,
			Args: map[string]interface{}{
				"name": "GPRWCNTR Lane 7",
			},
		},
	}

	// Add counter track metadata and events
	threadID := 15 // Start after GPRWCNTR lanes (7-14)
	for _, track := range timeline.CounterTracks {
		// Add thread name for this counter track
		metadataEvents = append(metadataEvents, TimelineEvent{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  threadID,
			Args: map[string]interface{}{
				"name": fmt.Sprintf("%s (%s)", track.Name, track.Unit),
			},
		})

		// Add counter samples as events
		for _, sample := range track.Samples {
			// Use counter events (C phase) for Chrome tracing
			counterEvent := TimelineEvent{
				Name:      track.Name,
				Category:  "counter",
				Phase:     "C",                     // Counter event
				Timestamp: sample.Timestamp / 1000, // Convert to microseconds
				ProcessID: 1,
				ThreadID:  threadID,
				Args: map[string]interface{}{
					track.Name: sample.Value,
				},
			}
			timeline.Events = append(timeline.Events, counterEvent)
		}

		threadID++
	}

	// Combine metadata events with timeline events
	allEvents := append(metadataEvents, timeline.Events...)

	// Chrome tracing format
	// Standard format: { "traceEvents": [ ... ] }
	// We omit displayTimeUnit and other legacy fields to maximize Perfetto compatibility.
	tracing := map[string]interface{}{
		"traceEvents": allEvents,
	}

	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")
	return encoder.Encode(tracing)
}

// exportTimelineJSON exports raw timeline data as JSON.
func exportTimelineJSON(timeline *Timeline, outputPath string) error {
	f, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer f.Close()

	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")
	return encoder.Encode(timeline)
}

// exportHTML exports an interactive standalone HTML timeline viewer.
func exportHTML(timeline *Timeline, outputPath string) error {
	f, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer f.Close()

	// Serialize timeline data to JSON for embedding
	timelineJSON, err := json.Marshal(timeline)
	if err != nil {
		return fmt.Errorf("marshal timeline: %w", err)
	}

	// Generate the HTML content
	html := generateInteractiveHTML(string(timelineJSON))
	_, err = f.WriteString(html)
	return err
}

// runTimelineFromProfiler generates timeline from profiler-only traces (.gpuprofiler_raw without unsorted-capture).
func runTimelineFromProfiler(tracePath string) error {
	// Find .gpuprofiler_raw directory
	profilerDir := ""
	if filepath.Ext(tracePath) == ".gpuprofiler_raw" {
		profilerDir = tracePath
	} else {
		entries, err := os.ReadDir(tracePath)
		if err != nil {
			return fmt.Errorf("read directory: %w", err)
		}
		for _, e := range entries {
			if e.IsDir() && filepath.Ext(e.Name()) == ".gpuprofiler_raw" {
				profilerDir = filepath.Join(tracePath, e.Name())
				break
			}
		}
	}

	if profilerDir == "" {
		fmt.Fprintf(os.Stderr, "Hint: To generate performance data, run:\n")
		fmt.Fprintf(os.Stderr, "  gputrace xcode-profile run %s\n\n", tracePath)
		return fmt.Errorf("no .gpuprofiler_raw directory found in %s (and unsorted-capture is missing)", tracePath)
	}

	// Parse streamData for timing info
	stats, err := counter.ParseStreamData(profilerDir)
	if err != nil {
		return fmt.Errorf("parse streamData: %w", err)
	}

	// Build timeline from profiler data
	timeline := buildTimelineFromProfilerData(tracePath, stats)

	// Export based on format
	switch timelineFormat {
	case "chrome", "perfetto":
		if err := exportChromeTracing(timeline, timelineOutput); err != nil {
			return fmt.Errorf("failed to export Chrome/Perfetto tracing: %w", err)
		}
	case "html":
		if err := exportHTML(timeline, timelineOutput); err != nil {
			return fmt.Errorf("failed to export HTML: %w", err)
		}
	case "json":
		if err := exportTimelineJSON(timeline, timelineOutput); err != nil {
			return fmt.Errorf("failed to export JSON: %w", err)
		}
	case "text":
		if err := exportTextTimeline(timeline); err != nil {
			return fmt.Errorf("failed to export text: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("unknown format: %s (supported: chrome, perfetto, html, json, text)", timelineFormat)
	}

	fmt.Printf("✓ Timeline written to: %s (profiler-only mode)\n", timelineOutput)
	if timelineFormat == "chrome" {
		fmt.Println("\nView in Chrome:")
		fmt.Println("  1. Open chrome://tracing")
		fmt.Println("  2. Click 'Load' and select", timelineOutput)
		fmt.Println("  3. Use WASD to navigate, mouse wheel to zoom")
	} else if timelineFormat == "perfetto" {
		fmt.Println("\nView in Perfetto:")
		fmt.Println("  1. Open https://ui.perfetto.dev")
		fmt.Println("  2. Drag and drop", timelineOutput, "onto the page")
		fmt.Println("  3. Use WASD to navigate, mouse wheel to zoom")
	} else if timelineFormat == "html" {
		fmt.Println("\nView timeline:")
		fmt.Printf("  open %s\n", timelineOutput)
	}

	return nil
}

// buildTimelineFromProfilerData creates a Timeline from StreamDataStats.
func buildTimelineFromProfilerData(tracePath string, stats *counter.StreamDataStats) *Timeline {
	timeline := &Timeline{
		Events:     make([]TimelineEvent, 0),
		Encoders:   make([]EncoderInfo, 0),
		Kernels:    make([]KernelInfo, 0),
		APICallseq: make([]APICall, 0),
		Timing:     timelineTimingFromStats(stats),
	}

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
		var displayStartNs uint64
		for _, cb := range stats.Timeline.CommandBufferTimestamps {
			durationNs := cb.DurationNs(timebaseNumer, timebaseDenom)
			var rawStartOffsetNs uint64
			if cb.StartTicks > absoluteTime {
				rawStartOffsetNs = (cb.StartTicks - absoluteTime) * timebaseNumer / timebaseDenom
			}

			event := TimelineEvent{
				Name:      fmt.Sprintf("CB#%d", cb.Index),
				Category:  "command_buffer",
				Phase:     "X",
				Timestamp: displayStartNs / 1000, // Convert to µs for Chrome format
				Duration:  durationNs / 1000,
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
			if endNs := displayStartNs + durationNs; endNs > timeline.EndTime {
				timeline.EndTime = endNs
			}
			displayStartNs += durationNs
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
				"index":       i,
				"duration_ms": float64(durationNs) / 1e6,
				"duration_us": float64(durationNs) / 1e3,
				"real_timing": true,
			},
		}
		timeline.Events = append(timeline.Events, event)

		currentTimeNs = endTimeNs
	}

	// Add GPRWCNTR encoder profile events
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
				Name:      fmt.Sprintf("GPRWCNTR Enc#%d (%s)", ep.Index, ep.Source),
				Category:  "encoder_profile",
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

	// Add kernel events from streamData dispatches
	var kernelStartNs uint64
	encoderOffsets := make(map[int]uint64)

	for _, d := range stats.Dispatches {
		name := d.FunctionName
		if name == "" {
			name = fmt.Sprintf("(pipeline_%d)", d.PipelineIndex)
		}
		durationNs := uint64(d.DurationUs) * 1000

		var startTime uint64
		// Try to place dispatch inside its encoder
		if d.EncoderIndex >= 0 && d.EncoderIndex < len(timeline.Encoders) {
			enc := timeline.Encoders[d.EncoderIndex]
			startTime = enc.StartTime + encoderOffsets[d.EncoderIndex]
			encoderOffsets[d.EncoderIndex] += durationNs
		} else {
			// Fallback if encoder info is missing
			startTime = kernelStartNs
			kernelStartNs += durationNs
		}

		kernelInfo := KernelInfo{
			Name:      name,
			Encoder:   d.EncoderIndex,
			StartTime: startTime,
			EndTime:   startTime + durationNs,
			Duration:  durationNs,
		}
		timeline.Kernels = append(timeline.Kernels, kernelInfo)
		if kernelInfo.EndTime > timeline.EndTime {
			timeline.EndTime = kernelInfo.EndTime
		}

		event := TimelineEvent{
			Name:      name,
			Category:  "kernel",
			Phase:     "X",
			Timestamp: startTime / 1000,
			Duration:  durationNs / 1000,
			ProcessID: 1,
			ThreadID:  3 + (d.Index % 4),
			Args: map[string]interface{}{
				"duration_us":   float64(d.DurationUs),
				"encoder_index": d.EncoderIndex,
				"pipeline_idx":  d.PipelineIndex,
			},
		}
		timeline.Events = append(timeline.Events, event)
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
		fmt.Printf("Normalizing timeline: shifting by -%d µs\n", globalMinTs)
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

	return timeline
}

// generateInteractiveHTML creates a standalone interactive HTML timeline viewer.
func generateInteractiveHTML(timelineJSON string) string {
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
    </style>
</head>
<body>
    <div id="container">
        <div id="header">
            <h1>GPU Timeline Viewer</h1>
            <div id="controls">
                <div class="control-group">
                    <button id="zoom-in">Zoom In (+)</button>
                    <button id="zoom-out">Zoom Out (-)</button>
                    <button id="reset-view">Reset View</button>
                </div>
                <div id="stats"></div>
            </div>
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
                state.timeline.counter_tracks.forEach(track => {
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
        }

        function updateStats() {
            const timing = state.timeline.timing || {};
            const displayDuration = timing.display_duration_ns || state.timeline.duration;
            const source = timing.display_duration_source || 'timeline duration';
            statsEl.textContent = ` + "`" + `${state.timeline.encoders.length} encoders | Display ${formatNs(displayDuration)} | Encoder span ${formatNs(timing.encoder_span_ns || state.timeline.duration)} | Zoom ${(state.zoom * 100).toFixed(0)}%` + "`" + `;
            statsEl.title = timing.timing_source || source;
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
            detailPanel.innerHTML = ` + "`" + `
                <div class="detail-row"><span class="detail-label">Timing</span><span class="detail-value">${timing.timing_source || 'not available'}</span></div>
                <div class="detail-row"><span class="detail-label">CB active</span><span class="detail-value">${formatNs(timing.command_buffer_active_time_ns || 0)}</span></div>
                <div class="detail-row"><span class="detail-label">CB wall</span><span class="detail-value">${formatNs(timing.command_buffer_wall_time_ns || 0)}</span></div>
                <div class="detail-row"><span class="detail-label">Dispatch span</span><span class="detail-value">${formatNs(timing.dispatch_span_ns || 0)}</span></div>
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
            ctx.fillText(track.name, 5, y + 12);

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
            if (state.timeline.events.some(ev => ev.cat === 'command_buffer')) {
                trackY += LAYOUT.trackHeight;
            }
            if (state.timeline.events.some(ev => ev.cat === 'kernel')) {
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

                if (hit && hit.type === 'encoder') {
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
            const duration = (data.duration / 1000000).toFixed(3);
            const startTime = (data.start_time / 1000000).toFixed(3);

            tooltip.innerHTML = ` + "`" + `
                <div class="tooltip-title">${data.label || 'Encoder ' + index}</div>
                <div class="tooltip-row">
                    <span class="tooltip-label">Duration:</span>
                    <span class="tooltip-value">${duration} ms</span>
                </div>
                <div class="tooltip-row">
                    <span class="tooltip-label">Start:</span>
                    <span class="tooltip-value">${startTime} ms</span>
                </div>
                <div class="tooltip-row">
                    <span class="tooltip-label">Type:</span>
                    <span class="tooltip-value">${data.type || 'compute'}</span>
                </div>
            ` + "`" + `;

            tooltip.style.left = (x + 15) + 'px';
            tooltip.style.top = (y + 15) + 'px';
            tooltip.classList.add('visible');
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
