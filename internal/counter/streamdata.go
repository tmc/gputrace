// Package counter provides GPU performance counter parsing and mapping.
// This file parses streamData from .gpuprofiler_raw to extract pipeline metadata.

package counter

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tmc/apple/x/plist"
	"github.com/tmc/gputrace/internal/trace"
)

// PipelineStats contains shader compilation statistics from streamData.
type PipelineStats struct {
	PipelineID                                int     `json:"pipeline_id"`
	PipelineAddress                           uint64  `json:"pipeline_address,omitempty"`                    // Metal pipeline address (e.g., 0x1051ddd70)
	FunctionName                              string  `json:"function_name,omitempty"`                       // Kernel function name
	TemporaryRegisterCount                    int     `json:"temporary_register_count"`                      // "# Allocated Registers" in Xcode
	UniformRegisterCount                      int     `json:"uniform_register_count"`                        // Uniform registers
	SpilledBytes                              int     `json:"spilled_bytes"`                                 // Register spill to memory
	ThreadInvariantSpilled                    int     `json:"thread_invariant_spilled"`                      // Thread-invariant spilled bytes
	ThreadgroupMemory                         int     `json:"threadgroup_memory"`                            // Threadgroup memory usage
	InstructionCount                          int     `json:"instruction_count"`                             // Total instructions
	ALUInstructionCount                       int     `json:"alu_instruction_count"`                         // ALU instructions
	FP32InstructionCount                      int     `json:"fp32_instruction_count"`                        // FP32 instructions
	FP16InstructionCount                      int     `json:"fp16_instruction_count"`                        // FP16 instructions
	INT32InstructionCount                     int     `json:"int32_instruction_count"`                       // INT32 instructions
	INT16InstructionCount                     int     `json:"int16_instruction_count"`                       // INT16 instructions
	BranchInstructionCount                    int     `json:"branch_instruction_count"`                      // Branch instructions
	DeviceLoadCount                           int     `json:"device_load_instruction_count"`                 // Device memory loads
	DeviceStoreCount                          int     `json:"device_store_instruction_count"`                // Device memory stores
	DeviceAtomicCount                         int     `json:"device_atomic_instruction_count"`               // Device atomics
	TextureReadCount                          int     `json:"texture_reads_instruction_count"`               // Texture reads
	TextureWriteCount                         int     `json:"texture_writes_instruction_count"`              // Texture writes
	ThreadgroupLoadCount                      int     `json:"threadgroup_load_instruction_count"`            // Threadgroup loads
	ThreadgroupStoreCount                     int     `json:"threadgroup_store_instruction_count"`           // Threadgroup stores
	ThreadgroupAtomicCount                    int     `json:"threadgroup_atomic_instruction_count"`          // Threadgroup atomics
	WaitInstructionCount                      int     `json:"wait_instruction_count"`                        // Wait instructions
	ConstantCalculationTemporaryRegisterCount int     `json:"constant_calculation_temporary_register_count"` // Temporary registers used by constant calculation
	ConstantCalculationPhasePresent           bool    `json:"constant_calculation_phase_present"`            // Whether constant calculation was present
	CompilationTimeMs                         float64 `json:"compilation_time_ms"`                           // Shader compilation time
}

// DispatchInfo contains per-dispatch timing and metadata.
type DispatchInfo struct {
	Index                   int     `json:"index"`                                // Dispatch index (0-based)
	PipelineIndex           int     `json:"pipeline_index"`                       // Index into Pipelines array
	PipelineID              int     `json:"pipeline_id,omitempty"`                // Pipeline ID for execution cost lookup
	FunctionName            string  `json:"function_name,omitempty"`              // Kernel function name
	EncoderIndex            int     `json:"encoder_index"`                        // Which encoder this dispatch belongs to
	CumulativeUs            int     `json:"cumulative_us"`                        // Cumulative time in microseconds
	DurationUs              int     `json:"duration_us"`                          // Duration of this dispatch in microseconds
	ExecutionCostPct        float64 `json:"execution_cost_pct,omitempty"`         // Cost from a validated source (0-100%)
	ProfilingSampleSharePct float64 `json:"profiling_sample_share_pct,omitempty"` // Pipeline share of Profiling_f samples; an estimate, not Xcode Execution Cost
	// GPRWCNTR sample correlation (populated by CorrelateDispatchSamples)
	SampleCount     int     `json:"sample_count,omitempty"`     // GPRWCNTR samples in the estimated dispatch window
	SamplingDensity float64 `json:"sampling_density,omitempty"` // Estimated samples per dispatch microsecond
	StartTicks      uint64  `json:"start_ticks,omitempty"`      // Estimated dispatch-window start, in mach absolute ticks
	EndTicks        uint64  `json:"end_ticks,omitempty"`        // Estimated dispatch-window end, in mach absolute ticks
}

// DisplayName returns the shader name used in Xcode-style reports.
func (d DispatchInfo) DisplayName() string {
	if d.FunctionName != "" {
		return d.FunctionName
	}
	if d.PipelineID != 0 {
		return fmt.Sprintf("(pipeline_%d)", d.PipelineID)
	}
	if d.PipelineIndex >= 0 {
		return fmt.Sprintf("(pipeline_index_%d)", d.PipelineIndex)
	}
	return "(pipeline_unknown)"
}

// EncoderTimingInfo contains timing information for a single encoder from streamData.
type EncoderTimingInfo struct {
	Index           int    `json:"index"`             // Encoder index (0-based)
	Label           string `json:"label,omitempty"`   // Encoder label from trace
	SequenceID      uint64 `json:"sequence_id"`       // Sequence identifier
	StartTimestamp  uint64 `json:"start_timestamp"`   // Start timestamp (raw)
	EndOffsetMicros int    `json:"end_offset_micros"` // Cumulative offset in microseconds
	DurationMicros  int    `json:"duration_micros"`   // Duration of this encoder in microseconds
}

// CommandBufferTimestamp contains start/end timing for a command buffer.
// Extracted from APSTimelineData blob's "Command Buffer Timestamps" field.
type CommandBufferTimestamp struct {
	Index      int    `json:"index"`       // Command buffer index (0-based)
	StartTicks uint64 `json:"start_ticks"` // Start time in GPU ticks
	EndTicks   uint64 `json:"end_ticks"`   // End time in GPU ticks
}

// DurationNs returns the duration in nanoseconds using the provided timebase.
func (cb CommandBufferTimestamp) DurationNs(numer, denom uint64) uint64 {
	return ticksToNs(cb.StartTicks, cb.EndTicks, numer, denom)
}

// TimestampRange contains a start/end pair in GPU ticks.
type TimestampRange struct {
	Index      int    `json:"index"`
	StartTicks uint64 `json:"start_ticks"`
	EndTicks   uint64 `json:"end_ticks"`
}

// DurationNs returns the range duration in nanoseconds using the provided timebase.
func (r TimestampRange) DurationNs(numer, denom uint64) uint64 {
	return ticksToNs(r.StartTicks, r.EndTicks, numer, denom)
}

// TimelineInfo contains timeline data extracted from APSTimelineData blobs.
type TimelineInfo struct {
	CommandBufferTimestamps []CommandBufferTimestamp `json:"command_buffer_timestamps"`
	RestoreTimestamps       []TimestampRange         `json:"restore_timestamps,omitempty"`
	EncoderProfiles         []EncoderProfile         `json:"encoder_profiles,omitempty"` // GPRWCNTR data per encoder
	TimebaseNumer           uint64                   `json:"timebase_numer"`             // Tick-to-ns numerator (e.g., 125)
	TimebaseDenom           uint64                   `json:"timebase_denom"`             // Tick-to-ns denominator (e.g., 3)
	AbsoluteTime            uint64                   `json:"absolute_time"`              // Capture start time in ticks
	ContinuousTime          uint64                   `json:"continuous_time,omitempty"`
	PState                  *int                     `json:"pstate,omitempty"`
	ReplayerGPUTimeNs       uint64                   `json:"replayer_gpu_time_ns,omitempty"`
	CommandBufferActiveNs   uint64                   `json:"command_buffer_active_time_ns,omitempty"`
	CommandBufferWallNs     uint64                   `json:"command_buffer_wall_time_ns,omitempty"`
	RestoreActiveNs         uint64                   `json:"restore_active_time_ns,omitempty"`
	RestoreWallNs           uint64                   `json:"restore_wall_time_ns,omitempty"`
}

