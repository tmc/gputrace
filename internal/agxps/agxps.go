//go:build darwin

// Package agxps is a thin adapter over the AGX profiler C surface in
// GTShaderProfiler.framework.
//
// # Exported surface
//
// Only two things here are reachable end to end with input a caller can
// legitimately produce: [DecodeCounterProfileShape], which parses one
// Counters_f_*.raw, and the [GPU] handle family. Everything else on the parse
// side is unexported. That is deliberate — see "Handles are not pointers".
//
// # Every wrong reading here is silent
//
// GTShaderProfiler is undocumented and the C declarations we started from were
// derived from symbol names. Roughly half were wrong, and a wrong declaration
// does not fail: it corrupts the argument registers and returns something
// plausible. docs/research/agxps-signatures.yaml records every signature we
// have established, how it was established, and the six distinct ways a wrong
// one has already produced believable garbage. Consult it before adding a call,
// and prefer the locally declared purego bindings in countershape_darwin.go
// over the generated github.com/tmc/apple forwarders wherever the two disagree:
// the generated arities are name-derived and several are known wrong.
//
// # Handles are not pointers
//
// Several accessors hand back small integers that read like handles but are
// composite table indices, and several callees dereference their first argument
// deep into a large object. An exported function taking a caller-supplied
// uintptr and passing it to one of those is a crash, not a type error, so this
// package does not have one. Four such functions (TimingStatsForAnalyzer,
// TraceInstructionStats, ESLCliqueInstructionTrace, NewCliqueTimeStats) were
// deleted rather than documented; the reasons are in the yaml under their
// symbol names.
package agxps

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	"unsafe"

	"github.com/tmc/apple/private/xcode/gtshaderprofiler"
	"github.com/tmc/gputrace/internal/xcodepath"
)

// defaultGTShaderProfilerPath is used when no installed bundle resolves. It is
// the historical hardcoded path, kept only so a failure to find any Xcode
// produces a dlopen error naming a real location.
const defaultGTShaderProfilerPath = "/Applications/Xcode.app/Contents/PlugIns/GPUDebugger.ideplugin/Contents/Frameworks/GTShaderProfiler.framework/Versions/A/GTShaderProfiler"

// gtShaderProfilerPath resolves through xcodepath so that GPUTRACE_XCODE_APP
// selects the same bundle for the framework as for the counter catalog. It was
// a constant pinned to /Applications/Xcode.app, which meant a pin moved the
// names without moving the binary that produced the numbers.
//
// It is resolved once, at initialization, so every dlopen in this package
// agrees. Changing the environment afterwards does not move a framework that
// is already mapped.
var gtShaderProfilerPath = resolveGTShaderProfilerPath()

func resolveGTShaderProfilerPath() string {
	if p := xcodepath.FrameworkPath(); p != "" {
		return p
	}
	return defaultGTShaderProfilerPath
}

var (
	loadMu sync.Mutex
	loaded bool
)

// GPU is an opaque handle for GPU configuration.
type GPU uintptr

// profileData is an opaque handle for parsed profile data.
type profileData uintptr

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

// parser wraps agxps_aps_parser for parsing timeline data.
//
// There is no exported constructor. Both former ones were built on
// agxps_aps_descriptor_create, which takes no arguments and returns a 104-byte
// struct by value through x8, the AArch64 indirect result register. purego
// cannot set x8: a pointer argument lands in x0 and leaves x8 zero, so the
// callee's first store faults. It is also avoidable — descriptor_create only
// installs defaults, one of which is a zero pulse/era/count period, for which
// agxps_aps_parser_create returns NULL. So "create defaults, then use them"
// never worked. [DecodeCounterProfileShape] builds the descriptor itself, and
// that is the pattern any future constructor should follow.
type parser struct {
	handle uintptr
}

