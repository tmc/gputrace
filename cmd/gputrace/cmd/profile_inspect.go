package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/counter"
)

var profileInspectJSON bool
var profileInspectRequireReal bool
var profileInspectRequireCounters bool

type profileInspectOutput struct {
	TracePath                 string         `json:"trace_path"`
	Capture                   filePresence   `json:"capture"`
	UnsortedCapture           filePresence   `json:"unsorted_capture"`
	Store0                    filePresence   `json:"store0"`
	GPUProfilerRaw            filePresence   `json:"gpuprofiler_raw"`
	StreamData                filePresence   `json:"streamData"`
	CountersFiles             []filePresence `json:"counters_files,omitempty"`
	ProfilingFiles            []filePresence `json:"profiling_files,omitempty"`
	TimelineFiles             []filePresence `json:"timeline_files,omitempty"`
	KdebugFiles               []filePresence `json:"kdebug_files,omitempty"`
	RealTiming                bool           `json:"real_timing"`
	TimingClaimsAllowed       bool           `json:"timing_claims_allowed"`
	ProfilerCounters          bool           `json:"profiler_counters"`
	CounterBearingStreamData  bool           `json:"counter_bearing_streamData"`
	DerivedCounterBlocks      int            `json:"derived_counter_blocks"`
	DerivedCounterSampleCount int            `json:"derived_counter_sample_count"`
	CounterClaimsAllowed      bool           `json:"counter_claims_allowed"`
	HardwareClaimsAllowed     bool           `json:"hardware_claims_allowed"`
	ExecutionCostData         bool           `json:"execution_cost_data"`
	Reason                    string         `json:"reason,omitempty"`
}

type filePresence struct {
	Present bool   `json:"present"`
	Path    string `json:"path,omitempty"`
	Bytes   int64  `json:"bytes,omitempty"`
}

var profileInspectCmd = &cobra.Command{
	Use:   "profile-inspect <trace.gputrace>",
	Short: "Inspect profiler payload availability in a .gputrace bundle",
	Long: `Inspect which command-capture and profiler payload files exist in a
.gputrace bundle.

This command does not synthesize timing. It separates timing metadata
(.gpuprofiler_raw/streamData) from counter-bearing profiler payloads. Hardware
counter and bandwidth claims require parsed counter samples and a later
counter-rows join to stable xctrace command-buffer/encoder IDs.`,
	Args: cobra.ExactArgs(1),
	RunE: runProfileInspect,
}

func init() {
	rootCmd.AddCommand(profileInspectCmd)
	profileInspectCmd.Flags().BoolVar(&profileInspectJSON, "json", false, "Output in JSON format")
	profileInspectCmd.Flags().BoolVar(&profileInspectRequireReal, "require-real", false, "Fail unless real timing rows are present")
	profileInspectCmd.Flags().BoolVar(&profileInspectRequireCounters, "require-counters", false, "Fail unless counter-bearing streamData is present")
}

func runProfileInspect(cmd *cobra.Command, args []string) error {
	tracePath := args[0]
	if err := checkTraceFile(tracePath); err != nil {
		return err
	}

	out, err := inspectProfilePayload(tracePath)
	if err != nil {
		return err
	}

	if profileInspectJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			return err
		}
		return enforceProfileInspectRequirements(out)
	}

	fmt.Printf("Trace:             %s\n", out.TracePath)
	printPresence("capture", out.Capture)
	printPresence("unsorted-capture", out.UnsortedCapture)
	printPresence("store0", out.Store0)
	printPresence(".gpuprofiler_raw", out.GPUProfilerRaw)
	printPresence("streamData", out.StreamData)
	fmt.Printf("Counters_f:        %d\n", len(out.CountersFiles))
	fmt.Printf("Profiling_f:       %d\n", len(out.ProfilingFiles))
	fmt.Printf("Timeline_f:        %d\n", len(out.TimelineFiles))
	fmt.Printf("kdebug:            %d\n", len(out.KdebugFiles))
	fmt.Println()
	fmt.Printf("Real timing:       %v\n", out.RealTiming)
	fmt.Printf("Timing claims:     %v\n", out.TimingClaimsAllowed)
	fmt.Printf("Profiler counters: %v\n", out.ProfilerCounters)
	fmt.Printf("Counter samples:   %d\n", out.DerivedCounterSampleCount)
	fmt.Printf("Counter claims:    %v\n", out.CounterClaimsAllowed)
	fmt.Printf("Hardware claims:   %v\n", out.HardwareClaimsAllowed)
	fmt.Printf("Execution cost:    %v\n", out.ExecutionCostData)
	if out.Reason != "" {
		fmt.Println()
		fmt.Println(out.Reason)
	}
	if !out.RealTiming {
		fmt.Println()
		fmt.Println("Missing real profiler timing: .gpuprofiler_raw/streamData is required.")
	}
	return enforceProfileInspectRequirements(out)
}