// EncoderProfile contains GPRWCNTR profiler data for one ShaderProfilerData
// blob of APSTimelineData.
//
// Despite the name, these samples are not per-encoder: every record in the
// reference archives carries GRC_ENCODER_ID 0xFFFFFFFF, meaning the GPU counter
// stream sampled the whole machine rather than the capture's own encoders. See
// MachineWideSamples, and CounterArchive for the samples that do attribute.
type EncoderProfile struct {
	Index              int              `json:"index"`                   // Blob index (0-based)
	Source             string           `json:"source,omitempty"`        // Source type (RDE_0, BMPR_RDE_0, Firmware)
	RingBufferIndex    int              `json:"ring_buffer_index"`       // Ring buffer index
	SampleCount        int              `json:"sample_count"`            // Number of profiler samples
	MachineWideSamples int              `json:"machine_wide_samples"`    // Samples belonging to no encoder in this capture
	RecordStride       int              `json:"record_stride,omitempty"` // Bytes per record, derived from the blob
	Samples            []GPRWCNTRSample `json:"samples,omitempty"`       // Individual records
	StartTicks         uint64           `json:"start_ticks,omitempty"`   // First sample timestamp
	EndTicks           uint64           `json:"end_ticks,omitempty"`     // Last sample timestamp
	DurationNs         uint64           `json:"duration_ns,omitempty"`   // Total duration in nanoseconds
}

// GPRWCNTRMagic is the 8-byte magic that starts every GPRWCNTR record. It is
// per-record, not a one-time blob header; see gprwcntr.go for the layout.
const GPRWCNTRMagic = "GPRWCNTR"

// StreamDataStats contains all parsed statistics from streamData.
type StreamDataStats struct {
	GPUGeneration         *uint32             `json:"gpu_generation,omitempty"`
	MetalDeviceName       string              `json:"metal_device_name,omitempty"`
	MetalPluginName       string              `json:"metal_plugin_name,omitempty"`
	Metadata              StreamDataMetadata  `json:"metadata"`
	Pipelines             []PipelineStats     `json:"pipelines"`
	Dispatches            []DispatchInfo      `json:"dispatches"`     // Per-dispatch timing and metadata
	FunctionNames         []string            `json:"function_names"` // Unique function names from strings array
	EncoderTimings        []EncoderTimingInfo `json:"encoder_timings"`
	Timeline              *TimelineInfo       `json:"timeline,omitempty"`        // CB timestamps from APSTimelineData
	CounterArchive        *CounterArchive     `json:"counter_archive,omitempty"` // Per-encoder counter attribution from APSCounterData
	APSTimelineData       [][]byte            `json:"-"`                         // Raw APSTimelineData blobs (nested plists)
	APSCounterData        [][]byte            `json:"-"`                         // Raw APSCounterData blobs (nested archives)
	NumEncoders           int                 `json:"num_encoders"`
	NumGPUCommands        int                 `json:"num_gpu_commands"`
	NumPipelines          int                 `json:"num_pipelines"`
	TotalTimeUs           int                 `json:"total_time_us"` // Backward-compatible alias for TotalEncoderTimeUs.
	TotalEncoderTimeUs    int                 `json:"total_encoder_time_us"`
	TotalDispatchTimeUs   int                 `json:"total_dispatch_time_us"`
	EffectiveGPUTimeUs    *int                `json:"effective_gpu_time_us"` // APSTimelineData ReplayerGPUTime, when present.
	EffectiveGPUTimeNs    *uint64             `json:"effective_gpu_time_ns,omitempty"`
	CommandBufferActiveNs uint64              `json:"command_buffer_active_time_ns,omitempty"`
	CommandBufferWallNs   uint64              `json:"command_buffer_wall_time_ns,omitempty"`
	TimingSource          string              `json:"timing_source"`
}

// StreamDataMetadata retains scalar provenance from the streamData archive
// root. Enum and capture-range meanings are private and remain uninterpreted.
// Pointer fields distinguish a recorded zero or false from an absent key.
type StreamDataMetadata struct {
	Version                      *int64             `json:"version,omitempty"`
	UnixTimestamp                *int64             `json:"unix_timestamp,omitempty"`
	TraceName                    string             `json:"trace_name,omitempty"`
	ProfiledExecutionMode        *int64             `json:"profiled_execution_mode,omitempty"`
	ProfiledPerformanceState     *int64             `json:"profiled_performance_state,omitempty"`
	ProfiledProfilerMode         *int64             `json:"profiled_profiler_mode,omitempty"`
	CaptureRangeLocation         *int64             `json:"capture_range_location,omitempty"`
	CaptureRangeLength           *int64             `json:"capture_range_length,omitempty"`
	DataSourceHasUnusedResources *bool              `json:"data_source_has_unused_resources,omitempty"`
	SupportsSeparateAPSData      *bool              `json:"supports_separate_aps_data,omitempty"`
	NumBlitCalls                 *int64             `json:"num_blit_calls,omitempty"`
	Tables                       StreamDataTables   `json:"tables"`
	Families                     StreamDataFamilies `json:"families"`
}

// StreamDataFamilies reports top-level archive array entry counts. Counts are
// inventory only; one array entry is not necessarily one decoded sample.
type StreamDataFamilies struct {
	APSData                     *int64 `json:"aps_data,omitempty"`
	APSTimelineData             *int64 `json:"aps_timeline_data,omitempty"`
	APSCounterData              *int64 `json:"aps_counter_data,omitempty"`
	ShaderProfilerData          *int64 `json:"shader_profiler_data,omitempty"`
	GPUTimelineData             *int64 `json:"gpu_timeline_data,omitempty"`
	BatchIDFilteredCountersData *int64 `json:"batch_id_filtered_counters_data,omitempty"`
}

// StreamDataTables reports the byte-level integrity of fixed-record archive
// tables. A nil table means the data key is absent. A nil record size means the
// table exists but its schema size is absent or invalid.
type StreamDataTables struct {
	CommandBuffers *StreamDataTable `json:"command_buffers,omitempty"`
	Encoders       *StreamDataTable `json:"encoders,omitempty"`
	GPUCommands    *StreamDataTable `json:"gpu_commands,omitempty"`
	Pipelines      *StreamDataTable `json:"pipelines,omitempty"`
	Functions      *StreamDataTable `json:"functions,omitempty"`
}

// StreamDataTable describes one fixed-record byte table.
type StreamDataTable struct {
	Bytes          int64  `json:"bytes"`
	RecordSize     *int64 `json:"record_size,omitempty"`
	RecordCount    *int64 `json:"record_count,omitempty"`
	RemainderBytes *int64 `json:"remainder_bytes,omitempty"`
}

