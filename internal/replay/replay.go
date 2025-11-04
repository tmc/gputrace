package replay

import (
	"fmt"
	"sort"

	"github.com/tmc/mlx-go/experiments/gputrace/internal/counter"
	"github.com/tmc/mlx-go/experiments/gputrace/internal/trace"
)

// Type aliases for commonly used types from other packages
type (
	Trace                 = trace.Trace
	CounterSampler        = counter.CounterSampler
	CounterSamplingConfig = counter.CounterSamplingConfig
	CounterSamplingResult = counter.CounterSamplingResult
	RecordType            = trace.RecordType
)

// Re-export common record type constants from mtsp.go
const (
	RecordTypeCt     = "Ct"
	RecordTypeCi     = "Ci"
	RecordTypeCS     = "CS"
	RecordTypeCulul  = "Culul"
	RecordTypeCU     = "CU"
	RecordTypeCul    = "Cul"
	RecordTypeCut    = "Cut"
	RecordTypeCuw    = "Cuw"
)

// Function aliases from counter package
var (
	DefaultCounterSamplingConfig = counter.DefaultCounterSamplingConfig
	NewCounterSampler            = counter.NewCounterSampler
)

// ReplayEngine orchestrates GPU trace replay from .gputrace files.
// This implementation provides analysis and structure extraction without
// requiring Metal bindings, serving as the foundation for actual GPU replay.
type ReplayEngine struct {
	Trace        *Trace
	State        *ReplayState
	Commands     []ReplayCommand
	Encoders     []ReplayEncoderInfo
	CommandQueue CommandQueueInfo

	// Counter sampling support (optional)
	CounterSampler *CounterSampler
}

// ReplayCommand represents a reconstructed Metal command from the trace.
type ReplayCommand struct {
	Type         string // "compute_dispatch", "bind_buffer", "set_pipeline"
	Offset       int    // Offset in capture file
	SequenceNum  int    // Execution order
	EncoderIndex int    // Which encoder this belongs to

	// Command-specific data
	PipelineAddr   uint64   // For set_pipeline commands
	FunctionAddr   uint64   // For compute dispatches
	FunctionName   string   // Kernel function name
	BufferBindings []uint64 // Buffer addresses bound to this command

	// Dispatch parameters (for compute dispatches)
	ThreadsPerGrid        [3]uint32 // Total threads (x, y, z)
	ThreadsPerThreadgroup [3]uint32 // Threadgroup size (x, y, z)

	// Indirect command buffer (for ICB references)
	ICBAddr  uint64
	ICBCount uint32
}

// ReplayEncoderInfo represents a command encoder in the trace.
type ReplayEncoderInfo struct {
	Index        int
	Type         string // "compute", "render", "blit"
	Label        string // Debug label if available
	CommandCount int    // Number of commands in this encoder
	StartOffset  int    // Offset in capture where encoder starts
	EndOffset    int    // Offset where encoder ends
}

// CommandQueueInfo represents the Metal command queue.
type CommandQueueInfo struct {
	Label          string
	CommandBuffers int // Number of command buffers
}

// NewReplayEngine creates a new replay engine for the given trace.
func NewReplayEngine(trace *Trace) *ReplayEngine {
	return &ReplayEngine{
		Trace:    trace,
		State:    NewReplayState(trace),
		Commands: make([]ReplayCommand, 0),
		Encoders: make([]ReplayEncoderInfo, 0),
	}
}

