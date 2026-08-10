package counter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Manual: extract per-encoder MMU raw counter + GPUCycles from a real Xcode
// bundle, so the native evaluator can compute MMU Utilization and be compared
// to the Xcode oracle. Run:
//
//	MMU_BUNDLE=/path/to/x.gputrace go test ./internal/counter -run TestMMUExtract -v
//
// Writes JSON to $MMU_OUT (default /tmp path).
func TestMMUExtract(t *testing.T) {
	bundle := os.Getenv("MMU_BUNDLE")
	if bundle == "" {
		t.Skip("set MMU_BUNDLE")
	}
	dir := profilerRawDir(t, bundle)
	stats, err := ParseStreamData(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats.APSCounterData) == 0 {
		t.Fatal("no APSCounterData blobs")
	}

	// Locate the counter-archive blob (last one that decodes), replicating
	// ParseCounterArchive's selection, but keep raw values.
	var root any
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
			root, objects, dict = r, o, d
			break
		}
	}
	if dict == nil {
		t.Fatal("no counter archive")
	}
	_ = root

	known := encoderInfoIDs(dict["Encoder Infos"], objects)
	place := encoderInfoPlacement(dict["Encoder Infos"], objects)
	passCols := passColumnNames(dict["Subdivided Dictionary"], objects)
	t.Logf("known encoders=%d passes=%d", len(known), len(passCols))

	// MMU Utilization raw grc.
	const mmuHash = "8ff5f6e1c2e52558354049aef96f7abf429f223a3fc4e626292d894456e02fc2"
	// For each pass width, find the record index (full column index) of MMU.
	mmuIdxByWidth := map[int]int{}
	for _, cols := range passCols {
		for j, name := range cols {
			if strings.Contains(name, mmuHash) {
				mmuIdxByWidth[len(cols)] = j
			}
		}
	}
	t.Logf("MMU column index by pass width: %v", mmuIdxByWidth)
	if len(mmuIdxByWidth) == 0 {
		t.Fatal("MMU grc not found in any pass column list")
	}

	type enc struct {
		EncoderID uint64 `json:"encoder_id"`
		Ordinal   int    `json:"ordinal"`
		Group     int    `json:"group"`
		MMURaw    uint64 `json:"mmu_raw"`    // sum over end records
		GPUCycles uint64 `json:"gpu_cycles"` // sum over end records
		EndCount  int    `json:"end_count"`
	}
	byEnc := map[uint64]*enc{}

	for _, blob := range gprwcntrBlobs(dict["Derived Counter Sample Data"], objects) {
		samples, stride, err := ParseGPRWCNTR(blob)
		if err != nil {
			continue
		}
		ncols := (stride - len(GPRWCNTRMagic)) / 8
		fullIdx, ok := mmuIdxByWidth[ncols]
		if !ok {
			continue // this pass does not collect MMU
		}
		colIdx := fullIdx - grcNumFixedColumns // index into Counters[]
		for _, s := range samples {
			if s.MachineWide() {
				continue
			}
			if _, ok := known[s.EncoderID]; !ok {
				continue
			}
			if s.SampleType != GRCSampleTypeEncoderEnd {
				continue
			}
			if colIdx < 0 || colIdx >= len(s.Counters) {
				continue
			}
			e := byEnc[s.EncoderID]
			if e == nil {
				e = &enc{EncoderID: s.EncoderID}
				if pl, ok := place[s.EncoderID]; ok {
					e.Ordinal, e.Group = pl.ordinal, pl.group
				}
				byEnc[s.EncoderID] = e
			}
			e.MMURaw += s.Counters[colIdx]
			e.GPUCycles += s.GPUCycles
			e.EndCount++
		}
	}

	var out []enc
	for _, e := range byEnc {
		out = append(out, *e)
	}
	t.Logf("extracted %d encoders with MMU data", len(out))
	path := os.Getenv("MMU_OUT")
	if path == "" {
		path = filepath.Join(outDir(t), "mmu-per-encoder.json")
	}
	b, _ := json.MarshalIndent(out, "", " ")
	if err := os.WriteFile(path, b, 0644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s", path)
}

func profilerRawDir(t *testing.T, bundle string) string {
	entries, err := os.ReadDir(bundle)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".gpuprofiler_raw") {
			return bundle + "/" + e.Name()
		}
	}
	t.Fatalf("no .gpuprofiler_raw dir in %s", bundle)
	return ""
}
