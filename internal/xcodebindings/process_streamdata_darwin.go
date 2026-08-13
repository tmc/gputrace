//go:build darwin

package xcodebindings

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"unsafe"

	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/private/xcode/gtshaderprofiler"
)

// ProcessedStreamData reports the shader model Xcode derives from a profiler
// archive: the top-level counts, the device metadata, and one record per
// pipeline state.
// The raw cost collections are deliberately not exposed as Go slices. They
// are C arrays rather than objects. Pipeline timing is an Objective-C model
// and is exposed through PipelineRecord when Xcode builds it.
type ProcessedStreamData struct {
	Path           string `json:"path"`
	LLVMHelperPath string `json:"llvm_helper_path"`

	DrawCount    uint64           `json:"draw_count"`
	EncoderCount uint64           `json:"encoder_count"`
	CostCount    uint64           `json:"cost_count"`
	CostModel    CostModelSummary `json:"cost_model,omitempty"`
	Timeline     TimelineSummary  `json:"timeline,omitempty"`

	GPUTime          uint64 `json:"gpu_time,omitempty"`
	GPUName          string `json:"gpu_name,omitempty"`
	MetalPluginName  string `json:"metal_plugin_name,omitempty"`
	GPUGeneration    uint32 `json:"gpu_generation,omitempty"`
	PerformanceState uint32 `json:"performance_state,omitempty"`
	UnixTimestamp    int64  `json:"unix_timestamp,omitempty"`

	ShaderBinaryCount uint64 `json:"shader_binary_count"`
	GPUCommandCount   uint64 `json:"gpu_command_count"`

	Pipelines   []PipelineRecord   `json:"pipelines,omitempty"`
	Encoders    []EncoderRecord    `json:"encoders,omitempty"`
	GPUCommands []GPUCommandRecord `json:"gpu_commands,omitempty"`
	Binaries    BinarySummary      `json:"binaries"`
	Tracks      TrackSummary       `json:"tracks,omitempty"`
	USC         USCSummary         `json:"usc,omitempty"`
}

// CostModelSummary contains the scalar cost values that GTMioTraceData exposes
// without traversing its raw C arrays. It is populated only when
// GPUTRACE_MIO_SETUP_DATA_PATH=1.
type CostModelSummary struct {
	Ready             bool    `json:"ready"`
	Scope0DataMaster2 float64 `json:"scope0_data_master2,omitempty"`
	Scope4DataMaster2 float64 `json:"scope4_data_master2,omitempty"`
}

// TimelineSummary contains scalar, attributed values from Xcode's serialized
// cost timeline. It is populated only when GPUTRACE_MIO_TIMELINE_DATA=1.
// The timeline's raw arrays are C pointers and are intentionally not exposed.
type TimelineSummary struct {
	Ready                    bool                      `json:"ready"`
	DrawCount                uint64                    `json:"draw_count,omitempty"`
	EncoderCount             uint64                    `json:"encoder_count,omitempty"`
	CostCount                uint64                    `json:"cost_count,omitempty"`
	PipelineStateCount       uint64                    `json:"pipeline_state_count,omitempty"`
	ComputePositionCount     uint64                    `json:"compute_position_count,omitempty"`
	Scope0DataMaster2        float64                   `json:"scope0_data_master2,omitempty"`
	Scope4DataMaster2        float64                   `json:"scope4_data_master2,omitempty"`
	PipelineDraws            []TimelinePipelineSummary `json:"pipeline_draws,omitempty"`
	EncoderDurations         []TimelineEncoderSummary  `json:"encoder_durations,omitempty"`
	DrawDurationsDataMaster2 []uint64                  `json:"draw_durations_data_master2,omitempty"`
	// Error carries the reason the timeline could not be reconstructed.
	// Empty when Ready is true or when reconstruction was never attempted.
	Error string `json:"error,omitempty"`
}

// nsErrorMessage reads -[NSError localizedDescription] as a Go string.
// It returns "" for a nil error or one that does not respond, so a missing
// diagnostic never panics a caller that is only reporting status.
func nsErrorMessage(err objc.ID) string {
	if err == 0 || !responds(err, "localizedDescription") {
		return ""
	}
	desc := objc.Send[objc.ID](err, objc.Sel("localizedDescription"))
	if desc == 0 || !responds(desc, "UTF8String") {
		return ""
	}
	cstr := objc.Send[*byte](desc, objc.Sel("UTF8String"))
	if cstr == nil {
		return ""
	}
	return objc.GoString(cstr)
}

// TimelinePipelineSummary attributes draws and their Data Master 2 duration
// aggregate to one pipeline. The duration aggregate is not Xcode GPU Time:
// on the reference capture its sum differs from the model's GPUTime.
type TimelinePipelineSummary struct {
	ObjectID                uint64 `json:"object_id"`
	DrawCount               uint64 `json:"draw_count"`
	DrawDurationDataMaster2 uint64 `json:"draw_duration_data_master2,omitempty"`
}

// TimelineEncoderSummary attributes duration and draw count to one encoder.
type TimelineEncoderSummary struct {
	EncoderIndex uint32 `json:"encoder_index"`
	DrawCount    uint64 `json:"draw_count"`
	KickDuration uint64 `json:"kick_duration_data_master2"`
}

// TrackSummary contains the top-level track model generated by
// GTMioTraceDataHelper. It is populated only when GPUTRACE_MIO_TRACE_TRACKS=1.
// The per-encoder and per-pipeline aggregate constructors are not represented:
// they return empty tracks on the measured fixture.
type TrackSummary struct {
	TopDrawCount   uint64        `json:"top_draw_count,omitempty"`
	TopBinaryCount uint64        `json:"top_binary_count,omitempty"`
	TopKickCount   uint64        `json:"top_kick_count,omitempty"`
	TopRIACount    uint64        `json:"top_ria_count,omitempty"`
	DrawSamples    []TrackSample `json:"draw_samples,omitempty"`
	KickSamples    []TrackSample `json:"kick_samples,omitempty"`
}

// TrackSample is a bounded, attributed sample from a GTMioTraceTrack.
type TrackSample struct {
	FirstIndex uint64             `json:"first_index"`
	Duration   uint64             `json:"duration"`
	Empty      bool               `json:"empty"`
	Lanes      []TrackLaneSummary `json:"lanes,omitempty"`
}

// TrackLaneSummary reports object-valued metadata for one track lane. The
// lane indexes themselves are a C pointer and are intentionally not read.
type TrackLaneSummary struct {
	LaneID     int32  `json:"lane_id"`
	IndexCount uint64 `json:"index_count"`
	Empty      bool   `json:"empty"`
}

