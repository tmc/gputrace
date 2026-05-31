package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/spf13/cobra"

	"github.com/tmc/gputrace"
)

var (
	bufferTimelineFormat string
	bufferTimelineWidth  int
	bufferTimelineOutput string
)

const minBufferTimelineWidth = 21

var bufferTimelineCmd = &cobra.Command{
	Use:   "buffer-timeline <trace.gputrace>",
	Short: "Visualize buffer allocation and usage timeline",
	Long: `Analyze and visualize buffer lifecycle events across the trace.

This command extracts buffer allocation, usage, and deallocation patterns
and presents them in various formats:

  - ASCII: Terminal-based bar chart showing buffer lifetimes
  - summary: Text summary with statistics and top buffers
  - chrome: Chrome tracing format for ui.perfetto.dev
  - json: Raw JSON data

The timeline shows:
  - Buffer allocation/deallocation times
  - Memory usage over time
  - Peak memory usage
  - Buffer sizes and usage patterns

Examples:
  # Show ASCII timeline
  gputrace buffer-timeline trace.gputrace

  # Export to Chrome tracing format
  gputrace buffer-timeline trace.gputrace --format chrome -o buffers.json

  # Show summary statistics
  gputrace buffer-timeline trace.gputrace --format summary

  # Wider ASCII display
  gputrace buffer-timeline trace.gputrace --width 120`,
	Args: cobra.ExactArgs(1),
	RunE: runBufferTimeline,
}

func init() {
	rootCmd.AddCommand(bufferTimelineCmd)

	bufferTimelineCmd.Flags().StringVarP(&bufferTimelineFormat, "format", "f", "ascii",
		"Output format: ascii, summary, chrome, json")
	bufferTimelineCmd.Flags().IntVarP(&bufferTimelineWidth, "width", "w", 100,
		"Width for ASCII visualization")
	bufferTimelineCmd.Flags().StringVarP(&bufferTimelineOutput, "output", "o", "",
		"Output file (default: stdout)")
}

func runBufferTimeline(cmd *cobra.Command, args []string) error {
	tracePath := args[0]

	if err := validateBufferTimelineWidth(bufferTimelineWidth); err != nil {
		return err
	}

	// Verify trace file exists
	if err := checkTraceFile(tracePath); err != nil {
		return err
	}

	// Open trace
	trace, err := gputrace.Open(tracePath)
	if err != nil {
		return fmt.Errorf("failed to open trace: %w", err)
	}

	// Extract buffer timeline
	timeline, err := gputrace.ExtractBufferTimeline(trace)
	if err != nil {
		return fmt.Errorf("failed to extract buffer timeline: %w", err)
	}

	// Generate output based on format
	var output string
	switch bufferTimelineFormat {
	case "ascii":
		output = gputrace.FormatBufferTimelineASCII(timeline, bufferTimelineWidth)
	case "summary":
		output = gputrace.FormatBufferTimelineSummary(timeline)
	case "chrome":
		output, err = formatBufferTimelineChrome(timeline)
		if err != nil {
			return fmt.Errorf("format chrome trace: %w", err)
		}
	case "json":
		output, err = formatBufferTimelineJSON(timeline)
		if err != nil {
			return fmt.Errorf("format json: %w", err)
		}
	default:
		return fmt.Errorf("unknown format: %s (valid: ascii, summary, chrome, json)", bufferTimelineFormat)
	}

	return writeBufferTimelineOutput(bufferTimelineOutput, output)
}

func validateBufferTimelineWidth(width int) error {
	if width < minBufferTimelineWidth {
		return fmt.Errorf("--width must be >= %d", minBufferTimelineWidth)
	}
	return nil
}

func writeBufferTimelineOutput(outputPath, output string) error {
	writer, closeOutput, err := createCommandOutput(outputPath)
	if err != nil {
		return err
	}
	if closeOutput != nil {
		defer closeOutput()
	}
	_, err = io.WriteString(writer, output)
	return err
}

type bufferTimelineJSON struct {
	TotalBuffers     int                        `json:"total_buffers"`
	PeakMemoryBytes  uint64                     `json:"peak_memory_bytes"`
	PeakMemoryMB     float64                    `json:"peak_memory_mb"`
	TotalAllocations int                        `json:"total_allocations"`
	AverageLifetime  float64                    `json:"average_lifetime_records"`
	MinRecordIndex   int                        `json:"min_record_index"`
	MaxRecordIndex   int                        `json:"max_record_index"`
	Buffers          []bufferTimelineJSONBuffer `json:"buffers"`
}

