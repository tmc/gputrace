package tracebench

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Config is one benchfmt file configuration value.
type Config struct {
	Key   string
	Value string
}

// BenchfmtOptions controls benchmark identity and additional configuration.
type BenchfmtOptions struct {
	Name   string
	Config []Config
}

type measurement struct {
	value  float64
	unit   string
	better string
}

// MetricReporter is implemented by testing.B and testing.BenchmarkResult-like
// adapters. ReportMetric writes measurements into the ordinary Go benchmark
// result stream consumed by benchfmt and benchstat.
type MetricReporter interface {
	ReportMetric(value float64, unit string)
}

type attributeReporter interface {
	Attr(key, value string)
}

// ReportMetrics reports every supported measurement through dst. The units
// are trace-scoped unless Analyze was given an explicit Work denominator.
func (r *Report) ReportMetrics(dst MetricReporter) error {
	if r == nil {
		return fmt.Errorf("tracebench: nil report")
	}
	if dst == nil {
		return fmt.Errorf("tracebench: nil metric reporter")
	}
	values := r.measurements()
	if len(values) == 0 {
		return fmt.Errorf("tracebench: report has no benchmark measurements")
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
	for _, value := range values {
		if math.IsNaN(value.value) || math.IsInf(value.value, 0) || value.value < 0 {
			return fmt.Errorf("tracebench: invalid value for %s", value.unit)
		}
		dst.ReportMetric(value.value, value.unit)
	}
	return nil
}

// WriteJSON writes report as indented JSON.
func WriteJSON(w io.Writer, report *Report) error {
	if report == nil {
		return fmt.Errorf("tracebench: nil report")
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		return fmt.Errorf("tracebench: write JSON: %w", err)
	}
	return nil
}

// WriteBenchfmt writes one benchfmt result. Values are trace totals unless
// report.Work declares an explicit normalization denominator.
func WriteBenchfmt(w io.Writer, report *Report, opts BenchfmtOptions) error {
	if report == nil {
		return fmt.Errorf("tracebench: nil report")
	}
	name := opts.Name
	if name == "" {
		name = "BenchmarkGPUTrace"
	}
	if err := validateBenchmarkName(name); err != nil {
		return err
	}
	measurements := report.measurements()
	if len(measurements) == 0 {
		return fmt.Errorf("tracebench: report has no benchmark measurements")
	}
	config, err := benchfmtConfig(report, opts.Config)
	if err != nil {
		return err
	}

	var out strings.Builder
	for _, m := range measurements {
		if m.better != "" {
			fmt.Fprintf(&out, "Unit %s better=%s\n", m.unit, m.better)
		}
	}
	for _, item := range config {
		fmt.Fprintf(&out, "%s: %s\n", item.Key, item.Value)
	}
	if len(config) > 0 {
		out.WriteByte('\n')
	}
	fmt.Fprintf(&out, "%s-1 1", name)
	for _, m := range measurements {
		if math.IsNaN(m.value) || math.IsInf(m.value, 0) || m.value < 0 {
			return fmt.Errorf("tracebench: invalid value for %s", m.unit)
		}
		out.WriteByte(' ')
		out.WriteString(strconv.FormatFloat(m.value, 'g', -1, 64))
		out.WriteByte(' ')
		out.WriteString(m.unit)
	}
	out.WriteByte('\n')
	if _, err := io.WriteString(w, out.String()); err != nil {
		return fmt.Errorf("tracebench: write benchfmt: %w", err)
	}
	return nil
}

func (r *Report) measurements() []measurement {
	denom := float64(1)
	suffix := "trace"
	if r.Work != nil {
		denom = float64(r.Work.Count)
		suffix = r.Work.Unit
	}
	var values []measurement
	if r.Structure.Status == StatusStructural {
		appendCount := func(value *uint64, name string) {
			if value != nil {
				values = append(values, measurement{float64(*value) / denom, name + "/" + suffix, ""})
			}
		}
		appendCount(r.Structure.Dispatches, "dispatches")
		appendCount(r.Structure.CommandBuffers, "command-buffers")
		appendCount(r.Structure.Encoders, "encoders")
	}
	if r.Timing.Status == StatusMeasured {
		appendDuration := func(value *uint64, name string) {
			if value != nil {
				values = append(values, measurement{float64(*value) / denom, name + "/" + suffix, "lower"})
			}
		}
		appendDuration(r.Timing.DispatchSpanNS, "dispatch_span_ns")
		appendDuration(r.Timing.CommandBufferActiveNS, "command_buffer_active_ns")
		appendDuration(r.Timing.CommandBufferWallNS, "command_buffer_wall_ns")
		appendDuration(r.Timing.EffectiveGPUNS, "effective_gpu_ns")
	}
	return values
}

func benchfmtConfig(report *Report, extra []Config) ([]Config, error) {
	values := []Config{
		{"observer", "gputrace"},
		{"observer-version", report.Identity.ObserverVersion},
		{"payload", report.Identity.Payload},
	}
	if report.Identity.TraceUUID != "" {
		values = append(values, Config{"trace-uuid", report.Identity.TraceUUID})
	}
	if report.Timing.Status == StatusMeasured && report.Timing.Source != "" {
		values = append(values, Config{"timing-source", report.Timing.Source})
	}
	if report.Work != nil {
		values = append(values,
			Config{"work-count", strconv.FormatUint(report.Work.Count, 10)},
			Config{"work-unit", report.Work.Unit},
		)
	}
	values = append(values, extra...)
	seen := make(map[string]bool, len(values))
	for _, item := range values {
		if !validConfigKey(item.Key) {
			return nil, fmt.Errorf("tracebench: invalid benchfmt config key %q", item.Key)
		}
		if seen[item.Key] {
			return nil, fmt.Errorf("tracebench: duplicate benchfmt config key %q", item.Key)
		}
		seen[item.Key] = true
		if item.Value == "" || strings.TrimSpace(item.Value) != item.Value || strings.ContainsAny(item.Value, "\r\n") {
			return nil, fmt.Errorf("tracebench: invalid benchfmt config value for %q", item.Key)
		}
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Key < values[j].Key })
	return values, nil
}

func validateBenchmarkName(name string) error {
	if !strings.HasPrefix(name, "Benchmark") || len(name) == len("Benchmark") {
		return fmt.Errorf("tracebench: benchmark name %q must start with Benchmark", name)
	}
	if strings.ContainsAny(name, " \t\r\n") {
		return fmt.Errorf("tracebench: invalid benchmark name %q", name)
	}
	return nil
}

func validConfigKey(key string) bool {
	if key == "" {
		return false
	}
	for i, r := range key {
		if i == 0 && !unicode.IsLower(r) {
			return false
		}
		if !(unicode.IsLower(r) || unicode.IsDigit(r) || r == '-' || r == '_') {
			return false
		}
	}
	return true
}