// USCSummary reports the structural execution data that the framework builds
// when the raw data path is enabled. Binary-index attribution is intentionally
// absent: firstBinaryIndexForCliqueAtIndex: is not reproducible.
type USCSummary struct {
	CoreCount        uint64            `json:"core_count,omitempty"`
	TotalCliqueCount uint64            `json:"total_clique_count,omitempty"`
	TotalKickCount   uint64            `json:"total_kick_count,omitempty"`
	TotalTileCount   uint64            `json:"total_tile_count,omitempty"`
	CliqueSamples    []USCCliqueSample `json:"clique_samples,omitempty"`
}

// USCCliqueSample is a stable pipeline attribution for one USC clique.
type USCCliqueSample struct {
	USCIndex        uint32 `json:"usc_index"`
	CliqueIndex     uint32 `json:"clique_index"`
	PipelineStateID uint64 `json:"pipeline_state_id"`
	FirstPC         uint64 `json:"first_pc"`
}

// PipelineRecord is one compiled pipeline as Xcode models it. The fields are
// the named form of the 40-byte pipeline record the archive stores, which
// GTMioShaderProfilerPipelineState wraps.
type PipelineRecord struct {
	ObjectID         uint64 `json:"object_id"`
	PointerID        uint64 `json:"pointer_id"`
	FunctionIndex    uint64 `json:"function_index"`
	FunctionObjectID uint64 `json:"function_object_id,omitempty"`
	LibraryObjectID  uint64 `json:"library_object_id,omitempty"`
	Index            uint32 `json:"index"`
	NumGPUCommands   uint32 `json:"num_gpu_commands"`
	FunctionName     string `json:"function_name,omitempty"`
	ComputeTime      uint64 `json:"compute_time,omitempty"`

	// The MCA fields describe the register allocation the compiler chose for
	// this pipeline. They are read only when MCA analysis is requested, and on
	// a processor-built model they stay zero; see readMCARegisters.
	MCAHighRegister int32  `json:"mca_high_register,omitempty"`
	MCAAllocatedGPR int32  `json:"mca_allocated_gpr,omitempty"`
	MCABinaryCount  uint64 `json:"mca_binary_count,omitempty"`
}

// ShaderCost is one row in Xcode's All Shaders cost table.
type ShaderCost struct {
	Name        string  `json:"name"`
	ComputeTime uint64  `json:"compute_time"`
	Cost        float64 `json:"cost"`
}

// EncoderRecord identifies one encoder and its contiguous GPU-command range.
// It is structural metadata only: these fields do not supply a busy-time
// interval or establish a command-buffer clock.
type EncoderRecord struct {
	Index                uint32 `json:"index"`
	FunctionIndex        uint64 `json:"function_index"`
	GPUCommandStartIndex uint32 `json:"gpu_command_start_index"`
	NumGPUCommands       uint32 `json:"num_gpu_commands"`
}

// GPUCommandRecord is one processed GPU command and its capture-local
// ownership identifiers. CommandBufferIndex is an identifier, not a duration
// or timestamp.
type GPUCommandRecord struct {
	Index                 uint32 `json:"index"`
	CommandBufferIndex    uint32 `json:"command_buffer_index"`
	EncoderInfoIndex      uint32 `json:"encoder_info_index"`
	EncoderObjectID       uint64 `json:"encoder_object_id"`
	FunctionIndex         uint64 `json:"function_index"`
	PipelineInfoIndex     uint32 `json:"pipeline_info_index"`
	PipelineStateObjectID uint64 `json:"pipeline_state_object_id"`
}

// BinarySummary aggregates the compiled shader binaries. HighRegister is the
// largest live-register count over every instruction of every binary. It is a
// whole-capture aggregate; the processor does not provide a reproducible
// binary-to-pipeline edge, so it must not be used as a per-kernel metric.
//
// InstructionsExecuted stays zero unless the capture recorded execution
// counters; the instruction tables themselves are always present.
type BinarySummary struct {
	Count                 uint64                 `json:"count"`
	InstructionCount      uint64                 `json:"instruction_count"`
	InstructionsExecuted  uint64                 `json:"instructions_executed"`
	HighRegister          int32                  `json:"high_register"`
	DebugLocationCount    uint64                 `json:"debug_location_count"`
	DebugLocations        []ShaderSourceLocation `json:"debug_locations,omitempty"`
	SourceCost            SourceCostEvidence     `json:"source_cost"`
	DebugSelectorFile     string                 `json:"-"`
	DebugSelectorFunction string                 `json:"-"`
	DebugSelectorString   string                 `json:"-"`
}

// SourceCostEvidence reports whether the processed model contains the inputs
// needed for measured source-line attribution. Ready is false unless every
// edge in that join has been established; callers must not infer readiness
// from a non-nil private-framework pointer.
type SourceCostEvidence struct {
	Ready                          bool   `json:"ready"`
	CostModelReady                 bool   `json:"cost_model_ready"`
	Status                         string `json:"status"`
	Reason                         string `json:"reason"`
	NonzeroInstructionAddressCount uint64 `json:"nonzero_instruction_address_count"`
	DebugRangeInstructionCount     uint64 `json:"debug_range_instruction_count"`
	NonzeroInstructionCostCount    uint64 `json:"nonzero_instruction_cost_count"`
	CostBearingBinaryCount         uint64 `json:"cost_bearing_binary_count"`
}

// ShaderSourceLocation maps a compiler-reported shader location to its source
// file and function. The capture contains no cost attributed to this location.
type ShaderSourceLocation struct {
	BinaryIndex  uint64 `json:"binary_index"`
	FilePath     string `json:"file_path"`
	FunctionName string `json:"function_name"`
	Line         uint32 `json:"line"`
	Column       uint32 `json:"column"`
}

// llvmHelperRelPath locates GTLLVMHelper inside a Developer directory.
const llvmHelperRelPath = "Platforms/MacOSX.platform/Developer/Library/GPUToolsPlatform/PlugIns/GTLLVMHelper"

// ProcessStreamData builds Xcode's shader trace model for a streamData archive
// and reports its top-level counts.
//
// The work is expensive: GTShaderProfilerStreamDataProcessor spawns the
// GTLLVMHelper child process and disassembles every shader in the capture, so
// callers should treat this as an opt-in diagnostic rather than part of a
// normal trace read. Processing is asynchronous, and the counts are only valid
// once every wait selector has returned.
func ProcessStreamData(path string) (ProcessedStreamData, error) {
	summary := ProcessedStreamData{Path: path}
	var err error
	// Autorelease pools are thread-affine and this path starts threads of its
	// own, so hold the goroutine on one OS thread for the whole push/pop pair.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	objc.AutoreleasePool(func() {
		err = processStreamData(&summary, false)
	})
	return summary, err
}