// AnalyzeReplay performs complete analysis of what would be replayed.
// This extracts the command structure without requiring actual Metal execution.
func (re *ReplayEngine) AnalyzeReplay() (*ReplayPlan, error) {
	plan := &ReplayPlan{
		TraceePath: re.Trace.Path,
		Commands:   make([]ReplayCommand, 0),
		Encoders:   make([]ReplayEncoderInfo, 0),
	}

	// Parse MTSP records to extract command structure
	records, err := re.Trace.ParseMTSPRecords()
	if err != nil {
		return nil, fmt.Errorf("parse MTSP records: %w", err)
	}

	// Analyze state restoration requirements
	stateAnalysis, err := re.State.RestoreState()
	if err != nil {
		return nil, fmt.Errorf("analyze state: %w", err)
	}
	plan.StateAnalysis = stateAnalysis

	// Extract command sequence
	sequenceNum := 0
	encoderIndex := 0
	currentEncoder := ReplayEncoderInfo{
		Index: encoderIndex,
		Type:  "compute", // Default assumption
	}

	for _, record := range records {
		switch record.Type {
		case RecordTypeCt:
			// Ct records represent compute dispatches
			ct, err := record.ParseCtRecord()
			if err != nil {
				continue // Skip malformed records
			}

			// This is a dispatch command
			cmd := ReplayCommand{
				Type:           "compute_dispatch",
				Offset:         record.Offset,
				SequenceNum:    sequenceNum,
				EncoderIndex:   encoderIndex,
				PipelineAddr:   ct.PipelineAddr,
				FunctionAddr:   ct.FunctionAddr,
				BufferBindings: ct.BufferBindings,
			}

			// Try to resolve function name
			if stateAnalysis != nil {
				for _, fn := range stateAnalysis.Functions {
					if fn.Address == ct.FunctionAddr {
						cmd.FunctionName = fn.Name
						break
					}
				}
			}

			plan.Commands = append(plan.Commands, cmd)
			currentEncoder.CommandCount++
			sequenceNum++

		case RecordTypeCi:
			// Ci records represent indirect command buffer execution
			ci, err := record.ParseCiRecord()
			if err != nil {
				continue
			}

			cmd := ReplayCommand{
				Type:         "execute_icb",
				Offset:       record.Offset,
				SequenceNum:  sequenceNum,
				EncoderIndex: encoderIndex,
				ICBAddr:      ci.ICBAddr,
				ICBCount:     ci.Count,
			}

			plan.Commands = append(plan.Commands, cmd)
			currentEncoder.CommandCount++
			sequenceNum++

		case RecordTypeCulul:
			// Culul records mark encoder boundaries or ICB definitions
			// Start a new encoder
			if currentEncoder.CommandCount > 0 {
				plan.Encoders = append(plan.Encoders, currentEncoder)
				encoderIndex++
			}

			currentEncoder = ReplayEncoderInfo{
				Index:       encoderIndex,
				Type:        "compute",
				StartOffset: record.Offset,
			}

		case RecordTypeCS:
			// CS records contain labels - associate with current encoder
			if record.Label != "" && currentEncoder.Label == "" {
				currentEncoder.Label = record.Label
			}
		}
	}

	// Add final encoder
	if currentEncoder.CommandCount > 0 {
		plan.Encoders = append(plan.Encoders, currentEncoder)
	}

	// Set command queue label
	plan.CommandQueue.Label = re.Trace.CommandQueueLabel
	if plan.CommandQueue.Label == "" {
		plan.CommandQueue.Label = "ReplayQueue"
	}
	plan.CommandQueue.CommandBuffers = len(plan.Encoders)

	// Calculate statistics
	plan.TotalCommands = len(plan.Commands)
	plan.TotalEncoders = len(plan.Encoders)
	plan.ComputeDispatches = 0
	plan.ICBExecutions = 0

	for _, cmd := range plan.Commands {
		switch cmd.Type {
		case "compute_dispatch":
			plan.ComputeDispatches++
		case "execute_icb":
			plan.ICBExecutions++
		}
	}

	return plan, nil
}

// ReplayPlan describes what would be executed during replay.
type ReplayPlan struct {
	TraceePath    string
	Commands      []ReplayCommand
	Encoders      []ReplayEncoderInfo
	CommandQueue  CommandQueueInfo
	StateAnalysis *ReplayAnalysis

	// Statistics
	TotalCommands     int
	TotalEncoders     int
	ComputeDispatches int
	ICBExecutions     int
}

// GetComputeDispatches returns all compute dispatch commands from the plan.
func (plan *ReplayPlan) GetComputeDispatches() []ReplayCommand {
	var dispatches []ReplayCommand
	for _, cmd := range plan.Commands {
		if cmd.Type == "compute_dispatch" {
			dispatches = append(dispatches, cmd)
		}
	}
	return dispatches
}

