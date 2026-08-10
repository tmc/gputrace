//go:build darwin

package counter

import (
	"math"
	"runtime"
	"sync"
	"testing"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/objectivec"
	"github.com/tmc/apple/private/xcode/gtshaderprofiler"
)

// The samples the stub counters serve. These are package-level so the buffers
// the Objective-C side is handed outlive the call that returns them.
//
// The values are small integers on purpose. Reinterpreting them as float64
// produces subnormals, not NaNs or infinities, so a wrong element width here is
// exactly the case the old finite-exponent heuristic accepted and returned as
// data. TestCounterSeriesWrongWidthWasSilent asserts that.
var (
	gateSamples    = []uint64{1, 2, 3, 4}
	gateTimestamps = []uint64{10, 20, 30, 40}
	gateFloats     = []float64{1, 2, 3, 4}
)

// stubCounterClass registers an Objective-C class that answers the three
// selectors GTMioCounterData's sample accessors read, with valuesEncoding as
// the declared return type of -values. The class name doubles as the
// registration key: a class pair cannot be allocated twice under one name.
func stubCounterClass(t *testing.T, name, valuesEncoding string, values unsafe.Pointer) objc.ID {
	t.Helper()
	stubClassesOnce.Do(func() { stubClasses = map[string]objc.Class{} })

	cls, ok := stubClasses[name]
	if !ok {
		cls = objectivec.Objc_allocateClassPair(objc.GetClass("NSObject"), name, 0)
		if cls == 0 {
			t.Fatalf("objc_allocateClassPair(%s) returned nil", name)
		}
		sampleCount := purego.NewCallback(func(self objc.ID, cmd objc.SEL) uint64 {
			return uint64(len(gateSamples))
		})
		valuesIMP := purego.NewCallback(func(self objc.ID, cmd objc.SEL) unsafe.Pointer {
			return values
		})
		timestamps := purego.NewCallback(func(self objc.ID, cmd objc.SEL) unsafe.Pointer {
			return unsafe.Pointer(&gateTimestamps[0])
		})
		for _, method := range []struct {
			sel   string
			imp   uintptr
			types string
		}{
			{"sampleCount", sampleCount, "Q@:"},
			{"values", valuesIMP, valuesEncoding + "@:"},
			{"timestamps", timestamps, "^Q@:"},
		} {
			if !objc.AddMethod(cls, objc.Sel(method.sel), method.imp, method.types) {
				t.Fatalf("adding -%s to %s failed", method.sel, name)
			}
		}
		objc.RegisterClassPair(cls)
		stubClasses[name] = cls
	}

	id := objc.Send[objc.ID](objc.ID(cls), objc.Sel("alloc"))
	if id == 0 {
		t.Fatalf("%s alloc returned nil", name)
	}
	id = objc.Send[objc.ID](id, objc.Sel("init"))
	if id == 0 {
		t.Fatalf("%s init returned nil", name)
	}
	return id
}

var (
	stubClassesOnce sync.Once
	stubClasses     map[string]objc.Class
)

// TestCounterSeriesRejectsWrongElementWidth is the mutation check on the
// encoding gate: a counter whose -values is declared to return a pointer to
// 64-bit integers must not be read as float64. Nothing else about the object
// differs from a well-formed one, so a pass here is the gate firing and not a
// bounds or nil check catching the case by accident.
func TestCounterSeriesRejectsWrongElementWidth(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	id := stubCounterClass(t, "gputraceWrongWidthCounterData", "^Q", unsafe.Pointer(&gateSamples[0]))
	_, _, err := CounterSeries(gtshaderprofiler.GTMioCounterDataFromID(id))
	if err == nil {
		t.Fatal("CounterSeries accepted a counter whose -values returns ^Q; the element-width gate did not fire")
	}
	t.Logf("rejected as expected: %v", err)
}

// TestCounterSeriesAcceptsDeclaredWidth is the other half of the mutation
// check. Without it, a CounterSeries that failed on every input would pass the
// test above.
func TestCounterSeriesAcceptsDeclaredWidth(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	id := stubCounterClass(t, "gputraceDeclaredWidthCounterData", "^d", unsafe.Pointer(&gateFloats[0]))
	values, timestamps, err := CounterSeries(gtshaderprofiler.GTMioCounterDataFromID(id))
	if err != nil {
		t.Fatalf("CounterSeries rejected a well-formed counter: %v", err)
	}
	if len(values) != len(gateFloats) || len(timestamps) != len(gateTimestamps) {
		t.Fatalf("read %d values and %d timestamps, want %d and %d",
			len(values), len(timestamps), len(gateFloats), len(gateTimestamps))
	}
	for i, want := range gateFloats {
		if values[i] != want {
			t.Fatalf("value[%d] = %v, want %v", i, values[i], want)
		}
	}
	for i, want := range gateTimestamps {
		if timestamps[i] != want {
			t.Fatalf("timestamp[%d] = %d, want %d", i, timestamps[i], want)
		}
	}
}

// TestCounterSeriesWrongWidthWasSilent records why the encoding gate replaced
// the finite-exponent heuristic rather than joining it. The heuristic rejected
// a sample that decoded to a NaN or an infinity; these integers decode to
// subnormals, which are finite, so it would have returned them as data.
func TestCounterSeriesWrongWidthWasSilent(t *testing.T) {
	for i, raw := range gateSamples {
		got := math.Float64frombits(raw)
		if math.IsNaN(got) || math.IsInf(got, 0) {
			t.Fatalf("sample %d decodes to %v, which the old heuristic would have caught; "+
				"this test needs samples it would have missed", i, got)
		}
		t.Logf("sample %d: uint64 %d read as float64 is %v, which is finite", i, raw, got)
	}
}