// ShaderCosts builds Xcode's shader model and returns the same pipeline timing
// rows used by its All Shaders table.
func ShaderCosts(path string) ([]ShaderCost, uint64, error) {
	summary := ProcessedStreamData{Path: path}
	var err error
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	objc.AutoreleasePool(func() {
		err = processStreamData(&summary, true)
	})
	if err != nil {
		return nil, 0, err
	}
	return shaderCostRows(summary)
}

func shaderCostRows(summary ProcessedStreamData) ([]ShaderCost, uint64, error) {
	if summary.GPUTime == 0 {
		return nil, 0, fmt.Errorf("Xcode shader profiler returned zero GPU time")
	}
	rows := make([]ShaderCost, 0, len(summary.Pipelines))
	for _, pipeline := range summary.Pipelines {
		name := pipeline.FunctionName
		if name == "" && pipeline.LibraryObjectID != 0 && pipeline.FunctionObjectID > pipeline.LibraryObjectID {
			name = fmt.Sprintf("MTLFunction %d", pipeline.FunctionObjectID-pipeline.LibraryObjectID)
		}
		rows = append(rows, ShaderCost{
			Name:        name,
			ComputeTime: pipeline.ComputeTime,
			Cost:        100 * float64(pipeline.ComputeTime) / float64(summary.GPUTime),
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].ComputeTime != rows[j].ComputeTime {
			return rows[i].ComputeTime > rows[j].ComputeTime
		}
		return rows[i].Name < rows[j].Name
	})
	return rows, summary.GPUTime, nil
}

// processedModel is one in-flight or completed ProcessStreamData call.
// done closes when model and err are final.
type processedModel struct {
	done  chan struct{}
	model ProcessedStreamData
	err   error
}

// modelCache holds successful results keyed by archive identity, so repeated
// reads of the same archive pay the disassembly cost once. Failures are
// evicted: they are usually environmental (missing Xcode, absent helper) and
// should not be sticky for the life of the process.
var (
	modelCacheMu sync.Mutex
	modelCache   = map[string]*processedModel{}
)

// modelCacheKey identifies an archive by path, size, and modification time, so
// a recaptured archive at the same path is not served from the cache. It
// returns false when the archive cannot be stat'd, which disables caching
// rather than risking a stale hit.
func modelCacheKey(path string) (string, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return "", false
	}
	return fmt.Sprintf("%s\x00%d\x00%d", path, info.Size(), info.ModTime().UnixNano()), true
}

// sharedProcessedModel returns the in-flight or cached build for path, starting
// one if needed. Concurrent callers for the same archive share a single build
// instead of each spawning GTLLVMHelper.
func sharedProcessedModel(path string) *processedModel {
	key, cacheable := modelCacheKey(path)
	if !cacheable {
		entry := &processedModel{done: make(chan struct{})}
		go runProcessedModel(entry, path, "", false)
		return entry
	}

	modelCacheMu.Lock()
	if entry, ok := modelCache[key]; ok {
		modelCacheMu.Unlock()
		return entry
	}
	entry := &processedModel{done: make(chan struct{})}
	modelCache[key] = entry
	modelCacheMu.Unlock()

	go runProcessedModel(entry, path, key, true)
	return entry
}

func runProcessedModel(entry *processedModel, path, key string, cacheable bool) {
	entry.model, entry.err = ProcessStreamData(path)
	if entry.err != nil && cacheable {
		modelCacheMu.Lock()
		delete(modelCache, key)
		modelCacheMu.Unlock()
	}
	close(entry.done)
}

// WithProcessedModel builds a summary of Xcode's shader trace model and passes
// it to fn.
//
// The build runs on its own goroutine, so a canceled context returns promptly.
// The private-framework call itself cannot be interrupted: it continues in the
// background, and its result is cached for a later caller rather than
// discarded. Repeated calls for the same archive reuse that result, since a
// build spawns GTLLVMHelper and disassembles every shader in the capture.
func WithProcessedModel(ctx context.Context, path string, fn func(model *ProcessedStreamData) error) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	if fn == nil {
		return fmt.Errorf("callback fn is nil")
	}

	entry := sharedProcessedModel(path)

	var done <-chan struct{}
	if ctx != nil {
		done = ctx.Done()
	}
	select {
	case <-entry.done:
	case <-done:
		return ctx.Err()
	}

	if entry.err != nil {
		return entry.err
	}
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	model := entry.model
	return fn(&model)
}

func processStreamData(summary *ProcessedStreamData, requireDataPath bool) error {
	loadPath := summary.Path
	setupDataPath := requireDataPath || mioDataPathRequired()
	if setupDataPath && filepath.Base(loadPath) == "streamData" {
		// The data-path setup resolves sibling Counters_f_*.raw files only when
		// the archive directory, rather than its inner streamData file, is the
		// URL passed to GTShaderProfilerStreamData.
		if info, statErr := os.Stat(filepath.Dir(loadPath)); statErr == nil && info.IsDir() {
			loadPath = filepath.Dir(loadPath)
		}
	}
	stream, err := loadStreamData(loadPath)
	if err != nil {
		return err
	}
	if setupDataPath {
		if !responds(stream, "_setupDataPath") {
			return fmt.Errorf("GTShaderProfilerStreamData does not respond to _setupDataPath")
		}
		if path := objc.Send[objc.ID](stream, objc.Sel("_setupDataPath")); path == 0 {
			return fmt.Errorf("_setupDataPath returned nil")
		}
	}
	helper, err := llvmHelperPath()
	if err != nil {
		return err
	}
	summary.LLVMHelperPath = helper

	processor, err := newStreamDataProcessor(stream, helper)
	if err != nil {
		return err
	}
	// Release only after the counts are read. The model's lazy collections
	// reference the helper process, which exits with the processor.
	defer func() {
		if responds(processor, "release") {
			objc.Send[objc.ID](processor, objc.Sel("release"))
		}
	}()

	// Each pass runs asynchronously; the matching wait must follow.
	for _, selector := range []string{
		"processStreamData",
		"processShaderProfilerStreamData",
		"processTimelineStreamData",
	} {
		if !objc.RespondsToSelector(processor, objc.Sel(selector)) {
			return fmt.Errorf("GTShaderProfilerStreamDataProcessor does not respond to %s", selector)
		}
		objc.Send[objc.ID](processor, objc.Sel(selector))
	}
	for _, selector := range []string{
		"waitUntilShaderProfilerFinished",
		"waitUntilTimelineFinished",
		"waitUntilFinished",
	} {
		if !objc.RespondsToSelector(processor, objc.Sel(selector)) {
			return fmt.Errorf("GTShaderProfilerStreamDataProcessor does not respond to %s", selector)
		}
		objc.Send[objc.ID](processor, objc.Sel(selector))
	}

	if !responds(processor, "mioData") {
		return fmt.Errorf("GTShaderProfilerStreamDataProcessor does not respond to mioData")
	}
	mio := objc.Send[objc.ID](processor, objc.Sel("mioData"))
	if mio == 0 {
		return fmt.Errorf("mioData returned nil")
	}
	summary.DrawCount = uint64Property(mio, "drawCount")
	summary.EncoderCount = uint64Property(mio, "encoderCount")
	summary.CostCount = uint64Property(mio, "costCount")
	if os.Getenv("GPUTRACE_MIO_SETUP_DATA_PATH") == "1" {
		summary.CostModel = readCostModel(mio)
	}
	readResult(summary, processor, setupDataPath)
	if os.Getenv("GPUTRACE_MIO_TIMELINE_DATA") == "1" {
		summary.Timeline = readSerializedTimeline(mio, stream, summary.Pipelines)
	}
	if os.Getenv("GPUTRACE_MIO_TRACE_TRACKS") == "1" {
		summary.Tracks = readTraceTracks(mio)
	}
	if os.Getenv("GPUTRACE_MIO_USC_CLIQUES") == "1" {
		summary.USC = readUSCSummary(mio)
	}
	return nil
}

