//go:build darwin

package cmd

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/profilerraw"
	"github.com/tmc/gputrace/internal/xcodebindings"
)

func runShadersXcodeCost(cmd *cobra.Command, tracePath string, opts *shadersOptions) error {
	if opts.all {
		return fmt.Errorf("--xcode-cost cannot be combined with --all")
	}
	if reexeced, err := ensureXcodeCostFramework(); err != nil || reexeced {
		return err
	}
	profilerDir := profilerraw.FindDirWithStreamData(tracePath)
	if profilerDir == "" {
		return fmt.Errorf("find profiler archive")
	}

	var (
		rows  []xcodebindings.ShaderCost
		total uint64
	)
	err := withDiscardedXcodeGPUTimeStderr(func() error {
		var err error
		rows, total, err = xcodebindings.ShaderCosts(filepath.Join(profilerDir, "streamData"))
		return err
	})
	if err != nil {
		return fmt.Errorf("compute Xcode shader costs: %w", err)
	}
	return writeShadersXcodeCost(cmd.OutOrStdout(), opts.format, rows, total)
}

const xcodeCostReexecEnv = "GPUTRACE_XCODE_COST_REEXEC"

// ensureXcodeCostFramework restarts gputrace once with the chosen framework
// fixed before Go package initialization. The generated binding loads during
// init, before Cobra can inspect GPUTRACE_XCODE_APP.
func ensureXcodeCostFramework() (bool, error) {
	framework := xcodebindings.FrameworkPath()
	if framework == "" {
		return false, fmt.Errorf("find GTShaderProfiler framework")
	}
	if os.Getenv(xcodebindings.FrameworkPathEnv) == framework {
		return false, nil
	}
	if os.Getenv(xcodeCostReexecEnv) != "" {
		return false, fmt.Errorf("GTShaderProfiler framework override did not resolve to %s", framework)
	}
	executable, err := os.Executable()
	if err != nil {
		return false, fmt.Errorf("locate gputrace executable: %w", err)
	}
	child := exec.Command(executable, os.Args[1:]...)
	child.Env = replaceEnv(os.Environ(), xcodebindings.FrameworkPathEnv, framework)
	child.Env = replaceEnv(child.Env, xcodeCostReexecEnv, "1")
	child.Stdin = os.Stdin
	child.Stdout = os.Stdout
	child.Stderr = os.Stderr
	if err := child.Run(); err != nil {
		return true, err
	}
	return true, nil
}

func replaceEnv(env []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if !strings.HasPrefix(entry, prefix) {
			out = append(out, entry)
		}
	}
	return append(out, prefix+value)
}

func writeShadersXcodeCost(w io.Writer, format string, rows []xcodebindings.ShaderCost, total uint64) error {
	switch format {
	case "text":
		fmt.Fprintln(w, "Cost        Name")
		for _, row := range rows {
			fmt.Fprintf(w, "%-12s  %s\n", fmt.Sprintf("%.2f%%", row.Cost), row.Name)
		}
		return nil
	case "json":
		return json.NewEncoder(w).Encode(struct {
			Total uint64                     `json:"total_gpu_time"`
			Basis string                     `json:"cost_basis"`
			Rows  []xcodebindings.ShaderCost `json:"shaders"`
		}{total, "pipeline compute time / GPU time", rows})
	case "csv":
		out := csv.NewWriter(w)
		if err := out.Write([]string{"cost", "name", "compute_time"}); err != nil {
			return err
		}
		for _, row := range rows {
			if err := out.Write([]string{
				strconv.FormatFloat(row.Cost, 'f', 2, 64),
				row.Name,
				strconv.FormatUint(row.ComputeTime, 10),
			}); err != nil {
				return err
			}
		}
		out.Flush()
		return out.Error()
	default:
		return invalidShadersFormatError(format)
	}
}
