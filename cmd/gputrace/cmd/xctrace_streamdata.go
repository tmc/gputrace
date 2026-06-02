//go:build darwin

package cmd

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
)

var (
	xctraceStreamDataJSON        bool
	xctraceStreamDataInput       string
	xctraceStreamDataOutDir      string
	xctraceStreamDataProcessName string
	xctraceStreamDataMaxRows     int
	xctraceStreamDataTimeout     time.Duration
	xctraceStreamDataClang       string
	xctraceStreamDataMinFreeGiB  float64
	xctraceStreamDataMinMemFree  int
)

type xctraceStreamDataOutput struct {
	InputXML          string                   `json:"input_xml"`
	OutputDir         string                   `json:"output_dir"`
	ResourcePreflight profileResourcePreflight `json:"resource_preflight"`
	RowsRead          int                      `json:"rows_read"`
	RowsEncoded       int                      `json:"rows_encoded"`
	ProcessName       string                   `json:"process_name,omitempty"`
	Helper            profileCommandResult     `json:"helper"`
	StreamData        filePresence             `json:"streamData"`
	StreamDataStats   *streamDataProbeStats    `json:"streamData_stats,omitempty"`
	TimingUsable      bool                     `json:"timing_usable"`
	CounterUsable     bool                     `json:"counter_usable"`
}

var xctraceStreamDataCmd = &cobra.Command{
	Use:   "xctrace-streamdata --input metal-gpu-intervals.xml --process name --out-dir out.gpuprofiler_raw",
	Short: "Encode real xctrace Metal GPU intervals into streamData",
	Long: `Encode target-attributed xctrace Metal GPU interval rows into a
.gpuprofiler_raw/streamData archive using Apple's GTMutableShaderProfilerStreamData.

This command does not fabricate timings: it requires non-empty exported
metal-gpu-intervals rows for the requested process. It does not make hardware
counter claims.`,
	Args: cobra.NoArgs,
	RunE: runXctraceStreamData,
}

func init() {
	rootCmd.AddCommand(xctraceStreamDataCmd)
	xctraceStreamDataCmd.Flags().BoolVar(&xctraceStreamDataJSON, "json", false, "Output in JSON format")
	xctraceStreamDataCmd.Flags().StringVar(&xctraceStreamDataInput, "input", "", "Exported xctrace metal-gpu-intervals XML")
	xctraceStreamDataCmd.Flags().StringVar(&xctraceStreamDataOutDir, "out-dir", "", "Output .gpuprofiler_raw directory")
	xctraceStreamDataCmd.Flags().StringVar(&xctraceStreamDataProcessName, "process", "", "Process name substring required for rows")
	xctraceStreamDataCmd.Flags().IntVar(&xctraceStreamDataMaxRows, "max-rows", 20000, "Maximum interval rows to encode")
	xctraceStreamDataCmd.Flags().DurationVar(&xctraceStreamDataTimeout, "timeout", 20*time.Second, "Helper compile/run timeout")
	xctraceStreamDataCmd.Flags().StringVar(&xctraceStreamDataClang, "clang", "clang", "C compiler used for the streamData helper")
	xctraceStreamDataCmd.Flags().Float64Var(&xctraceStreamDataMinFreeGiB, "min-out-dir-free-gib", 24, "Minimum free GiB required on the output volume")
	xctraceStreamDataCmd.Flags().IntVar(&xctraceStreamDataMinMemFree, "min-memory-free-percent", 10, "Minimum memory_pressure free percentage required")
}

