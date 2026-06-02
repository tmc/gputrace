//go:build darwin

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
)

var (
	xctraceRecordJSON         bool
	xctraceRecordOutput       string
	xctraceRecordTemplate     string
	xctraceRecordInstruments  []string
	xctraceRecordTimeLimit    string
	xctraceRecordAttach       string
	xctraceRecordAllProcesses bool
	xctraceRecordTimeout      time.Duration
	xctraceRecordMinFreeGiB   float64
	xctraceRecordMinMemFree   int
)

type xctraceRecordOutputDoc struct {
	Cmd               []string                 `json:"cmd"`
	Output            filePresence             `json:"output"`
	ResourcePreflight profileResourcePreflight `json:"resource_preflight"`
	ElapsedMillis     int64                    `json:"elapsed_millis"`
	TimedOut          bool                     `json:"timed_out"`
	ExitCode          int                      `json:"exit_code,omitempty"`
	Signal            string                   `json:"signal,omitempty"`
	StdoutBytes       int                      `json:"stdout_bytes"`
	StderrBytes       int                      `json:"stderr_bytes"`
	StdoutPreview     string                   `json:"stdout_preview,omitempty"`
	StderrPreview     string                   `json:"stderr_preview,omitempty"`
}

var xctraceRecordCmd = &cobra.Command{
	Use:   "xctrace-record --output out.trace [-- command args...]",
	Short: "Record a guarded headless xctrace Metal System Trace",
	Long: `Record a headless Instruments trace with xctrace, defaulting to the
Metal System Trace template.

The command checks output-volume free space and memory pressure before launch.
Use --attach, --all-processes, or pass a command after -- to launch a target.`,
	Args: cobra.ArbitraryArgs,
	RunE: runXctraceRecord,
}

func init() {
	rootCmd.AddCommand(xctraceRecordCmd)
	xctraceRecordCmd.Flags().BoolVar(&xctraceRecordJSON, "json", false, "Output in JSON format")
	xctraceRecordCmd.Flags().StringVar(&xctraceRecordOutput, "output", "", "Output .trace path or directory")
	xctraceRecordCmd.Flags().StringVar(&xctraceRecordTemplate, "template", "Metal System Trace", "xctrace template name or path")
	xctraceRecordCmd.Flags().StringArrayVar(&xctraceRecordInstruments, "instrument", nil, "Additional xctrace instrument name; may be repeated")
	xctraceRecordCmd.Flags().StringVar(&xctraceRecordTimeLimit, "time-limit", "10s", "xctrace recording time limit")
	xctraceRecordCmd.Flags().StringVar(&xctraceRecordAttach, "attach", "", "Attach to process name or pid")
	xctraceRecordCmd.Flags().BoolVar(&xctraceRecordAllProcesses, "all-processes", false, "Record all processes")
	xctraceRecordCmd.Flags().DurationVar(&xctraceRecordTimeout, "timeout", 30*time.Second, "Outer timeout for xctrace")
	xctraceRecordCmd.Flags().Float64Var(&xctraceRecordMinFreeGiB, "min-out-dir-free-gib", 24, "Minimum free GiB required on the output volume")
	xctraceRecordCmd.Flags().IntVar(&xctraceRecordMinMemFree, "min-memory-free-percent", 10, "Minimum memory_pressure free percentage required")
}

func runXctraceRecord(cmd *cobra.Command, args []string) error {
	if xctraceRecordOutput == "" {
		return fmt.Errorf("--output is required")
	}
	targetModes := 0
	if len(args) > 0 {
		targetModes++
	}
	if xctraceRecordAttach != "" {
		targetModes++
	}
	if xctraceRecordAllProcesses {
		targetModes++
	}
	if targetModes != 1 {
		return fmt.Errorf("choose exactly one target mode: command after --, --attach, or --all-processes")
	}
	if err := preflightXctraceRecordResources(xctraceRecordOutput); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(xctraceRecordOutput), 0o755); err != nil {
		return err
	}

	argv := buildXctraceRecordArgv(args)
	result := runXctraceRecordCommand(argv)
	if xctraceRecordJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}
	fmt.Printf("xctrace: elapsed=%dms timed_out=%v exit=%d output=%s bytes=%d\n",
		result.ElapsedMillis,
		result.TimedOut,
		result.ExitCode,
		result.Output.Path,
		result.Output.Bytes,
	)
	if result.ResourcePreflight.CheckedPath != "" || result.ResourcePreflight.MemoryFreePercent != 0 {
		fmt.Printf("preflight: output=%s free=%.1fGiB memory_free=%d%%\n",
			result.ResourcePreflight.OutputDir,
			result.ResourcePreflight.FreeGiB,
			result.ResourcePreflight.MemoryFreePercent,
		)
	}
	if result.StderrPreview != "" {
		fmt.Println(result.StderrPreview)
	}
	if result.ExitCode != 0 || result.Signal != "" || result.TimedOut {
		return fmt.Errorf("xctrace record failed")
	}
	return nil
}

