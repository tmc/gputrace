//go:build darwin

package agxps

// Derived-counter evaluation through GTShaderProfiler's own evaluator.
//
// Every declaration in this file was read off the arm64 disassembly of
// /Applications/Xcode.app/.../GTShaderProfiler before being called, and the
// register evidence is recorded on each field. Addresses are file offsets in
// the arm64 slice of the Xcode 26.3 build (40350288 bytes, mtime 2026-02-20).

import (
	"errors"
	"fmt"
	"math"
	"runtime"
	"unsafe"

	"github.com/ebitengine/purego"
)

// timeseriesDatatype is the agxps_timeseries_datatype_t enum.
//
// [V] agxps_timeseries_create @0x4b21e0 and agxps_timeseries_get_size_in_bytes
// @0x4b2460 both compute the byte size as length<<(datatype==2 ? 0 : 3), and
// the alignment table at 0x91dd48 holds {8, 8, 1}. So 0 and 1 are 8-byte
// elements and 2 is a byte. Which of 0 and 1 is float and which is integer is
// fixed by -[XRGPUAPSDataProcessor deriveRDECounters:...]: at 0xb084 it wraps a
// std::vector<double> with datatype 0, and at 0xb0b8 it wraps a
// std::vector<unsigned long long> with datatype 1.
type timeseriesDatatype uint32

const (
	timeseriesF64 timeseriesDatatype = 0
	timeseriesU64 timeseriesDatatype = 1
	timeseriesU8  timeseriesDatatype = 2
)

// derivedError is the value agxps_counter_compute_derived_counters writes
// through its last argument.
//
// [V] 1 at 0x559154, 2 at 0x55934c, 3 at 0x559328; each store is a `str w9,[x8]`
// through the pointer loaded from the third stack argument.
type derivedError uint32

const (
	derivedErrArguments  derivedError = 1 // a NULL/zero argument, or an invalid GPU
	derivedErrNotDerived derivedError = 2 // the ident is not a derived counter
	derivedErrInputs     derivedError = 3 // a raw counter the formula needs was not supplied
)

func (e derivedError) String() string {
	switch e {
	case 0:
		return "none"
	case derivedErrArguments:
		return "invalid arguments"
	case derivedErrNotDerived:
		return "ident is not a derived counter"
	case derivedErrInputs:
		return "a required raw counter input is missing"
	}
	return fmt.Sprintf("code %d", uint32(e))
}

