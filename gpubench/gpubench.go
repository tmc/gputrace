// Package gpubench integrates gputrace with Go benchmarks.
//
// The package is a standard-library-only client for the gputrace command. It
// deliberately does not import the parent gputrace module, so benchmark suites
// do not inherit its parser, CLI, private-framework, or Xcode dependencies.
package gpubench

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

// DefaultExecutable is the command used by a zero-value Client.
const DefaultExecutable = "gputrace"

// Client runs a gputrace executable. The zero value finds gputrace on PATH.
// All trace collection and analysis crosses this process boundary; Client does
// not link the parent gputrace module.
type Client struct {
	Executable string
	Env        []string
	Stderr     io.Writer
}

// Work declares the logical work represented by one trace.
type Work struct {
	Count uint64 `json:"count"`
	Unit  string `json:"unit"`
}

// AnalyzeOptions controls explicit per-work normalization.
type AnalyzeOptions struct {
	Work *Work
}

// CaptureOptions controls a structural Metal capture.
type CaptureOptions struct {
	Output string
	Dir    string
}

// ProfileOptions controls headless MTLReplayer profiling.
type ProfileOptions struct {
	Output string
	Embed  bool
	Wait   bool
}

// Status describes the quality of evidence in a report section.
type Status string

const (
	StatusMeasured    Status = "measured"
	StatusStructural  Status = "structural"
	StatusApproximate Status = "approximate"
	StatusUnsupported Status = "unsupported"
	StatusInvalid     Status = "invalid"
	StatusIncomplete  Status = "incomplete"
)