type bufferTimelineJSONBuffer struct {
	Address         string `json:"address"`
	FirstSeen       int    `json:"first_seen_record"`
	LastSeen        int    `json:"last_seen_record"`
	LifetimeRecords int    `json:"lifetime_records"`
	AccessCount     int    `json:"access_count"`
	EncoderIDs      []int  `json:"encoder_ids"`
	AccessIndices   []int  `json:"access_indices"`
	IsActive        bool   `json:"is_active"`
	SizeBytes       uint64 `json:"size_bytes,omitempty"`
}

type bufferTimelineChromeTrace struct {
	TraceEvents []TimelineEvent                 `json:"traceEvents"`
	Metadata    bufferTimelineChromeTraceHeader `json:"gputrace_buffer_timeline"`
}

type bufferTimelineChromeTraceHeader struct {
	TotalBuffers     int     `json:"total_buffers"`
	PeakMemoryBytes  uint64  `json:"peak_memory_bytes"`
	TotalAllocations int     `json:"total_allocations"`
	AverageLifetime  float64 `json:"average_lifetime_records"`
	MinRecordIndex   int     `json:"min_record_index"`
	MaxRecordIndex   int     `json:"max_record_index"`
	TimeUnit         string  `json:"time_unit"`
}

type bufferTimelineCounterDelta struct {
	recordIndex int
	activeDelta int
	bytesDelta  int64
}

func formatBufferTimelineJSON(timeline *gputrace.BufferTimelineAnalysis) (string, error) {
	doc := bufferTimelineJSON{
		TotalBuffers:     timeline.TotalBuffers,
		PeakMemoryBytes:  timeline.PeakMemoryBytes,
		PeakMemoryMB:     timeline.PeakMemoryMB,
		TotalAllocations: timeline.TotalAllocations,
		AverageLifetime:  timeline.AverageLifetime,
		MinRecordIndex:   timeline.MinRecordIndex,
		MaxRecordIndex:   timeline.MaxRecordIndex,
	}

	lifecycles := sortedBufferLifecycles(timeline)

	for _, lifecycle := range lifecycles {
		doc.Buffers = append(doc.Buffers, bufferTimelineJSONBuffer{
			Address:         fmt.Sprintf("0x%016x", lifecycle.Address),
			FirstSeen:       lifecycle.FirstSeen,
			LastSeen:        lifecycle.LastSeen,
			LifetimeRecords: lifecycle.LastSeen - lifecycle.FirstSeen,
			AccessCount:     lifecycle.AccessCount,
			EncoderIDs:      append([]int(nil), lifecycle.EncoderIDs...),
			AccessIndices:   append([]int(nil), lifecycle.AccessIndices...),
			IsActive:        lifecycle.IsActive,
			SizeBytes:       lifecycle.Size,
		})
	}

	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data) + "\n", nil
}

