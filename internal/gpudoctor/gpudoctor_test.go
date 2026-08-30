package gpudoctor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNsysVerdict(t *testing.T) {
	tests := []struct {
		version string
		want    Status
	}{
		// 2026.1 defaults -t cuda to hardware event tracing, whose GB10
		// support probe succeeds and then drops every kernel record.
		{"2026.1.1", StatusWarn},
		// 2024.5 bundles a CUPTI far older than current drivers.
		{"2024.5.1", StatusFail},
		{"2025.3.2", StatusOK},
		{"", StatusSkip},
	}
	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			got, detail, _ := nsysVerdict(tt.version)
			if got != tt.want {
				t.Errorf("nsysVerdict(%q) = %v, want %v", tt.version, got, tt.want)
			}
			if detail == "" {
				t.Error("verdict carries no explanation")
			}
		})
	}
}

func TestMajorOf(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{{"13.0", 13}, {"12.6", 12}, {"", 0}, {"nonsense", 0}}
	for _, tt := range tests {
		if got := majorOf(tt.in); got != tt.want {
			t.Errorf("majorOf(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestReportWorst(t *testing.T) {
	tests := []struct {
		name   string
		checks []Check
		want   Status
	}{
		{"empty", nil, StatusOK},
		{"warn beats skip", []Check{{Status: StatusSkip}, {Status: StatusWarn}}, StatusWarn},
		{"fail beats warn", []Check{{Status: StatusWarn}, {Status: StatusFail}, {Status: StatusOK}}, StatusFail},
		{"all ok", []Check{{Status: StatusOK}, {Status: StatusOK}}, StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rep := &Report{Checks: tt.checks}
			if got := rep.Worst(); got != tt.want {
				t.Errorf("Worst() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Run must produce a diagnosis on any host, including one with no GPU:
// an undiagnosable environment is itself the diagnosis.
func TestRunAlwaysReports(t *testing.T) {
	rep := Run(Options{})
	if len(rep.Checks) == 0 {
		t.Fatal("Run() produced no checks")
	}
	for _, c := range rep.Checks {
		if c.Name == "" || c.Status == "" || c.Detail == "" {
			t.Errorf("incomplete check: %+v", c)
		}
	}
}

// writeCaptureBundle creates a minimal .gpucapture holding the given JSONL.
func writeCaptureBundle(t *testing.T, records string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "run.gpucapture")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(records), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// captureCheck runs the bundle check alone. Run would also probe the whole
// environment, which is slow and has nothing to do with the bundle.
func captureCheck(t *testing.T, bundle string) Check {
	t.Helper()
	rep := &Report{}
	checkCapture(rep, bundle)
	if len(rep.Checks) != 1 {
		t.Fatalf("checkCapture added %d checks, want 1", len(rep.Checks))
	}
	return rep.Checks[0]
}

// TestDoctorDiagnosesPartialCaptures extends the diagnosis from empty to
// half. The Go-binary warning is about a capture that comes back with
// nothing in it, and it fired correctly on a capture that came back with
// half — which looks entirely healthy, because it renders, summarizes, and
// diffs into confident numbers.
func TestDoctorDiagnosesPartialCaptures(t *testing.T) {
	const kernel = `{"kind":"kernel","raw_symbol":"_Z1kv","start_ns":%d,"end_ns":%d}` + "\n"

	whole := writeCaptureBundle(t, fmt.Sprintf(kernel, 100, 200)+fmt.Sprintf(kernel, 300, 400))
	c := captureCheck(t, whole)
	if c.Status != StatusOK {
		t.Errorf("an intact capture reports %q: %s", c.Status, c.Detail)
	}
	if !strings.Contains(c.Detail, "2 kernel records") {
		t.Errorf("detail does not state what the bundle holds: %s", c.Detail)
	}

	partial := writeCaptureBundle(t, `{"kind":"dropped","records":31144}`+"\n"+fmt.Sprintf(kernel, 100, 200))
	c = captureCheck(t, partial)
	if c.Status != StatusFail {
		t.Errorf("a capture that dropped 31144 records reports %q: %s", c.Status, c.Detail)
	}
	if !strings.Contains(c.Detail, "31144") {
		t.Errorf("detail does not name the drop count: %s", c.Detail)
	}
	if c.Remedy == "" {
		t.Error("a partial capture is reported with no remedy")
	}

	// doctor must route a bundle to this check rather than to the target
	// checks, which would try to ldd a directory.
	rep := Run(Options{Target: whole})
	found := false
	for _, c := range rep.Checks {
		if strings.HasPrefix(c.Name, "bundle ") {
			found = true
		}
	}
	if !found {
		t.Error("doctor <bundle> did not run the bundle check")
	}

	empty := writeCaptureBundle(t, `{"kind":"span","name":"decode","start_ns":1,"end_ns":2}`+"\n")
	c = captureCheck(t, empty)
	if c.Status != StatusFail || !strings.Contains(c.Detail, "no kernel records") {
		t.Errorf("an empty capture reports %q: %s", c.Status, c.Detail)
	}
	if !strings.Contains(c.Remedy, "flush") {
		t.Errorf("the empty-capture remedy does not name the missing flush: %s", c.Remedy)
	}
}