// ParseStreamData parses the streamData plist from a .gpuprofiler_raw directory.
// addressToName maps pipeline addresses to function names. Pass nil when no
// trace-derived mapping is available.
func ParseStreamData(gpuprofilerDir string, addressToName map[uint64]string) (*StreamDataStats, error) {
	streamDataPath := filepath.Join(gpuprofilerDir, "streamData")

	data, err := os.ReadFile(streamDataPath)
	if err != nil {
		return nil, fmt.Errorf("read streamData: %w", err)
	}

	// Parse NSKeyedArchiver plist
	var archive map[string]interface{}
	_, err = plist.Unmarshal(data, &archive)
	if err != nil {
		return nil, fmt.Errorf("parse plist: %w", err)
	}

	objects, ok := archive["$objects"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid archive: missing $objects")
	}

	stats := &StreamDataStats{}

	// Find pipelinePerformanceStatistics in object 1
	if len(objects) > 1 {
		if obj1, ok := objects[1].(map[string]any); ok {
			if _, ok := obj1["gpuGeneration"]; ok {
				generation := uint32(plistUint64(obj1["gpuGeneration"]))
				stats.GPUGeneration = &generation
			}
			stats.MetalDeviceName = archivedString(objects, obj1["metalDeviceName"])
			stats.MetalPluginName = archivedString(objects, obj1["metalPluginName"])
			stats.Metadata = parseStreamDataMetadata(objects, obj1)

			// Extract function names from strings array
			stats.FunctionNames = extractFunctionNames(objects, obj1)

			// Extract pipeline addresses and link to functions.
			pipelineInfos := extractPipelineInfo(objects, obj1)

			if ppsUID, ok := obj1["pipelinePerformanceStatistics"].(plist.UID); ok {
				applyCompilerNames(pipelineInfos, compilerFunctionNames(objects, int(ppsUID)))
				stats.Pipelines = extractPipelineStats(objects, int(ppsUID))
				stats.NumPipelines = len(stats.Pipelines)
				attachPipelineMetadata(stats.Pipelines, pipelineInfos, addressToName)
			}

			// Extract encoder timing from encoderInfoData
			if encoderInfoUID, ok := obj1["encoderInfoData"].(plist.UID); ok {
				encoderInfoSize := 40 // default
				if size := plistUint64(obj1["encoderInfoSize"]); size > 0 {
					encoderInfoSize = int(size)
				}
				stats.EncoderTimings = extractEncoderTimings(objects, int(encoderInfoUID), encoderInfoSize)
				stats.NumEncoders = len(stats.EncoderTimings)

				// Calculate total time by summing encoder durations
				for _, et := range stats.EncoderTimings {
					stats.TotalTimeUs += et.DurationMicros
				}
				stats.TotalEncoderTimeUs = stats.TotalTimeUs
			}

			// Extract per-dispatch timing from gpuCommandInfoData
			if gpuCmdUID, ok := obj1["gpuCommandInfoData"].(plist.UID); ok {
				gpuCmdSize := 32 // default
				if size := plistUint64(obj1["gpuCommandInfoSize"]); size > 0 {
					gpuCmdSize = int(size)
				}
				// Build pipeline index maps from pipelineStateInfoData order.
				// pipelinePerformanceStatistics is a dictionary and may be in a
				// different order from gpuCommandInfoData pipeline indices.
				pipelineToName, pipelineToID := pipelineDispatchMaps(pipelineInfos, addressToName)
				stats.Dispatches = extractDispatchInfoWithMap(objects, int(gpuCmdUID), gpuCmdSize, pipelineToName, pipelineToID)
				stats.NumGPUCommands = len(stats.Dispatches)
				for _, d := range stats.Dispatches {
					stats.TotalDispatchTimeUs += d.DurationUs
				}
			}

			// Extract APSTimelineData blobs (nested plists with CB timestamps)
			stats.APSTimelineData = extractDataArray(objects, obj1, "APSTimelineData")
			if len(stats.APSTimelineData) > 0 {
				stats.Timeline = parseAPSTimelineData(stats.APSTimelineData)
				stats.applyTimelineTiming()
			}

			// Counter samples that name an encoder live in APSCounterData, not
			// in the ShaderProfilerData blobs above.
			if counterBlobs := extractDataArray(objects, obj1, "APSCounterData"); len(counterBlobs) > 0 {
				stats.APSCounterData = counterBlobs
				numer, denom := uint64(1), uint64(1)
				if stats.Timeline != nil {
					numer, denom = stats.Timeline.TimebaseNumer, stats.Timeline.TimebaseDenom
				}
				stats.CounterArchive = ParseCounterArchive(counterBlobs, numer, denom, ParseTraceIDTable(counterBlobs))
			}
		}
	}

	stats.setTimingSource()
	return stats, nil
}

func parseStreamDataMetadata(objects []any, root map[string]any) StreamDataMetadata {
	return StreamDataMetadata{
		Version:                      archivedInt64(objects, root, "version"),
		UnixTimestamp:                archivedInt64(objects, root, "unixTimestamp"),
		TraceName:                    archivedString(objects, root["traceName"]),
		ProfiledExecutionMode:        archivedInt64(objects, root, "profiledExecutionMode"),
		ProfiledPerformanceState:     archivedInt64(objects, root, "profiledPerformanceState"),
		ProfiledProfilerMode:         archivedInt64(objects, root, "profiledProfilerMode"),
		CaptureRangeLocation:         archivedInt64(objects, root, "captureRangeLocation"),
		CaptureRangeLength:           archivedInt64(objects, root, "captureRangeLength"),
		DataSourceHasUnusedResources: archivedBool(objects, root, "dataSourceHasUnusedResources"),
		SupportsSeparateAPSData:      archivedBool(objects, root, "supportsSeparateAPSData"),
		NumBlitCalls:                 archivedInt64(objects, root, "numBlitCalls"),
		Tables: StreamDataTables{
			CommandBuffers: parseStreamDataTable(objects, root, "commandBufferInfoData", "commandBufferInfoSize"),
			Encoders:       parseStreamDataTable(objects, root, "encoderInfoData", "encoderInfoSize"),
			GPUCommands:    parseStreamDataTable(objects, root, "gpuCommandInfoData", "gpuCommandInfoSize"),
			Pipelines:      parseStreamDataTable(objects, root, "pipelineStateInfoData", "pipelineStateInfoSize"),
			Functions:      parseStreamDataTable(objects, root, "functionInfoData", "functionInfoSize"),
		},
		Families: StreamDataFamilies{
			APSData:                     archivedArrayCount(objects, root, "APSData"),
			APSTimelineData:             archivedArrayCount(objects, root, "APSTimelineData"),
			APSCounterData:              archivedArrayCount(objects, root, "APSCounterData"),
			ShaderProfilerData:          archivedArrayCount(objects, root, "shaderProfilerData"),
			GPUTimelineData:             archivedArrayCount(objects, root, "gpuTimelineData"),
			BatchIDFilteredCountersData: archivedArrayCount(objects, root, "batchIdFilteredCountersData"),
		},
	}
}

func archivedArrayCount(objects []any, root map[string]any, key string) *int64 {
	value, ok := root[key]
	if !ok {
		return nil
	}
	object, ok := archivedValue(objects, value).(map[string]any)
	if !ok {
		return nil
	}
	values, ok := object["NS.objects"].([]any)
	if !ok {
		return nil
	}
	count := int64(len(values))
	return &count
}

func parseStreamDataTable(objects []any, root map[string]any, dataKey, sizeKey string) *StreamDataTable {
	data, ok := archivedData(objects, root[dataKey])
	if !ok {
		return nil
	}
	table := &StreamDataTable{Bytes: int64(len(data))}
	size := archivedInt64(objects, root, sizeKey)
	if size == nil || *size <= 0 {
		return table
	}
	table.RecordSize = size
	count := table.Bytes / *size
	remainder := table.Bytes % *size
	table.RecordCount = &count
	table.RemainderBytes = &remainder
	return table
}

func archivedData(objects []any, value any) ([]byte, bool) {
	value = archivedValue(objects, value)
	object, ok := value.(map[string]any)
	if !ok {
		return nil, false
	}
	data, ok := object["NS.data"].([]byte)
	return data, ok
}

func archivedInt64(objects []any, root map[string]any, key string) *int64 {
	value, ok := root[key]
	if !ok {
		return nil
	}
	value = archivedValue(objects, value)
	var result int64
	switch v := value.(type) {
	case int:
		result = int64(v)
	case int32:
		result = int64(v)
	case int64:
		result = v
	case uint32:
		result = int64(v)
	case uint64:
		if v > uint64(^uint64(0)>>1) {
			return nil
		}
		result = int64(v)
	default:
		return nil
	}
	return &result
}

func archivedBool(objects []any, root map[string]any, key string) *bool {
	value, ok := root[key]
	if !ok {
		return nil
	}
	result, ok := archivedValue(objects, value).(bool)
	if !ok {
		return nil
	}
	return &result
}

func archivedValue(objects []any, value any) any {
	for depth := 0; depth < 4; depth++ {
		uid, ok := value.(plist.UID)
		if !ok {
			return value
		}
		if int(uid) >= len(objects) {
			return nil
		}
		value = objects[int(uid)]
	}
	return nil
}

func archivedString(objects []any, value any) string {
	for depth := 0; depth < 4; depth++ {
		switch v := value.(type) {
		case string:
			return v
		case plist.UID:
			if int(v) >= len(objects) {
				return ""
			}
			value = objects[int(v)]
		case map[string]any:
			value = v["NS.string"]
		default:
			return ""
		}
	}
	return ""
}

