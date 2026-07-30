package cmd

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/spf13/cobra"
)

const (
	benchfmtDispatchSpanUnit        = "dispatch_span_ns/op"
	benchfmtCBActiveUnit            = "cb_active_ns/op"
	benchfmtCBWallUnit              = "cb_wall_ns/op"
	benchfmtEffectiveGPUUnit        = "effective_gpu_ns/op"
	benchfmtProfilerSampleCostUnit  = "profiler_sample_cost_percent"
	benchfmtProfilerCostSamplesUnit = "profiler_cost_samples/op"
	benchfmtGPRWCNTRSamplesUnit     = "gprwcntr_samples/op"
	benchfmtDispatchesUnit          = "dispatches/op"
	benchfmtCommandBuffersUnit      = "command-buffers/op"
	benchfmtEncodersUnit            = "encoders/op"
)

var benchfmtConfigOrder = []string{
	"goos",
	"goarch",
	"pkg",
	"cpu",
	"runtime",
	"model",
	"prompt-tokens",
	"capture-range",
	"compile-mode",
	"cache-mode",
	"trace-uuid",
	"mlx-version",
	"payload",
	"timing-source",
}

var benchfmtUnitOrder = []string{
	benchfmtDispatchSpanUnit,
	benchfmtCBActiveUnit,
	benchfmtCBWallUnit,
	benchfmtEffectiveGPUUnit,
	benchfmtProfilerSampleCostUnit,
	benchfmtProfilerCostSamplesUnit,
	benchfmtGPRWCNTRSamplesUnit,
	benchfmtDispatchesUnit,
	benchfmtCommandBuffersUnit,
	benchfmtEncodersUnit,
}

type benchfmtConfig struct {
	Key   string
	Value string
}

type benchfmtValue struct {
	Value float64
	Unit  string
}

type benchfmtRecord struct {
	Suffix string
	Iters  int
	Config []benchfmtConfig
	Values []benchfmtValue
}

type benchfmtConfigFlags []benchfmtConfig

func (f *benchfmtConfigFlags) String() string {
	if f == nil {
		return ""
	}
	values := make([]string, len(*f))
	for i, item := range *f {
		values[i] = item.Key + "=" + item.Value
	}
	return strings.Join(values, ",")
}

func (f *benchfmtConfigFlags) Set(value string) error {
	key, configValue, ok := strings.Cut(value, "=")
	if !ok {
		return fmt.Errorf("bench config must be key=value")
	}
	item := benchfmtConfig{Key: key, Value: configValue}
	if _, err := validateBenchfmtConfig([]benchfmtConfig{item}); err != nil {
		return err
	}
	*f = append(*f, item)
	return nil
}

func (f *benchfmtConfigFlags) Type() string {
	return "key=value"
}

func addBenchfmtFlags(cmd *cobra.Command, enabled *bool, config *benchfmtConfigFlags) {
	cmd.Flags().BoolVar(enabled, "benchfmt", false, "Output Go benchmark format")
	cmd.Flags().Var(config, "bench-config", "Set benchmark configuration key=value (repeatable)")
}

func validateBenchfmtFlags(enabled bool, config benchfmtConfigFlags) error {
	if len(config) > 0 && !enabled {
		return fmt.Errorf("--bench-config requires --benchfmt")
	}
	return nil
}

func mergeBenchfmtConfig(defaults []benchfmtConfig, flags benchfmtConfigFlags) ([]benchfmtConfig, error) {
	base, err := validateBenchfmtConfig(defaults)
	if err != nil {
		return nil, err
	}
	overrides := make(map[string]string, len(flags))
	for _, item := range flags {
		if _, ok := overrides[item.Key]; ok {
			return nil, fmt.Errorf("duplicate --bench-config key %q", item.Key)
		}
		if _, err := validateBenchfmtConfig([]benchfmtConfig{item}); err != nil {
			return nil, err
		}
		overrides[item.Key] = item.Value
	}
	for key, value := range overrides {
		base[key] = value
	}

	merged := make([]benchfmtConfig, 0, len(base))
	for _, key := range benchfmtConfigOrder {
		if value, ok := base[key]; ok {
			merged = append(merged, benchfmtConfig{Key: key, Value: value})
			delete(base, key)
		}
	}
	keys := make([]string, 0, len(base))
	for key := range base {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		merged = append(merged, benchfmtConfig{Key: key, Value: base[key]})
	}
	return merged, nil
}