func mioDataPathRequired() bool {
	for _, name := range []string{
		"GPUTRACE_MIO_SETUP_DATA_PATH",
		"GPUTRACE_MIO_TIMELINE_DATA",
		"GPUTRACE_MIO_TRACE_TRACKS",
		"GPUTRACE_MIO_USC_CLIQUES",
	} {
		if os.Getenv(name) == "1" {
			return true
		}
	}
	return false
}

func readUSCSummary(mio objc.ID) USCSummary {
	var summary USCSummary
	for i, usc := range elementsOf(objectFor(mio, "uscs")) {
		if usc == 0 {
			continue
		}
		summary.CoreCount++
		cliques := uint64Property(usc, "cliquesCount")
		summary.TotalCliqueCount += cliques
		summary.TotalKickCount += uint64Property(usc, "kicksCount")
		summary.TotalTileCount += uint64Property(usc, "tilesCount")
		if i >= 2 || !responds(usc, "pipelineStateIdForCliqueAtIndex:") || !responds(usc, "firstPCForCliqueAtIndex:") {
			continue
		}
		limit := cliques
		if limit > 6 {
			limit = 6
		}
		for c := uint64(0); c < limit; c++ {
			idx := uint32(c)
			summary.CliqueSamples = append(summary.CliqueSamples, USCCliqueSample{
				USCIndex:        uint32(i),
				CliqueIndex:     idx,
				PipelineStateID: objc.Send[uint64](usc, objc.Sel("pipelineStateIdForCliqueAtIndex:"), idx),
				FirstPC:         objc.Send[uint64](usc, objc.Sel("firstPCForCliqueAtIndex:"), idx),
			})
		}
	}
	return summary
}

func readTraceTracks(mio objc.ID) TrackSummary {
	var summary TrackSummary
	cls := objc.GetClass("GTMioTraceDataHelper")
	if cls == 0 {
		return summary
	}
	allocated := objc.Send[objc.ID](objc.ID(cls), objc.Sel("alloc"))
	if allocated == 0 || !responds(allocated, "initWithTraceData:") {
		return summary
	}
	helper := objc.Send[objc.ID](allocated, objc.Sel("initWithTraceData:"), mio)
	if helper == 0 {
		return summary
	}
	defer func() {
		if responds(helper, "release") {
			objc.Send[objc.ID](helper, objc.Sel("release"))
		}
	}()
	for _, item := range []struct {
		selector string
		count    *uint64
		samples  *[]TrackSample
	}{
		{"generateTopDrawTracks", &summary.TopDrawCount, &summary.DrawSamples},
		{"generateTopBinaryTracks", &summary.TopBinaryCount, nil},
		{"generateTopKickTracks", &summary.TopKickCount, &summary.KickSamples},
		{"generateTopRIATracks", &summary.TopRIACount, nil},
	} {
		if !responds(helper, item.selector) {
			continue
		}
		tracks := objc.Send[objc.ID](helper, objc.Sel(item.selector))
		if tracks == 0 || !responds(tracks, "count") {
			continue
		}
		*item.count = uint64(objc.Send[uint](tracks, objc.Sel("count")))
		if item.samples == nil || !responds(tracks, "objectAtIndex:") {
			continue
		}
		limit := *item.count
		if limit > 3 {
			limit = 3
		}
		for i := uint64(0); i < limit; i++ {
			track := objc.Send[objc.ID](tracks, objc.Sel("objectAtIndex:"), i)
			if track == 0 {
				continue
			}
			itemSample := TrackSample{}
			if responds(track, "firstIndex") {
				itemSample.FirstIndex = objc.Send[uint64](track, objc.Sel("firstIndex"))
			}
			if responds(track, "duration") {
				itemSample.Duration = objc.Send[uint64](track, objc.Sel("duration"))
			}
			if responds(track, "isEmpty") {
				itemSample.Empty = objc.Send[bool](track, objc.Sel("isEmpty"))
			}
			if responds(track, "lanes") {
				lanes := objc.Send[objc.ID](track, objc.Sel("lanes"))
				if lanes != 0 && responds(lanes, "count") && responds(lanes, "objectAtIndex:") {
					for j, n := uint64(0), uint64(objc.Send[uint](lanes, objc.Sel("count"))); j < n; j++ {
						lane := objc.Send[objc.ID](lanes, objc.Sel("objectAtIndex:"), j)
						if lane == 0 {
							continue
						}
						laneSummary := TrackLaneSummary{}
						if responds(lane, "laneId") {
							laneSummary.LaneID = objc.Send[int32](lane, objc.Sel("laneId"))
						}
						if responds(lane, "indexCount") {
							laneSummary.IndexCount = objc.Send[uint64](lane, objc.Sel("indexCount"))
						}
						if responds(lane, "isEmpty") {
							laneSummary.Empty = objc.Send[bool](lane, objc.Sel("isEmpty"))
						}
						itemSample.Lanes = append(itemSample.Lanes, laneSummary)
					}
				}
			}
			*item.samples = append(*item.samples, itemSample)
		}
	}
	return summary
}

func readCostModel(mio objc.ID) CostModelSummary {
	result := CostModelSummary{}
	if mio == 0 || !responds(mio, "totalCostForScope:scopeIdentifier:dataMaster:") {
		return result
	}
	result.Scope0DataMaster2 = objc.Send[float64](mio,
		objc.Sel("totalCostForScope:scopeIdentifier:dataMaster:"), uint16(0), uint64(0), uint16(2))
	result.Scope4DataMaster2 = objc.Send[float64](mio,
		objc.Sel("totalCostForScope:scopeIdentifier:dataMaster:"), uint16(4), uint64(0), uint16(2))
	result.Ready = result.Scope0DataMaster2 != 0 || result.Scope4DataMaster2 != 0
	return result
}