func (stats *StreamDataStats) applyTimelineTiming() {
	if stats.Timeline == nil {
		return
	}
	stats.CommandBufferActiveNs = stats.Timeline.CommandBufferActiveNs
	stats.CommandBufferWallNs = stats.Timeline.CommandBufferWallNs
	if stats.Timeline.ReplayerGPUTimeNs > 0 {
		ns := stats.Timeline.ReplayerGPUTimeNs
		us := int(ns / 1000)
		stats.EffectiveGPUTimeNs = &ns
		stats.EffectiveGPUTimeUs = &us
	}
}

func (stats *StreamDataStats) setTimingSource() {
	switch {
	case stats.EffectiveGPUTimeNs != nil:
		stats.TimingSource = "APSTimelineData ReplayerGPUTime (Xcode Effective GPU Time)"
	case stats.CommandBufferActiveNs > 0:
		stats.TimingSource = "APSTimelineData Command Buffer Timestamps active time; encoderInfoData/gpuCommandInfoData cumulative offsets"
	default:
		stats.TimingSource = "streamData encoderInfoData/gpuCommandInfoData cumulative offsets"
	}
}

// extractFunctionNames extracts kernel function names from the strings array.
func extractFunctionNames(objects []any, obj1 map[string]any) []string {
	uid, ok := obj1["strings"].(plist.UID)
	if !ok {
		return nil
	}

	arrObj, ok := objects[int(uid)].(map[string]any)
	if !ok {
		return nil
	}

	nsObjects, ok := arrObj["NS.objects"].([]any)
	if !ok {
		return nil
	}

	var names []string
	for _, elem := range nsObjects {
		if elemUID, ok := elem.(plist.UID); ok {
			if str, ok := objects[int(elemUID)].(string); ok {
				names = append(names, str)
			}
		}
	}
	return names
}

type pipelineInfo struct {
	ID           int
	Address      uint64
	FunctionName string
}

// extractPipelineInfo extracts pipeline IDs, addresses, and function names from pipelineStateInfoData.
// The result is indexed by pipeline-state record order, matching gpuCommandInfoData pipeline indices.
//
// pipelineStateInfoData struct layout (40 bytes per record):
//
//	[0:8]   pipeline ID (one uint64, e.g., 446, 447, 448...)
//	[8:16]  pipeline address (Metal pipeline pointer)
//	[16:20] function info index
//	[20:28] reserved
//	[28:40] reserved/flags
//
// functionInfoData struct layout (48 bytes per record):
//
//	[0:28]  various metadata
//	[28:32] string index (into strings array) - key field for function name
//	[32:48] reserved
//
// The correct mapping uses functionInfoData[i][@28:32] as the string index,
// NOT pipelineStateInfoData[@24:28] which often points to empty strings.
func extractPipelineInfo(objects []any, obj1 map[string]any) []pipelineInfo {
	uid, ok := obj1["pipelineStateInfoData"].(plist.UID)
	if !ok {
		return nil
	}

	pipeSize := 40
	if size := plistUint64(obj1["pipelineStateInfoSize"]); size > 0 {
		pipeSize = int(size)
	}

	dataObj, ok := objects[int(uid)].(map[string]any)
	if !ok {
		return nil
	}

	nsData, ok := dataObj["NS.data"].([]byte)
	if !ok || len(nsData) < pipeSize {
		return nil
	}

	// Get function names from strings array
	funcNames := extractFunctionNames(objects, obj1)

	// Get functionInfoData for string index lookup
	var funcInfoData []byte
	funcInfoSize := 48
	if funcInfoUID, ok := obj1["functionInfoData"].(plist.UID); ok {
		if size := plistUint64(obj1["functionInfoSize"]); size > 0 {
			funcInfoSize = int(size)
		}
		if funcInfoObj, ok := objects[int(funcInfoUID)].(map[string]any); ok {
			funcInfoData, _ = funcInfoObj["NS.data"].([]byte)
		}
	}

	numRecs := len(nsData) / pipeSize
	numFuncInfo := 0
	if funcInfoData != nil && funcInfoSize > 0 {
		numFuncInfo = len(funcInfoData) / funcInfoSize
	}

	infos := make([]pipelineInfo, numRecs)

	for i := range numRecs {
		off := i * pipeSize
		rec := nsData[off : off+pipeSize]

		infos[i].ID = int(binary.LittleEndian.Uint64(rec[0:8]))
		infos[i].Address = binary.LittleEndian.Uint64(rec[8:16])

		// Use functionInfoData[i][@28:32] for string index (correct mapping)
		if funcInfoData != nil && i < numFuncInfo {
			fiOff := i * funcInfoSize
			fiRec := funcInfoData[fiOff : fiOff+funcInfoSize]
			funcStrIdx := int(binary.LittleEndian.Uint32(fiRec[28:32]))
			if funcStrIdx >= 0 && funcStrIdx < len(funcNames) {
				infos[i].FunctionName = funcNames[funcStrIdx]
				continue
			}
		}

		// Fall back: try using pipeline index directly into strings array
		if i < len(funcNames) {
			infos[i].FunctionName = funcNames[i]
		}
	}

	return infos
}

// compilerFunctionNames returns pipeline ID to shader compiler function name, read
// from pipelinePerformanceStatistics[id]["Compile Performance"]["Function Name"].
//
// Some archives leave a function's name-string index pointing at the empty entry of
// the strings array, so the strings table alone cannot name every pipeline. The
// compiler statistics carry the name independently, keyed by pipeline ID.
func compilerFunctionNames(objects []any, ppsIdx int) map[int]string {
	if ppsIdx <= 0 || ppsIdx >= len(objects) {
		return nil
	}
	ppsObj, ok := objects[ppsIdx].(map[string]any)
	if !ok {
		return nil
	}
	keys, keysOK := ppsObj["NS.keys"].([]any)
	values, valsOK := ppsObj["NS.objects"].([]any)
	if !keysOK || !valsOK || len(keys) != len(values) {
		return nil
	}

	names := make(map[int]string)
	for i, key := range keys {
		keyUID, ok := key.(plist.UID)
		if !ok || int(keyUID) >= len(objects) {
			continue
		}
		id := int(plistUint64(objects[int(keyUID)]))
		compile, ok := nsDictionary(objects, nsDictionary(objects, values[i])["Compile Performance"])["Function Name"].(string)
		if ok && compile != "" {
			names[id] = compile
		}
	}
	return names
}

// applyCompilerNames fills in names the strings array could not supply.
func applyCompilerNames(infos []pipelineInfo, names map[int]string) {
	for i := range infos {
		if infos[i].FunctionName == "" {
			infos[i].FunctionName = names[infos[i].ID]
		}
	}
}

// nsDictionary resolves an archived NSDictionary reference to a map of its
// string-keyed entries, with UID values dereferenced. Non-string keys are skipped.
func nsDictionary(objects []any, ref any) map[string]any {
	dict, ok := deref(objects, ref).(map[string]any)
	if !ok {
		return nil
	}
	keys, _ := dict["NS.keys"].([]any)
	values, _ := dict["NS.objects"].([]any)
	if len(keys) != len(values) {
		return nil
	}

	out := make(map[string]any, len(keys))
	for i, key := range keys {
		keyUID, ok := key.(plist.UID)
		if !ok || int(keyUID) >= len(objects) {
			continue
		}
		name, ok := objects[int(keyUID)].(string)
		if !ok {
			continue
		}
		out[name] = deref(objects, values[i])
	}
	return out
}

func deref(objects []any, v any) any {
	if uid, ok := v.(plist.UID); ok && int(uid) < len(objects) {
		return objects[int(uid)]
	}
	return v
}

func attachPipelineMetadata(pipelines []PipelineStats, infos []pipelineInfo, addressToName map[uint64]string) {
	if len(pipelines) == 0 || len(infos) == 0 {
		return
	}
	metaByID := make(map[int]pipelineInfo, len(infos))
	for _, info := range infos {
		metaByID[info.ID] = info
	}
	for i := range pipelines {
		info, ok := metaByID[pipelines[i].PipelineID]
		if !ok {
			continue
		}
		pipelines[i].PipelineAddress = info.Address
		if addressToName != nil {
			if name, ok := addressToName[info.Address]; ok {
				pipelines[i].FunctionName = name
				continue
			}
		}
		pipelines[i].FunctionName = info.FunctionName
	}
}

