//go:build darwin

package agxps

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"unsafe"

	"github.com/ebitengine/purego"
)

// The framework exports a counter-name obfuscation map:
//
//	agxps_load_counter_obfuscation_map(const char *csvPath) -> bool
//	agxps_counter_deobfuscate_name(const char *name) -> const char *
//	agxps_counter_obfuscated_name(const char *name) -> const char *
//
// If that map resolves the 64-hex-digit names the timeline counter dictionary
// serves, it is the crosswalk from those names to readable ones, established by
// the framework itself rather than by a string transformation someone invented.
// This probe asks whether it does. Manual, because it dlopens Xcode's
// GTShaderProfiler and calls into it.
//
// The round trip is the falsifier. Both accessors return their argument
// unchanged when the map is missing, so a "resolved" name that equals its input
// is not evidence of anything. A pass requires a name that changed and that
// maps back.

// runtimeTimelineCounterNames are the 30 keys GTMioTimelineCounters served for
// the 413-draw recapture archive. Thirteen are 64-hex-digit strings. These are
// recorded rather than read from a trace so the probe stays runnable without
// the 9.7G archive.
var runtimeTimelineCounterNames = []string{
	"100299043F027ADADB62685130C7FBE549E29F08B58C365844FF8EC25BAEEAB0",
	"1FFBA951E06F1A7810DC823264210F0C13273E454D699383F3D6265630FEDD53",
	"260130B343BA0695AB911D986B3870FA0CCD0EC58E6F55895A856F37201CE9F8",
	"295D65BB175E4E4EEF9003E008E093043C9B8CE43190BE0A2D8F1771F9837033",
	"3476066F46CC277DE7616AAAD8FCDF2C28DA42293B231F74A62159EB6EDAC78C",
	"3856FBD8576C0AA988700D7EF5787AAAE94A3BBFBB393B0426FA9D379DA69C91",
	"3AFE7FC24E518305DB9BB516AE4AA6725E13A423016B31BAFEBFD6FA09AFAFCD",
	"4BF63E209F7D92B4E8341476C80013664D3299327C72E7A7F0D16E1CBD4904FC",
	"547021D0E82D62B7841769A23FC7FE04F7A63B8A0528A3F6E4C67E8B9420360E",
	"5D4640C1160E691CF9E1DA7FE475482756D03567716B9856424469B31049A457",
	"76F5A23AACC27615C980BE3E58B52994192195866836855BCA7C3F885796297B",
	"79E88035C9BC883D403F17831B8C9264E643C6B76E9B3C1451B49B0F672C32BF",
	"AA1E812506867A5F2C54D3BA3268DB5C4BB2C6B0E4F500340DD23C4E1E637D9D",
	"AGenInstructions",
	"ALU F16 Instructions",
	"ALU F32 Instructions",
	"ALU Total Instructions",
	"ALUF16Issued",
	"ALUF16Percent",
	"ALUF32Issued",
	"ALUF32Percent",
	"ALUICPercent",
	"ALUInstructions",
	"ALUInt32AndCondIssued",
	"ALUIntAndComplexIssued",
	"ALUSCIBPercent",
	"CFInstructions",
	"CFIssued",
	"GT Active Core Count",
	"Instructions Executed",
}

type obfuscationMap struct {
	load   func(*byte) bool
	unload func()
	deobf  func(*byte) *byte
	obf    func(*byte) *byte
}

func loadObfuscationMap(t *testing.T, h uintptr) obfuscationMap {
	t.Helper()
	var m obfuscationMap
	purego.RegisterLibFunc(&m.load, h, "agxps_load_counter_obfuscation_map")
	purego.RegisterLibFunc(&m.deobf, h, "agxps_counter_deobfuscate_name")
	purego.RegisterLibFunc(&m.obf, h, "agxps_counter_obfuscated_name")
	purego.RegisterLibFunc(&m.unload, h, "_Z36agxps_unload_counter_obfuscation_mapv")
	return m
}

func (m obfuscationMap) call(fn func(*byte) *byte, name string) string {
	arg := append([]byte(name), 0)
	p := fn(&arg[0])
	if p == nil {
		return ""
	}
	var out []byte
	for i := 0; ; i++ {
		c := *(*byte)(unsafe.Add(unsafe.Pointer(p), i))
		if c == 0 {
			break
		}
		out = append(out, c)
	}
	return string(out)
}

// TestProbeCounterObfuscationMap reports whether the framework can resolve the
// obfuscated timeline counter names, and with which map file.
//
// GPUTRACE_COUNTER_OBFUSCATION_CSV names a map to load; without it the probe
// asks the framework for its default by passing a null path. The four CSV names
// the binary mentions -- AGXCounterMapping.csv, AGXRawCounterMapping.csv,
// RawCountersMapping.csv, remapping.csv -- are not present in either installed
// Xcode, so the default is expected to fail; the probe records which.
func TestProbeCounterObfuscationMap(t *testing.T) {
	h := probeHandle(t)
	m := loadObfuscationMap(t, h)

	csv := os.Getenv("GPUTRACE_COUNTER_OBFUSCATION_CSV")
	var loaded bool
	if csv == "" {
		loaded = m.load(nil)
		t.Logf("agxps_load_counter_obfuscation_map(NULL) = %t", loaded)
	} else {
		arg := append([]byte(csv), 0)
		loaded = m.load(&arg[0])
		t.Logf("agxps_load_counter_obfuscation_map(%q) = %t", csv, loaded)
	}
	defer m.unload()

	type resolution struct {
		Name      string `json:"name"`
		Deobf     string `json:"deobfuscated"`
		RoundTrip string `json:"round_trip"`
		Changed   bool   `json:"changed"`
		Recovered bool   `json:"recovered"`
	}
	var results []resolution
	var changed int
	for _, name := range runtimeTimelineCounterNames {
		d := m.call(m.deobf, name)
		r := resolution{Name: name, Deobf: d, Changed: d != "" && d != name}
		if r.Changed {
			r.RoundTrip = m.call(m.obf, d)
			r.Recovered = r.RoundTrip == name
			changed++
		}
		results = append(results, r)
	}

	encoded, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("resolutions:\n%s", string(encoded))

	hex := 0
	for _, name := range runtimeTimelineCounterNames {
		if len(name) == 64 && strings.Trim(strings.ToUpper(name), "0123456789ABCDEF") == "" {
			hex++
		}
	}
	t.Logf("summary: loaded=%t names=%d hex_names=%d resolved=%d",
		loaded, len(runtimeTimelineCounterNames), hex, changed)

	if changed == 0 {
		// Not a failure. A map that resolves nothing is the measurement: it
		// says the crosswalk is not available from this installation, which is
		// what a caller needs to know before inventing one.
		t.Log("no name resolved; the obfuscation map is unavailable or empty in this install")
	}
}