// ValidateReplay checks if the trace can be replayed.
func (re *ReplayEngine) ValidateReplay() (*ReplayValidation, error) {
	validation := &ReplayValidation{
		CanReplay: true,
		Errors:    make([]string, 0),
		Warnings:  make([]string, 0),
	}

	// Analyze replay plan
	plan, err := re.AnalyzeReplay()
	if err != nil {
		validation.CanReplay = false
		validation.Errors = append(validation.Errors, fmt.Sprintf("analyze replay: %v", err))
		return validation, nil
	}

	// Check for commands
	if len(plan.Commands) == 0 {
		validation.Warnings = append(validation.Warnings, "no commands found in trace")
	}

	// Check for buffers
	if plan.StateAnalysis != nil && len(plan.StateAnalysis.Buffers) == 0 {
		validation.Warnings = append(validation.Warnings, "no buffers found - output may be incorrect")
	}

	// Check for functions
	if plan.StateAnalysis != nil && len(plan.StateAnalysis.Functions) == 0 {
		validation.Warnings = append(validation.Warnings, "no functions found - cannot resolve kernel names")
	}

	// Check for unresolved function references
	unresolvedFunctions := 0
	for _, cmd := range plan.Commands {
		if cmd.Type == "compute_dispatch" && cmd.FunctionName == "" {
			unresolvedFunctions++
		}
	}
	if unresolvedFunctions > 0 {
		validation.Warnings = append(validation.Warnings,
			fmt.Sprintf("%d compute dispatches have unresolved function names", unresolvedFunctions))
	}

	// Check for buffer address correlation
	if plan.StateAnalysis != nil {
		uncorrelatedBuffers := 0
		for _, buf := range plan.StateAnalysis.Buffers {
			if buf.Address == 0 {
				uncorrelatedBuffers++
			}
		}
		if uncorrelatedBuffers > 0 {
			validation.Warnings = append(validation.Warnings,
				fmt.Sprintf("%d buffers have unknown addresses", uncorrelatedBuffers))
		}
	}

	return validation, nil
}

// ReplayValidation contains validation results.
type ReplayValidation struct {
	CanReplay bool
	Errors    []string
	Warnings  []string
}

// FormatReplayPlan generates a human-readable report of the replay plan.
func FormatReplayPlan(plan *ReplayPlan) string {
	output := "=== Replay Plan ===\n\n"

	output += fmt.Sprintf("Trace: %s\n\n", plan.TraceePath)

	output += "Execution Summary:\n"
	output += fmt.Sprintf("  Command Queue: %s\n", plan.CommandQueue.Label)
	output += fmt.Sprintf("  Command Buffers: %d\n", plan.CommandQueue.CommandBuffers)
	output += fmt.Sprintf("  Total Encoders: %d\n", plan.TotalEncoders)
	output += fmt.Sprintf("  Total Commands: %d\n", plan.TotalCommands)
	output += fmt.Sprintf("    - Compute Dispatches: %d\n", plan.ComputeDispatches)
	output += fmt.Sprintf("    - ICB Executions: %d\n", plan.ICBExecutions)
	output += "\n"

	// Show encoders
	if len(plan.Encoders) > 0 {
		output += "Encoders:\n"
		for i, encoder := range plan.Encoders {
			label := encoder.Label
			if label == "" {
				label = "(unlabeled)"
			}
			output += fmt.Sprintf("  [%2d] %-20s  %3d commands\n",
				i, truncateString(label, 20), encoder.CommandCount)
		}
		output += "\n"
	}

	// Show command sequence
	if len(plan.Commands) > 0 {
		output += "Command Sequence (first 20):\n"
		output += fmt.Sprintf("  %-4s %-8s %-20s %-40s\n",
			"Seq", "Encoder", "Type", "Function/Target")
		output += "  " + repeatChar('-', 75) + "\n"

		count := min(20, len(plan.Commands))
		for i := 0; i < count; i++ {
			cmd := plan.Commands[i]
			target := ""
			switch cmd.Type {
			case "compute_dispatch":
				if cmd.FunctionName != "" {
					target = cmd.FunctionName
				} else {
					target = fmt.Sprintf("func@0x%x", cmd.FunctionAddr)
				}
			case "execute_icb":
				target = fmt.Sprintf("ICB@0x%x (count=%d)", cmd.ICBAddr, cmd.ICBCount)
			}

			output += fmt.Sprintf("  %-4d %-8d %-20s %-40s\n",
				cmd.SequenceNum,
				cmd.EncoderIndex,
				cmd.Type,
				truncateString(target, 40))
		}

		if len(plan.Commands) > 20 {
			output += fmt.Sprintf("  ... and %d more commands\n", len(plan.Commands)-20)
		}
		output += "\n"
	}

	// Show state analysis if available
	if plan.StateAnalysis != nil {
		output += FormatReplayAnalysis(plan.StateAnalysis)
	}

	return output
}

