package analysis

import (
	"fmt"
	"sort"

	"github.com/tmc/gputrace/internal/trace"
)

// BufferAccessAnalysis contains buffer access pattern analysis results.
type BufferAccessAnalysis struct {
	BufferAccesses      map[uint64]*BufferAccessInfo `json:"buffer_accesses"`
	EncoderAccesses     map[int]*EncoderAccessInfo   `json:"encoder_accesses"`
	TotalBuffers        int                          `json:"total_buffers"`
	UnusedBuffers       int                          `json:"unused_buffers"`
	ReadOnlyBuffers     int                          `json:"read_only_buffers"`
	SharedBuffers       int                          `json:"shared_buffers"`
	AliasingDetected    bool                         `json:"aliasing_detected"`
	AliasingInstances   []BufferAlias                `json:"aliasing_instances,omitempty"`
	ExpectedEncoders    int                          `json:"expected_encoders"`
	AttributedEncoders  int                          `json:"attributed_encoders"`
	AttributionComplete bool                         `json:"attribution_complete"`
	AttributionNote     string                       `json:"attribution_note"`
}

// BufferAccessInfo tracks access patterns for a single buffer.
type BufferAccessInfo struct {
	Address     uint64 `json:"address"`
	AccessCount int    `json:"access_count"`
	EncoderIDs  []int  `json:"encoder_ids"`
	FirstAccess int    `json:"first_access"`
	LastAccess  int    `json:"last_access"`
	IsShared    bool   `json:"is_shared"`
}

// EncoderAccessInfo tracks buffer access for a single encoder.
type EncoderAccessInfo struct {
	EncoderID     int      `json:"encoder_id"`
	BufferCount   int      `json:"buffer_count"`
	UniqueBuffers []uint64 `json:"unique_buffers"`
	RecordIndices []int    `json:"record_indices"`
}

// BufferAlias represents potential memory aliasing.
type BufferAlias struct {
	Address  uint64 `json:"address"`
	Encoders []int  `json:"encoders"`
	Indices  []int  `json:"indices"`
}

// AnalyzeBufferAccess analyzes buffer access patterns from Ct and Cul records.
func AnalyzeBufferAccess(t *trace.Trace) (*BufferAccessAnalysis, error) {
	analysis := &BufferAccessAnalysis{
		BufferAccesses:  make(map[uint64]*BufferAccessInfo),
		EncoderAccesses: make(map[int]*EncoderAccessInfo),
	}

	// Parse MTSP records
	records, err := t.ParseMTSPRecords()
	if err != nil {
		return nil, fmt.Errorf("parse MTSP records: %w", err)
	}

	// Track current encoder (increments on each CS record)
	encoderID := 0

	// Process each record
	for recordIdx, record := range records {
		switch record.Type {
		case trace.RecordTypeCS:
			// New compute encoder
			encoderID++

		case trace.RecordTypeCt:
			// Parse Ct record to get buffer bindings
			ct, err := record.ParseCtRecord()
			if err != nil {
				continue
			}

			// Track buffer accesses
			for _, bufferAddr := range ct.BufferBindings {
				if bufferAddr == 0 {
					continue
				}

				// Update buffer access info
				bufInfo, exists := analysis.BufferAccesses[bufferAddr]
				if !exists {
					bufInfo = &BufferAccessInfo{
						Address:     bufferAddr,
						FirstAccess: recordIdx,
						EncoderIDs:  []int{},
					}
					analysis.BufferAccesses[bufferAddr] = bufInfo
				}

				bufInfo.AccessCount++
				bufInfo.LastAccess = recordIdx

				// Track encoder access
				if !containsInt(bufInfo.EncoderIDs, encoderID) {
					bufInfo.EncoderIDs = append(bufInfo.EncoderIDs, encoderID)
				}

				// Update encoder access info
				encInfo, exists := analysis.EncoderAccesses[encoderID]
				if !exists {
					encInfo = &EncoderAccessInfo{
						EncoderID:     encoderID,
						UniqueBuffers: []uint64{},
						RecordIndices: []int{},
					}
					analysis.EncoderAccesses[encoderID] = encInfo
				}

				if !containsUint64(encInfo.UniqueBuffers, bufferAddr) {
					encInfo.UniqueBuffers = append(encInfo.UniqueBuffers, bufferAddr)
				}
				encInfo.BufferCount++
				encInfo.RecordIndices = append(encInfo.RecordIndices, recordIdx)
			}

		case trace.RecordTypeCul:
			// Parse Cul record (similar structure to Ct for buffer tracking)
			// Cul records also contain resource bindings
			// For now, we focus on Ct records which are more structured
		}
	}

	// Compute summary statistics
	analysis.computeStatistics()
	analysis.ExpectedEncoders, _ = t.CountComputeEncoders()
	analysis.AttributedEncoders = len(analysis.EncoderAccesses)
	// This decoder currently observes structured Ct bindings only. Cul and
	// other resource records are not decoded, so matching bucket counts alone
	// cannot prove complete attribution.
	analysis.AttributionComplete = false
	analysis.AttributionNote = fmt.Sprintf(
		"attributed encoder buckets: %d; trace-reported compute encoders: %d; Cul and other resource records are not attributed",
		analysis.AttributedEncoders, analysis.ExpectedEncoders)

	return analysis, nil
}

