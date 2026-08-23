package optimize

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunHarnessExecutesAndMeasures(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "run.json")
	cfg := Config{
		Command:    []string{"echo", "hello"},
		Warmups:    1,
		Iterations: 3,
		OutputPath: out,
	}
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Iterations) != 3 {
		t.Fatalf("iterations = %d, want 3", len(res.Iterations))
	}
	for i, it := range res.Iterations {
		if it.ExitCode != 0 {
			t.Errorf("iteration %d exit = %d", i, it.ExitCode)
		}
		if it.WallNS <= 0 {
			t.Errorf("iteration %d unmeasured", i)
		}
	}
	if res.MedianNS <= 0 {
		t.Errorf("median = %d", res.MedianNS)
	}
	if !strings.Contains(res.Iterations[0].StdoutTail, "hello") {
		t.Errorf("stdout tail = %q", res.Iterations[0].StdoutTail)
	}
}

func TestRunHarnessRecordsFailure(t *testing.T) {
	cfg := Config{Command: []string{"false"}, Warmups: 0, Iterations: 2}
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run should record, not fail: %v", err)
	}
	if res.FailedCount != 2 {
		t.Errorf("failed = %d, want 2", res.FailedCount)
	}
}

func TestRunHarnessPersistsResult(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "run.json")
	_, err := Run(Config{Command: []string{"true"}, Iterations: 1, OutputPath: out})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("result not persisted: %v", err)
	}
	if !strings.Contains(string(data), `"iterations"`) {
		t.Errorf("persisted result missing iterations: %s", data)
	}
}

func TestRunHarnessRejectsEmptyCommand(t *testing.T) {
	if _, err := Run(Config{}); err == nil {
		t.Fatal("empty command accepted")
	}
}

func TestMedianAndIQR(t *testing.T) {
	ns := []uint64{10, 20, 30, 40, 100}
	if got := median(ns); got != 30 {
		t.Errorf("median = %d, want 30", got)
	}
	q1, q3 := quartiles(ns)
	if q1 != 20 || q3 != 40 {
		// Integer positions land exactly on samples: pos(.25)=1.0 -> 20,
		// pos(.75)=3.0 -> 40.
		t.Errorf("quartiles = %d/%d, want 20/40", q1, q3)
	}
	if iqr := q3 - q1; iqr != 20 {
		t.Errorf("iqr = %d", iqr)
	}
}