func pipelineDispatchMaps(infos []pipelineInfo, addressToName map[uint64]string) (map[int]string, map[int]int) {
	pipelineToName := make(map[int]string, len(infos))
	pipelineToID := make(map[int]int, len(infos))
	for i, info := range infos {
		name := info.FunctionName
		if addressToName != nil {
			if mapped, ok := addressToName[info.Address]; ok {
				name = mapped
			}
		}
		pipelineToName[i] = name
		pipelineToID[i] = info.ID
	}
	return pipelineToName, pipelineToID
}

// extractDispatchInfoWithMap is like extractDispatchInfo but uses maps for pipeline-to-name and pipeline-to-ID lookup.
func extractDispatchInfoWithMap(objects []any, gpuCmdIdx, recordSize int, pipelineToName map[int]string, pipelineToID map[int]int) []DispatchInfo {
	if gpuCmdIdx >= len(objects) {
		return nil
	}

	dataObj, ok := objects[gpuCmdIdx].(map[string]any)
	if !ok {
		return nil
	}

	nsData, ok := dataObj["NS.data"].([]byte)
	if !ok || len(nsData) < recordSize {
		return nil
	}

	numRecords := len(nsData) / recordSize
	dispatches := make([]DispatchInfo, 0, numRecords)

	var prevTime int
	for i := range numRecords {
		off := i * recordSize
		rec := nsData[off : off+recordSize]

		pipelineIdx := int(binary.LittleEndian.Uint64(rec[8:16]) >> 32)
		cumTime := int(binary.LittleEndian.Uint64(rec[16:24]))
		// The encoder index is at [4:8], not [24:28]. [24:28] holds the
		// constant 2 in every record, so reading it there put every dispatch
		// on encoder 2. Checked against encoderInfoData, whose first-command
		// and command-count fields tile all commands exactly: [4:8] agrees
		// with that ownership for every record.
		encoderIdx := int(binary.LittleEndian.Uint32(rec[4:8]))

		duration := cumTime
		if i > 0 {
			duration = cumTime - prevTime
		}
		prevTime = cumTime

		dispatches = append(dispatches, DispatchInfo{
			Index:         i,
			PipelineIndex: pipelineIdx,
			PipelineID:    pipelineToID[pipelineIdx],
			FunctionName:  pipelineToName[pipelineIdx],
			EncoderIndex:  encoderIdx,
			CumulativeUs:  cumTime,
			DurationUs:    duration,
		})
	}

	return dispatches
}

// extractEncoderTimings parses encoder timing data from encoderInfoData.
// Each record is 40 bytes with structure:
//
//	[0:8]   sequence ID / timestamp
//	[8:16]  start timestamp
//	[16:24] cumulative offset in microseconds (key timing field!)
//	[24:32] unknown (possibly dependency info)
//	[32:40] unknown
func extractEncoderTimings(objects []interface{}, encoderInfoIdx, recordSize int) []EncoderTimingInfo {
	if encoderInfoIdx >= len(objects) {
		return nil
	}

	dataObj, ok := objects[encoderInfoIdx].(map[string]interface{})
	if !ok {
		return nil
	}

	nsData, ok := dataObj["NS.data"].([]byte)
	if !ok || len(nsData) < recordSize {
		return nil
	}

	numRecords := len(nsData) / recordSize
	timings := make([]EncoderTimingInfo, 0, numRecords)

	var prevOffset int
	for i := 0; i < numRecords; i++ {
		offset := i * recordSize
		rec := nsData[offset : offset+recordSize]

		timing := EncoderTimingInfo{
			Index:          i,
			SequenceID:     binary.LittleEndian.Uint64(rec[0:8]),
			StartTimestamp: binary.LittleEndian.Uint64(rec[8:16]),
		}

		// The third field (bytes 16-24) appears to be cumulative microseconds
		// based on analysis showing values like 290, 880, 1460... increasing
		timing.EndOffsetMicros = int(binary.LittleEndian.Uint64(rec[16:24]))

		// Calculate per-encoder duration
		// EndOffsetMicros is cumulative, so duration = current - previous
		if i == 0 {
			timing.DurationMicros = timing.EndOffsetMicros // First encoder's duration is its end offset
		} else {
			timing.DurationMicros = timing.EndOffsetMicros - prevOffset
		}
		prevOffset = timing.EndOffsetMicros

		timings = append(timings, timing)
	}

	return timings
}

func extractPipelineStats(objects []interface{}, ppsIdx int) []PipelineStats {
	if ppsIdx >= len(objects) {
		return nil
	}

	ppsObj, ok := objects[ppsIdx].(map[string]interface{})
	if !ok {
		return nil
	}

	// NSDictionary has NS.keys and NS.objects
	keys, keysOK := ppsObj["NS.keys"].([]interface{})
	values, valsOK := ppsObj["NS.objects"].([]interface{})
	if !keysOK || !valsOK || len(keys) != len(values) {
		return nil
	}

	var pipelines []PipelineStats
	for i, keyUID := range keys {
		valUID := values[i]

		// Get pipeline ID from key
		pipelineID := 0
		if uid, ok := keyUID.(plist.UID); ok {
			if id, ok := objects[int(uid)].(uint64); ok {
				pipelineID = int(id)
			} else if id, ok := objects[int(uid)].(int64); ok {
				pipelineID = int(id)
			}
		}

		// Get stats from value
		statsIdx := 0
		if uid, ok := valUID.(plist.UID); ok {
			statsIdx = int(uid)
		}

		if statsIdx >= len(objects) {
			continue
		}

		statsObj, ok := objects[statsIdx].(map[string]interface{})
		if !ok {
			continue
		}

		ps := PipelineStats{PipelineID: pipelineID}
		assignPipelineStatFields(&ps, resolveKeyedDictionary(objects, statsObj))

		pipelines = append(pipelines, ps)
	}

	return pipelines
}

// resolveKeyedDictionary flattens an NSKeyedArchiver NSDictionary into a plain
// map, dereferencing the UIDs its keys and values hold into objects.
func resolveKeyedDictionary(objects []interface{}, dict map[string]interface{}) map[string]interface{} {
	keys, _ := dict["NS.keys"].([]interface{})
	values, _ := dict["NS.objects"].([]interface{})

	resolved := make(map[string]interface{}, len(keys))
	for i, key := range keys {
		keyUID, ok := key.(plist.UID)
		if !ok || i >= len(values) || int(keyUID) >= len(objects) {
			continue
		}
		name := ""
		if s, ok := objects[int(keyUID)].(string); ok {
			name = s
		}
		valUID, ok := values[i].(plist.UID)
		if !ok {
			resolved[name] = values[i]
			continue
		}
		if int(valUID) < len(objects) {
			resolved[name] = objects[int(valUID)]
		}
	}
	return resolved
}

// assignPipelineStatFields copies the shader compilation statistics Xcode
// archives under well-known names. Both .gpuprofiler_raw streamData and the
// store sections of a capture bundle use these names.
func assignPipelineStatFields(ps *PipelineStats, keyMap map[string]interface{}) {
	ps.TemporaryRegisterCount = getInt(keyMap, "Temporary register count")
	ps.UniformRegisterCount = getInt(keyMap, "Uniform register count")
	ps.SpilledBytes = getInt(keyMap, "Spilled bytes")
	ps.ThreadInvariantSpilled = getInt(keyMap, "Thread invariant spilled bytes")
	ps.ThreadgroupMemory = getInt(keyMap, "Threadgroup memory")
	ps.InstructionCount = getInt(keyMap, "Instruction count")
	ps.ALUInstructionCount = getInt(keyMap, "ALU instruction count")
	ps.FP32InstructionCount = getInt(keyMap, "FP32 instruction count")
	ps.FP16InstructionCount = getInt(keyMap, "FP16 instruction count")
	ps.INT32InstructionCount = getInt(keyMap, "INT32 instruction count")
	ps.INT16InstructionCount = getInt(keyMap, "INT16 instruction count")
	ps.BranchInstructionCount = getInt(keyMap, "Branch instruction count")
	ps.DeviceLoadCount = getInt(keyMap, "Device load instruction count")
	ps.DeviceStoreCount = getInt(keyMap, "Device store instruction count")
	ps.DeviceAtomicCount = getInt(keyMap, "Device atomic instruction count")
	ps.TextureReadCount = getInt(keyMap, "Texture reads instruction count")
	ps.TextureWriteCount = getInt(keyMap, "Texture writes instruction count")
	ps.ThreadgroupLoadCount = getInt(keyMap, "Threadgroup load instruction count")
	ps.ThreadgroupStoreCount = getInt(keyMap, "Threadgroup store instruction count")
	ps.ThreadgroupAtomicCount = getInt(keyMap, "Threadgroup atomic instruction count")
	ps.WaitInstructionCount = getInt(keyMap, "Wait instruction count")
	ps.ConstantCalculationTemporaryRegisterCount = getInt(keyMap, "Constant calculation temporary register count")
	ps.ConstantCalculationPhasePresent = getBool(keyMap, "Constant calculation phase present")
	ps.CompilationTimeMs = getFloat(keyMap, "Compilation time in milliseconds")
}