type derivedAPI struct {
	initialize func(list0 uintptr, count0 uint64, list1 uintptr, count1 uint64) int32

	counterIsDerived func(uint64) bool
	counterGetName   func(uint64) string

	// [V] agxps_counter_get_ident @0x4add6c takes TWO arguments, a GPU and a
	// counter NAME, and returns a uint64 ident: 0x4add88 validates x0 as a GPU,
	// 0x4add90-0x4adda4 build the (gen<<16)|variant registry key, and x1 is
	// carried through in x19 as the lookup argument. It is not the
	// single-argument ident-to-string accessor the name suggests.
	counterGetIdent func(gpu uintptr, name string) uint64

	// [V] agxps_counter_get_raw_counters_used_by_derived_counters @0x558944.
	// 0x558964-0x558998 fold four non-NULL tests over x1..x4 into the result of
	// agxps_gpu_is_valid(x0) with ccmp/csel, so all five arguments are live.
	// 0x558b68 stores the count through the pointer that entered in x4 and
	// 0x558b7c-0x558b84 malloc(count*8) and store that array through x3.
	rawCountersUsed func(gpu uintptr, derived *uint64, numDerived uint64, outRaw *unsafe.Pointer, outNum *uint64) bool

	// [V] agxps_counter_get_always_on_raw_counters_list @0x4ae3cc takes
	// (gpu, out, capacity) and returns how many idents it wrote: 0x4ae3f4
	// validates x0, 0x4ae464 skips the store when x1 is NULL, 0x4ae468 compares
	// the running index against x2 and 0x4ae4d8 returns (size_t)-1 when the
	// buffer is too small. The idents come from a 16-byte-record table spanning
	// 0xebf748..0xec0748 keyed on (gen<<16)|variant.
	alwaysOnRawCounters func(gpu uintptr, out *uint64, capacity uint64) uint64

	// [V] agxps_timeseries_create @0x4b21e0 allocates a 40-byte header
	// (`operator new(0x28)`), stores the datatype at +0, the length at +8, and
	// posix_memaligns a length<<3 (or <<0) buffer into +0x10 with the
	// framework's own deleter at +0x18. Using it instead of
	// agxps_timeseries_create_with_bytes_no_copy keeps the buffer's lifetime
	// inside the framework: no Go pointer is handed to C and no Go callback has
	// to survive as a C deleter.
	timeseriesCreate   func(datatype uint32, length uint64) uintptr
	timeseriesData     func(uintptr) unsafe.Pointer
	timeseriesLength   func(uintptr) uint64
	timeseriesDatatype func(uintptr) uint32
	timeseriesIsValid  func(uintptr) bool
	timeseriesDestroy  func(uintptr)

	// [V] agxps_counter_compute_derived_counters @0x558f6c takes ELEVEN
	// arguments: x0..x7 plus three stack words. The prologue sets
	// x29 = sp+0x50 after `stp x28,x27,[sp,#-0x60]!`, so the incoming stack
	// arguments are at [x29,#0x10], [x29,#0x18] and [x29,#0x20]; all three are
	// loaded (0x558ff0, 0x558fb4, 0x558fac).
	//
	// Argument roles are pinned by the call site in
	// -[XRGPUAPSDataProcessor deriveRDECounters:counterIndexes:rawCounterIds:
	// derivedCounterIds:deltaSecondsIndex:] at 0xb0e4-0xb128, where the ObjC
	// selector names the vectors being passed:
	//
	//	x0  = self->_gpu                        agxps_gpu_is_valid(x0) at 0x558fc8
	//	x1  = vector of agxps_timeseries_t      built by create_with_bytes_no_copy
	//	x2  = rawCounterIds.begin               parallel to x1
	//	x3  = rawCounterIds.size()
	//	x4  = 16-byte scalar array              read `ldr q0,[x23],#0x10` at 0x5590b4
	//	x5  = const char *const *               each passed to strlen at 0x5590ac
	//	x6  = number of (name, scalar) pairs    `cbz x21` at 0x55909c skips both
	//	x7  = derivedCounterIds.begin           agxps_counter_is_derived at 0x5591b8
	//	s0  = derivedCounterIds.size()
	//	s1  = agxps_timeseries_t **out          malloc'd at 0x559b60, stored 0x559b68
	//	s2  = uint32_t *error                   optional; `cbz x9` at 0x559148
	//
	// The return value is NOT "all requested counters were computed": 0x559b7c
	// loads the size of the failed-ident map and 0x559b88 is
	// `cset w19, lo`, i.e. it returns failed < numDerived. Ask for two counters,
	// have one fail, and it still returns true.
	computeDerived func(gpu uintptr, inputs *uintptr, inputIdents *uint64, numInputs uint64,
		constValues unsafe.Pointer, constNames unsafe.Pointer, numConstants uint64,
		derivedIdents *uint64, numDerived uint64, out *unsafe.Pointer, errOut *uint32) bool

	malloc func(uint64) unsafe.Pointer
	free   func(unsafe.Pointer)
}