// Section records collector status and provenance.
type Section struct {
	Status Status `json:"status"`
	Source string `json:"source,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// Identity binds measurements to their trace and gputrace observer.
type Identity struct {
	Path            string `json:"path"`
	TraceUUID       string `json:"trace_uuid,omitempty"`
	Payload         string `json:"payload"`
	ObserverVersion string `json:"observer_version"`
}

// Structure contains trace-derived workload shape.
type Structure struct {
	Section
	CommandBuffers *uint64 `json:"command_buffers,omitempty"`
	Encoders       *uint64 `json:"encoders,omitempty"`
	Dispatches     *uint64 `json:"dispatches,omitempty"`
	UniqueKernels  *uint64 `json:"unique_kernels,omitempty"`
}

// Timing contains distinct measured GPU timing boundaries. Nil means the
// source did not supply the metric; it is not a measured zero.
type Timing struct {
	Section
	DispatchSpanNS        *uint64 `json:"dispatch_span_ns,omitempty"`
	CommandBufferActiveNS *uint64 `json:"command_buffer_active_ns,omitempty"`
	CommandBufferWallNS   *uint64 `json:"command_buffer_wall_ns,omitempty"`
	EffectiveGPUNS        *uint64 `json:"effective_gpu_ns,omitempty"`
}

// Refusal records evidence that could not support a claim.
type Refusal struct {
	Collector string `json:"collector"`
	Reason    string `json:"reason"`
}

// Report is the stable result returned by gputrace bench --format json.
type Report struct {
	SchemaVersion int       `json:"schema_version"`
	Identity      Identity  `json:"identity"`
	Work          *Work     `json:"work,omitempty"`
	Structure     Structure `json:"structure"`
	Timing        Timing    `json:"timing"`
	Refusals      []Refusal `json:"refusals,omitempty"`
}

// MetricReporter is implemented by testing.B.
type MetricReporter interface {
	ReportMetric(value float64, unit string)
}

type attributeReporter interface {
	Attr(key, value string)
}

// Analyze asks gputrace to produce its stable sectioned report for trace.
func (c Client) Analyze(ctx context.Context, trace string, opts AnalyzeOptions) (*Report, error) {
	args := []string{"bench", trace, "--format", "json"}
	if opts.Work != nil {
		if err := validateWork(opts.Work); err != nil {
			return nil, err
		}
		args = append(args,
			"--bench-work", strconv.FormatUint(opts.Work.Count, 10),
			"--bench-work-unit", opts.Work.Unit,
		)
	}
	out, err := c.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	var report Report
	if err := json.Unmarshal(out, &report); err != nil {
		return nil, fmt.Errorf("gpubench: decode gputrace report: %w", err)
	}
	if report.SchemaVersion != 1 {
		return nil, fmt.Errorf("gpubench: unsupported report schema %d", report.SchemaVersion)
	}
	return &report, nil
}

// Capture runs argv under the Metal capture interposer. Output is required and
// must not already exist.
func (c Client) Capture(ctx context.Context, opts CaptureOptions, argv ...string) (string, error) {
	if opts.Output == "" {
		return "", errors.New("gpubench: capture output is required")
	}
	if len(argv) == 0 {
		return "", errors.New("gpubench: capture command is required")
	}
	args := []string{"capture", "--output", opts.Output}
	if opts.Dir != "" {
		args = append(args, "--dir", opts.Dir)
	}
	args = append(args, "--")
	args = append(args, argv...)
	if _, err := c.run(ctx, args...); err != nil {
		return "", err
	}
	path, err := filepath.Abs(opts.Output)
	if err != nil {
		return "", fmt.Errorf("gpubench: resolve capture output: %w", err)
	}
	return path, nil
}

// Profile adds measured profiler data to trace using headless MTLReplayer.
// Wait queues behind another replay; it does not alter overlap within trace.
func (c Client) Profile(ctx context.Context, trace string, opts ProfileOptions) (string, error) {
	args := []string{"profile-replay", trace}
	if opts.Output != "" {
		args = append(args, "--output", opts.Output)
	}
	if opts.Embed {
		args = append(args, "--embed")
	}
	if opts.Wait {
		args = append(args, "--wait")
	}
	if _, err := c.run(ctx, args...); err != nil {
		return "", err
	}
	output := opts.Output
	if output == "" {
		output = defaultProfileOutput(trace)
	}
	path, err := filepath.Abs(output)
	if err != nil {
		return "", fmt.Errorf("gpubench: resolve profile output: %w", err)
	}
	return path, nil
}

// ReportMetrics writes supported measurements to dst. Trace-scoped units are
// used unless the report carries an explicit work denominator.
func (r *Report) ReportMetrics(dst MetricReporter) error {
	if r == nil {
		return errors.New("gpubench: nil report")
	}
	if dst == nil {
		return errors.New("gpubench: nil metric reporter")
	}
	if attrs, ok := dst.(attributeReporter); ok {
		attrs.Attr("gputrace_observer", r.Identity.ObserverVersion)
		attrs.Attr("gputrace_payload", r.Identity.Payload)
		if r.Identity.TraceUUID != "" {
			attrs.Attr("gputrace_trace_uuid", r.Identity.TraceUUID)
		}
		if r.Timing.Status == StatusMeasured && r.Timing.Source != "" {
			attrs.Attr("gputrace_timing_source", r.Timing.Source)
		}
		if r.Work != nil {
			attrs.Attr("gputrace_work_count", strconv.FormatUint(r.Work.Count, 10))
			attrs.Attr("gputrace_work_unit", r.Work.Unit)
		}
	}
	denom, suffix, err := r.denominator()
	if err != nil {
		return err
	}
	reported := 0
	report := func(value *uint64, name string) {
		if value != nil {
			dst.ReportMetric(float64(*value)/denom, name+"/"+suffix)
			reported++
		}
	}
	if r.Structure.Status == StatusStructural {
		report(r.Structure.Dispatches, "dispatches")
		report(r.Structure.CommandBuffers, "command-buffers")
		report(r.Structure.Encoders, "encoders")
	}
	if r.Timing.Status == StatusMeasured {
		report(r.Timing.DispatchSpanNS, "dispatch_span_ns")
		report(r.Timing.CommandBufferActiveNS, "command_buffer_active_ns")
		report(r.Timing.CommandBufferWallNS, "command_buffer_wall_ns")
		report(r.Timing.EffectiveGPUNS, "effective_gpu_ns")
	}
	if reported == 0 {
		return errors.New("gpubench: report has no benchmark measurements")
	}
	return nil
}

func (c Client) run(ctx context.Context, args ...string) ([]byte, error) {
	path := c.Executable
	if path == "" {
		path = DefaultExecutable
	}
	cmd := exec.CommandContext(ctx, path, args...)
	if c.Env != nil {
		cmd.Env = append(os.Environ(), c.Env...)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if c.Stderr != nil && stderr.Len() > 0 {
			_, _ = io.Copy(c.Stderr, bytes.NewReader(stderr.Bytes()))
		}
		if detail := bytes.TrimSpace(stderr.Bytes()); len(detail) > 0 {
			return nil, fmt.Errorf("gpubench: gputrace %s: %w: %s", args[0], err, detail)
		}
		return nil, fmt.Errorf("gpubench: gputrace %s: %w", args[0], err)
	}
	return stdout.Bytes(), nil
}

func (r *Report) denominator() (float64, string, error) {
	if r.Work == nil {
		return 1, "trace", nil
	}
	if err := validateWork(r.Work); err != nil {
		return 0, "", err
	}
	return float64(r.Work.Count), r.Work.Unit, nil
}

func validateWork(work *Work) error {
	if work.Count == 0 {
		return errors.New("gpubench: work count must be positive")
	}
	switch work.Unit {
	case "op", "token", "step", "byte":
		return nil
	default:
		return fmt.Errorf("gpubench: unsupported work unit %q", work.Unit)
	}
}

func defaultProfileOutput(path string) string {
	clean := filepath.Clean(path)
	return clean[:len(clean)-len(filepath.Ext(clean))] + "-perfdata.gputrace"
}
