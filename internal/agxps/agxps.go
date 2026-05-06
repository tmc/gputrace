//go:build darwin

// Package agxps provides a small adapter over
// github.com/tmc/apple/private/xcode/gtshaderprofiler.
//
// The package preserves the gputrace-facing API for the AGX profiler C surface:
// GPU handles, parser handles, profile data queries, kick timing, ESL clique
// timing, and instruction-trace statistics.
//
// The underlying generated bindings currently load GTShaderProfiler.framework at
// import time. They do not expose framework load status or the older GPUPlugin
// fallback path, so Init reports framework availability rather than managing the
// dynamic library handle directly.
package agxps

import (
	"fmt"
	"os"
	"sync"
	"unsafe"

	"github.com/tmc/apple/private/xcode/gtshaderprofiler"
)

const gtShaderProfilerPath = "/Applications/Xcode.app/Contents/PlugIns/GPUDebugger.ideplugin/Contents/Frameworks/GTShaderProfiler.framework/Versions/A/GTShaderProfiler"

var (
	loadMu sync.Mutex
	loaded bool
)

// GPU is an opaque handle for GPU configuration.
type GPU uintptr

// ProfileData is an opaque handle for parsed profile data.
type ProfileData uintptr

// ParserHandle is an opaque handle for a parser instance.
type ParserHandle uintptr

// Descriptor configures the parser for parsing trace data.
// This layout matches agxps_aps_descriptor_create.
type Descriptor struct {
	GPU                    GPU
	PulsePeriod            uint32
	EraPeriod              uint32
	CountPeriod            uint32
	ChunkSize              uint64
	CounterUarchBehaviour  int32
	ExcludeFlags           int32
	MinTimestamp           uint64
	MaxTimestamp           uint64
	CountersFilter         uintptr
	CountersFilterSize     uint64
	TimestampSyncPointData uintptr
	TimestampSyncPointSize uint64
	MaxParseErrorCount     uint32
	_                      uint32
	TimebaseOffset         uint64
}

// Init reports whether GTShaderProfiler is available through the generated
// bindings package.
func Init() error {
	loadMu.Lock()
	defer loadMu.Unlock()
	if loaded {
		return nil
	}
	if _, err := os.Stat(gtShaderProfilerPath); err != nil {
		return fmt.Errorf("gtshaderprofiler not available: %w", err)
	}
	loaded = true
	return nil
}

// Close is a no-op. The generated bindings own framework lifetime.
func Close() {}

// IsLoaded reports whether Init confirmed GTShaderProfiler availability.
func IsLoaded() bool {
	loadMu.Lock()
	defer loadMu.Unlock()
	return loaded
}

// Parser wraps agxps_aps_parser for parsing timeline data.
type Parser struct {
	handle ParserHandle
}

// Initialize calls agxps_initialize.
func Initialize() error {
	if err := Init(); err != nil {
		return err
	}
	result, err := gtshaderprofiler.Agxps_initialize()
	if err != nil {
		return fmt.Errorf("agxps_initialize: %w", err)
	}
	if result != 0 {
		return fmt.Errorf("agxps_initialize returned error: %d", result)
	}
	return nil
}

// NewParser creates a new timeline data parser.
func NewParser() (*Parser, error) {
	return nil, fmt.Errorf("parser creation requires descriptor - use NewParserWithGPU or NewParserWithDescriptor")
}

// NewParserWithGPU creates a parser configured for the specified GPU.
func NewParserWithGPU(gpu GPU) (*Parser, error) {
	desc := &Descriptor{ChunkSize: 262144}
	descPtr, err := gtshaderprofiler.Agxps_aps_descriptor_create(unsafe.Pointer(desc))
	if err != nil {
		return nil, fmt.Errorf("create descriptor: %w", err)
	}
	if descPtr == 0 {
		return nil, fmt.Errorf("failed to initialize descriptor")
	}
	desc.GPU = gpu

	handle, err := gtshaderprofiler.Agxps_aps_parser_create(descPtr)
	if err != nil {
		return nil, fmt.Errorf("create parser: %w", err)
	}
	valid, err := gtshaderprofiler.Agxps_aps_parser_is_valid(handle)
	if err != nil {
		return nil, fmt.Errorf("validate parser: %w", err)
	}
	if handle == 0 || !valid {
		return nil, fmt.Errorf("failed to create parser")
	}
	return &Parser{handle: ParserHandle(handle)}, nil
}