// Initialize calls agxps_initialize and loads the counter tables.
//
// agxps_initialize returns a bool, not an errno: 1 is success. The value is
// seeded to 1 at function entry and cleared only if the table load fails, so
// treating non-zero as an error inverts it.
//
// It takes four arguments, not zero, so this deliberately does not use the
// generated zero-argument binding. See counterShapeAPI.initialize.
func Initialize() error {
	if err := Init(); err != nil {
		return err
	}
	a, err := loadCounterShapeAPI()
	if err != nil {
		return err
	}
	if a.initialize(0, 0, 0, 0) == 0 {
		return fmt.Errorf("agxps_initialize failed to load the counter tables")
	}
	return nil
}

// Close destroys the parser.
func (p *parser) Close() {
	if p.handle == 0 {
		return
	}
	_ = gtshaderprofiler.AgxpsApsParserDestroy(gtshaderprofiler.AGXPSParserHandle(p.handle))
	p.handle = 0
}

// Parse parses timeline data from a byte slice.
func (p *parser) Parse(data []byte) (profileData, error) {
	if len(data) == 0 {
		return 0, fmt.Errorf("empty data")
	}
	if p == nil || p.handle == 0 {
		return 0, fmt.Errorf("invalid parser")
	}
	a, err := loadCounterShapeAPI()
	if err != nil {
		return 0, err
	}
	var parseError uint32
	pd := a.parserParse(p.handle, unsafe.Pointer(&data[0]), uint64(len(data)), 1, &parseError)
	runtime.KeepAlive(data)
	if pd == 0 || parseError != 0 {
		return 0, fmt.Errorf("parse failed with code %d", parseError)
	}
	return profileData(pd), nil
}

// IsValid returns true if the parser is in a valid state.
func (p *parser) IsValid() bool {
	if p.handle == 0 {
		return false
	}
	valid, err := gtshaderprofiler.AgxpsApsParserIsValid(gtshaderprofiler.AGXPSParserHandle(p.handle))
	return err == nil && valid
}

// IsValid returns true if the profile data handle is valid.
func (pd profileData) IsValid() bool {
	if pd == 0 {
		return false
	}
	valid, err := gtshaderprofiler.AgxpsApsProfileDataIsValid(gtshaderprofiler.AGXPSProfileData(pd))
	return err == nil && valid
}

// Destroy releases the profile data.
func (pd profileData) Destroy() {
	if pd == 0 {
		return
	}
	_ = gtshaderprofiler.AgxpsApsProfileDataDestroy(gtshaderprofiler.AGXPSProfileData(pd))
}

// kickReference identifies one kick in the profiler's raw timestamp tables.
//
// Start and End are packed (usc_timestamp_index<<32)|system_timestamp_index
// values, not ticks or nanoseconds. The generated accessors establish their
// layout only; converting them to a duration requires both timestamp-table
// joins and is intentionally left to a higher-level decoder.
type kickReference struct {
	Index uint64
	ID    uint32
	Start uint64
	End   uint64
}

// kickReferences returns the raw references for every kick in pd.
func kickReferences(pd profileData) ([]kickReference, error) {
	handle := gtshaderprofiler.AGXPSProfileData(pd)
	if handle == 0 {
		return nil, fmt.Errorf("zero profile data")
	}
	n, err := gtshaderprofiler.AgxpsApsProfileDataGetKicksNum(handle)
	if err != nil {
		return nil, fmt.Errorf("get kick count: %w", err)
	}
	if n == 0 {
		return nil, nil
	}
	starts := make([]uint64, n)
	ends := make([]uint64, n)
	ids := make([]uint32, n)
	if ok, err := gtshaderprofiler.AgxpsApsProfileDataGetKickStart(handle, &starts[0], 0, n); err != nil || !ok {
		return nil, fmt.Errorf("get kick starts: ok=%v: %w", ok, err)
	}
	if ok, err := gtshaderprofiler.AgxpsApsProfileDataGetKickEnd(handle, &ends[0], 0, n); err != nil || !ok {
		return nil, fmt.Errorf("get kick ends: ok=%v: %w", ok, err)
	}
	// The accessor writes 4-byte elements, against 8 bytes for kick_start and
	// kick_end in the same family: docs/research/agxps-signatures.yaml records
	// that width as verified at runtime over a 2510-kick parse. The binding now
	// declares *uint32 to match, so the cast this call used to carry is gone --
	// the widths agree at the type level instead of being reconciled here.
	if ok, err := gtshaderprofiler.AgxpsApsProfileDataGetKickID(handle, &ids[0], 0, n); err != nil || !ok {
		return nil, fmt.Errorf("get kick IDs: ok=%v: %w", ok, err)
	}
	out := make([]kickReference, n)
	for i := range out {
		out[i] = kickReference{Index: uint64(i), ID: ids[i], Start: starts[i], End: ends[i]}
	}
	return out, nil
}