func formatBufferTimelineChrome(timeline *gputrace.BufferTimelineAnalysis) (string, error) {
	doc := bufferTimelineChromeTrace{
		Metadata: bufferTimelineChromeTraceHeader{
			TotalBuffers:     timeline.TotalBuffers,
			PeakMemoryBytes:  timeline.PeakMemoryBytes,
			TotalAllocations: timeline.TotalAllocations,
			AverageLifetime:  timeline.AverageLifetime,
			MinRecordIndex:   timeline.MinRecordIndex,
			MaxRecordIndex:   timeline.MaxRecordIndex,
			TimeUnit:         "record_index_as_microseconds",
		},
	}

	events := []TimelineEvent{
		{
			Name:      "process_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  0,
			Args: map[string]interface{}{
				"name": "Buffer Timeline",
			},
		},
		{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  0,
			Args: map[string]interface{}{
				"name": "Summary",
			},
		},
		{
			Name:      "Buffer Timeline Summary",
			Category:  "buffer_summary",
			Phase:     "i",
			Timestamp: 0,
			ProcessID: 1,
			ThreadID:  0,
			Args: map[string]interface{}{
				"total_buffers":            timeline.TotalBuffers,
				"total_allocations":        timeline.TotalAllocations,
				"peak_memory_bytes":        timeline.PeakMemoryBytes,
				"average_lifetime_records": timeline.AverageLifetime,
				"min_record_index":         timeline.MinRecordIndex,
				"max_record_index":         timeline.MaxRecordIndex,
				"time_unit":                "record_index_as_microseconds",
			},
		},
	}

	lifecycles := sortedBufferLifecycles(timeline)
	counterDeltas := make([]bufferTimelineCounterDelta, 0, len(lifecycles)*2)
	for i, lifecycle := range lifecycles {
		threadID := i + 1
		address := formatBufferAddress(lifecycle.Address)
		lifetime := lifecycle.LastSeen - lifecycle.FirstSeen
		duration := uint64(lifetime)
		if duration == 0 {
			duration = 1
		}

		events = append(events,
			TimelineEvent{
				Name:      "thread_name",
				Category:  "__metadata",
				Phase:     "M",
				ProcessID: 1,
				ThreadID:  threadID,
				Args: map[string]interface{}{
					"name": address,
				},
			},
			TimelineEvent{
				Name:      "Buffer " + address,
				Category:  "buffer_lifetime",
				Phase:     "X",
				Timestamp: normalizedBufferRecordTimestamp(lifecycle.FirstSeen, timeline.MinRecordIndex),
				Duration:  duration,
				ProcessID: 1,
				ThreadID:  threadID,
				Args: map[string]interface{}{
					"address":           address,
					"first_seen_record": lifecycle.FirstSeen,
					"last_seen_record":  lifecycle.LastSeen,
					"lifetime_records":  lifetime,
					"access_count":      lifecycle.AccessCount,
					"encoder_ids":       append([]int(nil), lifecycle.EncoderIDs...),
					"is_active":         lifecycle.IsActive,
					"size_bytes":        lifecycle.Size,
				},
			},
		)

		for _, accessIndex := range lifecycle.AccessIndices {
			events = append(events, TimelineEvent{
				Name:      "Buffer access",
				Category:  "buffer_access",
				Phase:     "i",
				Timestamp: normalizedBufferRecordTimestamp(accessIndex, timeline.MinRecordIndex),
				ProcessID: 1,
				ThreadID:  threadID,
				Args: map[string]interface{}{
					"address":      address,
					"record_index": accessIndex,
					"size_bytes":   lifecycle.Size,
				},
			})
		}

		counterDeltas = append(counterDeltas,
			bufferTimelineCounterDelta{
				recordIndex: lifecycle.FirstSeen,
				activeDelta: 1,
				bytesDelta:  int64(lifecycle.Size),
			},
			bufferTimelineCounterDelta{
				recordIndex: lifecycle.LastSeen,
				activeDelta: -1,
				bytesDelta:  -int64(lifecycle.Size),
			},
		)
	}

	if len(lifecycles) > 0 {
		counterThreadID := len(lifecycles) + 1
		events = append(events, TimelineEvent{
			Name:      "thread_name",
			Category:  "__metadata",
			Phase:     "M",
			ProcessID: 1,
			ThreadID:  counterThreadID,
			Args: map[string]interface{}{
				"name": "Observed buffer counters",
			},
		})
		events = append(events, bufferTimelineCounterEvents(counterDeltas, counterThreadID, timeline.MinRecordIndex)...)
	}

	doc.TraceEvents = events
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data) + "\n", nil
}

func sortedBufferLifecycles(timeline *gputrace.BufferTimelineAnalysis) []*gputrace.BufferLifecycle {
	lifecycles := make([]*gputrace.BufferLifecycle, 0, len(timeline.BufferEvents))
	for _, lifecycle := range timeline.BufferEvents {
		lifecycles = append(lifecycles, lifecycle)
	}
	sort.Slice(lifecycles, func(i, j int) bool {
		if lifecycles[i].FirstSeen != lifecycles[j].FirstSeen {
			return lifecycles[i].FirstSeen < lifecycles[j].FirstSeen
		}
		return lifecycles[i].Address < lifecycles[j].Address
	})
	return lifecycles
}

func bufferTimelineCounterEvents(deltas []bufferTimelineCounterDelta, threadID, minRecordIndex int) []TimelineEvent {
	sort.Slice(deltas, func(i, j int) bool {
		if deltas[i].recordIndex != deltas[j].recordIndex {
			return deltas[i].recordIndex < deltas[j].recordIndex
		}
		if deltas[i].activeDelta != deltas[j].activeDelta {
			return deltas[i].activeDelta > deltas[j].activeDelta
		}
		return deltas[i].bytesDelta > deltas[j].bytesDelta
	})

	var events []TimelineEvent
	var activeBuffers int
	var knownBytes int64
	for _, delta := range deltas {
		activeBuffers += delta.activeDelta
		knownBytes += delta.bytesDelta
		if knownBytes < 0 {
			knownBytes = 0
		}
		events = append(events, TimelineEvent{
			Name:      "Observed buffer counters",
			Category:  "buffer_counter",
			Phase:     "C",
			Timestamp: normalizedBufferRecordTimestamp(delta.recordIndex, minRecordIndex),
			ProcessID: 1,
			ThreadID:  threadID,
			Args: map[string]interface{}{
				"active_buffers":     activeBuffers,
				"known_memory_bytes": knownBytes,
			},
		})
	}
	return events
}

func normalizedBufferRecordTimestamp(recordIndex, minRecordIndex int) uint64 {
	if recordIndex <= minRecordIndex {
		return 0
	}
	return uint64(recordIndex - minRecordIndex)
}

func formatBufferAddress(address uint64) string {
	return fmt.Sprintf("0x%016x", address)
}
