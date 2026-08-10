package counter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// outDir is where the probes in this package write the JSON they dump.
// MMU_OUT_DIR names it; otherwise it is the test's own temp directory, so a
// probe run on a machine other than the one that wrote it still succeeds
// instead of failing on an absent path.
func outDir(t *testing.T) string {
	if dir := os.Getenv("MMU_OUT_DIR"); dir != "" {
		return dir
	}
	return t.TempDir()
}

func TestDumpPassHashes(t *testing.T) {
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
	attributed := map[int]bool{12: true, 14: true, 16: true, 17: true, 20: true, 22: true, 28: true, 38: true, 43: true}
	// pass -> {width, hashes(full 64), colIndex}
	type pass struct {
		Width  int      `json:"width"`
		Hashes []string `json:"hashes"`
	}
	var out []pass
	seen := map[int]bool{}
	for _, cols := range passColumnNames(dict["Subdivided Dictionary"], objects) {
		w := len(cols)
		if !attributed[w] || seen[w] {
			continue
		}
		seen[w] = true
		var hs []string
		for _, n := range cols {
			if strings.HasPrefix(n, "_") && len(n) == 65 {
				hs = append(hs, n[1:])
			}
		}
		out = append(out, pass{w, hs})
	}
	b, err := json.MarshalIndent(out, "", "")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(outDir(t), "attributed-passes.json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %d attributed pass shapes to %s", len(out), path)
}
