package analysis

import (
	"fmt"
	"sort"

	"github.com/tmc/gputrace/internal/trace"
)

// BufferAccessAnalysis contains buffer access pattern analysis results.
//
// Bindings are grouped by CS ordinal, not by encoder. See BindingGroupInfo:
// those are different populations and nothing here maps between them.
type BufferAccessAnalysis struct {
	BufferAccesses      map[uint64]*BufferAccessInfo `json:"buffer_accesses"`
	BindingGroups       map[int]*BindingGroupInfo    `json:"binding_groups"`
	TotalBuffers        int                          `json:"total_buffers"`
	UnusedBuffers       int                          `json:"unused_buffers"`
	ReadOnlyBuffers     int                          `json:"read_only_buffers"`
	SharedBuffers       int                          `json:"shared_buffers"`
	AliasingDetected    bool                         `json:"aliasing_detected"`
	AliasingInstances   []BufferAlias                `json:"aliasing_instances,omitempty"`
	ExpectedEncoders    int                          `json:"expected_encoders"`
	AttributedGroups    int                          `json:"attributed_groups"`
	AttributionComplete bool                         `json:"attribution_complete"`
	AttributionNote     string                       `json:"attribution_note"`
}

// BufferAccessInfo tracks access patterns for a single buffer.
type BufferAccessInfo struct {
	Address     uint64 `json:"address"`
	AccessCount int    `json:"access_count"`
	// GroupOrdinals holds the CS ordinals of the binding groups that
	// referenced this buffer. They are not encoder indices.
	GroupOrdinals []int `json:"group_ordinals"`
	FirstAccess   int   `json:"first_access"`
	LastAccess    int   `json:"last_access"`
	IsShared      bool  `json:"is_shared"`
}

// BindingGroupInfo holds the buffers bound within one CS ordinal.
//
// CSOrdinal is a running count of CS records at the point the bindings were
// seen. It is not an encoder index and must not be joined to one. On the one
// trace where both sides were measured, a capture with three compute encoders
// carried ten CS records and produced groups at ordinals 3, 6, 9 and 10: four
// groups against three encoders, numbered in a space that shares no value with
// the encoder indices reported by profiler, timing, or the timeline.
//
// Recovering an encoder from an ordinal needs the CS grouping rule, which one
// capture cannot establish, so this type deliberately offers no mapping. The
// field was called EncoderID and rendered as "Encoder 3", which is how a
// reader arrives at a join that silently mislabels which kernel touched which
// buffer.
type BindingGroupInfo struct {
	CSOrdinal     int      `json:"cs_ordinal"`
	BufferCount   int      `json:"buffer_count"`
	UniqueBuffers []uint64 `json:"unique_buffers"`
	RecordIndices []int    `json:"record_indices"`
}

// BufferAlias represents potential memory aliasing.
type BufferAlias struct {
	Address uint64 `json:"address"`
	// Groups holds CS ordinals, not encoder indices.
	Groups  []int `json:"groups"`
	Indices []int `json:"indices"`
}

// AnalyzeBufferAccess analyzes buffer access patterns from Ct and Cul records.
func AnalyzeBufferAccess(t *trace.Trace) (*BufferAccessAnalysis, error) {
	analysis := &BufferAccessAnalysis{
		BufferAccesses: make(map[uint64]*BufferAccessInfo),
		BindingGroups:  make(map[int]*BindingGroupInfo),
	}

	// Parse MTSP records
	records, err := t.ParseMTSPRecords()
	if err != nil {
		return nil, fmt.Errorf("parse MTSP records: %w", err)
	}

	// Running count of CS records. This is the key bindings are grouped
	// under; it is not an encoder index. See BindingGroupInfo.
	csOrdinal := 0

	// Process each record
	for recordIdx, record := range records {
		switch record.Type {
		case trace.RecordTypeCS:
			csOrdinal++

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
						Address:       bufferAddr,
						FirstAccess:   recordIdx,
						GroupOrdinals: []int{},
					}
					analysis.BufferAccesses[bufferAddr] = bufInfo
				}

				bufInfo.AccessCount++
				bufInfo.LastAccess = recordIdx

				// Track which binding groups referenced this buffer
				if !containsInt(bufInfo.GroupOrdinals, csOrdinal) {
					bufInfo.GroupOrdinals = append(bufInfo.GroupOrdinals, csOrdinal)
				}

				// Update the binding group for this CS ordinal
				group, exists := analysis.BindingGroups[csOrdinal]
				if !exists {
					group = &BindingGroupInfo{
						CSOrdinal:     csOrdinal,
						UniqueBuffers: []uint64{},
						RecordIndices: []int{},
					}
					analysis.BindingGroups[csOrdinal] = group
				}

				if !containsUint64(group.UniqueBuffers, bufferAddr) {
					group.UniqueBuffers = append(group.UniqueBuffers, bufferAddr)
				}
				group.BufferCount++
				group.RecordIndices = append(group.RecordIndices, recordIdx)
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
	analysis.AttributedGroups = len(analysis.BindingGroups)
	// This decoder currently observes structured Ct bindings only. Cul and
	// other resource records are not decoded, so matching bucket counts alone
	// cannot prove complete attribution.
	analysis.AttributionComplete = false
	analysis.AttributionNote = fmt.Sprintf(
		"binding groups (by CS ordinal): %d; trace-reported compute encoders: %d; these are different populations and are not mapped to each other; Cul and other resource records are not attributed",
		analysis.AttributedGroups, analysis.ExpectedEncoders)

	return analysis, nil
}