// FormatReplayValidation generates a human-readable validation report.
func FormatReplayValidation(validation *ReplayValidation) string {
	output := "=== Replay Validation ===\n\n"

	if validation.CanReplay {
		output += "Status: ✓ Trace can be replayed\n\n"
	} else {
		output += "Status: ✗ Trace CANNOT be replayed\n\n"
	}

	if len(validation.Errors) > 0 {
		output += "Errors:\n"
		for _, err := range validation.Errors {
			output += fmt.Sprintf("  ✗ %s\n", err)
		}
		output += "\n"
	}

	if len(validation.Warnings) > 0 {
		output += "Warnings:\n"
		for _, warning := range validation.Warnings {
			output += fmt.Sprintf("  ⚠ %s\n", warning)
		}
		output += "\n"
	}

	if validation.CanReplay && len(validation.Warnings) == 0 && len(validation.Errors) == 0 {
		output += "No issues found. Trace is ready for replay.\n"
	}

	return output
}

// GetCommandTimeline returns commands sorted by execution order.
func (plan *ReplayPlan) GetCommandTimeline() []ReplayCommand {
	commands := make([]ReplayCommand, len(plan.Commands))
	copy(commands, plan.Commands)

	sort.Slice(commands, func(i, j int) bool {
		return commands[i].SequenceNum < commands[j].SequenceNum
	})

	return commands
}

// GetEncoderCommands returns all commands for a specific encoder.
func (plan *ReplayPlan) GetEncoderCommands(encoderIndex int) []ReplayCommand {
	var commands []ReplayCommand
	for _, cmd := range plan.Commands {
		if cmd.EncoderIndex == encoderIndex {
			commands = append(commands, cmd)
		}
	}
	return commands
}

// GetUniqueBufferAddresses returns all unique buffer addresses used in the replay.
func (plan *ReplayPlan) GetUniqueBufferAddresses() []uint64 {
	seen := make(map[uint64]bool)
	var addresses []uint64

	for _, cmd := range plan.Commands {
		for _, addr := range cmd.BufferBindings {
			if addr != 0 && !seen[addr] {
				seen[addr] = true
				addresses = append(addresses, addr)
			}
		}
	}

	sort.Slice(addresses, func(i, j int) bool {
		return addresses[i] < addresses[j]
	})

	return addresses
}

// GetUniqueFunctionAddresses returns all unique function addresses used in the replay.
func (plan *ReplayPlan) GetUniqueFunctionAddresses() []uint64 {
	seen := make(map[uint64]bool)
	var addresses []uint64

	for _, cmd := range plan.Commands {
		if cmd.Type == "compute_dispatch" && cmd.FunctionAddr != 0 && !seen[cmd.FunctionAddr] {
			seen[cmd.FunctionAddr] = true
			addresses = append(addresses, cmd.FunctionAddr)
		}
	}

	sort.Slice(addresses, func(i, j int) bool {
		return addresses[i] < addresses[j]
	})

	return addresses
}

// EnableCounterSampling enables performance counter collection during replay.
// This must be called before AnalyzeReplayWithCounters or ReplayWithCounters.
func (re *ReplayEngine) EnableCounterSampling(config *CounterSamplingConfig) error {
	if config == nil {
		config = DefaultCounterSamplingConfig()
	}

	re.CounterSampler = NewCounterSampler(config)
	return nil
}