func inspectProfilePayload(tracePath string) (*profileInspectOutput, error) {
	profilerDir := findAnyProfilerDir(tracePath)
	out := &profileInspectOutput{
		TracePath:       tracePath,
		Capture:         presence(filepath.Join(tracePath, "capture")),
		UnsortedCapture: presence(filepath.Join(tracePath, "unsorted-capture")),
		Store0:          presence(filepath.Join(tracePath, "store0")),
	}
	if profilerDir != "" {
		out.GPUProfilerRaw = presence(profilerDir)
		out.StreamData = presence(filepath.Join(profilerDir, "streamData"))
		out.CountersFiles = globPresence(filepath.Join(profilerDir, "Counters_f_*"))
		out.ProfilingFiles = globPresence(filepath.Join(profilerDir, "Profiling_f_*"))
		out.TimelineFiles = globPresence(filepath.Join(profilerDir, "Timeline_f_*"))
		out.KdebugFiles = globPresence(filepath.Join(profilerDir, "kdebug*"))
	}
	if out.GPUProfilerRaw.Present && out.StreamData.Present {
		if stats, err := counter.ParseStreamData(profilerDir); err == nil {
			out.RealTiming = len(stats.Dispatches) > 0 || len(stats.EncoderTimings) > 0 || stats.TotalTimeUs > 0
			out.TimingClaimsAllowed = out.RealTiming
			out.DerivedCounterBlocks = len(stats.DerivedCounters)
			out.DerivedCounterSampleCount = stats.DerivedCounterSampleCount()
			out.CounterBearingStreamData = out.DerivedCounterSampleCount > 0
			out.ProfilerCounters = out.CounterBearingStreamData
		}
	}
	out.ExecutionCostData = out.RealTiming && len(out.ProfilingFiles) > 0
	out.CounterClaimsAllowed = false
	out.HardwareClaimsAllowed = false
	if out.CounterBearingStreamData {
		out.Reason = "counter samples exist, but counter/hardware claims still require counter-rows join to xctrace command-buffer/encoder IDs"
	} else if out.RealTiming {
		out.Reason = "timing metadata exists, but no parsed counter samples were found; bandwidth/cache/occupancy claims are disallowed"
	} else {
		out.Reason = "no usable timing metadata or counter samples were found"
	}
	return out, nil
}

func enforceProfileInspectRequirements(out *profileInspectOutput) error {
	if profileInspectRequireReal && (out == nil || !out.RealTiming) {
		return fmt.Errorf("required real profiler timing is missing")
	}
	if profileInspectRequireCounters && (out == nil || !out.CounterBearingStreamData) {
		return fmt.Errorf("required counter-bearing streamData is missing")
	}
	return nil
}

func findAnyProfilerDir(tracePath string) string {
	if filepath.Ext(tracePath) == ".gpuprofiler_raw" {
		return tracePath
	}
	entries, err := os.ReadDir(tracePath)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() && filepath.Ext(e.Name()) == ".gpuprofiler_raw" {
			return filepath.Join(tracePath, e.Name())
		}
	}
	return ""
}

func presence(path string) filePresence {
	info, err := os.Stat(path)
	if err != nil {
		return filePresence{}
	}
	return filePresence{Present: true, Path: path, Bytes: sizeOf(info, path)}
}

func globPresence(pattern string) []filePresence {
	matches, _ := filepath.Glob(pattern)
	sort.Strings(matches)
	out := make([]filePresence, 0, len(matches))
	for _, path := range matches {
		if p := presence(path); p.Present {
			out = append(out, p)
		}
	}
	return out
}

func sizeOf(info os.FileInfo, path string) int64 {
	if !info.IsDir() {
		return info.Size()
	}
	var total int64
	_ = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if fi, statErr := d.Info(); statErr == nil {
			total += fi.Size()
		}
		return nil
	})
	return total
}

func printPresence(label string, p filePresence) {
	if p.Present {
		fmt.Printf("%-18s present (%d bytes)\n", label+":", p.Bytes)
		return
	}
	fmt.Printf("%-18s missing\n", label+":")
}