// NewParserWithDescriptor creates a parser with an explicit descriptor.
func NewParserWithDescriptor(desc *Descriptor) (*Parser, error) {
	descPtr, err := gtshaderprofiler.Agxps_aps_descriptor_create(unsafe.Pointer(desc))
	if err != nil {
		return nil, fmt.Errorf("create descriptor: %w", err)
	}
	if descPtr == 0 {
		return nil, fmt.Errorf("failed to create descriptor")
	}
	handle, err := gtshaderprofiler.Agxps_aps_parser_create(descPtr)
	if err != nil {
		return nil, fmt.Errorf("create parser: %w", err)
	}
	valid, err := gtshaderprofiler.Agxps_aps_parser_is_valid(handle)
	if err != nil {
		return nil, fmt.Errorf("validate parser: %w", err)
	}
	if handle == 0 || !valid {
		return nil, fmt.Errorf("failed to create parser")
	}
	return &Parser{handle: ParserHandle(handle)}, nil
}

// Close destroys the parser.
func (p *Parser) Close() {
	if p.handle == 0 {
		return
	}
	_ = gtshaderprofiler.Agxps_aps_parser_destroy(gtshaderprofiler.AGXPSParserHandle(p.handle))
	p.handle = 0
}

// Parse parses timeline data from a byte slice.
func (p *Parser) Parse(data []byte) (ProfileData, error) {
	if len(data) == 0 {
		return 0, fmt.Errorf("empty data")
	}
	var pd gtshaderprofiler.AGXPSProfileData
	result, err := gtshaderprofiler.Agxps_aps_parser_parse(
		gtshaderprofiler.AGXPSParserHandle(p.handle),
		unsafe.Pointer(&data[0]),
		uint64(len(data)),
		&pd,
	)
	if err != nil {
		return 0, fmt.Errorf("parse: %w", err)
	}
	if result != 0 {
		return 0, fmt.Errorf("parse failed with code %d", result)
	}
	if pd == 0 {
		return 0, fmt.Errorf("parse returned zero profile data")
	}
	return ProfileData(pd), nil
}

// IsValid returns true if the parser is in a valid state.
func (p *Parser) IsValid() bool {
	if p.handle == 0 {
		return false
	}
	valid, err := gtshaderprofiler.Agxps_aps_parser_is_valid(gtshaderprofiler.AGXPSParserHandle(p.handle))
	return err == nil && valid
}

// IsValid returns true if the profile data handle is valid.
func (pd ProfileData) IsValid() bool {
	if pd == 0 {
		return false
	}
	valid, err := gtshaderprofiler.Agxps_aps_profile_data_is_valid(gtshaderprofiler.AGXPSProfileData(pd))
	return err == nil && valid
}

// Destroy releases the profile data.
func (pd ProfileData) Destroy() {
	if pd == 0 {
		return
	}
	_ = gtshaderprofiler.Agxps_aps_profile_data_destroy(gtshaderprofiler.AGXPSProfileData(pd))
}

// KickTiming represents timing data for a single GPU kick.
type KickTiming struct {
	Index       uint64
	ID          uint64
	StartTimeNs uint64
	EndTimeNs   uint64
	DurationNs  uint64
}

// GetKickTimings extracts kick timing data from parsed profile data.
func GetKickTimings(profileData ProfileData) ([]KickTiming, error) {
	if profileData == 0 {
		return nil, fmt.Errorf("invalid profile data")
	}
	pd := gtshaderprofiler.AGXPSProfileData(profileData)
	numKicks, err := gtshaderprofiler.Agxps_aps_profile_data_get_kicks_num(pd)
	if err != nil {
		return nil, fmt.Errorf("get kicks count: %w", err)
	}
	if numKicks == 0 {
		return nil, nil
	}

	timings := make([]KickTiming, numKicks)
	for i := range timings {
		idx := uint64(i)
		startNs, err := gtshaderprofiler.Agxps_aps_profile_data_get_kick_start(pd, idx)
		if err != nil {
			return nil, fmt.Errorf("get kick %d start: %w", idx, err)
		}
		endNs, err := gtshaderprofiler.Agxps_aps_profile_data_get_kick_end(pd, idx)
		if err != nil {
			return nil, fmt.Errorf("get kick %d end: %w", idx, err)
		}
		kickID, err := gtshaderprofiler.Agxps_aps_profile_data_get_kick_id(pd, idx)
		if err != nil {
			return nil, fmt.Errorf("get kick %d id: %w", idx, err)
		}
		timings[i] = KickTiming{
			Index:       idx,
			ID:          kickID,
			StartTimeNs: startNs,
			EndTimeNs:   endNs,
			DurationNs:  endNs - startNs,
		}
	}
	return timings, nil
}