func loadDerivedAPI() (*derivedAPI, error) {
	handle, err := purego.Dlopen(gtShaderProfilerPath, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return nil, fmt.Errorf("agxps: load GTShaderProfiler: %w", err)
	}
	libc, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return nil, fmt.Errorf("agxps: load libSystem: %w", err)
	}
	a := new(derivedAPI)
	bindings := []struct {
		lib    uintptr
		name   string
		target any
	}{
		{handle, "agxps_initialize", &a.initialize},
		{handle, "agxps_counter_is_derived", &a.counterIsDerived},
		{handle, "agxps_counter_get_name", &a.counterGetName},
		{handle, "agxps_counter_get_ident", &a.counterGetIdent},
		{handle, "agxps_counter_get_raw_counters_used_by_derived_counters", &a.rawCountersUsed},
		{handle, "agxps_counter_get_always_on_raw_counters_list", &a.alwaysOnRawCounters},
		{handle, "agxps_timeseries_create", &a.timeseriesCreate},
		{handle, "agxps_timeseries_get_data", &a.timeseriesData},
		{handle, "agxps_timeseries_get_length", &a.timeseriesLength},
		{handle, "agxps_timeseries_get_datatype", &a.timeseriesDatatype},
		{handle, "agxps_timeseries_is_valid", &a.timeseriesIsValid},
		{handle, "agxps_timeseries_destroy", &a.timeseriesDestroy},
		{handle, "agxps_counter_compute_derived_counters", &a.computeDerived},
		{libc, "malloc", &a.malloc},
		{libc, "free", &a.free},
	}
	for _, binding := range bindings {
		symbol, err := purego.Dlsym(binding.lib, binding.name)
		if err != nil {
			return nil, fmt.Errorf("agxps: resolve %s: %w", binding.name, err)
		}
		purego.RegisterFunc(binding.target, symbol)
	}
	return a, nil
}

// RawCounterSeries is one raw counter series offered as evaluator input.
//
// Ident is a raw counter ident in the AGXPS registry's space, the same space
// agxps_aps_profile_data_get_counter_names copies out of a decoded
// Counters_f_*.raw. Values are the samples of that series.
type RawCounterSeries struct {
	Ident  uint64
	Values []float64
}

// DerivedCounterSeries is one evaluated derived counter.
type DerivedCounterSeries struct {
	Ident  uint64
	Name   string
	Values []float64
}

// DerivedConstant is a named scalar a derived-counter formula reads.
//
// The evaluator looks these up by name and dereferences the result without
// checking it: the constants-provider `get` at 0x55a84c does
// `bl <find>` then `ldp x8,x1,[x0,#0x20]`, so a name the caller did not supply
// is a NULL dereference inside the framework, not an error return. Formulas on
// this GPU ask for NUM_CORES; see [ErrDerivedConstantMissing].
type DerivedConstant struct {
	Name  string
	Value float64
}

// derivedScalar is the 16-byte value the constants array holds.
//
// [V] 0x5590b4 copies one 16-byte element per name with `ldr q0,[x23],#0x10`,
// and the only caller that fills one in
// (-[XRGPUAPSDataProcessor ...] at 0x8da0) writes a zero u32 at +0 and a double
// at +8: `str wzr,[x8]` then `str d0,[x8,#0x8]`. The u32 is a
// timeseriesDatatype, so 0 selects the f64 member.
type derivedScalar struct {
	datatype uint32
	_        uint32
	value    float64
}

// ErrDerivedConstantMissing reports that a formula asked for a named constant
// the caller did not supply. It cannot be detected after the fact — the
// framework segfaults — so [ComputeDerivedCounters] refuses up front unless the
// known-required constants are present.
var ErrDerivedConstantMissing = errors.New("agxps: derived formula needs a named constant that was not supplied")

// requiredDerivedConstants are the constant names the evaluator was observed to
// look up on this GPU. Established by breaking on the constants-provider get at
// 0x55a864 under lldb and reading its argument; a formula that asks for a name
// outside this set will crash rather than fail, so the list is a floor, not a
// contract.
var requiredDerivedConstants = []string{"NUM_CORES"}

