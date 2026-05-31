package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/spf13/cobra"

	"github.com/tmc/gputrace"
)

var (
	bufferTimelineFormat string
	bufferTimelineWidth  int
	bufferTimelineOutput string
)

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
		return fmt.Errorf("chrome trace format not yet implemented")
	case "json":
		output, err = formatBufferTimelineJSON(timeline)
		if err != nil {
			return fmt.Errorf("format json: %w", err)
		}
	default:
		return fmt.Errorf("unknown format: %s (valid: ascii, summary, chrome, json)", bufferTimelineFormat)
	}

	// Output to file or stdout
	if bufferTimelineOutput != "" {
		return os.WriteFile(bufferTimelineOutput, []byte(output), 0644)
	}

	fmt.Print(output)
	return nil
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