// TimingStats represents aggregate timing statistics.
type TimingStats struct {
	NumCommands uint64
	AvgDuration float64
	MinDuration float64
	MaxDuration float64
}

// GetTimingStats extracts timing statistics from a timing analyzer.
func GetTimingStats(analyzer uintptr) TimingStats {
	numCommands, _ := gtshaderprofiler.Agxps_aps_timing_analyzer_get_num_commands(analyzer)
	avgDuration, _ := gtshaderprofiler.Agxps_aps_timing_analyzer_get_work_cliques_average_duration(analyzer)
	minDuration, _ := gtshaderprofiler.Agxps_aps_timing_analyzer_get_work_cliques_min_duration(analyzer)
	maxDuration, _ := gtshaderprofiler.Agxps_aps_timing_analyzer_get_work_cliques_max_duration(analyzer)
	return TimingStats{
		NumCommands: numCommands,
		AvgDuration: avgDuration,
		MinDuration: minDuration,
		MaxDuration: maxDuration,
	}
}

// ESLCliqueTiming represents timing data for a single ESL clique.
type ESLCliqueTiming struct {
	Index      uint64
	CliqueID   uint64
	KickID     uint64
	EslID      uint64
	StartTime  uint64
	EndTime    uint64
	Duration   uint64
	MissingEnd bool
}

// GetESLCliqueTimings extracts ESL clique timing data from parsed profile data.
func GetESLCliqueTimings(profileData ProfileData) ([]ESLCliqueTiming, error) {
	if profileData == 0 {
		return nil, fmt.Errorf("invalid profile data")
	}
	pd := gtshaderprofiler.AGXPSProfileData(profileData)
	numCliques, err := gtshaderprofiler.Agxps_aps_profile_data_get_esl_cliques_num(pd)
	if err != nil {
		return nil, fmt.Errorf("get esl clique count: %w", err)
	}
	if numCliques == 0 {
		return nil, nil
	}

	timings := make([]ESLCliqueTiming, numCliques)
	for i := range timings {
		idx := uint64(i)
		start, err := gtshaderprofiler.Agxps_aps_profile_data_get_esl_clique_start(pd, idx)
		if err != nil {
			return nil, fmt.Errorf("get esl clique %d start: %w", idx, err)
		}
		end, err := gtshaderprofiler.Agxps_aps_profile_data_get_esl_clique_end(pd, idx)
		if err != nil {
			return nil, fmt.Errorf("get esl clique %d end: %w", idx, err)
		}
		cliqueID, err := gtshaderprofiler.Agxps_aps_profile_data_get_esl_clique_clique_id(pd, idx)
		if err != nil {
			return nil, fmt.Errorf("get esl clique %d clique id: %w", idx, err)
		}
		kickID, err := gtshaderprofiler.Agxps_aps_profile_data_get_esl_clique_kick_id(pd, idx)
		if err != nil {
			return nil, fmt.Errorf("get esl clique %d kick id: %w", idx, err)
		}
		eslID, err := gtshaderprofiler.Agxps_aps_profile_data_get_esl_clique_esl_id(pd, idx)
		if err != nil {
			return nil, fmt.Errorf("get esl clique %d esl id: %w", idx, err)
		}
		missingEnd, err := gtshaderprofiler.Agxps_aps_profile_data_get_esl_clique_missing_end(pd, idx)
		if err != nil {
			return nil, fmt.Errorf("get esl clique %d missing end: %w", idx, err)
		}
		timings[i] = ESLCliqueTiming{
			Index:      idx,
			CliqueID:   cliqueID,
			KickID:     kickID,
			EslID:      eslID,
			StartTime:  start,
			EndTime:    end,
			Duration:   end - start,
			MissingEnd: missingEnd,
		}
	}
	return timings, nil
}

// GetESLCliqueInstructionTrace returns the instruction trace handle for a clique.
func GetESLCliqueInstructionTrace(profileData ProfileData, cliqueIndex uint64) uintptr {
	if profileData == 0 {
		return 0
	}
	ref, err := gtshaderprofiler.Agxps_aps_profile_data_get_esl_clique_instruction_trace(
		gtshaderprofiler.AGXPSProfileData(profileData),
		cliqueIndex,
	)
	if err != nil {
		return 0
	}
	return uintptr(ref)
}