// ErrDerivedInputsMissing reports that the evaluator declined every requested
// derived counter because a raw counter its formula reads was not among the
// supplied inputs. Use [RawCountersUsedBy] to find out which.
var ErrDerivedInputsMissing = errors.New("agxps: a raw counter the derived formula needs was not supplied")

// RawCountersUsedBy returns the raw counter idents the given derived counters
// read, for the given GPU.
//
// The list is not the whole input set the evaluator requires. [V] Before
// returning, agxps_counter_get_raw_counters_used_by_derived_counters erases two
// idents from the set it built (0x558b3c and 0x558b60, `__erase_unique` against
// the idents of two counter objects held in globals at 0xee7d70 and 0xee7d78).
// Those two are supplied from elsewhere, so a caller that offers exactly this
// list can still be told a required input is missing.
func RawCountersUsedBy(gpu GPU, derived []uint64) ([]uint64, error) {
	if gpu == 0 {
		return nil, errors.New("agxps: nil GPU handle")
	}
	if len(derived) == 0 {
		return nil, errors.New("agxps: no derived counters requested")
	}
	a, err := loadDerivedAPI()
	if err != nil {
		return nil, err
	}
	if a.initialize(0, 0, 0, 0) == 0 {
		return nil, errors.New("agxps: initialize counter tables")
	}
	var out unsafe.Pointer
	var num uint64
	ok := a.rawCountersUsed(uintptr(gpu), &derived[0], uint64(len(derived)), &out, &num)
	runtime.KeepAlive(derived)
	if !ok {
		return nil, errors.New("agxps: agxps_counter_get_raw_counters_used_by_derived_counters failed")
	}
	if out == nil || num == 0 {
		return nil, nil
	}
	defer a.free(out)
	if num > 1<<20 {
		return nil, fmt.Errorf("agxps: implausible raw dependency count %d", num)
	}
	idents := make([]uint64, num)
	copy(idents, unsafe.Slice((*uint64)(out), num))
	return idents, nil
}

// AlwaysOnRawCounters returns the GPU's always-on raw counter list.
//
// It is NOT the source of the two idents [RawCountersUsedBy] erases. On
// G16 16/6/1 it returns an empty list, because the 16-byte table it walks
// (0xebf748..0xec0748) has no group whose GPU-key set contains this triple,
// while the two erased idents are named counters resolved by name. Use
// [CounterIdent] with "GPUCycles" and "DeltaSeconds" for those.
func AlwaysOnRawCounters(gpu GPU) ([]uint64, error) {
	if gpu == 0 {
		return nil, errors.New("agxps: nil GPU handle")
	}
	a, err := loadDerivedAPI()
	if err != nil {
		return nil, err
	}
	if a.initialize(0, 0, 0, 0) == 0 {
		return nil, errors.New("agxps: initialize counter tables")
	}
	n := a.alwaysOnRawCounters(uintptr(gpu), nil, 0)
	if n == 0 || n == ^uint64(0) {
		// A zero-capacity query returns either the count or the too-small
		// sentinel depending on the build; retry into a generous buffer.
		n = 64
	}
	if n > 1<<10 {
		return nil, fmt.Errorf("agxps: implausible always-on counter count %d", n)
	}
	out := make([]uint64, n)
	written := a.alwaysOnRawCounters(uintptr(gpu), &out[0], n)
	runtime.KeepAlive(out)
	if written == ^uint64(0) {
		return nil, errors.New("agxps: always-on raw counter buffer too small")
	}
	if written > n {
		return nil, fmt.Errorf("agxps: always-on list wrote %d of %d slots", written, n)
	}
	return out[:written], nil
}