func buildXctraceRecordArgv(args []string) []string {
	argv := []string{
		"xcrun", "xctrace", "record",
		"--template", xctraceRecordTemplate,
	}
	for _, instrument := range xctraceRecordInstruments {
		if instrument == "" {
			continue
		}
		argv = append(argv, "--instrument", instrument)
	}
	argv = append(argv,
		"--time-limit", xctraceRecordTimeLimit,
		"--output", xctraceRecordOutput,
		"--no-prompt",
	)
	if xctraceRecordAllProcesses {
		argv = append(argv, "--all-processes")
	} else if xctraceRecordAttach != "" {
		argv = append(argv, "--attach", xctraceRecordAttach)
	} else {
		argv = append(argv, "--launch", "--")
		argv = append(argv, args...)
	}
	return argv
}

func preflightXctraceRecordResources(output string) error {
	if xctraceRecordMinFreeGiB > 0 {
		freeBytes, checkedPath, err := availableBytesForPath(output)
		if err != nil {
			return fmt.Errorf("resource preflight failed for output %s: %w", output, err)
		}
		freeGiB := float64(freeBytes) / (1024 * 1024 * 1024)
		if freeGiB < xctraceRecordMinFreeGiB {
			return fmt.Errorf("refusing to launch xctrace: output volume at %s has %.1f GiB free, below %.1f GiB threshold", checkedPath, freeGiB, xctraceRecordMinFreeGiB)
		}
	}
	if xctraceRecordMinMemFree > 0 {
		freePercent, err := currentMemoryFreePercent()
		if err != nil {
			return fmt.Errorf("resource preflight failed reading memory pressure: %w", err)
		}
		if freePercent < xctraceRecordMinMemFree {
			return fmt.Errorf("refusing to launch xctrace: memory_pressure free percentage is %d%%, below %d%% threshold", freePercent, xctraceRecordMinMemFree)
		}
	}
	return nil
}

func runXctraceRecordCommand(argv []string) xctraceRecordOutputDoc {
	ctx, cancel := contextWithTimeout(xctraceRecordTimeout)
	defer cancel()
	start := time.Now()
	command := exec.CommandContext(ctx, argv[0], argv[1:]...)
	stdout, stderr, err := runCommandCapture(ctx, command)
	result := xctraceRecordOutputDoc{
		Cmd:               append([]string{}, argv...),
		Output:            presence(xctraceRecordOutput),
		ResourcePreflight: collectResourcePreflight(xctraceRecordOutput, xctraceRecordMinFreeGiB, xctraceRecordMinMemFree, false),
		ElapsedMillis:     time.Since(start).Milliseconds(),
		TimedOut:          ctx.Err() != nil,
		StdoutBytes:       len(stdout),
		StderrBytes:       len(stderr),
		StdoutPreview:     previewOutput(stdout),
		StderrPreview:     previewOutput(stderr),
	}
	if err == nil {
		return result
	}
	result.Signal = err.Error()
	if exitCode, ok := commandExitCode(err); ok {
		result.Signal = ""
		result.ExitCode = exitCode
	}
	return result
}

func contextWithTimeout(timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), timeout)
}

func commandExitCode(err error) (int, bool) {
	if exitErr, ok := err.(*exec.ExitError); ok {
		if code := exitErr.ExitCode(); code >= 0 {
			return code, true
		}
	}
	return 0, false
}
