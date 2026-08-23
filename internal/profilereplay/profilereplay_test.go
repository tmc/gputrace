package profilereplay

import (
	"runtime"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultOutput(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"bundle", "run.gputrace", "run-perfdata.gputrace"},
		{"path", "/traces/run.gputrace", "/traces/run-perfdata.gputrace"},
		{"trailing slash", "/traces/run.gputrace/", "/traces/run-perfdata.gputrace"},
		{"no extension", "run", "run-perfdata.gputrace"},
		{"dotted name", "run.v2.gputrace", "run.v2-perfdata.gputrace"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DefaultOutput(tt.input); got != tt.want {
				t.Fatalf("DefaultOutput(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestDefaultProfilerOutput(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"run.gputrace", "run-perfdata.gpuprofiler_raw"},
		{"/traces/run.gputrace", "/traces/run-perfdata.gpuprofiler_raw"},
		{"run", "run-perfdata.gpuprofiler_raw"},
	}
	for _, test := range tests {
		if got := DefaultProfilerOutput(test.input); got != test.want {
			t.Errorf("DefaultProfilerOutput(%q) = %q, want %q", test.input, got, test.want)
		}
	}
}

func TestProfileRejectsMisleadingProfilerOnlySuffix(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "trace.gputrace")
	if err := os.Mkdir(in, 0755); err != nil {
		t.Fatal(err)
	}
	_, err := Profile(context.Background(), in, Options{
		Output:       filepath.Join(dir, "profile.gputrace"),
		ProfilerOnly: true,
	})
	if err == nil || !strings.Contains(err.Error(), ".gpuprofiler_raw") {
		t.Fatalf("Profile error = %v, want .gpuprofiler_raw suffix refusal", err)
	}
}

func TestAssembleOutput(t *testing.T) {
	tests := []struct {
		name         string
		profilerOnly bool
		wantCapture  bool
		wantRaw      string
	}{
		{"self-contained", false, true, "profile.gpuprofiler_raw/streamData"},
		{"profiler-only", true, false, "streamData"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			in := filepath.Join(dir, "in.gputrace")
			payload := filepath.Join(dir, "profile.gpuprofiler_raw")
			out := filepath.Join(dir, "out")
			if err := os.Mkdir(in, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(in, "capture"), []byte("capture"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(payload, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(payload, "streamData"), []byte("profile"), 0644); err != nil {
				t.Fatal(err)
			}

			if err := assembleOutput(in, out, payload, test.profilerOnly); err != nil {
				t.Fatal(err)
			}
			_, captureErr := os.Stat(filepath.Join(out, "capture"))
			if got := captureErr == nil; got != test.wantCapture {
				t.Errorf("capture present = %v, want %v", got, test.wantCapture)
			}
			if _, err := os.Stat(filepath.Join(out, test.wantRaw)); err != nil {
				t.Errorf("profiler data: %v", err)
			}
		})
	}
}

func TestReplayable(t *testing.T) {
	tests := []struct {
		name    string
		entries []string
		want    error
	}{
		{"capture", []string{"capture", "metadata"}, nil},
		{"unsorted-capture only", []string{"unsorted-capture", "metadata"}, nil},
		// A profiler-only bundle is what a replay writes. Replaying one again
		// has nothing to work from, and would otherwise launch MTLReplayer only
		// to produce an empty result.
		{"profiler-only", []string{"trace.gpuprofiler_raw"}, ErrNoCapture},
		{"empty", nil, ErrNoCapture},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bundle := filepath.Join(t.TempDir(), "trace.gputrace")
			if err := os.Mkdir(bundle, 0o755); err != nil {
				t.Fatal(err)
			}
			for _, entry := range tt.entries {
				path := filepath.Join(bundle, entry)
				if filepath.Ext(entry) == ".gpuprofiler_raw" {
					if err := os.Mkdir(path, 0o755); err != nil {
						t.Fatal(err)
					}
					continue
				}
				if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			err := Replayable(bundle)
			if !errors.Is(err, tt.want) {
				t.Fatalf("Replayable(%v) = %v, want %v", tt.entries, err, tt.want)
			}
		})
	}
}

func TestReplayableRejectsMissingAndNonDirectory(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "trace.gputrace")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Replayable(file); err == nil {
		t.Fatal("Replayable accepted a regular file")
	}
	if err := Replayable(filepath.Join(dir, "absent.gputrace")); err == nil {
		t.Fatal("Replayable accepted a path that does not exist")
	}
}

// TestProfileRefusesExistingOutput guards the destructive case: the output is
// assembled from a copy of the input, so silently reusing a populated directory
// would interleave two traces.
func TestProfileRefusesExistingOutput(t *testing.T) {
	if err := Available(); err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	in := filepath.Join(dir, "trace.gputrace")
	if err := os.Mkdir(in, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(in, "capture"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.gputrace")
	if err := os.Mkdir(out, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Profile(context.Background(), in, Options{Output: out}); err == nil {
		t.Fatal("Profile overwrote an existing output path")
	}
}

// TestProfileRejectsUnreplayableBeforeLaunch is the regression test for the
// failure that motivated the postcondition: a bad input made `open -W` exit 0
// in about a tenth of a second having done nothing, so a caller reading the
// exit status reported a successful profile of a run that never happened.
func TestProfileRejectsUnreplayableBeforeLaunch(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("MTLReplayer launch is darwin-only")
	}
	dir := t.TempDir()
	in := filepath.Join(dir, "profiler-only.gputrace")
	if err := os.MkdirAll(filepath.Join(in, "trace.gpuprofiler_raw"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.gputrace")
	_, err := Profile(context.Background(), in, Options{Output: out})
	if !errors.Is(err, ErrNoCapture) {
		t.Fatalf("Profile error = %v, want ErrNoCapture", err)
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatal("Profile created an output path for a trace it could not replay")
	}
}
