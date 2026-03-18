package command

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