// readSerializedTimeline reconstructs Xcode's cost timeline from the archive
// produced by the live model. It deliberately reads only scalar selectors:
// costs, draws, kicks, and binary metadata are C pointers in this API.
func readSerializedTimeline(mio, stream objc.ID, pipelines []PipelineRecord) TimelineSummary {
	var result TimelineSummary
	if mio == 0 || stream == 0 || !responds(mio, "archivedData:error:") {
		return result
	}
	var archiveError objc.ID
	data := objc.Send[objc.ID](mio, objc.Sel("archivedData:error:"), false, unsafe.Pointer(&archiveError))
	if data == 0 {
		result.Error = nsErrorMessage(archiveError)
		if result.Error == "" {
			result.Error = "archivedData:error: returned no data"
		}
		return result
	}
	kvClass := objc.GetClass("GTMioKVDataStore")
	timelineClass := objc.GetClass("GTMioTraceTimelineData")
	if kvClass == 0 || timelineClass == 0 {
		return result
	}
	kvAllocated := objc.Send[objc.ID](objc.ID(kvClass), objc.Sel("alloc"))
	kv := objc.Send[objc.ID](kvAllocated, objc.Sel("initWithData:"), data)
	if kv == 0 {
		return result
	}
	defer func() {
		if responds(kv, "release") {
			objc.Send[objc.ID](kv, objc.Sel("release"))
		}
	}()
	var child objc.ID
	if responds(kv, "getChild:") {
		child = objc.Send[objc.ID](kv, objc.Sel("getChild:"), objc.String("costTimeline"))
	}
	if child == 0 {
		return result
	}
	timelineAllocated := objc.Send[objc.ID](objc.ID(timelineClass), objc.Sel("alloc"))
	if timelineAllocated == 0 || !responds(timelineAllocated, "initWithSerializedData:streamData:parentData:") {
		return result
	}
	timeline := objc.Send[objc.ID](timelineAllocated,
		objc.Sel("initWithSerializedData:streamData:parentData:"), child, stream, mio)
	if timeline == 0 {
		return result
	}
	defer func() {
		if responds(timeline, "release") {
			objc.Send[objc.ID](timeline, objc.Sel("release"))
		}
	}()
	result.DrawCount = uint64Property(timeline, "drawCount")
	result.EncoderCount = uint64Property(timeline, "encoderCount")
	result.CostCount = uint64Property(timeline, "costCount")
	result.PipelineStateCount = uint64Property(timeline, "pipelineStateCount")
	result.ComputePositionCount = uint64Property(timeline, "computePositionCount")
	if responds(timeline, "totalCostForScope:scopeIdentifier:dataMaster:") {
		result.Scope0DataMaster2 = objc.Send[float64](timeline,
			objc.Sel("totalCostForScope:scopeIdentifier:dataMaster:"), uint16(0), uint64(0), uint16(2))
		result.Scope4DataMaster2 = objc.Send[float64](timeline,
			objc.Sel("totalCostForScope:scopeIdentifier:dataMaster:"), uint16(4), uint64(0), uint16(2))
	}
	for _, pipeline := range pipelines {
		if responds(timeline, "numDrawsForPipelineState:") {
			result.PipelineDraws = append(result.PipelineDraws, TimelinePipelineSummary{
				ObjectID:  pipeline.ObjectID,
				DrawCount: objc.Send[uint64](timeline, objc.Sel("numDrawsForPipelineState:"), pipeline.ObjectID),
			})
		}
	}
	for i := uint32(0); i < uint32(result.EncoderCount); i++ {
		item := TimelineEncoderSummary{EncoderIndex: i}
		if responds(timeline, "numDrawsForEncoder:") {
			item.DrawCount = objc.Send[uint64](timeline, objc.Sel("numDrawsForEncoder:"), i)
		}
		if responds(timeline, "kickDurationForEncoder:dataMaster:") {
			item.KickDuration = objc.Send[uint64](timeline,
				objc.Sel("kickDurationForEncoder:dataMaster:"), i, uint16(2))
		}
		result.EncoderDurations = append(result.EncoderDurations, item)
	}
	if responds(timeline, "durationForDraw:dataMaster:") {
		limit := result.DrawCount
		if limit > 3 {
			limit = 3
		}
		for i := uint32(0); i < uint32(limit); i++ {
			result.DrawDurationsDataMaster2 = append(result.DrawDurationsDataMaster2,
				objc.Send[uint64](timeline, objc.Sel("durationForDraw:dataMaster:"), i, uint16(2)))
		}
	}
	if responds(timeline, "draws") && responds(timeline, "durationForDraw:dataMaster:") {
		readTimelinePipelineTimes(timeline, result.DrawCount, &result, pipelines)
	}
	result.Ready = result.DrawCount != 0 && result.PipelineStateCount != 0
	return result
}

// readTimelinePipelineTimes attributes draw durations to pipelines. The draws
// property is a packed C array whose layout is not safe to express as a Go
// struct: the measured record size is 44 bytes although the C encoding's
// natural alignment suggests 48. Locate the layout by checking the complete
// pipeline-draw-count multiset that the framework reports. If no candidate
// reproduces that multiset, leave attribution absent rather than guessing.
func readTimelinePipelineTimes(timeline objc.ID, drawCount uint64, result *TimelineSummary, pipelines []PipelineRecord) bool {
	if drawCount == 0 || drawCount > uint64(int(^uint(0)>>1)) || result == nil {
		return false
	}
	want := make(map[uint64]uint64, len(pipelines))
	for _, pipeline := range pipelines {
		count := objc.Send[uint64](timeline, objc.Sel("numDrawsForPipelineState:"), pipeline.ObjectID)
		if count != 0 {
			want[pipeline.ObjectID] = count
		}
	}
	if len(want) == 0 {
		return false
	}
	base := objc.Send[unsafe.Pointer](timeline, objc.Sel("draws"))
	if base == nil {
		return false
	}
	count := int(drawCount)
	// The largest candidate span is deliberately the empirically observed
	// packed size. Candidate strides never read beyond this bounded span.
	const maxSpanStride = 44
	raw := unsafe.Slice((*byte)(base), count*maxSpanStride)
	stride, offset, ok := locateTimelineDrawLayout(raw, count, want)
	if !ok {
		return false
	}
	totals := make(map[uint64]uint64, len(want))
	for i := 0; i < count; i++ {
		id := binary.LittleEndian.Uint64(raw[i*stride+offset:])
		duration := objc.Send[uint64](timeline, objc.Sel("durationForDraw:dataMaster:"), uint32(i), uint16(2))
		totals[id] += duration
	}
	for i := range result.PipelineDraws {
		result.PipelineDraws[i].DrawDurationDataMaster2 = totals[result.PipelineDraws[i].ObjectID]
	}
	return true
}

