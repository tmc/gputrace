package counter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTWUSeries(t *testing.T) {
	bundle := os.Getenv("MMU_BUNDLE")
	if bundle == "" {
		t.Skip()
	}
	dir := profilerRawDir(t, bundle)
	stats, _ := ParseStreamData(dir, nil)
	var objects []any
	var dict map[string]any
	for i := len(stats.APSCounterData) - 1; i >= 0; i-- {
		r, o, ok := archiveRoot(stats.APSCounterData[i])
		if !ok {
			continue
		}
		d := keyedDict(r, o)
		if d == nil {
			continue
		}
		if _, ok := d["Derived Counter Sample Data"]; ok {
			objects, dict = o, d
			break
		}
	}
	known := encoderInfoIDs(dict["Encoder Infos"], objects)
	place := encoderInfoPlacement(dict["Encoder Infos"], objects)
	const grc = "8ff5f6e1c2e52558354049aef96f7abf429f223a3fc4e626292d894456e02fc2"
	idxByW := map[int]int{}
	for _, cols := range passColumnNames(dict["Subdivided Dictionary"], objects) {
		for j, n := range cols {
			if strings.Contains(n, grc) {
				idxByW[len(cols)] = j
			}
		}
	}
	type E struct {
		Ordinal int      `json:"ordinal"`
		Raw     []uint64 `json:"raw"`
		Cyc     []uint64 `json:"cyc"`
		Types   []uint64 `json:"types"`
	}
	byEnc := map[uint64]*E{}
	for _, blob := range gprwcntrBlobs(dict["Derived Counter Sample Data"], objects) {
		samples, stride, err := ParseGPRWCNTR(blob)
		if err != nil {
			continue
		}
		nc := (stride - len(GPRWCNTRMagic)) / 8
		fi, ok := idxByW[nc]
		if !ok {
			continue
		}
		ci := fi - grcNumFixedColumns
		for _, s := range samples {
			if s.MachineWide() {
				continue
			}
			if _, ok := known[s.EncoderID]; !ok {
				continue
			}
			if ci < 0 || ci >= len(s.Counters) {
				continue
			}
			e := byEnc[s.EncoderID]
			if e == nil {
				e = &E{}
				if pl, ok := place[s.EncoderID]; ok {
					e.Ordinal = pl.ordinal
				}
				byEnc[s.EncoderID] = e
			}
			// keep ALL sample types with their raw+cyc to inspect begin/end structure
			e.Raw = append(e.Raw, s.Counters[ci])
			e.Cyc = append(e.Cyc, s.GPUCycles)
			e.Types = append(e.Types, s.SampleType)
		}
	}
	var out []E
	for _, e := range byEnc {
		out = append(out, *e)
	}
	b, err := json.MarshalIndent(out, "", "")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(outDir(t), "twu-series.json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %d encoders to %s", len(out), path)
}
