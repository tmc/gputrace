package profilerraw

import (
	"os"
	"path/filepath"
	"testing"
)

// mkProfilerDir creates dir and, when withStream is true, a streamData file.
func mkProfilerDir(t *testing.T, dir string, withStream bool) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if withStream {
		if err := os.WriteFile(filepath.Join(dir, "streamData"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFindDirLayouts(t *testing.T) {
	tests := []struct {
		name       string
		withStream bool
		// setup returns the path callers pass in and the directory to expect.
		setup func(t *testing.T, root string, withStream bool) (query, want string)
	}{
		{
			name: "inside bundle",
			setup: func(t *testing.T, root string, ws bool) (string, string) {
				bundle := filepath.Join(root, "t.gputrace")
				dir := filepath.Join(bundle, "a.gpuprofiler_raw")
				mkProfilerDir(t, dir, ws)
				return bundle, dir
			},
		},
		{
			name: "adjacent sibling",
			setup: func(t *testing.T, root string, ws bool) (string, string) {
				bundle := filepath.Join(root, "t.gputrace")
				if err := os.MkdirAll(bundle, 0o755); err != nil {
					t.Fatal(err)
				}
				dir := bundle + ".gpuprofiler_raw"
				mkProfilerDir(t, dir, ws)
				return bundle, dir
			},
		},
		{
			name: "profiler directory itself",
			setup: func(t *testing.T, root string, ws bool) (string, string) {
				dir := filepath.Join(root, "a.gpuprofiler_raw")
				mkProfilerDir(t, dir, ws)
				return dir, dir
			},
		},
	}

	for _, tt := range tests {
		for _, withStream := range []bool{true, false} {
			name := tt.name
			if withStream {
				name += "/with streamData"
			} else {
				name += "/no streamData"
			}
			t.Run(name, func(t *testing.T) {
				root := t.TempDir()
				query, want := tt.setup(t, root, withStream)

				if got := FindDir(query); got != want {
					t.Errorf("FindDir(%q) = %q, want %q", query, got, want)
				}

				wantStrict := ""
				if withStream {
					wantStrict = want
				}
				if got := FindDirWithStreamData(query); got != wantStrict {
					t.Errorf("FindDirWithStreamData(%q) = %q, want %q", query, got, wantStrict)
				}
			})
		}
	}
}

func TestFindDirAbsent(t *testing.T) {
	root := t.TempDir()
	bundle := filepath.Join(root, "t.gputrace")
	if err := os.MkdirAll(bundle, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"", bundle, filepath.Join(root, "missing")} {
		if got := FindDir(query); got != "" {
			t.Errorf("FindDir(%q) = %q, want %q", query, got, "")
		}
		if got := FindDirWithStreamData(query); got != "" {
			t.Errorf("FindDirWithStreamData(%q) = %q, want %q", query, got, "")
		}
	}
}