// computeStatistics calculates summary statistics from collected data.
func (analysis *BufferAccessAnalysis) computeStatistics() {
	analysis.TotalBuffers = len(analysis.BufferAccesses)

	for _, bufInfo := range analysis.BufferAccesses {
		// Shared buffers (accessed by multiple encoders)
		if len(bufInfo.GroupOrdinals) > 1 {
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
		if len(bufInfo.GroupOrdinals) > 2 {
			// Multiple encoders accessing same buffer might indicate aliasing
			analysis.AliasingDetected = true
			analysis.AliasingInstances = append(analysis.AliasingInstances, BufferAlias{
				Address: addr,
				Groups:  bufInfo.GroupOrdinals,
				Indices: []int{bufInfo.FirstAccess, bufInfo.LastAccess},
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
	report += fmt.Sprintf("  Shared Buffers:  %d (bound in multiple binding groups)\n", analysis.SharedBuffers)
	report += fmt.Sprintf("  Unused Buffers:  %d\n", analysis.UnusedBuffers)
	report += fmt.Sprintf("  Binding Groups:  %d (keyed by CS ordinal, not encoder index)\n", len(analysis.BindingGroups))
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
				report += fmt.Sprintf("    [%d] Address 0x%016x bound in %d binding groups\n",
					i, alias.Address, len(alias.Groups))
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
					shareCount: len(info.GroupOrdinals),
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
			report += fmt.Sprintf("  [%d] 0x%016x - %d binding groups, %d accesses\n",
				i+1, buf.addr, buf.shareCount, buf.info.AccessCount)
		}
		report += "\n"
	}

	// Binding group statistics
	if verbose && len(analysis.BindingGroups) > 0 {
		report += "Per-Binding-Group Statistics:\n"

		// Sort encoders by ID
		var ordinals []int
		for id := range analysis.BindingGroups {
			ordinals = append(ordinals, id)
		}
		sort.Ints(ordinals)

		// Show all encoders in verbose mode, or top 10 in normal mode
		limit := len(ordinals)
		if !verbose && limit > 10 {
			limit = 10
		}

		for i := 0; i < limit; i++ {
			id := ordinals[i]
			group := analysis.BindingGroups[id]
			report += fmt.Sprintf("  CS ordinal %d: %d unique buffers, %d total accesses\n",
				group.CSOrdinal, len(group.UniqueBuffers), group.BufferCount)
		}

		if !verbose && len(ordinals) > 10 {
			report += fmt.Sprintf("  ... and %d more binding groups (use -v to see all)\n", len(ordinals)-10)
		}
		report += "\n"
	}

	if !analysis.AttributionComplete {
		report += "Interpretation:\n"
		report += "  Optimization advice withheld because encoder attribution is incomplete.\n"
		report += "  Binding groups are keyed by CS ordinal. Joining them to the encoder\n"
		report += "  indices from profiler, timing, or timeline would mislabel which kernel\n"
		report += "  touched which buffer.\n"
		report += "  Treat access counts as observed buffer references, not a complete usage model.\n"
		return report
	}
	// Optimization recommendations
	report += "Heuristic Opportunities (validate before acting):\n"
	if analysis.SharedBuffers > 0 {
		report += fmt.Sprintf("  • %d buffers are shared across binding groups\n", analysis.SharedBuffers)
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