func writeBenchfmt(w io.Writer, record benchfmtRecord) error {
	iters := record.Iters
	if iters == 0 {
		iters = 1
	}
	if iters < 0 {
		return fmt.Errorf("benchfmt iteration count must be positive")
	}
	if len(record.Values) == 0 {
		return fmt.Errorf("benchfmt record has no measurements")
	}

	config, err := validateBenchfmtConfig(record.Config)
	if err != nil {
		return err
	}
	values, err := validateBenchfmtValues(record.Values)
	if err != nil {
		return err
	}

	var out strings.Builder
	hasConfig := len(config) > 0
	for _, key := range benchfmtConfigOrder {
		if value, ok := config[key]; ok {
			fmt.Fprintf(&out, "%s: %s\n", key, value)
			delete(config, key)
		}
	}
	keys := make([]string, 0, len(config))
	for key := range config {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Fprintf(&out, "%s: %s\n", key, config[key])
	}
	if hasConfig {
		out.WriteByte('\n')
	}

	out.WriteString("BenchmarkGPUTrace")
	if suffix := sanitizeBenchfmtSuffix(record.Suffix); suffix != "" {
		out.WriteByte('/')
		out.WriteString(suffix)
	}
	fmt.Fprintf(&out, "-1 %d", iters)
	for _, value := range values {
		out.WriteByte(' ')
		out.WriteString(strconv.FormatFloat(value.Value, 'g', -1, 64))
		out.WriteByte(' ')
		out.WriteString(value.Unit)
	}
	out.WriteByte('\n')

	if _, err := io.WriteString(w, out.String()); err != nil {
		return fmt.Errorf("write benchfmt: %w", err)
	}
	return nil
}

func validateBenchfmtConfig(values []benchfmtConfig) (map[string]string, error) {
	config := make(map[string]string, len(values))
	for _, item := range values {
		if !validBenchfmtConfigKey(item.Key) {
			return nil, fmt.Errorf("invalid benchfmt config key %q", item.Key)
		}
		if _, ok := config[item.Key]; ok {
			return nil, fmt.Errorf("duplicate benchfmt config key %q", item.Key)
		}
		if item.Value == "" || strings.TrimSpace(item.Value) != item.Value ||
			strings.ContainsAny(item.Value, "\r\n") {
			return nil, fmt.Errorf("invalid benchfmt config value for %q", item.Key)
		}
		config[item.Key] = item.Value
	}
	return config, nil
}

func validBenchfmtConfigKey(key string) bool {
	for i, r := range key {
		if (i == 0 && !unicode.IsLower(r)) || unicode.IsSpace(r) ||
			unicode.IsUpper(r) || r == ':' {
			return false
		}
	}
	return key != ""
}

func validateBenchfmtValues(values []benchfmtValue) ([]benchfmtValue, error) {
	allowed := make(map[string]bool, len(benchfmtUnitOrder))
	for _, unit := range benchfmtUnitOrder {
		allowed[unit] = true
	}
	seen := make(map[string]benchfmtValue, len(values))
	for _, value := range values {
		if !allowed[value.Unit] {
			return nil, fmt.Errorf("invalid benchfmt unit %q", value.Unit)
		}
		if _, ok := seen[value.Unit]; ok {
			return nil, fmt.Errorf("duplicate benchfmt unit %q", value.Unit)
		}
		if math.IsNaN(value.Value) || math.IsInf(value.Value, 0) || value.Value < 0 {
			return nil, fmt.Errorf("invalid benchfmt value for %q", value.Unit)
		}
		seen[value.Unit] = value
	}
	out := make([]benchfmtValue, 0, len(values))
	for _, unit := range benchfmtUnitOrder {
		if value, ok := seen[unit]; ok {
			out = append(out, value)
		}
	}
	return out, nil
}

func sanitizeBenchfmtSuffix(s string) string {
	s = strings.TrimSpace(s)
	var out strings.Builder
	separator := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if separator && out.Len() > 0 {
				out.WriteByte('_')
			}
			out.WriteRune(r)
			separator = false
			continue
		}
		separator = true
	}
	return out.String()
}