func runXctraceStreamData(cmd *cobra.Command, args []string) error {
	if xctraceStreamDataInput == "" || xctraceStreamDataOutDir == "" {
		return fmt.Errorf("--input and --out-dir are required")
	}
	if xctraceStreamDataProcessName == "" {
		return fmt.Errorf("--process is required to avoid encoding system-wide GPU rows; use '*' only for diagnostics")
	}
	if err := preflightXctraceStreamDataResources(xctraceStreamDataOutDir); err != nil {
		return err
	}
	rows, rowsRead, err := parseXctraceGPUIntervalsXML(xctraceStreamDataInput, xctraceStreamDataProcessName, xctraceStreamDataMaxRows)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return fmt.Errorf("no metal-gpu-intervals rows matched process %q (rows read: %d)", xctraceStreamDataProcessName, rowsRead)
	}
	if err := os.RemoveAll(xctraceStreamDataOutDir); err != nil {
		return err
	}
	if err := os.MkdirAll(xctraceStreamDataOutDir, 0o755); err != nil {
		return err
	}
	helper := encodeXctraceRowsWithHelper(rows, xctraceStreamDataOutDir)
	if helper.Signal == "" && helper.ExitCode == 0 && !helper.TimedOut {
		_ = writeXctraceIntervalRowsJSON(filepath.Join(xctraceStreamDataOutDir, "xctrace-interval-rows.json"), rows)
	}
	streamStats := summarizeEncodedStreamData(xctraceStreamDataOutDir)
	out := xctraceStreamDataOutput{
		InputXML:  xctraceStreamDataInput,
		OutputDir: xctraceStreamDataOutDir,
		ResourcePreflight: collectResourcePreflight(
			xctraceStreamDataOutDir,
			xctraceStreamDataMinFreeGiB,
			xctraceStreamDataMinMemFree,
			false,
		),
		RowsRead:        rowsRead,
		RowsEncoded:     len(rows),
		ProcessName:     xctraceStreamDataProcessName,
		Helper:          helper,
		StreamData:      presence(filepath.Join(xctraceStreamDataOutDir, "streamData")),
		StreamDataStats: streamStats,
		CounterUsable:   false,
	}
	if streamStats != nil {
		out.TimingUsable = streamStats.TimingUsable
	}
	if xctraceStreamDataJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	fmt.Printf("Rows encoded: %d\n", out.RowsEncoded)
	fmt.Printf("streamData:   %v (%d bytes)\n", out.StreamData.Present, out.StreamData.Bytes)
	fmt.Printf("Timing usable: %v\n", out.TimingUsable)
	if out.ResourcePreflight.CheckedPath != "" || out.ResourcePreflight.MemoryFreePercent != 0 {
		fmt.Printf("Preflight:    output=%s free=%.1fGiB memory_free=%d%%\n",
			out.ResourcePreflight.OutputDir,
			out.ResourcePreflight.FreeGiB,
			out.ResourcePreflight.MemoryFreePercent,
		)
	}
	if helper.Signal != "" || helper.ExitCode != 0 || helper.TimedOut {
		return fmt.Errorf("xctrace streamData helper failed")
	}
	if !out.TimingUsable {
		return fmt.Errorf("encoded streamData did not contain usable timing rows")
	}
	return nil
}

func preflightXctraceStreamDataResources(output string) error {
	if xctraceStreamDataMinFreeGiB > 0 {
		freeBytes, checkedPath, err := availableBytesForPath(output)
		if err != nil {
			return fmt.Errorf("resource preflight failed for output %s: %w", output, err)
		}
		freeGiB := float64(freeBytes) / (1024 * 1024 * 1024)
		if freeGiB < xctraceStreamDataMinFreeGiB {
			return fmt.Errorf("refusing to encode streamData: output volume at %s has %.1f GiB free, below %.1f GiB threshold", checkedPath, freeGiB, xctraceStreamDataMinFreeGiB)
		}
	}
	if xctraceStreamDataMinMemFree > 0 {
		freePercent, err := currentMemoryFreePercent()
		if err != nil {
			return fmt.Errorf("resource preflight failed reading memory pressure: %w", err)
		}
		if freePercent < xctraceStreamDataMinMemFree {
			return fmt.Errorf("refusing to encode streamData: memory_pressure free percentage is %d%%, below %d%% threshold", freePercent, xctraceStreamDataMinMemFree)
		}
	}
	return nil
}

func encodeXctraceRowsWithHelper(rows []xctraceIntervalRow, outDir string) profileCommandResult {
	helperDir := filepath.Join(filepath.Dir(outDir), "xctrace-streamdata-helper")
	_ = os.MkdirAll(helperDir, 0o755)
	source := filepath.Join(helperDir, "xctrace_streamdata_helper.m")
	binary := filepath.Join(helperDir, "xctrace_streamdata_helper")
	inputJSON := filepath.Join(helperDir, "rows.json")
	data, _ := json.Marshal(rows)
	if err := os.WriteFile(inputJSON, data, 0o644); err != nil {
		return profileCommandResult{Name: "xctrace_streamdata_write_rows", Signal: err.Error()}
	}
	if err := os.WriteFile(source, []byte(xctraceStreamDataHelperSource), 0o644); err != nil {
		return profileCommandResult{Name: "xctrace_streamdata_write_helper", Signal: err.Error()}
	}
	compile := runExternalCommand(
		"xctrace_streamdata_compile_helper",
		[]string{xctraceStreamDataClang, "-fobjc-arc", "-Wall", "-Wextra", "-O0", "-g", "-framework", "Foundation", "-o", binary, source},
		"",
		xctraceStreamDataTimeout,
	)
	if compile.Signal != "" || compile.ExitCode != 0 || compile.TimedOut {
		return compile
	}
	return runExternalCommand(
		"xctrace_streamdata_encode_helper",
		[]string{binary, inputJSON, outDir},
		outDir,
		xctraceStreamDataTimeout,
	)
}

//go:embed assets/xctrace_streamdata_helper.m
var xctraceStreamDataHelperSource string