// eslCliqueReference identifies one execution-state-log clique. Start and End
// have the same packed timestamp-index representation as [kickReference].
type eslCliqueReference struct {
	Index      uint64
	CliqueID   byte
	KickID     uint32
	ESLID      uint64
	Start      uint64
	End        uint64
	MissingEnd bool
}

// eslCliqueReferences returns the raw references for every ESL clique in pd. It
// does not turn their timestamp references into durations.
func eslCliqueReferences(pd profileData) ([]eslCliqueReference, error) {
	handle := gtshaderprofiler.AGXPSProfileData(pd)
	if handle == 0 {
		return nil, fmt.Errorf("zero profile data")
	}
	n, err := gtshaderprofiler.AgxpsApsProfileDataGetEslCliquesNum(handle)
	if err != nil {
		return nil, fmt.Errorf("get ESL clique count: %w", err)
	}
	if n == 0 {
		return nil, nil
	}
	starts := make([]uint64, n)
	ends := make([]uint64, n)
	cliqueIDs := make([]byte, n)
	kickIDs := make([]uint32, n)
	eslIDs := make([]uint64, n)
	missingEnds := make([]byte, n)
	getters := []struct {
		name string
		call func() (bool, error)
	}{
		{"starts", func() (bool, error) {
			return gtshaderprofiler.AgxpsApsProfileDataGetEslCliqueStart(handle, &starts[0], 0, n)
		}},
		{"ends", func() (bool, error) {
			return gtshaderprofiler.AgxpsApsProfileDataGetEslCliqueEnd(handle, &ends[0], 0, n)
		}},
		{"clique IDs", func() (bool, error) {
			return gtshaderprofiler.AgxpsApsProfileDataGetEslCliqueCliqueID(handle, cliqueIDs, 0, n)
		}},
		{"kick IDs", func() (bool, error) {
			return gtshaderprofiler.AgxpsApsProfileDataGetEslCliqueKickID(handle, &kickIDs[0], 0, n)
		}},
		{"ESL IDs", func() (bool, error) {
			return gtshaderprofiler.AgxpsApsProfileDataGetEslCliqueEslID(handle, &eslIDs[0], 0, n)
		}},
		{"missing ends", func() (bool, error) {
			return gtshaderprofiler.AgxpsApsProfileDataGetEslCliqueMissingEnd(handle, missingEnds, 0, n)
		}},
	}
	for _, getter := range getters {
		ok, err := getter.call()
		if err != nil || !ok {
			return nil, fmt.Errorf("get ESL clique %s: ok=%v: %w", getter.name, ok, err)
		}
	}
	out := make([]eslCliqueReference, n)
	for i := range out {
		out[i] = eslCliqueReference{
			Index: uint64(i), CliqueID: cliqueIDs[i], KickID: kickIDs[i], ESLID: eslIDs[i],
			Start: starts[i], End: ends[i], MissingEnd: missingEnds[i] != 0,
		}
	}
	return out, nil
}

