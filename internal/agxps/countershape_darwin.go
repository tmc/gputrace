//go:build darwin

package agxps

import (
	"errors"
	"fmt"
	"runtime"
	"unsafe"

	"github.com/ebitengine/purego"
)

// CounterDecodeConfig identifies the GPU used to decode an APS counter shard.
// Values must come from the capture or device; this package does not guess them.
type CounterDecodeConfig struct {
	Generation     uint32
	Variant        uint32
	Revision       uint32
	UarchBehaviour int32
	PulsePeriod    uint32
	EraPeriod      uint32
	CountPeriod    uint32
	ParseFlags     uint32
}

// CounterProfileShape contains only arrays that GTShaderProfiler copies into
// caller-owned memory. Counter sample values are excluded because the current
// framework exposes only their foreign data pointers.
type CounterProfileShape struct {
	SystemTimestamps []uint64
	CounterIDs       []uint64
	CounterGroupIDs  []uint8
	CounterValueNums []uint64
	KickCount        uint64
	ParsedTokens     uint64
	ParsedBits       uint64
}

// counterDescriptor is the 0x68-byte descriptor copied by
// agxps_aps_descriptor_create. [V] Field offsets and size were established by
// arm64 disassembly and a runtime sentinel probe; see
// docs/research/agxps-signatures.yaml.
type counterDescriptor struct {
	GPU                    uintptr
	PulsePeriod            uint32
	EraPeriod              uint32
	CountPeriod            uint32
	_                      uint32
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

type counterShapeAPI struct {
	// agxps_initialize takes FOUR arguments, not zero. [V] 0x4ac908 saves all
	// of x0..x3 (`stp x3,x0,[sp]`, `mov x22,x2`, `str x1,[sp,#0x10]`) and later
	// walks TWO (pointer, count) pairs: x2/x3 at 0x4acbf8 and x0/x1 at
	// 0x4acc18. Each loop is guarded by cbz on both the pointer and the count,
	// so all-zero is the safe no-op input and the only one we can supply
	// deliberately. A zero-argument declaration leaves four caller-saved
	// registers at whatever the trampoline last held; if x0 or x2 happens to be
	// a non-null non-pointer with a non-zero count beside it, the loop
	// dereferences it. Passing explicit zeros removes that dependence on luck.
	initialize func(list0 uintptr, count0 uint64, list1 uintptr, count1 uint64) int32
	// agxps_gpu_create takes FOUR arguments. [V] 0x49b528 keeps x3 in x23 and
	// tests it with `tbnz w23, #0x0`, so it is a bool; when set, the revision
	// fallback at 0x49b5b8 is skipped. The generated three-argument binding
	// leaves x3 undeclared. See docs/research/agxps-signatures.yaml.
	gpuCreate func(generation, variant, revision, exact uint32) uintptr
	// agxps_aps_gpu_is_supported takes the TRIPLE, not a handle. [V] 0x4eb35c
	// packs w0|w1<<32 and w2 into a 12-byte key and looks it up in a set.
	gpuIsSupported func(generation, variant, revision uint32) bool
	// agxps_gpu_get_rev_with_aps_fallback reads +0xc, the effective revision
	// the fallback may have corrected, where agxps_gpu_get_rev reads +0x8, the
	// revision that was requested. [V] 0x49b6c8 and 0x49b6d0 are two-
	// instruction loads at those offsets.
	gpuEffectiveRev func(uintptr) uint32
	gpuIsValid      func(uintptr) bool
	gpuDestroy      func(uintptr)
	parserCreate    func(unsafe.Pointer) uintptr
	parserIsValid   func(uintptr) bool
	parserParse     func(uintptr, unsafe.Pointer, uint64, uint32, *uint32) uintptr
	parserDestroy   func(uintptr)
	profileIsValid  func(uintptr) bool
	profileDestroy  func(uintptr)
	counterNum      func(uintptr) uint64
	counterIDs      func(uintptr, *uint64, uint64, uint64) bool
	counterGroups   func(uintptr, *uint8, uint64, uint64) bool
	counterValueNum func(uintptr, *uint64, uint64, uint64) bool
	systemTSNum     func(uintptr) uint64
	systemTS        func(uintptr, *uint64, uint64, uint64) bool
	kicksNum        func(uintptr) uint64
	parsedTokens    func(uintptr) uint64
	parsedBits      func(uintptr) uint64
	chunksFailed    func(uintptr) uint64
	parseErrorsNum  func(uintptr) uint64
}

func loadCounterShapeAPI() (*counterShapeAPI, error) {
	handle, err := purego.Dlopen(gtShaderProfilerPath, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return nil, fmt.Errorf("agxps: load GTShaderProfiler: %w", err)
	}
	a := new(counterShapeAPI)
	bindings := []struct {
		name   string
		target any
	}{
		{"agxps_initialize", &a.initialize},
		{"agxps_gpu_create", &a.gpuCreate},
		{"agxps_aps_gpu_is_supported", &a.gpuIsSupported},
		{"agxps_gpu_get_rev_with_aps_fallback", &a.gpuEffectiveRev},
		{"agxps_gpu_is_valid", &a.gpuIsValid},
		{"agxps_gpu_destroy", &a.gpuDestroy},
		{"agxps_aps_parser_create", &a.parserCreate},
		{"agxps_aps_parser_is_valid", &a.parserIsValid},
		{"agxps_aps_parser_parse", &a.parserParse},
		{"agxps_aps_parser_destroy", &a.parserDestroy},
		{"agxps_aps_profile_data_is_valid", &a.profileIsValid},
		{"agxps_aps_profile_data_destroy", &a.profileDestroy},
		{"agxps_aps_profile_data_get_counter_num", &a.counterNum},
		{"agxps_aps_profile_data_get_counter_names", &a.counterIDs},
		{"agxps_aps_profile_data_get_counter_group_id", &a.counterGroups},
		{"agxps_aps_profile_data_get_counter_values_num", &a.counterValueNum},
		{"agxps_aps_profile_data_get_system_timestamps_num", &a.systemTSNum},
		{"agxps_aps_profile_data_get_system_timestamps", &a.systemTS},
		{"agxps_aps_profile_data_get_kicks_num", &a.kicksNum},
		{"agxps_aps_profile_data_get_parsed_tokens_num", &a.parsedTokens},
		{"agxps_aps_profile_data_get_parsed_bits_num", &a.parsedBits},
		{"agxps_aps_profile_data_get_num_chunks_failed", &a.chunksFailed},
		{"agxps_aps_profile_data_get_parse_errors_num", &a.parseErrorsNum},
	}
	for _, binding := range bindings {
		symbol, err := purego.Dlsym(handle, binding.name)
		if err != nil {
			return nil, fmt.Errorf("agxps: resolve %s: %w", binding.name, err)
		}
		purego.RegisterFunc(binding.target, symbol)
	}
	return a, nil
}

// DecodeCounterProfileShape decodes the safely copyable shape of one
// Counters_f_*.raw file.
//
// The parser ABI used here is [V] for Xcode 26.4: profile data is returned in
// x0, with flags in x3 and parseErrorOut in x4. The generated
// github.com/tmc/apple v0.5.5 forwarder instead declares a four-argument
// out-parameter ABI and must not be used for this symbol.
func DecodeCounterProfileShape(data []byte, config CounterDecodeConfig) (*CounterProfileShape, error) {
	if len(data) == 0 {
		return nil, errors.New("agxps: empty counter shard")
	}
	if config.Generation == 0 {
		return nil, errors.New("agxps: GPU generation is required")
	}
	if config.PulsePeriod == 0 || config.EraPeriod == 0 || config.CountPeriod == 0 {
		return nil, errors.New("agxps: pulse, era, and count periods are required")
	}
	if config.ParseFlags == 0 {
		return nil, errors.New("agxps: parse flags are required")
	}
	a, err := loadCounterShapeAPI()
	if err != nil {
		return nil, err
	}
	if a.initialize(0, 0, 0, 0) == 0 {
		return nil, errors.New("agxps: initialize counter tables")
	}
	gpu := a.gpuCreate(config.Generation, config.Variant, config.Revision, 0)
	if gpu == 0 || !a.gpuIsValid(gpu) {
		return nil, fmt.Errorf("agxps: unsupported GPU %d/%d/%d", config.Generation, config.Variant, config.Revision)
	}
	defer a.gpuDestroy(gpu)

	descriptor := &counterDescriptor{
		GPU: gpu, PulsePeriod: config.PulsePeriod, EraPeriod: config.EraPeriod,
		CountPeriod: config.CountPeriod, ChunkSize: 0x1000,
		CounterUarchBehaviour: config.UarchBehaviour,
		MaxTimestamp:          ^uint64(0), MaxParseErrorCount: 50,
	}
	var pinner runtime.Pinner
	pinner.Pin(descriptor)
	defer pinner.Unpin()
	parser := a.parserCreate(unsafe.Pointer(descriptor))
	if parser == 0 || !a.parserIsValid(parser) {
		return nil, errors.New("agxps: create counter parser")
	}
	defer a.parserDestroy(parser)

	var parseError uint32
	profile := a.parserParse(parser, unsafe.Pointer(&data[0]), uint64(len(data)), config.ParseFlags, &parseError)
	runtime.KeepAlive(data)
	if profile == 0 {
		return nil, fmt.Errorf("agxps: parse counter shard: code %d", parseError)
	}
	defer a.profileDestroy(profile)
	if parseError != 0 {
		return nil, fmt.Errorf("agxps: parse counter shard: code %d", parseError)
	}
	if !a.profileIsValid(profile) {
		return nil, errors.New("agxps: invalid counter profile data")
	}
	if failed := a.chunksFailed(profile); failed != 0 {
		return nil, fmt.Errorf("agxps: counter shard has %d failed chunks", failed)
	}
	if parseErrors := a.parseErrorsNum(profile); parseErrors != 0 {
		return nil, fmt.Errorf("agxps: counter shard has %d parse errors", parseErrors)
	}

	counterCount, err := counterShapeSliceLength(a.counterNum(profile), "counter series")
	if err != nil {
		return nil, err
	}
	timestampCount, err := counterShapeSliceLength(a.systemTSNum(profile), "system timestamps")
	if err != nil {
		return nil, err
	}
	shape := &CounterProfileShape{
		CounterIDs:       make([]uint64, counterCount),
		CounterGroupIDs:  make([]uint8, counterCount),
		CounterValueNums: make([]uint64, counterCount),
		SystemTimestamps: make([]uint64, timestampCount),
		KickCount:        a.kicksNum(profile),
		ParsedTokens:     a.parsedTokens(profile),
		ParsedBits:       a.parsedBits(profile),
	}
	if counterCount > 0 {
		n := uint64(counterCount)
		if !a.counterIDs(profile, &shape.CounterIDs[0], 0, n) {
			return nil, errors.New("agxps: copy counter IDs")
		}
		if !a.counterGroups(profile, &shape.CounterGroupIDs[0], 0, n) {
			return nil, errors.New("agxps: copy counter group IDs")
		}
		if !a.counterValueNum(profile, &shape.CounterValueNums[0], 0, n) {
			return nil, errors.New("agxps: copy counter sample counts")
		}
	}
	if timestampCount > 0 && !a.systemTS(profile, &shape.SystemTimestamps[0], 0, uint64(timestampCount)) {
		return nil, errors.New("agxps: copy system timestamps")
	}
	return shape, nil
}

func counterShapeSliceLength(n uint64, what string) (int, error) {
	if n > uint64(^uint(0)>>1) {
		return 0, fmt.Errorf("agxps: %s count %d overflows int", what, n)
	}
	return int(n), nil
}