func getInt(m map[string]interface{}, key string) int {
	if v, ok := m[key]; ok {
		switch val := v.(type) {
		case int:
			return val
		case int64:
			return int(val)
		case uint64:
			return int(val)
		case float64:
			return int(val)
		}
	}
	return 0
}

func getFloat(m map[string]interface{}, key string) float64 {
	if v, ok := m[key]; ok {
		switch val := v.(type) {
		case float64:
			return val
		case int:
			return float64(val)
		case int64:
			return float64(val)
		case uint64:
			return float64(val)
		}
	}
	return 0
}

func getBool(m map[string]interface{}, key string) bool {
	if v, ok := m[key]; ok {
		if value, ok := v.(bool); ok {
			return value
		}
	}
	return false
}

// ExtractEncoderTimingsFromProfiler extracts per-encoder timing data from the trace's
// .gpuprofiler_raw/streamData. streamData carries replay profiler timing, and
// ParseStreamData also records APSTimelineData-derived timing source details
// such as ReplayerGPUTime and command-buffer timestamps when they are present.
//
// The returned timings are indexed to match ParseComputeEncoders() output order.
// Call this function first for profiled exports. If it fails, callers should
// fall back to TimingMetricsExtractor and preserve its approximate timing labels
// for kdebug/signpost heuristics or synthetic estimates.
//
// Usage:
//
//	timings, err := counter.ExtractEncoderTimingsFromProfiler(trace)
//	if err != nil {
//	    // Fall back to approximate kdebug/signpost or synthetic timing.
//	}
func ExtractEncoderTimingsFromProfiler(t *trace.Trace) ([]EncoderTimingInfo, int, error) {
	// Find .gpuprofiler_raw directory
	perfDir := t.Path + ".gpuprofiler_raw"
	if _, err := os.Stat(perfDir); os.IsNotExist(err) {
		// Check inside trace bundle
		entries, err := os.ReadDir(t.Path)
		if err != nil {
			return nil, 0, fmt.Errorf("no performance counter data: %w", err)
		}

		found := false
		for _, entry := range entries {
			if entry.IsDir() && filepath.Ext(entry.Name()) == ".gpuprofiler_raw" {
				perfDir = filepath.Join(t.Path, entry.Name())
				found = true
				break
			}
		}

		if !found {
			return nil, 0, fmt.Errorf("no .gpuprofiler_raw directory found")
		}
	}

	// Parse streamData for timing. APSTimelineData, when present, is captured
	// in stats.TimingSource and effective/command-buffer totals; this helper
	// returns encoderInfoData-derived per-encoder timings for existing callers.
	stats, err := ParseStreamData(perfDir, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("parse streamData: %w", err)
	}

	if len(stats.EncoderTimings) == 0 {
		return nil, 0, fmt.Errorf("no encoder timing data found")
	}

	// Get encoder labels from trace to correlate
	encoders := t.ParseComputeEncoders()
	if len(encoders) > 0 {
		// Correlate labels - encoders should match by index
		for i := range stats.EncoderTimings {
			if i < len(encoders) {
				stats.EncoderTimings[i].Label = encoders[i].Label
			}
		}
	}

	return stats.EncoderTimings, stats.TotalTimeUs, nil
}

// ExtractPipelineStatsFromTrace extracts pipeline compilation stats from the trace's
// .gpuprofiler_raw directory. This provides instruction counts, register allocation,
// and other compilation metrics.
func ExtractPipelineStatsFromTrace(t *trace.Trace) (*StreamDataStats, error) {
	return extractPipelineStatsFromTrace(t, t.FunctionToName)
}

// ExtractPipelineStatsFromTraceStreamData extracts pipeline stats using the
// function names embedded in streamData. This matches Xcode's profiler tables
// for perfdata bundles when capture-side function-address mappings disagree.
func ExtractPipelineStatsFromTraceStreamData(t *trace.Trace) (*StreamDataStats, error) {
	return extractPipelineStatsFromTrace(t, nil)
}

func extractPipelineStatsFromTrace(t *trace.Trace, addressToName map[uint64]string) (*StreamDataStats, error) {
	// Find .gpuprofiler_raw directory
	perfDir := t.Path + ".gpuprofiler_raw"
	if _, err := os.Stat(perfDir); os.IsNotExist(err) {
		// Check inside trace bundle
		entries, err := os.ReadDir(t.Path)
		if err != nil {
			return nil, fmt.Errorf("no performance counter data: %w", err)
		}

		found := false
		for _, entry := range entries {
			if entry.IsDir() && filepath.Ext(entry.Name()) == ".gpuprofiler_raw" {
				perfDir = filepath.Join(t.Path, entry.Name())
				found = true
				break
			}
		}

		if !found {
			return nil, fmt.Errorf("no .gpuprofiler_raw directory found")
		}
	}

	var stats *StreamDataStats
	var err error
	stats, err = ParseStreamData(perfDir, addressToName)
	if err != nil {
		return nil, fmt.Errorf("parse streamData: %w", err)
	}

	return stats, nil
}

// extractDataArray extracts an array of NSData blobs from a keyed field.
func extractDataArray(objects []any, obj1 map[string]any, key string) [][]byte {
	uid, ok := obj1[key].(plist.UID)
	if !ok {
		return nil
	}

	arrObj, ok := objects[int(uid)].(map[string]any)
	if !ok {
		return nil
	}

	nsObjects, ok := arrObj["NS.objects"].([]any)
	if !ok {
		return nil
	}

	var result [][]byte
	for _, item := range nsObjects {
		itemUID, ok := item.(plist.UID)
		if !ok {
			continue
		}
		if int(itemUID) >= len(objects) {
			continue
		}
		dataObj, ok := objects[int(itemUID)].(map[string]any)
		if !ok {
			continue
		}
		if data, ok := dataObj["NS.data"].([]byte); ok {
			result = append(result, data)
		}
	}
	return result
}

// parseAPSTimelineData parses APSTimelineData blobs for CB timestamps and timebase.
// The metadata blob (usually the largest one near the end) contains:
// - "Command Buffer Timestamps": 16 bytes per CB (uint64 start, uint64 end)
// - "Timebase": array [numer, denom] for tick-to-ns conversion
// - "Absolute Time": capture start time in ticks
//
// Additionally, blobs 1-11 contain GPRWCNTR encoder profiler data.
func parseAPSTimelineData(blobs [][]byte) *TimelineInfo {
	info := &TimelineInfo{
		TimebaseNumer: 1,
		TimebaseDenom: 1,
	}

	// Find the metadata blob (usually the largest blob, often last)
	for i := len(blobs) - 1; i >= 0; i-- {
		if len(blobs[i]) > 1000 { // Metadata blobs are large
			if parseTimelineMetadataBlob(blobs[i], info) {
				break
			}
		}
	}

	// Parse encoder profiler data from blobs 1-11 (GPRWCNTR format)
	info.EncoderProfiles = parseEncoderProfileBlobs(blobs, info.TimebaseNumer, info.TimebaseDenom)
	info.computeTimingTotals()

	return info
}