// AnalyzeReplayWithCounters performs replay analysis and simulates counter sampling.
// This shows where counter samples would be taken during actual GPU replay.
func (re *ReplayEngine) AnalyzeReplayWithCounters() (*ReplayPlan, *CounterSamplingResult, error) {
	// First analyze the replay plan
	plan, err := re.AnalyzeReplay()
	if err != nil {
		return nil, nil, fmt.Errorf("analyze replay: %w", err)
	}

	// Check if counter sampling is enabled
	if re.CounterSampler == nil {
		return plan, nil, fmt.Errorf("counter sampling not enabled (call EnableCounterSampling first)")
	}

	// Simulate counter sample buffer creation
	// Calculate max samples needed: 2 per encoder (start/end) + 2 per dispatch (start/end)
	maxSamples := len(plan.Encoders) * 2
	if re.CounterSampler.Config.SampleAtDispatchBoundaries {
		maxSamples += plan.ComputeDispatches * 2
	}

	if err := re.CounterSampler.CreateCounterSampleBuffers(re.State.Device, maxSamples); err != nil {
		return plan, nil, fmt.Errorf("create counter buffers: %w", err)
	}

	// Simulate counter sampling at encoder and dispatch boundaries
	sampleIndex := 0

	for i, encoder := range plan.Encoders {
		// Sample at encoder start
		if re.CounterSampler.Config.SampleAtEncoderBoundaries {
			if err := re.CounterSampler.SampleCounters(nil, "encoder_start", i, -1); err != nil {
				return plan, nil, fmt.Errorf("sample encoder start: %w", err)
			}
			sampleIndex++
		}

		// Sample at each dispatch within encoder
		if re.CounterSampler.Config.SampleAtDispatchBoundaries {
			encoderCommands := plan.GetEncoderCommands(i)
			for j, cmd := range encoderCommands {
				if cmd.Type == "compute_dispatch" {
					// Sample before dispatch
					if err := re.CounterSampler.SampleCounters(nil, "dispatch_start", i, j); err != nil {
						return plan, nil, fmt.Errorf("sample dispatch start: %w", err)
					}
					sampleIndex++

					// Sample after dispatch
					if err := re.CounterSampler.SampleCounters(nil, "dispatch_end", i, j); err != nil {
						return plan, nil, fmt.Errorf("sample dispatch end: %w", err)
					}
					sampleIndex++
				}
			}
		}

		// Sample at encoder end
		if re.CounterSampler.Config.SampleAtEncoderBoundaries {
			if err := re.CounterSampler.SampleCounters(nil, "encoder_end", i, encoder.CommandCount-1); err != nil {
				return plan, nil, fmt.Errorf("sample encoder end: %w", err)
			}
			sampleIndex++
		}
	}

	// Simulate counter resolution (in real implementation, this reads GPU data)
	// NOTE: This would call MTLCounterSampleBuffer.resolveCounterRange() to read
	// fresh counter data collected during GPU replay. It does NOT read .gpuprofiler_raw
	// files - those are for the separate "perfcounters" command.
	if err := re.CounterSampler.ResolveCounterSamples(); err != nil {
		return plan, nil, fmt.Errorf("resolve counters: %w", err)
	}

	// Aggregate metrics from counter samples
	// TODO: These functions are commented out in counter package due to import cycle
	// encoderMetrics := re.CounterSampler.AggregateEncoderMetrics(plan)
	// dispatchMetrics := re.CounterSampler.AggregateDispatchMetrics(plan)
	var encoderMetrics []counter.EncoderCounterMetrics
	var dispatchMetrics []counter.DispatchCounterMetrics

	// Build result
	result := &CounterSamplingResult{
		TracePath:       plan.TraceePath,
		Config:          re.CounterSampler.Config,
		Samples:         re.CounterSampler.Samples,
		EncoderMetrics:  encoderMetrics,
		DispatchMetrics: dispatchMetrics,
		SampleCount:     len(re.CounterSampler.Samples),
		EncoderCount:    len(plan.Encoders),
		DispatchCount:   plan.ComputeDispatches,
	}

	return plan, result, nil
}