func locateTimelineDrawLayout(raw []byte, count int, want map[uint64]uint64) (int, int, bool) {
	var foundStride, foundOffset int
	found := false
	for stride := 32; stride <= 44; stride += 4 {
		for offset := 0; offset+8 <= stride; offset += 4 {
			if (count-1)*stride+offset+8 > len(raw) {
				continue
			}
			buckets := make(map[uint64]uint64, len(want))
			for i := 0; i < count; i++ {
				id := binary.LittleEndian.Uint64(raw[i*stride+offset:])
				buckets[id]++
			}
			if len(buckets) != len(want) {
				continue
			}
			match := true
			for id, expected := range want {
				if buckets[id] != expected {
					match = false
					break
				}
			}
			if !match || found {
				if match && found {
					return 0, 0, false
				}
				continue
			}
			foundStride, foundOffset, found = stride, offset, true
		}
	}
	return foundStride, foundOffset, found
}

// readResult fills the device metadata and pipeline records from the profiler
// result. Failures are left as zero values: the result surface varies by Xcode
// version, and the counts above are useful without it.
func readResult(summary *ProcessedStreamData, processor objc.ID, costModelReady bool) {
	result := shaderProfilerResult(processor)
	if result == 0 {
		return
	}
	summary.GPUTime = uint64Property(result, "gpuTime")
	summary.GPUGeneration = uint32Property(result, "gpuGeneration")
	summary.PerformanceState = uint32Property(result, "performanceState")
	summary.UnixTimestamp = int64Property(result, "unixTimestamp")
	summary.MetalPluginName = stringProperty(result, "metalPluginName")
	// -gpuName: takes a BOOL (@20@0:8c16) selecting the codename form; false
	// reports the marketing name, which is the one worth showing.
	if responds(result, "gpuName:") {
		if name := objc.Send[objc.ID](result, objc.Sel("gpuName:"), false); name != 0 {
			summary.GPUName = objc.IDToString(name)
		}
	}
	summary.ShaderBinaryCount = collectionCount(result, "shaderBinaries")
	summary.Binaries = readBinaries(result, costModelReady)
	summary.GPUCommands = readGPUCommands(result)
	summary.GPUCommandCount = uint64(len(summary.GPUCommands))
	summary.Encoders = readEncoders(result)
	summary.Pipelines = readPipelines(result)
	if os.Getenv("GPUTRACE_MIO_MCA") != "" {
		readMCARegisters(result, summary.Pipelines)
	}
}

func readEncoders(result objc.ID) []EncoderRecord {
	objects := elementsOf(objectFor(result, "encoders"))
	records := make([]EncoderRecord, 0, len(objects))
	for _, object := range objects {
		encoder := gtshaderprofiler.GTMioShaderProfilerEncoderFromID(object)
		records = append(records, EncoderRecord{
			Index:                encoder.Index(),
			FunctionIndex:        encoder.FunctionIndex(),
			GPUCommandStartIndex: encoder.GpuCommandStartIndex(),
			NumGPUCommands:       encoder.NumGPUCommands(),
		})
	}
	return records
}

func readGPUCommands(result objc.ID) []GPUCommandRecord {
	objects := elementsOf(objectFor(result, "gpuCommands"))
	records := make([]GPUCommandRecord, 0, len(objects))
	for _, object := range objects {
		command := gtshaderprofiler.GTMioShaderProfilerGPUCommandFromID(object)
		records = append(records, GPUCommandRecord{
			Index:                 command.Index(),
			CommandBufferIndex:    command.CommandBufferIndex(),
			EncoderInfoIndex:      command.EncoderInfoIndex(),
			EncoderObjectID:       command.EncoderObjectId(),
			FunctionIndex:         command.FunctionIndex(),
			PipelineInfoIndex:     command.PipelineInfoIndex(),
			PipelineStateObjectID: command.PipelineStateObjectId(),
		})
	}
	return records
}

// shaderProfilerResult reaches the profiler result through the processed-data
// wrapper the processor hands back.
func shaderProfilerResult(processor objc.ID) objc.ID {
	if !objc.RespondsToSelector(processor, objc.Sel("result")) {
		return 0
	}
	processed := objc.Send[objc.ID](processor, objc.Sel("result"))
	if processed == 0 || !objc.RespondsToSelector(processed, objc.Sel("shaderProfilerResult")) {
		return 0
	}
	return objc.Send[objc.ID](processed, objc.Sel("shaderProfilerResult"))
}

func readPipelines(result objc.ID) []PipelineRecord {
	states := elementsOf(objectFor(result, "pipelineStates"))
	records := make([]PipelineRecord, 0, len(states))
	for _, state := range states {
		if !responds(state, "objectId") {
			continue
		}
		record := PipelineRecord{
			ObjectID:       uint64Property(state, "objectId"),
			PointerID:      uint64Property(state, "pointerId"),
			FunctionIndex:  uint64Property(state, "functionIndex"),
			Index:          uint32Property(state, "index"),
			NumGPUCommands: uint32Property(state, "numGPUCommands"),
			FunctionName:   firstFunctionName(state),
		}
		if functions := elementsOf(objectFor(state, "shaderFunctions")); len(functions) != 0 {
			record.FunctionObjectID = uint64Property(functions[0], "objectId")
			record.LibraryObjectID = uint64Property(functions[0], "libraryObjectId")
		}
		if timing := objectFor(state, "timingInfo"); timing != 0 {
			record.ComputeTime = uint64Property(timing, "computeTime")
		}
		records = append(records, record)
	}
	return records
}

// mcaProgramTypeCompute is the program type a compute pipeline reports. The
// enumeration is not published; this is the value the capture's own pipeline
// records carry.
const mcaProgramTypeCompute = 0

// readMCARegisters records the register allocation MCA derives for each
// pipeline. GTShaderProfilerMCABinaryList is keyed by pipeline state ID, which
// the model already reports, so no binary-key join is involved.
//
// On a model built by GTShaderProfilerStreamDataProcessor the list constructs
// but holds no binaries, for every pipeline and for every program type from 0
// to 5, so these fields stay zero. The pipeline-keyed MCA index appears to
// belong to a trace database rather than to a processed stream, which is the
// same boundary -[GTMioTraceDataStats initWithTraceData:] runs into.
//
// Do not substitute -[GTMioShaderProfilerResult mcaBinaryForBinaryKey:] here.
// It does return populated GTShaderProfilerMCABinary objects, which makes it
// look like the answer, but the keys reachable from a GPU command do not
// identify that command's pipeline: across three runs of one capture the same
// pipeline reported 98, 60, and 66. MCA analysis is asynchronous
// (-generateMCAOutput:callback: against -_generateMCAOutputSync:), and the walk
// races it. Per-pipeline registers are not available by that route.
func readMCARegisters(result objc.ID, pipelines []PipelineRecord) {
	cls := objc.GetClass("GTShaderProfilerMCABinaryList")
	if cls == 0 {
		return
	}
	for i := range pipelines {
		list := newMCABinaryList(cls, result, pipelines[i].ObjectID, mcaProgramTypeCompute)
		if list == 0 {
			continue
		}
		if responds(list, "highRegisterCount") {
			pipelines[i].MCAHighRegister = int32(objc.Send[int16](list, objc.Sel("highRegisterCount")))
		}
		if responds(list, "allocatedGPRCount") {
			pipelines[i].MCAAllocatedGPR = int32(objc.Send[int16](list, objc.Sel("allocatedGPRCount")))
		}
		if responds(list, "mcaBinaries") {
			pipelines[i].MCABinaryCount = collectionCount(list, "mcaBinaries")
		}
		objc.Send[objc.ID](list, objc.Sel("release"))
	}
}