// parseTimelineMetadataBlob parses a single APSTimelineData blob for metadata.
func parseTimelineMetadataBlob(data []byte, info *TimelineInfo) bool {
	var archive map[string]any
	_, err := plist.Unmarshal(data, &archive)
	if err != nil {
		return false
	}

	objects, ok := archive["$objects"].([]any)
	if !ok {
		return false
	}

	top, ok := archive["$top"].(map[string]any)
	if !ok {
		return false
	}

	rootUID, ok := top["root"].(plist.UID)
	if !ok {
		return false
	}
	if int(rootUID) >= len(objects) {
		return false
	}

	root, ok := objects[int(rootUID)].(map[string]any)
	if !ok {
		return false
	}

	// Parse NSDictionary (NS.keys + NS.objects)
	keys, ok1 := root["NS.keys"].([]any)
	vals, ok2 := root["NS.objects"].([]any)
	if !ok1 || !ok2 || len(keys) != len(vals) {
		return false
	}

	found := false
	for i := range keys {
		keyUID, ok := keys[i].(plist.UID)
		if !ok {
			continue
		}
		key, ok := objects[int(keyUID)].(string)
		if !ok {
			continue
		}

		valUID, ok := vals[i].(plist.UID)
		if !ok {
			continue
		}
		if int(valUID) >= len(objects) {
			continue
		}
		val := objects[int(valUID)]

		switch key {
		case "Command Buffer Timestamps":
			if m, ok := val.(map[string]any); ok {
				if cbData, ok := m["NS.data"].([]byte); ok {
					parseCBTimestamps(cbData, info)
					found = true
				}
			}
		case "Absolute Time":
			info.AbsoluteTime = plistUint64(val)
		case "Continuous Time":
			info.ContinuousTime = plistUint64(val)
		case "PState":
			value := int(plistUint64(val))
			info.PState = &value
		case "ReplayerGPUTime":
			if v, ok := val.(float64); ok && v > 0 {
				info.ReplayerGPUTimeNs = uint64(v*1e9 + 0.5)
			}
		case "Restore Timestamps":
			if ranges := parseTimestampRanges(val, objects); len(ranges) > 0 {
				info.RestoreTimestamps = ranges
			}
		case "Timebase":
			if m, ok := val.(map[string]any); ok {
				if arr, ok := m["NS.objects"].([]any); ok && len(arr) >= 2 {
					if idx0, ok := arr[0].(plist.UID); ok && int(idx0) < len(objects) {
						info.TimebaseNumer = plistUint64(objects[int(idx0)])
					}
					if idx1, ok := arr[1].(plist.UID); ok && int(idx1) < len(objects) {
						info.TimebaseDenom = plistUint64(objects[int(idx1)])
					}
				}
			}
		}
	}

	return found
}

func parseTimestampRanges(val any, objects []any) []TimestampRange {
	m, ok := val.(map[string]any)
	if !ok {
		return nil
	}
	arr, ok := m["NS.objects"].([]any)
	if !ok {
		return nil
	}
	ranges := make([]TimestampRange, 0, len(arr))
	for i, elem := range arr {
		uid, ok := elem.(plist.UID)
		if !ok || int(uid) >= len(objects) {
			continue
		}
		pair, ok := objects[int(uid)].(map[string]any)
		if !ok {
			continue
		}
		pairObjects, ok := pair["NS.objects"].([]any)
		if !ok || len(pairObjects) < 2 {
			continue
		}
		start := uidValue(pairObjects[0], objects)
		end := uidValue(pairObjects[1], objects)
		if start == 0 && end == 0 {
			continue
		}
		ranges = append(ranges, TimestampRange{
			Index:      i,
			StartTicks: start,
			EndTicks:   end,
		})
	}
	return ranges
}

func uidValue(v any, objects []any) uint64 {
	if uid, ok := v.(plist.UID); ok && int(uid) < len(objects) {
		return plistUint64(objects[int(uid)])
	}
	return plistUint64(v)
}

// parseCBTimestamps parses "Command Buffer Timestamps" data.
// Format: 16 bytes per CB (uint64 start + uint64 end)
func parseCBTimestamps(data []byte, info *TimelineInfo) {
	numCBs := len(data) / 16
	for i := range numCBs {
		offset := i * 16
		if offset+16 > len(data) {
			break
		}
		start := binary.LittleEndian.Uint64(data[offset : offset+8])
		end := binary.LittleEndian.Uint64(data[offset+8 : offset+16])
		info.CommandBufferTimestamps = append(info.CommandBufferTimestamps, CommandBufferTimestamp{
			Index:      i,
			StartTicks: start,
			EndTicks:   end,
		})
	}
}

func (info *TimelineInfo) computeTimingTotals() {
	if info == nil {
		return
	}
	info.CommandBufferActiveNs, info.CommandBufferWallNs = rangeTotals(info.CommandBufferTimestamps, info.TimebaseNumer, info.TimebaseDenom)
	info.RestoreActiveNs, info.RestoreWallNs = rangeTotals(info.RestoreTimestamps, info.TimebaseNumer, info.TimebaseDenom)
}

type tickRange interface {
	rangeTicks() (uint64, uint64)
}

func (cb CommandBufferTimestamp) rangeTicks() (uint64, uint64) {
	return cb.StartTicks, cb.EndTicks
}

func (r TimestampRange) rangeTicks() (uint64, uint64) {
	return r.StartTicks, r.EndTicks
}

func rangeTotals[T tickRange](ranges []T, numer, denom uint64) (activeNs, wallNs uint64) {
	var minStart, maxEnd uint64
	for _, r := range ranges {
		start, end := r.rangeTicks()
		if end < start {
			continue
		}
		activeNs += ticksToNs(start, end, numer, denom)
		if minStart == 0 || start < minStart {
			minStart = start
		}
		if end > maxEnd {
			maxEnd = end
		}
	}
	if maxEnd >= minStart && minStart != 0 {
		wallNs = ticksToNs(minStart, maxEnd, numer, denom)
	}
	return activeNs, wallNs
}

func ticksToNs(start, end, numer, denom uint64) uint64 {
	if end < start {
		return 0
	}
	if denom == 0 {
		denom = 1
	}
	return (end - start) * numer / denom
}

// plistUint64 extracts a uint64 from various plist number types.
func plistUint64(v any) uint64 {
	switch n := v.(type) {
	case uint64:
		return n
	case int64:
		return uint64(n)
	case int:
		return uint64(n)
	case float64:
		return uint64(n)
	}
	return 0
}

// parseGPRWCNTRBlob parses a ShaderProfilerData blob in GPRWCNTR format.
//
// The record stride comes from the blob itself rather than a constant: the
// magic starts every record, so the distance to the second magic is the
// stride. See gprwcntr.go. A blob whose stride does not divide its length is
// rejected outright — the earlier fixed-size parse accepted such blobs and
// produced plausible garbage.
func parseGPRWCNTRBlob(data []byte, encoderIndex int, timebaseNumer, timebaseDenom uint64) *EncoderProfile {
	samples, stride, err := ParseGPRWCNTR(data)
	if err != nil {
		return nil
	}

	profile := &EncoderProfile{
		Index:        encoderIndex,
		RecordStride: stride,
		SampleCount:  len(samples),
		Samples:      samples,
	}
	if len(samples) == 0 {
		return profile
	}

	var minTS, maxTS uint64 = ^uint64(0), 0
	for _, s := range samples {
		if s.MachineWide() {
			profile.MachineWideSamples++
		}
		if s.Timestamp > 0 && s.Timestamp < minTS {
			minTS = s.Timestamp
		}
		if s.Timestamp > maxTS {
			maxTS = s.Timestamp
		}
	}

	// Set start/end ticks and compute duration
	if minTS != ^uint64(0) {
		profile.StartTicks = minTS
		profile.EndTicks = maxTS
		if timebaseDenom > 0 {
			profile.DurationNs = (maxTS - minTS) * timebaseNumer / timebaseDenom
		}
	}

	return profile
}

// parseEncoderProfileBlobs extracts encoder profile data from APSTimelineData blobs.
// Blobs 1-11 contain nested plists with ShaderProfilerData in GPRWCNTR format.
func parseEncoderProfileBlobs(blobs [][]byte, timebaseNumer, timebaseDenom uint64) []EncoderProfile {
	var profiles []EncoderProfile

	// Blobs 1-11 are encoder ShaderProfilerData (skip blob 0 which is Limiter Counter)
	// and skip blob 12+ which are metadata and per-command mappings
	maxEncoderBlob := 12
	if len(blobs) < maxEncoderBlob {
		maxEncoderBlob = len(blobs)
	}

	encoderIdx := 0
	for i := 1; i < maxEncoderBlob; i++ {
		if i >= len(blobs) {
			break
		}

		// Blobs are nested plists - extract metadata and ShaderProfilerData
		source, ringIdx, spd := extractEncoderBlobData(blobs[i])
		if spd == nil {
			continue
		}

		// Only process RDE_0 sources (skip Firmware and BMPR_RDE_0 which have different formats)
		if source != "RDE_0" {
			continue
		}

		profile := parseGPRWCNTRBlob(spd, encoderIdx, timebaseNumer, timebaseDenom)
		if profile != nil {
			profile.Source = source
			profile.RingBufferIndex = ringIdx
			profiles = append(profiles, *profile)
			encoderIdx++
		}
	}

	return profiles
}

