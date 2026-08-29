// Package optimize runs GPU workloads reproducibly and compares runs with
// noise-aware verdicts, closing the measure-change-verify loop.
package optimize

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"sort"
	"time"
)

// Config describes one reproducible workload execution series.
type Config struct {
	Command    []string `json:"command"`               // argv to execute
	Dir        string   `json:"dir,omitempty"`         // working directory
	Warmups    int      `json:"warmups"`               // discarded runs before measurement
	Iterations int      `json:"iterations"`            // measured runs
	OutputPath string   `json:"output_path,omitempty"` // where to persist the Result
	// Env adds "K=V" entries to the child's inherited environment. It is
	// how a series runs the same command under an instrumented
	// configuration, so the two sides differ only in the instrumentation.
	Env []string `json:"env,omitempty"`
}

// Iteration is one measured run.
type Iteration struct {
	Index      int    `json:"index"`
	WallNS     uint64 `json:"wall_ns"`
	ExitCode   int    `json:"exit_code"`
	StdoutTail string `json:"stdout_tail,omitempty"`
}

// Result summarizes one Config executed Iterations times.
type Result struct {
	Config      Config      `json:"config"`
	Iterations  []Iteration `json:"iterations"`
	MedianNS    uint64      `json:"median_ns"`
	Q1NS        uint64      `json:"q1_ns"`
	Q3NS        uint64      `json:"q3_ns"`
	FailedCount int         `json:"failed_count"`
	StartedAt   time.Time   `json:"started_at"`
	DurationS   float64     `json:"duration_s"`
}

const maxStdoutTail = 2048

// Run executes cfg.Warmups discarded runs then cfg.Iterations measured runs,
// returning wall-clock statistics. A nonzero child exit is recorded on the
// iteration, not treated as a harness error; the caller decides whether a
// failed workload invalidates the comparison.
func Run(cfg Config) (*Result, error) {
	if len(cfg.Command) == 0 {
		return nil, fmt.Errorf("optimize: no command configured")
	}
	if cfg.Iterations <= 0 {
		cfg.Iterations = 1
	}
	res := &Result{Config: cfg, StartedAt: time.Now()}
	total := cfg.Warmups + cfg.Iterations
	for i := 0; i < total; i++ {
		it, err := runOnce(cfg)
		if err != nil {
			return res, fmt.Errorf("optimize: run %d: %w", i, err)
		}
		if i >= cfg.Warmups {
			it.Index = len(res.Iterations)
			res.Iterations = append(res.Iterations, it)
			if it.ExitCode != 0 {
				res.FailedCount++
			}
		}
	}
	res.DurationS = time.Since(res.StartedAt).Seconds()
	walls := make([]uint64, len(res.Iterations))
	for i, it := range res.Iterations {
		walls[i] = it.WallNS
	}
	res.MedianNS = median(walls)
	res.Q1NS, res.Q3NS = quartiles(walls)
	if cfg.OutputPath != "" {
		if data, err := json.MarshalIndent(res, "", "  "); err == nil {
			_ = os.WriteFile(cfg.OutputPath, append(data, '\n'), 0o644)
		}
	}
	return res, nil
}

func runOnce(cfg Config) (Iteration, error) {
	cmd := exec.Command(cfg.Command[0], cfg.Command[1:]...)
	cmd.Dir = cfg.Dir
	if len(cfg.Env) > 0 {
		cmd.Env = append(os.Environ(), cfg.Env...)
	}
	var stdout io.ReadCloser
	start := time.Now()
	out, err := cmd.Output()
	wall := time.Since(start)
	it := Iteration{WallNS: uint64(wall.Nanoseconds())}
	if out != nil {
		stdout = nil
		_ = stdout
		s := string(out)
		if len(s) > maxStdoutTail {
			s = s[len(s)-maxStdoutTail:]
		}
		it.StdoutTail = s
	}
	if ee, ok := err.(*exec.ExitError); ok {
		it.ExitCode = ee.ExitCode()
	} else if err != nil {
		return it, err // could not start the process at all
	}
	return it, nil
}

func median(sorted []uint64) uint64 {
	if len(sorted) == 0 {
		return 0
	}
	s := append([]uint64(nil), sorted...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	mid := len(s) / 2
	if len(s)%2 == 1 {
		return s[mid]
	}
	return (s[mid-1] + s[mid]) / 2
}

// quartiles returns interpolated Q1/Q3 of the sample.
func quartiles(v []uint64) (uint64, uint64) {
	if len(v) == 0 {
		return 0, 0
	}
	s := append([]uint64(nil), v...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return interp(s, 0.25), interp(s, 0.75)
}

func interp(sorted []uint64, q float64) uint64 {
	pos := q * float64(len(sorted)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if lo == hi || hi >= len(sorted) {
		return sorted[lo]
	}
	frac := pos - float64(lo)
	return uint64(float64(sorted[lo])*(1-frac) + float64(sorted[hi])*frac)
}