// InstructionTraceStats represents statistics from an instruction trace.
type InstructionTraceStats struct {
	NumTimestampRefs   uint64
	NumExecutionEvents uint64
	NumPcAdvances      uint64
}

// GetInstructionTraceStats returns statistics about an instruction trace.
func GetInstructionTraceStats(trace uintptr) InstructionTraceStats {
	if trace == 0 {
		return InstructionTraceStats{}
	}
	ref := gtshaderprofiler.AGXPSCliqueInstructionTraceRef(trace)
	numTimestampRefs, _ := gtshaderprofiler.Agxps_aps_clique_instruction_trace_get_timestamp_references_num(ref)
	numExecutionEvents, _ := gtshaderprofiler.Agxps_aps_clique_instruction_trace_get_execution_events_num(ref)
	numPcAdvances, _ := gtshaderprofiler.Agxps_aps_clique_instruction_trace_get_pc_advances_num(ref)
	return InstructionTraceStats{
		NumTimestampRefs:   numTimestampRefs,
		NumExecutionEvents: numExecutionEvents,
		NumPcAdvances:      numPcAdvances,
	}
}

// CreateCliqueTimeStats creates a time stats object for a specific clique.
func CreateCliqueTimeStats(profileData ProfileData, cliqueIndex uint64) uintptr {
	if profileData == 0 {
		return 0
	}
	ref, err := gtshaderprofiler.Agxps_aps_clique_time_stats_create(
		gtshaderprofiler.AGXPSProfileData(profileData),
		cliqueIndex,
	)
	if err != nil {
		return 0
	}
	return uintptr(ref)
}

// CreateGPU creates a GPU handle for the given generation, variant, and revision.
func CreateGPU(gen, variant, rev uint32) (GPU, error) {
	gpuHandle, err := gtshaderprofiler.Agxps_gpu_create(uint(gen), uint(variant), uint(rev))
	if err != nil {
		return 0, fmt.Errorf("create GPU: %w", err)
	}
	gpu := GPU(gpuHandle)
	if !gpu.IsValid() {
		return 0, fmt.Errorf("failed to create GPU for gen=%d variant=%d rev=%d", gen, variant, rev)
	}
	return gpu, nil
}

// IsValid returns true if the GPU handle is valid.
func (g GPU) IsValid() bool {
	if g == 0 {
		return false
	}
	valid, err := gtshaderprofiler.Agxps_gpu_is_valid(gtshaderprofiler.AGXPSGPU(g))
	return err == nil && valid
}

// Destroy releases the GPU handle.
func (g GPU) Destroy() {
	if g == 0 {
		return
	}
	_ = gtshaderprofiler.Agxps_gpu_destroy(gtshaderprofiler.AGXPSGPU(g))
}

// Gen returns the GPU generation.
func (g GPU) Gen() uint32 {
	if g == 0 {
		return 0
	}
	gen, err := gtshaderprofiler.Agxps_gpu_get_gen(gtshaderprofiler.AGXPSGPU(g))
	if err != nil {
		return 0
	}
	return uint32(gen)
}

// Variant returns the GPU variant.
func (g GPU) Variant() uint32 {
	if g == 0 {
		return 0
	}
	variant, err := gtshaderprofiler.Agxps_gpu_get_variant(gtshaderprofiler.AGXPSGPU(g))
	if err != nil {
		return 0
	}
	return uint32(variant)
}

// Rev returns the GPU revision.
func (g GPU) Rev() uint32 {
	if g == 0 {
		return 0
	}
	rev, err := gtshaderprofiler.Agxps_gpu_get_rev(gtshaderprofiler.AGXPSGPU(g))
	if err != nil {
		return 0
	}
	return uint32(rev)
}

// Name returns the formatted GPU name.
func (g GPU) Name() string {
	if g == 0 {
		return ""
	}
	buf := make([]byte, 256)
	if _, err := gtshaderprofiler.Agxps_gpu_format_name(gtshaderprofiler.AGXPSGPU(g), &buf[0], uint64(len(buf))); err != nil {
		return ""
	}
	for i, b := range buf {
		if b == 0 {
			return string(buf[:i])
		}
	}
	return string(buf)
}

// IsSupported returns true if the GPU is supported for profiling.
func (g GPU) IsSupported() bool {
	if g == 0 {
		return false
	}
	supported, err := gtshaderprofiler.Agxps_aps_gpu_is_supported(gtshaderprofiler.AGXPSGPU(g))
	return err == nil && supported
}