// CounterIdent returns the registry ident of a counter named for this GPU, or
// an error if the registry does not know the name.
//
// This is the join between a decoded capture and the registry.
// agxps_aps_profile_data_get_counter_names hands back `const char *` values,
// not idents: the 64-character uppercase-hex raw counter names. Passing one of
// those here yields the ident the evaluator wants. The two implicit evaluator
// inputs, "GPUCycles" and "DeltaSeconds", are reachable the same way.
//
// A name the registry does not know yields (uint64)-1 rather than a plausible
// number, so a wrong name is visible instead of silent.
func CounterIdent(gpu GPU, name string) (uint64, error) {
	if gpu == 0 {
		return 0, errors.New("agxps: nil GPU handle")
	}
	if name == "" {
		return 0, errors.New("agxps: empty counter name")
	}
	a, err := loadDerivedAPI()
	if err != nil {
		return 0, err
	}
	if a.initialize(0, 0, 0, 0) == 0 {
		return 0, errors.New("agxps: initialize counter tables")
	}
	ident := a.counterGetIdent(uintptr(gpu), name)
	if ident == ^uint64(0) {
		return 0, fmt.Errorf("agxps: no counter named %q for this GPU", name)
	}
	return ident, nil
}

// ComputeDerivedCounters evaluates derived counters with GTShaderProfiler's own
// evaluator, from raw counter series the caller supplies.
//
// Every input series is copied into a framework-allocated
// agxps_timeseries_t of datatype f64 rather than wrapped in place, so no Go
// memory is visible to C after the call returns and no Go function has to
// survive as a C deleter.
//
// A derived counter the evaluator declines is omitted from the result rather
// than reported as zero. The C function returns true when *any* requested
// counter succeeded, so a short result is the only signal that some did not.
func ComputeDerivedCounters(gpu GPU, inputs []RawCounterSeries, constants []DerivedConstant, derived []uint64) ([]DerivedCounterSeries, error) {
	if gpu == 0 {
		return nil, errors.New("agxps: nil GPU handle")
	}
	if len(inputs) == 0 {
		return nil, errors.New("agxps: no raw counter inputs")
	}
	if len(derived) == 0 {
		return nil, errors.New("agxps: no derived counters requested")
	}
	for _, want := range requiredDerivedConstants {
		found := false
		for _, c := range constants {
			if c.Name == want {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("%w: %s", ErrDerivedConstantMissing, want)
		}
	}
	a, err := loadDerivedAPI()
	if err != nil {
		return nil, err
	}
	if a.initialize(0, 0, 0, 0) == 0 {
		return nil, errors.New("agxps: initialize counter tables")
	}

	handles := make([]uintptr, len(inputs))
	idents := make([]uint64, len(inputs))
	defer func() {
		for _, h := range handles {
			if h != 0 {
				a.timeseriesDestroy(h)
			}
		}
	}()
	for i, in := range inputs {
		if len(in.Values) == 0 {
			return nil, fmt.Errorf("agxps: raw counter %d has no samples", in.Ident)
		}
		h := a.timeseriesCreate(uint32(timeseriesF64), uint64(len(in.Values)))
		if h == 0 || !a.timeseriesIsValid(h) {
			return nil, fmt.Errorf("agxps: create timeseries for raw counter %d", in.Ident)
		}
		handles[i] = h
		data := a.timeseriesData(h)
		if data == nil {
			return nil, fmt.Errorf("agxps: timeseries for raw counter %d has no buffer", in.Ident)
		}
		copy(unsafe.Slice((*float64)(data), len(in.Values)), in.Values)
		idents[i] = in.Ident
	}

	// The constant names have to reach C as a C array of C strings. Building
	// that out of Go allocations would hand the framework Go pointers it keeps
	// for the duration of the call, so build it in malloc'd memory instead.
	scalars := make([]derivedScalar, len(constants))
	var namesArray unsafe.Pointer
	if len(constants) > 0 {
		namesArray = a.malloc(uint64(len(constants)) * 8)
		if namesArray == nil {
			return nil, errors.New("agxps: allocate constant name array")
		}
		names := unsafe.Slice((*unsafe.Pointer)(namesArray), len(constants))
		defer func() {
			for _, p := range names {
				if p != nil {
					a.free(p)
				}
			}
			a.free(namesArray)
		}()
		for i, c := range constants {
			if c.Name == "" {
				return nil, errors.New("agxps: constant with an empty name")
			}
			p := a.malloc(uint64(len(c.Name)) + 1)
			if p == nil {
				return nil, errors.New("agxps: allocate constant name")
			}
			copy(unsafe.Slice((*byte)(p), len(c.Name)+1), append([]byte(c.Name), 0))
			names[i] = p
			scalars[i] = derivedScalar{datatype: uint32(timeseriesF64), value: c.Value}
		}
	}

	var pinner runtime.Pinner
	if len(scalars) > 0 {
		pinner.Pin(&scalars[0])
	}
	defer pinner.Unpin()
	var scalarPtr unsafe.Pointer
	if len(scalars) > 0 {
		scalarPtr = unsafe.Pointer(&scalars[0])
	}

	// One derived counter per call. The out array is malloc(numDerived*8) but
	// only the successful results are memmoved into it (0x559b60-0x559b78), and
	// the return value is `failed < numDerived` (0x559b88), so a batch call with
	// a partial failure hands back an array whose tail is uninitialised heap
	// while still reporting success. There is no exported way to learn how many
	// slots were written. Asking for one at a time makes the return value mean
	// exactly "this one succeeded" and the array exactly one initialised slot.
	results := make([]DerivedCounterSeries, 0, len(derived))
	var lastCode derivedError
	for _, ident := range derived {
		one := [1]uint64{ident}
		var out unsafe.Pointer
		var code uint32
		ok := a.computeDerived(uintptr(gpu), &handles[0], &idents[0], uint64(len(handles)),
			scalarPtr, namesArray, uint64(len(constants)),
			&one[0], 1, &out, &code)
		runtime.KeepAlive(handles)
		runtime.KeepAlive(idents)
		runtime.KeepAlive(scalars)
		if !ok {
			if out != nil {
				a.free(out)
			}
			lastCode = derivedError(code)
			continue
		}
		if out == nil {
			return nil, errors.New("agxps: evaluator reported success with no result array")
		}
		h := *(*uintptr)(out)
		a.free(out)
		if h == 0 || !a.timeseriesIsValid(h) {
			return nil, fmt.Errorf("agxps: evaluator reported success for %d with no timeseries", ident)
		}
		values, err := readTimeseries(a, h)
		a.timeseriesDestroy(h)
		if err != nil {
			return nil, err
		}
		results = append(results, DerivedCounterSeries{
			Ident:  ident,
			Name:   a.counterGetName(ident),
			Values: values,
		})
	}
	if len(results) == 0 {
		if lastCode == derivedErrInputs {
			return nil, ErrDerivedInputsMissing
		}
		return nil, fmt.Errorf("agxps: compute derived counters: %s", lastCode)
	}
	return results, nil
}

// readTimeseries copies a framework timeseries out as float64.
func readTimeseries(a *derivedAPI, h uintptr) ([]float64, error) {
	n := a.timeseriesLength(h)
	if n > 1<<28 {
		return nil, fmt.Errorf("agxps: implausible timeseries length %d", n)
	}
	data := a.timeseriesData(h)
	if data == nil || n == 0 {
		return nil, nil
	}
	values := make([]float64, n)
	switch timeseriesDatatype(a.timeseriesDatatype(h)) {
	case timeseriesF64:
		copy(values, unsafe.Slice((*float64)(data), n))
	case timeseriesU64:
		for i, v := range unsafe.Slice((*uint64)(data), n) {
			values[i] = float64(v)
		}
	case timeseriesU8:
		for i, v := range unsafe.Slice((*uint8)(data), n) {
			values[i] = float64(v)
		}
	default:
		return nil, fmt.Errorf("agxps: unknown timeseries datatype %d", a.timeseriesDatatype(h))
	}
	for _, v := range values {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return values, nil
		}
	}
	return values, nil
}