func newMCABinaryList(cls objc.Class, result objc.ID, pipelineStateID uint64, programType uint32) objc.ID {
	allocated := objc.Send[objc.ID](objc.ID(cls), objc.Sel("alloc"))
	if allocated == 0 {
		return 0
	}
	const sel = "initWithShaderProfilerResult:pipelineStateId:programType:"
	if !responds(allocated, sel) {
		objc.Send[objc.ID](allocated, objc.Sel("release"))
		return 0
	}
	return objc.Send[objc.ID](allocated, objc.Sel(sel), result, pipelineStateID, programType)
}

// readBinaries aggregates the compiled shader binaries.
//
// The binaries are read from the result's own collection rather than through
// -enumerateBinariesForPipelineState:enumerator:. Both reach the same objects,
// but the collection needs no block bridging, and a pipeline-keyed enumeration
// would double-count binaries shared between pipelines.
func readBinaries(result objc.ID, costModelReady bool) BinarySummary {
	var summary BinarySummary
	summary.SourceCost.CostModelReady = costModelReady
	summary.HighRegister = -1
	for _, binary := range elementsOf(objectFor(result, "shaderBinaries")) {
		if !objc.RespondsToSelector(binary, objc.Sel("instructionInfoCount")) {
			continue
		}
		summary.Count++
		instructions := objc.Send[uint64](binary, objc.Sel("instructionInfoCount"))
		summary.InstructionCount += instructions
		if objc.RespondsToSelector(binary, objc.Sel("instructionExecuted")) {
			summary.InstructionsExecuted += objc.Send[uint64](binary, objc.Sel("instructionExecuted"))
		}
		if objc.RespondsToSelector(binary, objc.Sel("debugLocationCount")) {
			locations := objc.Send[uint64](binary, objc.Sel("debugLocationCount"))
			summary.DebugLocationCount += locations
			summary.DebugLocations = append(summary.DebugLocations,
				decodeDebugLocations(binary, summary.Count-1, locations)...)
			if locations != 0 && summary.DebugSelectorFile == "" {
				model := gtshaderprofiler.GTMioShaderBinaryDataFromID(binary)
				summary.DebugSelectorFile = foundation.NSStringFromID(
					model.DebugFilePathForDebugLocationAtIndex(0).GetID()).UTF8String()
				summary.DebugSelectorFunction = foundation.NSStringFromID(
					model.DebugFunctionNameForDebugLocationAtIndex(0).GetID()).UTF8String()
				summary.DebugSelectorString = foundation.NSStringFromID(
					model.DebugStringForStringIndex(0).GetID()).UTF8String()
			}
		}
		if high := highestLiveRegister(binary, instructions); high > summary.HighRegister {
			summary.HighRegister = high
		}
		readSourceCostEvidence(&summary.SourceCost, binary, instructions, costModelReady)
	}
	if summary.HighRegister < 0 {
		summary.HighRegister = 0
	}
	summary.SourceCost.finish(summary.InstructionCount)
	return summary
}

func readSourceCostEvidence(evidence *SourceCostEvidence, id objc.ID, count uint64, costModelReady bool) {
	if count == 0 || count > uint64(^uint(0)>>1) {
		return
	}
	binary := gtshaderprofiler.GTMioShaderBinaryDataFromID(id)
	var binaryHasCost bool
	costs := binary.InstructionCosts()
	if costModelReady && costs != nil {
		values := unsafe.Slice(costs, int(count))
		for i := range values {
			if measuredCost(&values[i]) {
				evidence.NonzeroInstructionCostCount++
				binaryHasCost = true
			}
		}
	}
	if binaryHasCost {
		evidence.CostBearingBinaryCount++
	}
	for i := uint64(0); i < count && i <= uint64(^uint32(0)); i++ {
		index := uint32(i)
		if binary.AddressForInstructionAtIndex(index) != 0 {
			evidence.NonzeroInstructionAddressCount++
		}
		if binary.DebugRangeForInstructionAtIndex(index) != nil {
			evidence.DebugRangeInstructionCount++
		}
	}
}

func measuredCost(cost *gtshaderprofiler.GTMioCostInfo) bool {
	if cost == nil {
		return false
	}
	if cost.Field2 != 0 || cost.Field4 != 0 || cost.Field6 != 0 ||
		cost.Field8 != 0 || cost.Field9 != 0 || cost.Field10 != 0 {
		return true
	}
	for i := range cost.Field3 {
		if cost.Field3[i] != 0 || cost.Field5[i] != 0 || cost.Field7[i] != 0 {
			return true
		}
	}
	return false
}

func (e *SourceCostEvidence) finish(instructionCount uint64) {
	e.Ready = false
	switch {
	case instructionCount == 0:
		e.Status = "no_instruction_table"
		e.Reason = "processed model has no shader instruction table"
	case !e.CostModelReady:
		e.Status = "cost_model_not_built"
		e.Reason = "Xcode data-path setup was not requested"
	case e.NonzeroInstructionCostCount == 0:
		e.Status = "no_measured_instruction_cost"
		e.Reason = "processed model has no nonzero instruction cost payload"
	case e.DebugRangeInstructionCount != instructionCount:
		e.Status = "incomplete_debug_ranges"
		e.Reason = "not every instruction maps to a debug range"
	default:
		e.Status = "binary_identity_unproven"
		e.Reason = "processed model does not establish exact binary-to-pipeline and metallib identity"
	}
}

