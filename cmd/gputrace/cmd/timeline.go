package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/tmc/gputrace"
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
  - html: Interactive standalone HTML timeline viewer
  - json: Raw timeline data in JSON format
  - text: Hierarchical text output to stdout

Examples:
  # Generate interactive HTML timeline viewer
  gputrace timeline trace.gputrace -o timeline.html --format html

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
	timelineCmd.Flags().StringVar(&timelineFormat, "format", "chrome", "Output format: chrome, html, json, text")
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
		return fmt.Errorf("unknown format: %s (supported: chrome, html, json, text)", timelineFormat)
	}

	fmt.Printf("✓ Timeline written to: %s\n", timelineOutput)
	if timelineFormat == "chrome" {
		fmt.Println("\nView in Chrome:")
		fmt.Println("  1. Open chrome://tracing")
		fmt.Println("  2. Click 'Load' and select", timelineOutput)
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
		fmt.Printf("%s [%.1fms]\n", cb.Name, cbStart)

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
	APICallseq    []APICall       `json:"api_calls"`
	CounterTracks []CounterTrack  `json:"counter_tracks,omitempty"`
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
			Phase:     "X",                           // Complete event
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
	perfStats, err := gputrace.ParsePerfCounters(trace)
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
			// Use synthetic estimates as fallback
			activeCores = estimateActiveCores(encoder)
			occupancy = estimateOccupancy(encoder)
			aluUtil = estimateALUUtilization(encoder)
			bandwidth = estimateBandwidth(encoder)
			throughput = estimateThroughput(encoder)
			occupancyManager = estimateOccupancyManager(encoder)
			shaderLaunchLimiter = estimateShaderLaunchLimiter(encoder)
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

	// Track 6: Occupancy Manager
	occupancyManagerTrack := CounterTrack{
		Name:    "Occupancy Manager",
		Unit:    "%",
		Samples: make([]CounterSample, 0),
	}

	// Track 7: Shader Launch Limiter
	shaderLaunchLimiterTrack := CounterTrack{
		Name:    "Shader Launch Limiter",
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
		occupancyManager := estimateOccupancyManager(encoder)
		shaderLaunchLimiter := estimateShaderLaunchLimiter(encoder)

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

// estimateOccupancyManager estimates occupancy manager efficiency percentage.
func estimateOccupancyManager(encoder EncoderInfo) float64 {
	// Synthetic estimation: typically slightly lower than raw occupancy
	// Represents how well the GPU scheduler manages threadgroup dispatch
	// In reality, this would come from performance counters
	return 71.0
}

// estimateShaderLaunchLimiter estimates shader launch limiter percentage.
func estimateShaderLaunchLimiter(encoder EncoderInfo) float64 {
	// Synthetic estimation: percentage of time shader launches are resource-limited
	// High values indicate register pressure, threadgroup memory limits, etc.
	// In reality, this would come from performance counters
	return 25.0
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
				Phase:     "C",                     // Counter event
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
        }

        function updateStats() {
            const duration = (state.timeline.duration / 1000000).toFixed(2);
            statsEl.textContent = ` + "`" + `${state.timeline.encoders.length} encoders | ${duration} ms | Zoom: ${(state.zoom * 100).toFixed(0)}%` + "`" + `;
        }

        function selectEncoder(idx) {
            state.selectedEncoder = idx === state.selectedEncoder ? null : idx;

            // Update UI
            document.querySelectorAll('.encoder-item').forEach((item, i) => {
                item.classList.toggle('selected', i === state.selectedEncoder);
            });

            render();
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

            // Draw encoder tracks
            let y = LAYOUT.headerHeight;
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
