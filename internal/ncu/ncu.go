// Package ncu drives Nsight Compute against the kernels a capture says
// matter, and folds the counter results back into the capture's terms.
//
// Nsight Compute is a microscope, not a timeline: it replays each kernel
// many times with different counters armed and serializes execution, so
// its cost scales with how many launches it profiles. Pointing it only at
// the kernels a timeline has already ranked is what makes it affordable.
package ncu

import (
	"encoding/csv"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// DefaultMetrics are counters that exist on current parts and answer the
// question analyze cannot: whether a kernel is actually using the machine,
// and whether it is waiting on memory.
//
// The dram__ counters are deliberately absent. They are not merely n/a on
// GB10: the whole domain is missing from --query-metrics [V], because the
// part has no separate device DRAM to instrument. Asking for one collects
// nothing while looking like it collected something. What stands in for
// them is the cache hierarchy plus compute_memory_throughput, all three of
// which return real values here [V]; on a memory-bound kernel a low
// lts__t_sector_hit_rate with low throughput is the signature of reading
// something that does not live in device memory.
var DefaultMetrics = []string{
	"gpu__time_duration.sum",
	"sm__throughput.avg.pct_of_peak_sustained_elapsed",
	"sm__warps_active.avg.pct_of_peak_sustained_active",
	"launch__occupancy_limit_registers",
	"gpu__compute_memory_throughput.avg.pct_of_peak_sustained_elapsed",
	"l1tex__t_sector_hit_rate.pct",
	"lts__t_sector_hit_rate.pct",
}

// Options configures one escalation run.
type Options struct {
	// Kernels are the demangled kernel names to profile, most expensive
	// first. Empty profiles everything, which is rarely what anyone wants.
	Kernels []string
	// LaunchCount bounds how many launches of each kernel are replayed.
	LaunchCount int
	// Metrics are the counters to collect; DefaultMetrics when empty.
	Metrics []string
	// Sudo runs ncu through sudo, for hosts where the driver restricts
	// performance counters to administrators.
	Sudo bool
	// Path overrides the ncu binary.
	Path string
}

// KernelNamePattern is the regex ncu should match a kernel by. Matching
// runs against the bare function name (--kernel-name-base function): the
// demangled form ncu would otherwise use carries the parameter list, so a
// pattern built from a kernel name alone matches nothing and ncu reports
// "No kernels were profiled" rather than an error [V].
func KernelNamePattern(names []string) string {
	if len(names) == 0 {
		return ""
	}
	parts := make([]string, 0, len(names))
	for _, n := range names {
		parts = append(parts, regexp.QuoteMeta(baseName(n)))
	}
	return "regex:^(" + strings.Join(parts, "|") + ")$"
}

// baseName strips template arguments and parameter lists so a C++ symbol
// matches the base name ncu reports.
// SameKernel reports whether an ncu-reported kernel name and a name taken
// from a capture denote the same kernel. The two spell it differently: a
// capture carries the demangled symbol with its namespace
// (mlx::core::cu::gemv_single<__nv_bfloat16, 8, 4>), while ncu under
// --kernel-name-base function reports it with the namespace stripped and a
// return type and parameter list attached. Comparing the base names is what
// makes them meet.
func SameKernel(a, b string) bool {
	return baseName(stripReturnType(a)) == baseName(stripReturnType(b))
}

// stripReturnType removes a leading return type, which ncu prints and a
// demangled capture symbol does not.
func stripReturnType(name string) string {
	name = strings.TrimSpace(name)
	if i := strings.IndexAny(name, "<("); i >= 0 {
		if j := strings.LastIndex(name[:i], " "); j >= 0 {
			return name[j+1:]
		}
		return name
	}
	if j := strings.LastIndex(name, " "); j >= 0 {
		return name[j+1:]
	}
	return name
}

func baseName(name string) string {
	if i := strings.IndexAny(name, "<("); i > 0 {
		name = name[:i]
	}
	if i := strings.LastIndex(name, "::"); i >= 0 {
		name = name[i+2:]
	}
	return strings.TrimSpace(name)
}

// Command builds the ncu invocation for a workload. The workload is the
// same command the capture recorded, so the escalation profiles the run
// the timeline described rather than an approximation of it.
func Command(opts Options, workload []string) (*exec.Cmd, error) {
	if len(workload) == 0 {
		return nil, fmt.Errorf("ncu: no workload command to profile")
	}
	path := opts.Path
	if path == "" {
		found, err := exec.LookPath("ncu")
		if err != nil {
			return nil, fmt.Errorf("ncu: not found on PATH: %w", err)
		}
		path = found
	}
	metrics := opts.Metrics
	if len(metrics) == 0 {
		metrics = DefaultMetrics
	}
	args := []string{
		"--target-processes", "all",
		"--kernel-name-base", "function",
		"--metrics", strings.Join(metrics, ","),
		"--csv",
	}
	if pattern := KernelNamePattern(opts.Kernels); pattern != "" {
		args = append(args, "--kernel-name", pattern)
	}
	if opts.LaunchCount > 0 {
		args = append(args, "--launch-count", strconv.Itoa(opts.LaunchCount))
	}
	args = append(args, workload...)

	if opts.Sudo {
		return exec.Command("sudo", append([]string{"-E", path}, args...)...), nil
	}
	return exec.Command(path, args...), nil
}

// Measurement is one counter value ncu reported for one kernel.
type Measurement struct {
	Metric string  `json:"metric"`
	Unit   string  `json:"unit,omitempty"`
	Median float64 `json:"median"`
	Min    float64 `json:"min"`
	Max    float64 `json:"max"`
	Count  int     `json:"count"`
}

// KernelCounters holds every counter measured for one kernel.
type KernelCounters struct {
	Kernel   string        `json:"kernel"`
	Launches int           `json:"launches"`
	Metrics  []Measurement `json:"metrics"`
	// Unsupported names metrics that came back n/a on this part. They are
	// reported rather than dropped: a metric that does not exist here is a
	// fact about the hardware, not an absence of data.
	Unsupported []string `json:"unsupported,omitempty"`
}

// Result is the counter data merged from one ncu run.
type Result struct {
	Schema  string           `json:"schema"`
	Command []string         `json:"command,omitempty"`
	Kernels []KernelCounters `json:"kernels"`
}

// SchemaV1 identifies the merged counter payload written into a bundle.
const SchemaV1 = "gputrace.ncu/v1"

// ParseCSV reads ncu's --csv details output into per-kernel counters.
// ncu writes progress lines and warnings on the same stream, so parsing
// starts at the header row rather than at the first line.
func ParseCSV(r io.Reader) (*Result, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	text := string(data)
	idx := strings.Index(text, `"ID","Process ID"`)
	if idx < 0 {
		if idx = strings.Index(text, `"ID"`); idx < 0 {
			return nil, fmt.Errorf("ncu: no CSV table in output (%s)", firstLine(text))
		}
	}
	reader := csv.NewReader(strings.NewReader(text[idx:]))
	reader.FieldsPerRecord = -1
	rows, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("ncu: parse CSV: %w", err)
	}
	if len(rows) < 2 {
		return nil, fmt.Errorf("ncu: CSV table has no rows; treat this run as void")
	}
	col := map[string]int{}
	for i, name := range rows[0] {
		col[name] = i
	}
	need := []string{"ID", "Kernel Name", "Metric Name", "Metric Value"}
	for _, n := range need {
		if _, ok := col[n]; !ok {
			return nil, fmt.Errorf("ncu: CSV missing column %q", n)
		}
	}

	type key struct{ kernel, metric string }
	values := map[key][]float64{}
	units := map[key]string{}
	unsupported := map[key]bool{}
	launches := map[string]map[string]bool{}
	order := []string{}
	metricOrder := map[string][]string{}

	for _, row := range rows[1:] {
		get := func(name string) string {
			i, ok := col[name]
			if !ok || i >= len(row) {
				return ""
			}
			return row[i]
		}
		kernel := get("Kernel Name")
		metric := get("Metric Name")
		if kernel == "" || metric == "" {
			continue
		}
		k := key{kernel, metric}
		if _, seen := launches[kernel]; !seen {
			launches[kernel] = map[string]bool{}
			order = append(order, kernel)
		}
		launches[kernel][get("ID")] = true
		if _, seen := units[k]; !seen {
			units[k] = get("Metric Unit")
			metricOrder[kernel] = append(metricOrder[kernel], metric)
		}
		raw := strings.TrimSpace(get("Metric Value"))
		if raw == "" || raw == "n/a" {
			unsupported[k] = true
			continue
		}
		v, err := strconv.ParseFloat(strings.ReplaceAll(raw, ",", ""), 64)
		if err != nil {
			continue // non-numeric metric (a string-valued counter)
		}
		values[k] = append(values[k], v)
	}

	out := &Result{Schema: SchemaV1}
	for _, kernel := range order {
		kc := KernelCounters{Kernel: kernel, Launches: len(launches[kernel])}
		for _, metric := range metricOrder[kernel] {
			k := key{kernel, metric}
			vals := values[k]
			if len(vals) == 0 {
				if unsupported[k] {
					kc.Unsupported = append(kc.Unsupported, metric)
				}
				continue
			}
			sort.Float64s(vals)
			kc.Metrics = append(kc.Metrics, Measurement{
				Metric: metric,
				Unit:   units[k],
				Median: vals[len(vals)/2],
				Min:    vals[0],
				Max:    vals[len(vals)-1],
				Count:  len(vals),
			})
		}
		out.Kernels = append(out.Kernels, kc)
	}
	return out, nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 120 {
		s = s[:120]
	}
	if s == "" {
		return "no output"
	}
	return s
}

// PermissionDenied reports whether ncu refused for lack of counter access.
// ncu exits 0 in that case, so the output is the only signal.
func PermissionDenied(output string) bool {
	return strings.Contains(output, "ERR_NVGPUCTRPERM")
}