// extractEncoderBlobData extracts Source, RingBufferIndex and ShaderProfilerData from a nested plist blob.
func extractEncoderBlobData(data []byte) (source string, ringIdx int, spd []byte) {
	var archive map[string]any
	if _, err := plist.Unmarshal(data, &archive); err != nil {
		return "", 0, nil
	}

	objects, ok := archive["$objects"].([]any)
	if !ok {
		return "", 0, nil
	}

	top, ok := archive["$top"].(map[string]any)
	if !ok {
		return "", 0, nil
	}

	rootUID, ok := top["root"].(plist.UID)
	if !ok {
		return "", 0, nil
	}
	if int(rootUID) >= len(objects) {
		return "", 0, nil
	}

	root, ok := objects[int(rootUID)].(map[string]any)
	if !ok {
		return "", 0, nil
	}

	// Parse NSDictionary (NS.keys + NS.objects)
	keys, ok1 := root["NS.keys"].([]any)
	vals, ok2 := root["NS.objects"].([]any)
	if !ok1 || !ok2 || len(keys) != len(vals) {
		return "", 0, nil
	}

	for i := range keys {
		keyUID, ok := keys[i].(plist.UID)
		if !ok {
			continue
		}
		key, ok := objects[int(keyUID)].(string)
		if !ok {
			continue
		}

		valUID, ok := vals[i].(plist.UID)
		if !ok {
			continue
		}
		if int(valUID) >= len(objects) {
			continue
		}
		val := objects[int(valUID)]

		switch key {
		case "Source":
			if s, ok := val.(string); ok {
				source = s
			}
		case "RingBufferIndex":
			ringIdx = int(plistUint64(val))
		case "ShaderProfilerData":
			// ShaderProfilerData can be []uint8 directly or wrapped in NSData
			if data, ok := val.([]uint8); ok {
				spd = data
			} else if m, ok := val.(map[string]any); ok {
				if data, ok := m["NS.data"].([]byte); ok {
					spd = data
				}
			}
		}
	}

	return source, ringIdx, spd
}

// CorrelateDispatchSamples correlates GPRWCNTR samples with individual dispatch commands.
// It computes absolute timestamps for each dispatch based on CB timing and counts
// how many profiler samples fall within each dispatch window.
//
// This enables per-dispatch GPU utilization analysis: kernels with higher sampling
// density (samples/µs) are using more GPU resources per unit time.
//
// The function modifies dispatches in place, populating:
//   - SampleCount: number of GPRWCNTR samples during this dispatch
//   - SamplingDensity: samples per dispatch microsecond in the estimated window
//   - StartTicks/EndTicks: estimated bounds in mach absolute ticks
//
// The archive does not join cumulative dispatch offsets to the absolute sample
// clock. This routine scales dispatch offsets across the first command-buffer
// interval, so its attribution is an estimate rather than measured timing.
func CorrelateDispatchSamples(stats *StreamDataStats) {
	if stats == nil || stats.Timeline == nil || len(stats.Dispatches) == 0 {
		return
	}

	ti := stats.Timeline
	if len(ti.CommandBufferTimestamps) == 0 {
		return
	}

	// Collect all unique GPRWCNTR sample timestamps
	tsMap := make(map[uint64]bool)
	for _, ep := range ti.EncoderProfiles {
		for _, s := range ep.Samples {
			tsMap[s.Timestamp] = true
		}
	}

	// Sort timestamps
	samples := make([]uint64, 0, len(tsMap))
	for ts := range tsMap {
		samples = append(samples, ts)
	}
	sortUint64s(samples)

	if len(samples) == 0 {
		return
	}

	// Use first CB for dispatch timing (dispatches reference encoder which maps to CB)
	// In most traces, all dispatches are in one CB
	cb := ti.CommandBufferTimestamps[0]
	cbDurationTicks := cb.EndTicks - cb.StartTicks

	// Get total dispatch time in microseconds
	totalDispatchUs := stats.Dispatches[len(stats.Dispatches)-1].CumulativeUs
	if totalDispatchUs == 0 {
		return
	}

	// Compute ticks per microsecond (inverted timebase)
	// 1 tick = numer/denom ns, so 1 µs = 1000 * denom / numer ticks
	ticksPerUs := float64(ti.TimebaseDenom) * 1000.0 / float64(ti.TimebaseNumer)

	// Scale factor to map dispatch cumulative time to CB duration
	scale := float64(cbDurationTicks) / float64(totalDispatchUs) / ticksPerUs

	// Compute absolute timestamps for each dispatch
	for i := range stats.Dispatches {
		d := &stats.Dispatches[i]

		startUs := 0
		if i > 0 {
			startUs = stats.Dispatches[i-1].CumulativeUs
		}
		endUs := d.CumulativeUs

		d.StartTicks = cb.StartTicks + uint64(float64(startUs)*ticksPerUs*scale)
		d.EndTicks = cb.StartTicks + uint64(float64(endUs)*ticksPerUs*scale)

		// Count samples in this dispatch window
		for _, ts := range samples {
			if ts >= d.StartTicks && ts < d.EndTicks {
				d.SampleCount++
			}
		}

		// Compute sampling density (samples per microsecond)
		if d.DurationUs > 0 {
			d.SamplingDensity = float64(d.SampleCount) / float64(d.DurationUs)
		}
	}
}

// sortUint64s sorts a slice of uint64 in ascending order.
func sortUint64s(s []uint64) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// DispatchSampleStats contains aggregated sample statistics by function.
type DispatchSampleStats struct {
	FunctionName    string  `json:"function_name"`
	TotalSamples    int     `json:"total_samples"`
	TotalDurationUs int     `json:"total_duration_us"`
	DispatchCount   int     `json:"dispatch_count"`
	SampleCostPct   float64 `json:"sample_cost_pct"` // Cost based on sample count
	TimeCostPct     float64 `json:"time_cost_pct"`   // Cost based on duration
	CostDelta       float64 `json:"cost_delta"`      // SampleCost - TimeCost (positive = higher GPU utilization)
	AvgDensity      float64 `json:"avg_density"`     // Average samples per µs
}

// AggregateDispatchSamples aggregates per-dispatch sample data by function name.
// Returns stats sorted by total samples descending.
func AggregateDispatchSamples(dispatches []DispatchInfo) []DispatchSampleStats {
	funcSamples := make(map[string]int)
	funcDuration := make(map[string]int)
	funcCount := make(map[string]int)

	var totalSamples, totalDuration int
	for _, d := range dispatches {
		name := d.DisplayName()
		funcSamples[name] += d.SampleCount
		funcDuration[name] += d.DurationUs
		funcCount[name]++
		totalSamples += d.SampleCount
		totalDuration += d.DurationUs
	}

	var stats []DispatchSampleStats
	for name, samples := range funcSamples {
		duration := funcDuration[name]
		count := funcCount[name]

		samplePct := 0.0
		timePct := 0.0
		if totalSamples > 0 {
			samplePct = float64(samples) * 100 / float64(totalSamples)
		}
		if totalDuration > 0 {
			timePct = float64(duration) * 100 / float64(totalDuration)
		}

		avgDensity := 0.0
		if duration > 0 {
			avgDensity = float64(samples) / float64(duration)
		}

		stats = append(stats, DispatchSampleStats{
			FunctionName:    name,
			TotalSamples:    samples,
			TotalDurationUs: duration,
			DispatchCount:   count,
			SampleCostPct:   samplePct,
			TimeCostPct:     timePct,
			CostDelta:       samplePct - timePct,
			AvgDensity:      avgDensity,
		})
	}

	// Sort by total samples descending
	for i := 1; i < len(stats); i++ {
		for j := i; j > 0 && stats[j-1].TotalSamples < stats[j].TotalSamples; j-- {
			stats[j-1], stats[j] = stats[j], stats[j-1]
		}
	}

	return stats
}