// computeStatistics calculates summary statistics from collected data.
func (analysis *BufferAccessAnalysis) computeStatistics() {
	analysis.TotalBuffers = len(analysis.BufferAccesses)

	for _, bufInfo := range analysis.BufferAccesses {
		// Shared buffers (accessed by multiple encoders)
		if len(bufInfo.EncoderIDs) > 1 {
			analysis.SharedBuffers++
			bufInfo.IsShared = true
		}

		// Unused buffers (never accessed - though unlikely in Ct records)
		if bufInfo.AccessCount == 0 {
			analysis.UnusedBuffers++
		}
	}

	// Detect potential aliasing (same address accessed by different encoders with different patterns)
	// This is a heuristic - true aliasing requires deeper analysis
	for addr, bufInfo := range analysis.BufferAccesses {
		if len(bufInfo.EncoderIDs) > 2 {
			// Multiple encoders accessing same buffer might indicate aliasing
			analysis.AliasingDetected = true
			analysis.AliasingInstances = append(analysis.AliasingInstances, BufferAlias{
				Address:  addr,
				Encoders: bufInfo.EncoderIDs,
				Indices:  []int{bufInfo.FirstAccess, bufInfo.LastAccess},
			})
		}
	}
}

// FormatBufferAccessReport generates a human-readable report.
func FormatBufferAccessReport(analysis *BufferAccessAnalysis, verbose bool) string {
	report := "=== Buffer Access Analysis ===\n\n"

	// Summary statistics
	report += "Summary:\n"
	report += fmt.Sprintf("  Total Buffers:   %d\n", analysis.TotalBuffers)
	report += fmt.Sprintf("  Shared Buffers:  %d (accessed by multiple encoders)\n", analysis.SharedBuffers)
	report += fmt.Sprintf("  Unused Buffers:  %d\n", analysis.UnusedBuffers)
	report += fmt.Sprintf("  Total Encoders:  %d\n", len(analysis.EncoderAccesses))
	if analysis.AttributionComplete {
		report += "  Attribution:     complete\n"
	} else {
		report += "  Attribution:     incomplete\n"
		report += fmt.Sprintf("    %s\n", analysis.AttributionNote)
	}
	report += "\n"

	// Aliasing detection
	if analysis.AliasingDetected {
		report += "Memory Aliasing Detected:\n"
		report += fmt.Sprintf("  %d potential aliasing instances\n", len(analysis.AliasingInstances))
		if verbose {
			for i, alias := range analysis.AliasingInstances {
				report += fmt.Sprintf("    [%d] Address 0x%016x accessed by %d encoders\n",
					i, alias.Address, len(alias.Encoders))
			}
		}
		report += "\n"
	}

	// Top shared buffers
	if analysis.SharedBuffers > 0 {
		report += "Top Shared Buffers:\n"

		// Sort buffers by number of accessing encoders
		type bufferShare struct {
			addr       uint64
			info       *BufferAccessInfo
			shareCount int
		}
		var sharedBuffers []bufferShare
		for addr, info := range analysis.BufferAccesses {
			if info.IsShared {
				sharedBuffers = append(sharedBuffers, bufferShare{
					addr:       addr,
					info:       info,
					shareCount: len(info.EncoderIDs),
				})
			}
		}
		sort.Slice(sharedBuffers, func(i, j int) bool {
			return sharedBuffers[i].shareCount > sharedBuffers[j].shareCount
		})

		// Show top 10
		limit := 10
		if len(sharedBuffers) < limit {
			limit = len(sharedBuffers)
		}
		for i := 0; i < limit; i++ {
			buf := sharedBuffers[i]
			report += fmt.Sprintf("  [%d] 0x%016x - %d encoders, %d accesses\n",
				i+1, buf.addr, buf.shareCount, buf.info.AccessCount)
		}
		report += "\n"
	}

	// Encoder statistics
	if verbose && len(analysis.EncoderAccesses) > 0 {
		report += "Per-Encoder Statistics:\n"

		// Sort encoders by ID
		var encoderIDs []int
		for id := range analysis.EncoderAccesses {
			encoderIDs = append(encoderIDs, id)
		}
		sort.Ints(encoderIDs)

		// Show all encoders in verbose mode, or top 10 in normal mode
		limit := len(encoderIDs)
		if !verbose && limit > 10 {
			limit = 10
		}

		for i := 0; i < limit; i++ {
			id := encoderIDs[i]
			encInfo := analysis.EncoderAccesses[id]
			report += fmt.Sprintf("  Encoder %d: %d unique buffers, %d total accesses\n",
				encInfo.EncoderID, len(encInfo.UniqueBuffers), encInfo.BufferCount)
		}

		if !verbose && len(encoderIDs) > 10 {
			report += fmt.Sprintf("  ... and %d more encoders (use -v to see all)\n", len(encoderIDs)-10)
		}
		report += "\n"
	}

	if !analysis.AttributionComplete {
		report += "Interpretation:\n"
		report += "  Optimization advice withheld because encoder attribution is incomplete.\n"
		report += "  Treat access counts as observed buffer references, not a complete usage model.\n"
		return report
	}
	// Optimization recommendations
	report += "Heuristic Opportunities (validate before acting):\n"
	if analysis.SharedBuffers > 0 {
		report += fmt.Sprintf("  • %d buffers are shared across encoders\n", analysis.SharedBuffers)
		report += "    Consider analyzing access patterns for potential memory reuse\n"
	}
	if analysis.UnusedBuffers > 0 {
		report += fmt.Sprintf("  • %d buffers allocated but never accessed\n", analysis.UnusedBuffers)
		report += "    These could be removed to reduce memory usage\n"
	}
	if analysis.AliasingDetected {
		report += fmt.Sprintf("  • %d potential memory aliasing instances detected\n", len(analysis.AliasingInstances))
		report += "    Review these for correctness and potential optimization\n"
	}
	if analysis.SharedBuffers == 0 && analysis.UnusedBuffers == 0 && !analysis.AliasingDetected {
		report += "  • No obvious optimization opportunities detected\n"
		report += "    Buffer access patterns appear well-optimized\n"
	}

	return report
}

// Helper functions

func containsInt(slice []int, val int) bool {
	for _, item := range slice {
		if item == val {
			return true
		}
	}
	return false
}

func containsUint64(slice []uint64, val uint64) bool {
	for _, item := range slice {
		if item == val {
			return true
		}
	}
	return false
}
