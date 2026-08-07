//go:build darwin

package agxps

import (
	"math"
	"os"
	"runtime"
	"testing"
	"unsafe"

	"github.com/tmc/apple/private/xcode/gtshaderprofiler"
)

// TestGeneratedBindingsCounterFile verifies the generated bulk bindings on a
// real Counters_f_*.raw file. The parser setup remains the disassembly-proven
// test helper; the accessors under test are all generated declarations.
func TestGeneratedBindingsCounterFile(t *testing.T) {
	path := os.Getenv("GPUTRACE_PROBE_COUNTERS")
	if path == "" {
		t.Skip("set GPUTRACE_PROBE_COUNTERS to a Counters_f_*.raw path")
	}

	a := loadCounterAPI(t)
	a.initialize()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("empty counter file")
	}

	var pin runtime.Pinner
	defer pin.Unpin()
	p := counterProbeParser(t, a, &pin, 0)
	defer a.parserDestroy(p)

	var parseErr uint32
	pd := a.parserParse(p, unsafe.Pointer(&data[0]), uint64(len(data)), 1, &parseErr)
	if pd == 0 {
		parseErr = 0
		pd = a.parserParse(p, unsafe.Pointer(&data[0]), uint64(len(data)), 0x21, &parseErr)
	}
	if pd == 0 {
		t.Fatalf("parse returned nil, err=%d", parseErr)
	}
	defer a.pdDestroy(pd)

	profileData := gtshaderprofiler.AGXPSProfileData(pd)
	n := a.pdCounterNum(pd)
	if n == 0 {
		t.Fatal("parsed counter file has no counters")
	}
	names := make([]uint64, n)
	values := make([]uint64, n)
	valueCounts := make([]uint64, n)
	metadata := make([]uint64, n)
	groups := make([]byte, n)
	const poison64 = uint64(0xdeadbeefdeadbeef)
	const poison32 = uint32(0xdeadbeef)
	const poison8 = byte(0xde)
	for i := range names {
		names[i] = poison64
		values[i] = poison64
		valueCounts[i] = poison64
		metadata[i] = poison64
		groups[i] = poison8
	}

	must := func(name string, ok bool, err error) {
		if err != nil || !ok {
			t.Fatalf("generated %s accessor: ok=%v err=%v", name, ok, err)
		}
	}
	ok, err := gtshaderprofiler.AgxpsApsProfileDataGetCounterNames(profileData, &names[0], 0, n)
	must("names", ok, err)
	ok, err = gtshaderprofiler.AgxpsApsProfileDataGetCounterValues(profileData, &values[0], 0, n)
	must("values", ok, err)
	ok, err = gtshaderprofiler.AgxpsApsProfileDataGetCounterValuesNum(profileData, &valueCounts[0], 0, n)
	must("value counts", ok, err)
	ok, err = gtshaderprofiler.AgxpsApsProfileDataGetCounterGroupMetadata(profileData, &metadata[0], 0, n)
	must("metadata", ok, err)
	ok, err = gtshaderprofiler.AgxpsApsProfileDataGetCounterGroupID(profileData, groups, 0, n)
	must("groups", ok, err)
	for _, series := range []struct {
		name string
		data []uint64
	}{
		{"names", names},
		{"values", values},
		{"value counts", valueCounts},
		{"metadata", metadata},
	} {
		for i, value := range series.data {
			if value == poison64 {
				t.Fatalf("generated %s accessor left output[%d] unchanged", series.name, i)
			}
		}
	}
	for i, group := range groups {
		if group == poison8 {
			t.Fatalf("generated groups accessor left output[%d] unchanged", i)
		}
	}

	nsys := a.pdSysTSNum(pd)
	if nsys > 0 {
		system := make([]uint64, nsys)
		for i := range system {
			system[i] = poison64
		}
		ok, err := gtshaderprofiler.AgxpsApsProfileDataGetSystemTimestamps(profileData, &system[0], 0, nsys)
		if err != nil || !ok {
			t.Fatalf("generated system timestamps accessor: ok=%v err=%v", ok, err)
		}
		for i, value := range system {
			if value == poison64 {
				t.Fatalf("generated system timestamps accessor left output[%d] unchanged", i)
			}
		}
		ns, err := gtshaderprofiler.AgxpsApsSystemTimestampToNanoseconds(system[0])
		if err != nil || math.IsNaN(ns) || math.IsInf(ns, 0) {
			t.Fatalf("generated timestamp conversion: value=%v err=%v", ns, err)
		}
	}

	nk := a.pdKicksNum(pd)
	if nk > 0 {
		starts := make([]uint64, nk)
		ends := make([]uint64, nk)
		ids := make([]uint32, nk)
		for i := range starts {
			starts[i] = poison64
			ends[i] = poison64
			ids[i] = poison32
		}
		ok, err := gtshaderprofiler.AgxpsApsProfileDataGetKickStart(profileData, &starts[0], 0, nk)
		must("kick starts", ok, err)
		ok, err = gtshaderprofiler.AgxpsApsProfileDataGetKickEnd(profileData, &ends[0], 0, nk)
		must("kick ends", ok, err)
		ok, err = gtshaderprofiler.AgxpsApsProfileDataGetKickID(profileData, &ids[0], 0, nk)
		must("kick IDs", ok, err)
		for _, series := range []struct {
			name string
			data []uint64
		}{
			{"kick starts", starts},
			{"kick ends", ends},
		} {
			for i, value := range series.data {
				if value == poison64 {
					t.Fatalf("generated %s accessor left output[%d] unchanged", series.name, i)
				}
			}
		}
		for i, id := range ids {
			if id == poison32 {
				t.Fatalf("generated kick IDs accessor left output[%d] unchanged", i)
			}
		}
	}

	t.Logf("generated bindings read %d counter series, %d system timestamps, and %d kicks", n, nsys, nk)
}