// decodeDebugLocations resolves the location array through its per-binary
// NSString table. Field1 and Field2 are required to be table indices before
// any location is returned. On two capture-backed runs, Field1 selected paths
// and Field2 selected function names; Field3 and Field4 behaved as line and
// column coordinates.
func decodeDebugLocations(id objc.ID, binaryIndex, count uint64) []ShaderSourceLocation {
	if count == 0 || count > uint64(^uint(0)>>1) {
		return nil
	}
	binary := gtshaderprofiler.GTMioShaderBinaryDataFromID(id)
	table := binary.DebugStrings()
	if table.GetID() == 0 || table.Count() == 0 {
		return nil
	}
	stringCount := uint64(table.Count())
	locations := binary.DebugLocations()
	if locations == nil {
		return nil
	}
	values := unsafe.Slice(locations, int(count))
	for _, location := range values {
		if uint64(location.Field1) >= stringCount || uint64(location.Field2) >= stringCount {
			return nil
		}
	}
	result := make([]ShaderSourceLocation, 0, len(values))
	for _, location := range values {
		path := foundation.NSStringFromID(table.ObjectAtIndex(uint(location.Field1)).GetID()).UTF8String()
		function := foundation.NSStringFromID(table.ObjectAtIndex(uint(location.Field2)).GetID()).UTF8String()
		result = append(result, ShaderSourceLocation{
			BinaryIndex:  binaryIndex,
			FilePath:     path,
			FunctionName: function,
			Line:         location.Field3,
			Column:       location.Field4,
		})
	}
	return result
}

// highestLiveRegister reports the largest live-register count over a binary's
// instructions. -liveRegisterForInstructionAtIndex: is i20@0:8I16, so the index
// is a uint32 and the result is signed; negative means unknown.
func highestLiveRegister(binary objc.ID, instructions uint64) int32 {
	if !objc.RespondsToSelector(binary, objc.Sel("liveRegisterForInstructionAtIndex:")) {
		return -1
	}
	highest := int32(-1)
	for i := uint64(0); i < instructions; i++ {
		live := objc.Send[int32](binary, objc.Sel("liveRegisterForInstructionAtIndex:"), uint32(i))
		if live > highest {
			highest = live
		}
	}
	return highest
}

// firstFunctionName reports the Metal function a pipeline was compiled from.
func firstFunctionName(state objc.ID) string {
	for _, fn := range elementsOf(objectFor(state, "shaderFunctions")) {
		if name := stringProperty(fn, "name"); name != "" {
			return name
		}
	}
	return ""
}

// elementsOf returns the members of an Objective-C collection. The profiler
// model uses NSArray and NSDictionary interchangeably for these properties, so
// dictionaries are flattened to their values.
func elementsOf(collection objc.ID) []objc.ID {
	if collection == 0 {
		return nil
	}
	if responds(collection, "allValues") {
		collection = objc.Send[objc.ID](collection, objc.Sel("allValues"))
	} else if responds(collection, "allObjects") {
		collection = objc.Send[objc.ID](collection, objc.Sel("allObjects"))
	}
	if !responds(collection, "objectAtIndex:") || !responds(collection, "count") {
		return nil
	}
	n := uint64Property(collection, "count")
	elements := make([]objc.ID, 0, n)
	for i := uint64(0); i < n; i++ {
		if element := objc.Send[objc.ID](collection, objc.Sel("objectAtIndex:"), i); element != 0 {
			elements = append(elements, element)
		}
	}
	return elements
}

// objectFor reads a property that returns an Objective-C object. Several
// neighbouring properties on this model return raw C pointers instead, and
// messaging one of those crashes, so every read goes through the guard.
func objectFor(id objc.ID, selector string) objc.ID {
	if !responds(id, selector) {
		return 0
	}
	return objc.Send[objc.ID](id, objc.Sel(selector))
}

func uint64Property(id objc.ID, selector string) uint64 {
	if !responds(id, selector) {
		return 0
	}
	return objc.Send[uint64](id, objc.Sel(selector))
}

func uint32Property(id objc.ID, selector string) uint32 {
	if !responds(id, selector) {
		return 0
	}
	return objc.Send[uint32](id, objc.Sel(selector))
}

func int64Property(id objc.ID, selector string) int64 {
	if !responds(id, selector) {
		return 0
	}
	return objc.Send[int64](id, objc.Sel(selector))
}

// countOf returns the element count of an already-resolved Objective-C collection object.
func countOf(collection objc.ID) uint64 {
	if collection == 0 || !objc.RespondsToSelector(collection, objc.Sel("count")) {
		return 0
	}
	return objc.Send[uint64](collection, objc.Sel("count"))
}

// collectionCountFor resolves selector on id to an Objective-C collection object
// and returns its count.
func collectionCountFor(id objc.ID, selector string) uint64 {
	collection := objectFor(id, selector)
	return countOf(collection)
}

func collectionCount(id objc.ID, selector string) uint64 {
	return collectionCountFor(id, selector)
}

func newStreamDataProcessor(stream objc.ID, helper string) (objc.ID, error) {
	cls := objc.GetClass("GTShaderProfilerStreamDataProcessor")
	if cls == 0 {
		return 0, fmt.Errorf("GTShaderProfilerStreamDataProcessor class not found")
	}
	if !responds(objc.ID(cls), "alloc") {
		return 0, fmt.Errorf("GTShaderProfilerStreamDataProcessor class does not respond to alloc")
	}
	allocated := objc.Send[objc.ID](objc.ID(cls), objc.Sel("alloc"))
	if allocated == 0 {
		return 0, fmt.Errorf("allocate GTShaderProfilerStreamDataProcessor")
	}
	if !responds(allocated, "initWithStreamData:llvmHelperPath:") {
		return 0, fmt.Errorf("GTShaderProfilerStreamDataProcessor does not respond to initWithStreamData:llvmHelperPath:")
	}
	processor := objc.Send[objc.ID](allocated, objc.Sel("initWithStreamData:llvmHelperPath:"),
		stream, objc.String(helper))
	if processor == 0 {
		return 0, fmt.Errorf("initWithStreamData:llvmHelperPath: returned nil")
	}
	return processor, nil
}

// llvmHelperPath reports the GTLLVMHelper shipped alongside the loaded
// framework. The two must come from the same Xcode: the helper speaks a
// private protocol whose shape follows the framework version.
func llvmHelperPath() (string, error) {
	framework := resolvedFrameworkPath()
	if path, ok := llvmHelperForFramework(framework); ok {
		return path, nil
	}
	return "", fmt.Errorf("no GTLLVMHelper alongside %s", framework)
}

// llvmHelperForFramework searches the ancestors of a GTShaderProfiler path for
// the helper. Walking up from the framework rather than resolving an Xcode
// path independently is what guarantees the two come from the same install;
// the plugin lives under Contents/PlugIns in a full Xcode and directly under
// the Developer directory elsewhere, so both layouts are tried.
func llvmHelperForFramework(framework string) (string, bool) {
	for dir := filepath.Dir(framework); ; dir = filepath.Dir(dir) {
		for _, base := range []string{filepath.Join(dir, "Contents", "Developer"), dir} {
			if path := filepath.Join(base, llvmHelperRelPath); fileExists(path) {
				return path, true
			}
		}
		if parent := filepath.Dir(dir); parent == dir {
			return "", false
		}
	}
}
