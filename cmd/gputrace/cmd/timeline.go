package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/tmc/mlx-go/experiments/gputrace"
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
  - chrome: Chrome tracing format (chrome://tracing)
  - json: Raw timeline data in JSON format

Examples:
  # Generate Chrome tracing format
  gputrace timeline trace.gputrace -o timeline.json

  # View in Chrome
  # 1. Open chrome://tracing in Chrome
  # 2. Click "Load" and select timeline.json
  # 3. Use WASD keys to navigate, mouse wheel to zoom

  # Generate raw JSON for custom processing
  gputrace timeline trace.gputrace -o timeline.json --format json`,
	Args: cobra.ExactArgs(1),
	RunE: runTimeline,
}

func init() {
	rootCmd.AddCommand(timelineCmd)

	timelineCmd.Flags().StringVarP(&timelineOutput, "output", "o", "timeline.json", "Output file path")
	timelineCmd.Flags().StringVar(&timelineFormat, "format", "chrome", "Output format: chrome, json")
}

func runTimeline(cmd *cobra.Command, args []string) error {
	tracePath := args[0]

	// Verify trace file exists
	if err := checkTraceFile(tracePath); err != nil {
		return err
	}

	// Open trace
	trace, err := gputrace.Open(tracePath)
	if err != nil {
		return fmt.Errorf("failed to open trace: %w", err)
	}

	// Generate timeline data
	timeline, err := generateTimeline(trace)
	if err != nil {
		return fmt.Errorf("failed to generate timeline: %w", err)
	}

	// Export based on format
	switch timelineFormat {
	case "chrome":
		if err := exportChromeTracing(timeline, timelineOutput); err != nil {
			return fmt.Errorf("failed to export Chrome tracing: %w", err)
		}
	case "json":
		if err := exportTimelineJSON(timeline, timelineOutput); err != nil {
			return fmt.Errorf("failed to export JSON: %w", err)
		}
	default:
		return fmt.Errorf("unknown format: %s (supported: chrome, json)", timelineFormat)
	}

	fmt.Printf("✓ Timeline written to: %s\n", timelineOutput)
	if timelineFormat == "chrome" {
		fmt.Println("\nView in Chrome:")
		fmt.Println("  1. Open chrome://tracing")
		fmt.Println("  2. Click 'Load' and select", timelineOutput)
		fmt.Println("  3. Use WASD to navigate, mouse wheel to zoom")
	}

	return nil
}

// Timeline represents the complete timeline data.
type Timeline struct {
	StartTime      uint64                `json:"start_time"`
	EndTime        uint64                `json:"end_time"`
	Duration       uint64                `json:"duration"`
	Events         []TimelineEvent       `json:"events"`
	Encoders       []EncoderInfo         `json:"encoders"`
	Kernels        []KernelInfo          `json:"kernels"`
	APICallseq     []APICall             `json:"api_calls"`
	CounterTracks  []CounterTrack        `json:"counter_tracks,omitempty"`
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
	Name        string          `json:"name"`
	Unit        string          `json:"unit"`        // %, GB/s, count, etc.
	Samples     []CounterSample `json:"samples"`
	MinValue    float64         `json:"min_value"`
	MaxValue    float64         `json:"max_value"`
	AvgValue    float64         `json:"avg_value"`
}

// CounterSample represents a single counter measurement at a point in time.
type CounterSample struct {
	Timestamp uint64  `json:"ts"`  // Timestamp in nanoseconds
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

	// Extract timing metrics
	extractor := gputrace.NewTimingMetricsExtractor(trace)
	metrics, err := extractor.Extract()
	if err != nil {
		return nil, fmt.Errorf("extract timing: %w", err)
	}

	// Calculate timeline bounds
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

	// Add encoder events
	for i, encoder := range metrics.EncoderTimings {
		encoderInfo := EncoderInfo{
			Index:     i,
			Label:     encoder.Label,
			Type:      "compute", // Default type
			StartTime: encoder.StartTimestamp,
			EndTime:   encoder.EndTimestamp,
			Duration:  encoder.DurationNs,
		}
		timeline.Encoders = append(timeline.Encoders, encoderInfo)

		// Create timeline event for encoder
		event := TimelineEvent{
			Name:      encoder.Label,
			Category:  "encoder",
			Phase:     "X", // Complete event
			Timestamp: encoder.StartTimestamp / 1000, // Convert to microseconds
			Duration:  encoder.DurationNs / 1000,     // Convert to microseconds
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

	// Add kernel events (if we have kernel-level timing)
	if len(metrics.KernelTimings) > 0 {
		// Distribute kernels across encoder timeline
		// This is approximate since we don't have exact per-invocation timing
		for i, kernel := range metrics.KernelTimings {
			encoderIdx := i % len(timeline.Encoders)
			if len(timeline.Encoders) == 0 {
				break
			}

			encoder := timeline.Encoders[encoderIdx]
			// Create kernel event within encoder timeframe
			kernelInfo := KernelInfo{
				Name:      kernel.Name,
				Encoder:   encoderIdx,
				StartTime: encoder.StartTime,
				EndTime:   encoder.EndTime,
				Duration:  uint64(kernel.AvgDuration.Nanoseconds()),
			}
			timeline.Kernels = append(timeline.Kernels, kernelInfo)

			// Create timeline event for kernel
			event := TimelineEvent{
				Name:      kernel.Name,
				Category:  "kernel",
				Phase:     "X",
				Timestamp: encoder.StartTime / 1000, // Convert to microseconds
				Duration:  uint64(kernel.AvgDuration.Microseconds()),
				ProcessID: 1,
				ThreadID:  2, // Use different thread for kernels
				Args: map[string]interface{}{
					"invocations": kernel.InvocationCount,
					"avg_ns":      kernel.AvgDuration.Nanoseconds(),
					"min_ns":      kernel.MinDuration.Nanoseconds(),
					"max_ns":      kernel.MaxDuration.Nanoseconds(),
					"avg_us":      kernel.AvgDuration.Microseconds(),
				},
			}
			timeline.Events = append(timeline.Events, event)
		}
	}

	// Add command buffer events
	commandBuffers, err := trace.ParseCommandBuffers()
	if err == nil {
		for i, cb := range commandBuffers {
			event := TimelineEvent{
				Name:      fmt.Sprintf("CommandBuffer %d", i),
				Category:  "command_buffer",
				Phase:     "i", // Instant event
				Timestamp: uint64(cb.Offset),
				ProcessID: 1,
				ThreadID:  0, // Use thread 0 for command buffers
				Args: map[string]interface{}{
					"offset": cb.Offset,
					"index":  i,
				},
			}
			timeline.Events = append(timeline.Events, event)
		}
	}

	// Generate performance counter tracks
	timeline.CounterTracks = generateCounterTracks(trace, timeline)

	return timeline, nil
}

// generateCounterTracks creates performance counter tracks for the timeline.
func generateCounterTracks(trace *gputrace.Trace, timeline *Timeline) []CounterTrack {
	tracks := make([]CounterTrack, 0)

	// Skip if no encoders (can't generate meaningful counter data)
	if len(timeline.Encoders) == 0 {
		return tracks
	}

	// Try to use real performance counter data first
	perfStats, err := trace.ParsePerfCounters()
	if err == nil && len(perfStats.ShaderMetrics) > 0 {
		return generateCounterTracksFromPerfData(perfStats, timeline)
	}

	// Fallback to synthetic counter tracks based on encoder activity
	return generateSyntheticCounterTracks(timeline)
}

// generateCounterTracksFromPerfData creates counter tracks from real performance counter data.
func generateCounterTracksFromPerfData(perfStats *gputrace.PerfCounterStats, timeline *Timeline) []CounterTrack {
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

	// Create a map of shader name to hardware metrics
	shaderMetricsMap := make(map[string]*gputrace.ShaderHardwareMetrics)
	for i := range perfStats.ShaderMetrics {
		metric := &perfStats.ShaderMetrics[i]
		if metric.ShaderName != "" {
			shaderMetricsMap[metric.ShaderName] = metric
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
		} else {
			// Use synthetic estimates as fallback
			activeCores = estimateActiveCores(encoder)
			occupancy = estimateOccupancy(encoder)
			aluUtil = estimateALUUtilization(encoder)
			bandwidth = estimateBandwidth(encoder)
			throughput = estimateThroughput(encoder)
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
	}

	// Calculate statistics for each track
	calculateTrackStats(&activeCoresTrack)
	calculateTrackStats(&occupancyTrack)
	calculateTrackStats(&aluTrack)
	calculateTrackStats(&bandwidthTrack)
	calculateTrackStats(&throughputTrack)

	tracks = append(tracks, activeCoresTrack, occupancyTrack, aluTrack, bandwidthTrack, throughputTrack)

	return tracks
}

// generateSyntheticCounterTracks creates synthetic counter tracks when real performance data is unavailable.
func generateSyntheticCounterTracks(timeline *Timeline) []CounterTrack {
	tracks := make([]CounterTrack, 0)

	// Track 1: Active Cores (0-8 for M-series GPUs)
	activeCoresTrack := CounterTrack{
		Name:    "Active Cores",
		Unit:    "count",
		Samples: make([]CounterSample, 0),
	}

	// Track 2: Occupancy (0-100%)
	occupancyTrack := CounterTrack{
		Name:    "Occupancy",
		Unit:    "%",
		Samples: make([]CounterSample, 0),
	}

	// Track 3: ALU Utilization (0-100%)
	aluTrack := CounterTrack{
		Name:    "ALU Utilization",
		Unit:    "%",
		Samples: make([]CounterSample, 0),
	}

	// Track 4: Memory Bandwidth (GB/s)
	bandwidthTrack := CounterTrack{
		Name:    "Bandwidth",
		Unit:    "GB/s",
		Samples: make([]CounterSample, 0),
	}

	// Track 5: Instruction Throughput
	throughputTrack := CounterTrack{
		Name:    "Instruction Throughput",
		Unit:    "%",
		Samples: make([]CounterSample, 0),
	}

	// Generate samples for each encoder period
	for _, encoder := range timeline.Encoders {
		// Use synthetic estimates
		activeCores := estimateActiveCores(encoder)
		occupancy := estimateOccupancy(encoder)
		aluUtil := estimateALUUtilization(encoder)
		bandwidth := estimateBandwidth(encoder)
		throughput := estimateThroughput(encoder)

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
	}

	// Calculate statistics for each track
	calculateTrackStats(&activeCoresTrack)
	calculateTrackStats(&occupancyTrack)
	calculateTrackStats(&aluTrack)
	calculateTrackStats(&bandwidthTrack)
	calculateTrackStats(&throughputTrack)

	tracks = append(tracks, activeCoresTrack, occupancyTrack, aluTrack, bandwidthTrack, throughputTrack)

	return tracks
}

// estimateActiveCores estimates the number of active GPU cores for an encoder.
func estimateActiveCores(encoder EncoderInfo) float64 {
	// Synthetic estimation: assume 4-8 cores active during execution
	// In reality, this would come from performance counters
	return 6.0
}

// estimateOccupancy estimates GPU occupancy percentage.
func estimateOccupancy(encoder EncoderInfo) float64 {
	// Synthetic estimation: 60-90% occupancy
	// In reality, this would come from performance counters
	return 75.0
}

// estimateALUUtilization estimates ALU utilization percentage.
func estimateALUUtilization(encoder EncoderInfo) float64 {
	// Synthetic estimation: 40-80% ALU utilization
	// In reality, this would come from performance counters
	return 65.0
}

// estimateBandwidth estimates memory bandwidth in GB/s.
func estimateBandwidth(encoder EncoderInfo) float64 {
	// Synthetic estimation: 50-150 GB/s for M-series GPUs
	// In reality, this would come from performance counters
	return 100.0
}

// estimateThroughput estimates instruction throughput percentage.
func estimateThroughput(encoder EncoderInfo) float64 {
	// Synthetic estimation: 50-90% throughput
	// In reality, this would come from performance counters
	return 70.0
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
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  0,
			Args: map[string]interface{}{
				"name": "GPU Trace",
			},
		},
		{
			Name:      "thread_name",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  0,
			Args: map[string]interface{}{
				"name": "Command Buffers",
			},
		},
		{
			Name:      "thread_name",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  1,
			Args: map[string]interface{}{
				"name": "Encoders",
			},
		},
		{
			Name:      "thread_name",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  2,
			Args: map[string]interface{}{
				"name": "Kernels",
			},
		},
	}

	// Add counter track metadata and events
	threadID := 3
	for _, track := range timeline.CounterTracks {
		// Add thread name for this counter track
		metadataEvents = append(metadataEvents, TimelineEvent{
			Name:      "thread_name",
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
				Phase:     "C", // Counter event
				Timestamp: sample.Timestamp / 1000, // Convert to microseconds
				ProcessID: 1,
				ThreadID:  threadID,
				Args: map[string]interface{}{
					track.Name: sample.Value,
					"unit":     track.Unit,
				},
			}
			timeline.Events = append(timeline.Events, counterEvent)
		}

		threadID++
	}

	// Combine metadata events with timeline events
	allEvents := append(metadataEvents, timeline.Events...)

	// Chrome tracing format
	tracing := map[string]interface{}{
		"traceEvents":     allEvents,
		"displayTimeUnit": "ms",
		"metadata": map[string]interface{}{
			"start_time":    timeline.StartTime,
			"end_time":      timeline.EndTime,
			"duration_ns":   timeline.Duration,
			"encoder_count": len(timeline.Encoders),
			"kernel_count":  len(timeline.Kernels),
		},
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
