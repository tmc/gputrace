package gputrace

// DispatchCounts contains dispatch count information from a trace.
type DispatchCounts struct {
	// Direct dispatches from Ct records with flag 0xffffc01c
	DirectDispatches int

	// ICB executions (Ci records)
	ICBExecutions int

	// Estimated total dispatches (for compiled traces with ICBs)
	// Formula: ICBExecutions * AvgDispatchesPerICB
	EstimatedTotal int

	// Average dispatches per ICB (typically ~5)
	AvgDispatchesPerICB float64

	// Total Ct records (all types)
	TotalCtRecords int

	// Ct multiplier (TotalCtRecords / DirectDispatches)
	CtMultiplier float64

	// Command breakdown
	SetupCommands   int // 0xffffc02f
	BarrierCommands int // 0xffffc0fe
	FlushCommands   int // 0xffffc0ff
}

// CountDispatches analyzes a trace and counts dispatches using multiple methods.
func (t *Trace) CountDispatches() (*DispatchCounts, error) {
	records, err := t.ParseMTSPRecords()
	if err != nil {
		return nil, err
	}

	counts := &DispatchCounts{
		AvgDispatchesPerICB: 5.0, // Empirically determined from compiled traces
	}

	for _, rec := range records {
		if rec.Type == RecordTypeCi {
			counts.ICBExecutions++
		} else if rec.Type == RecordTypeCt {
			ct, err := rec.ParseCtRecord()
			if err != nil {
				continue
			}

			counts.TotalCtRecords++

			switch ct.CommandFlags {
			case 0xffffc01c:
				counts.DirectDispatches++
			case 0xffffc02f:
				counts.SetupCommands++
			case 0xffffc0fe:
				counts.BarrierCommands++
			case 0xffffc0ff:
				counts.FlushCommands++
			}
		}
	}

	// Calculate estimated total for traces with ICBs
	if counts.ICBExecutions > 0 {
		counts.EstimatedTotal = int(float64(counts.ICBExecutions) * counts.AvgDispatchesPerICB)
	} else {
		// No ICBs, use direct dispatch count
		counts.EstimatedTotal = counts.DirectDispatches
	}

	// Calculate Ct multiplier
	if counts.DirectDispatches > 0 {
		counts.CtMultiplier = float64(counts.TotalCtRecords) / float64(counts.DirectDispatches)
	}

	return counts, nil
}

// GetBestDispatchCount returns the most accurate dispatch count estimate.
// For compiled traces with ICBs, returns estimated total.
// For non-compiled traces, returns direct dispatch count.
func (dc *DispatchCounts) GetBestDispatchCount() int {
	if dc.ICBExecutions > 0 {
		return dc.EstimatedTotal
	}
	return dc.DirectDispatches
}

// IsCompiledTrace returns true if the trace appears to use graph compilation
// (indicated by presence of ICB executions).
func (dc *DispatchCounts) IsCompiledTrace() bool {
	return dc.ICBExecutions > 0
}