// NewGPU creates a GPU handle for the given generation, variant, and revision.
//
// A handle is not a supported GPU. agxps_gpu_create looks the triple up in a
// dense gen*42+variant*6+rev table of GPU descriptions and returns non-NULL for
// far more triples than the profiler supports; use [GPU.IsSupported] for that
// question. Passing exact=true skips the revision fallback, so the handle keeps
// a revision that may not exist.
func NewGPU(gen, variant, rev uint32, exact bool) (GPU, error) {
	a, err := loadCounterShapeAPI()
	if err != nil {
		return 0, err
	}
	var exactArg uint32
	if exact {
		exactArg = 1
	}
	gpu := GPU(a.gpuCreate(gen, variant, rev, exactArg))
	if gpu == 0 {
		return 0, fmt.Errorf("no GPU description for gen=%d variant=%d rev=%d", gen, variant, rev)
	}
	return gpu, nil
}

// IsValid reports whether the handle is non-nil, and nothing more.
// agxps_gpu_is_valid is `cmp x0, #0; cset w0, ne`. It does not validate the
// GPU: an unsupported triple that still has a description yields a handle this
// reports as valid.
func (g GPU) IsValid() bool {
	if g == 0 {
		return false
	}
	valid, err := gtshaderprofiler.AgxpsGPUIsValid(gtshaderprofiler.AGXPSGPU(g))
	return err == nil && valid
}

// Destroy releases the GPU handle.
func (g GPU) Destroy() {
	if g == 0 {
		return
	}
	_ = gtshaderprofiler.AgxpsGPUDestroy(gtshaderprofiler.AGXPSGPU(g))
}

// Gen returns the GPU generation.
func (g GPU) Gen() uint32 {
	if g == 0 {
		return 0
	}
	gen, err := gtshaderprofiler.AgxpsGPUGetGen(gtshaderprofiler.AGXPSGPU(g))
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
	variant, err := gtshaderprofiler.AgxpsGPUGetVariant(gtshaderprofiler.AGXPSGPU(g))
	if err != nil {
		return 0
	}
	return uint32(variant)
}

// Rev returns the revision the handle was created with.
//
// It is an echo of the NewGPU argument, not a property of the device:
// agxps_gpu_get_rev is `ldr w0, [x0, #0x8]`, the requested revision written at
// creation. The effective revision, which the revision fallback may have
// corrected, lives at +0xc behind agxps_gpu_get_rev_with_aps_fallback and is
// not exposed here.
func (g GPU) Rev() uint32 {
	if g == 0 {
		return 0
	}
	rev, err := gtshaderprofiler.AgxpsGPUGetRev(gtshaderprofiler.AGXPSGPU(g))
	if err != nil {
		return 0
	}
	return uint32(rev)
}

// Name returns the string agxps_gpu_format_name produces, which is the constant
// "AppleGPU" for every non-nil handle and "(invalid)" for nil.
//
// It does not identify the device. agxps_gpu_format_name selects between those
// two literals on a NULL test and passes neither the generation, the variant,
// nor the revision to the formatter, so a caller cannot tell two GPUs apart by
// it. Logging it beside a triple reads as confirmation and is not.
func (g GPU) Name() string {
	buf := make([]byte, 256)
	if _, err := gtshaderprofiler.AgxpsGPUFormatName(gtshaderprofiler.AGXPSGPU(g), &buf[0], uint64(len(buf))); err != nil {
		return ""
	}
	for i, b := range buf {
		if b == 0 {
			return string(buf[:i])
		}
	}
	return string(buf)
}

// IsSupported reports whether the GPU triple is one the profiler supports.
//
// It asks agxps_aps_gpu_is_supported with the triple, which is what that
// function takes; the generated binding declares a single handle parameter,
// which puts a pointer where the generation belongs, leaves the variant and
// revision registers unset, and so answers false for every input. Do not use
// the generated binding for this symbol.
//
// The triple comes from the handle's own fields, so this reports on the
// revision the handle was requested with, matching [GPU.Rev].
func (g GPU) IsSupported() bool {
	if g == 0 {
		return false
	}
	a, err := loadCounterShapeAPI()
	if err != nil {
		return false
	}
	return a.gpuIsSupported(g.Gen(), g.Variant(), g.Rev())
}