// SimulateCounterSampling simulates the counter sampling process for documentation/planning.
// This is useful for understanding the sampling overhead before actual implementation.
func (re *ReplayEngine) SimulateCounterSampling() (*CounterSamplingSimulation, error) {
	plan, err := re.AnalyzeReplay()
	if err != nil {
		return nil, fmt.Errorf("analyze replay: %w", err)
	}

	if re.CounterSampler == nil {
		return nil, fmt.Errorf("counter sampling not enabled")
	}

	simulation := &CounterSamplingSimulation{
		TracePath:     plan.TraceePath,
		EncoderCount:  len(plan.Encoders),
		DispatchCount: plan.ComputeDispatches,
		Config:        re.CounterSampler.Config,
	}

	// Calculate sampling overhead
	samplesPerEncoder := 0
	if re.CounterSampler.Config.SampleAtEncoderBoundaries {
		samplesPerEncoder = 2 // Start and end
	}

	samplesPerDispatch := 0
	if re.CounterSampler.Config.SampleAtDispatchBoundaries {
		samplesPerDispatch = 2 // Start and end
	}

	simulation.SamplesPerEncoder = samplesPerEncoder
	simulation.SamplesPerDispatch = samplesPerDispatch
	simulation.TotalSamples = (len(plan.Encoders) * samplesPerEncoder) +
		(plan.ComputeDispatches * samplesPerDispatch)

	// Estimate overhead (barrier synchronization cost)
	// Typical barrier cost: ~100-500ns per sample on Apple Silicon
	simulation.EstimatedBarrierOverheadNs = uint64(simulation.TotalSamples) * 250 // 250ns per sample
	simulation.EstimatedBarrierOverheadMs = float64(simulation.EstimatedBarrierOverheadNs) / 1e6

	// Calculate buffer sizes
	// Each counter sample buffer needs storage for all samples
	counterSetCount := len(re.CounterSampler.Config.EnabledCounterSets)
	simulation.CounterSetsEnabled = counterSetCount

	// Typical sample size: 64 bytes per counter × counters per set
	// timestamp: 1 counter (8 bytes)
	// stage_utilization: 3 counters (24 bytes)
	// statistics: 2 counters (16 bytes)
	bytesPerSample := 64 // Conservative estimate
	simulation.BufferSizeBytes = uint64(simulation.TotalSamples) * uint64(counterSetCount) * uint64(bytesPerSample)
	simulation.BufferSizeMB = float64(simulation.BufferSizeBytes) / (1024 * 1024)

	return simulation, nil
}

// CounterSamplingSimulation contains simulation results for counter sampling overhead.
type CounterSamplingSimulation struct {
	TracePath     string
	EncoderCount  int
	DispatchCount int
	Config        *CounterSamplingConfig

	// Sampling counts
	SamplesPerEncoder  int
	SamplesPerDispatch int
	TotalSamples       int
	CounterSetsEnabled int

	// Overhead estimates
	EstimatedBarrierOverheadNs uint64
	EstimatedBarrierOverheadMs float64

	// Memory requirements
	BufferSizeBytes uint64
	BufferSizeMB    float64
}

// FormatCounterSamplingSimulation generates a report of the simulation.
func FormatCounterSamplingSimulation(sim *CounterSamplingSimulation) string {
	output := "=== Counter Sampling Simulation ===\n\n"

	output += fmt.Sprintf("Trace: %s\n\n", sim.TracePath)

	output += "Workload:\n"
	output += fmt.Sprintf("  Encoders: %d\n", sim.EncoderCount)
	output += fmt.Sprintf("  Dispatches: %d\n\n", sim.DispatchCount)

	output += "Sampling Configuration:\n"
	output += fmt.Sprintf("  Sample at encoder boundaries: %v\n", sim.Config.SampleAtEncoderBoundaries)
	output += fmt.Sprintf("  Sample at dispatch boundaries: %v\n", sim.Config.SampleAtDispatchBoundaries)
	output += fmt.Sprintf("  Use barriers: %v\n", sim.Config.UseBarriers)
	output += fmt.Sprintf("  Counter sets enabled: %d\n", sim.CounterSetsEnabled)
	for _, set := range sim.Config.EnabledCounterSets {
		output += fmt.Sprintf("    - %s\n", set)
	}
	output += "\n"

	output += "Sampling Overhead:\n"
	output += fmt.Sprintf("  Samples per encoder: %d\n", sim.SamplesPerEncoder)
	output += fmt.Sprintf("  Samples per dispatch: %d\n", sim.SamplesPerDispatch)
	output += fmt.Sprintf("  Total samples: %d\n", sim.TotalSamples)
	output += fmt.Sprintf("  Estimated barrier overhead: %.3f ms\n\n", sim.EstimatedBarrierOverheadMs)

	output += "Memory Requirements:\n"
	output += fmt.Sprintf("  Counter buffer size: %.2f MB (%d bytes)\n",
		sim.BufferSizeMB, sim.BufferSizeBytes)
	output += "\n"

	output += "Notes:\n"
	output += "  - Barrier overhead assumes ~250ns per sample\n"
	output += "  - Actual overhead may vary based on GPU workload\n"
	output += "  - Buffer size is conservative estimate\n"
	output += "  - This is a simulation; actual Metal implementation required\n"

	return output
}
