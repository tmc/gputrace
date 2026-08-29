package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/cupticapture"
	"github.com/tmc/gputrace/internal/gpuevent"
	"github.com/tmc/gputrace/internal/ncu"
)

type ncuOptions struct {
	top         int
	launchCount int
	metrics     string
	sudo        bool
	dryRun      bool
	json        bool
	merge       bool
}

var ncuCmd = newNCUCommand(&ncuOptions{top: 3, launchCount: 20, merge: true})

func newNCUCommand(opts *ncuOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ncu <capture.gpucapture> [-- command [args...]]",
		Short: "Escalate a capture's hottest kernels to Nsight Compute counters",
		Long: `Re-run a captured workload under Nsight Compute, profiling only the
kernels the capture says matter.

ncu replays each kernel many times with different counters armed and
serializes execution, so profiling a whole run is prohibitive. This ranks
the capture's kernels by total GPU time, profiles the top few, and merges
the counter results back into the bundle as ncu.json.

The workload comes from the bundle's meta.json, so the escalation
profiles the run the timeline described. Pass a command after -- to
override it.

GPU performance counters are restricted to administrators on many
drivers; use --sudo when 'gputrace doctor' reports them refused.

Examples:
  gputrace ncu run.gpucapture
  gputrace ncu run.gpucapture --top 5 --launch-count 10
  gputrace ncu run.gpucapture --dry-run
  gputrace ncu run.gpucapture --sudo`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runNCU(cmd, args, opts)
		},
	}
	cmd.Flags().IntVar(&opts.top, "top", opts.top, "Kernels to profile, ranked by total GPU time")
	cmd.Flags().IntVar(&opts.launchCount, "launch-count", opts.launchCount, "Launches replayed per kernel (ncu serializes; keep this small)")
	cmd.Flags().StringVar(&opts.metrics, "metrics", "", "Comma-separated ncu metrics (default: a set that exists on current parts)")
	cmd.Flags().BoolVar(&opts.sudo, "sudo", opts.sudo, "Run ncu through sudo, for drivers restricting counters to admins")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", opts.dryRun, "Print the ncu command that would run and exit")
	cmd.Flags().BoolVar(&opts.json, "json", opts.json, "Output the merged counters as JSON")
	cmd.Flags().BoolVar(&opts.merge, "merge", opts.merge, "Write the counters into the bundle as ncu.json")
	return cmd
}

func runNCU(cmd *cobra.Command, args []string, opts *ncuOptions) error {
	capturePath := args[0]
	override := args[1:]

	rep, err := loadCaptureReport(capturePath)
	if err != nil {
		return err
	}
	if len(rep.Kernels) == 0 {
		return fmt.Errorf("ncu: %s has no kernels to escalate", capturePath)
	}
	top := opts.top
	if top <= 0 || top > len(rep.Kernels) {
		top = len(rep.Kernels)
	}
	names := make([]string, 0, top)
	for _, k := range rep.Kernels[:top] {
		names = append(names, k.Name)
	}

	workload := override
	if len(workload) == 0 {
		workload, err = captureWorkload(capturePath)
		if err != nil {
			return err
		}
	}

	ncuOpts := ncu.Options{
		Kernels:     names,
		LaunchCount: opts.launchCount,
		Sudo:        opts.sudo,
	}
	if opts.metrics != "" {
		ncuOpts.Metrics = strings.Split(opts.metrics, ",")
	}
	command, err := ncu.Command(ncuOpts, workload)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Escalating %d of %d kernels by GPU time:\n", top, len(rep.Kernels))
	for _, k := range rep.Kernels[:top] {
		fmt.Fprintf(out, "  %5.1f%%  %5dx  mean %-9s %s\n", k.SharePct, k.Count, dur(k.MeanNS), shortKernel(k.Name))
	}
	fmt.Fprintf(out, "\n%s\n\n", strings.Join(command.Args, " "))
	if opts.dryRun {
		return nil
	}

	// ncu writes its CSV table and its diagnostics to the same streams,
	// so both are captured and the workload's own output is forwarded.
	var buf bytes.Buffer
	command.Stdout = io.MultiWriter(&buf, cmd.ErrOrStderr())
	command.Stderr = io.MultiWriter(&buf, cmd.ErrOrStderr())
	command.Stdin = cmd.InOrStdin()
	runErr := command.Run()

	if ncu.PermissionDenied(buf.String()) {
		return fmt.Errorf("ncu: the driver refused GPU performance counters for this user (ERR_NVGPUCTRPERM)\n" +
			"  re-run with --sudo, or lift the restriction permanently:\n" +
			"    echo 'options nvidia NVreg_RestrictProfilingToAdminUsers=0' | sudo tee /etc/modprobe.d/nvidia-profiling.conf\n" +
			"    sudo update-initramfs -u && sudo reboot")
	}
	result, parseErr := ncu.ParseCSV(&buf)
	if parseErr != nil {
		if runErr != nil {
			return fmt.Errorf("ncu: %v (%w)", runErr, parseErr)
		}
		return parseErr
	}
	result.Command = command.Args

	if opts.merge && cupticapture.IsBundle(capturePath) {
		path := filepath.Join(capturePath, "ncu.json")
		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
			return fmt.Errorf("ncu: merge into bundle: %w", err)
		}
		fmt.Fprintf(out, "\nmerged counters -> %s\n", path)
	}
	if opts.json {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}
	writeNCUResult(out, result, rep)
	return runErr
}

// captureWorkload recovers the command a bundle recorded.
func captureWorkload(path string) ([]string, error) {
	if !cupticapture.IsBundle(path) {
		return nil, fmt.Errorf("ncu: %s is not a capture bundle; pass the workload after -- ", path)
	}
	meta, err := cupticapture.ReadMeta(path)
	if err != nil {
		return nil, fmt.Errorf("ncu: read bundle metadata: %w", err)
	}
	if len(meta.Command) == 0 {
		return nil, fmt.Errorf("ncu: %s records no command; pass the workload after -- ", path)
	}
	return meta.Command, nil
}

// writeNCUResult prints measured counters beside the timeline's own
// numbers, so a reader can see the replay agreeing (or not) with what the
// capture measured without replaying anything.
func writeNCUResult(out io.Writer, result *ncu.Result, rep *gpuevent.Report) {
	timeline := map[string]gpuevent.KernelStats{}
	for _, k := range rep.Kernels {
		timeline[k.Name] = k
	}
	fmt.Fprintf(out, "\nMeasured counters (%d kernel%s profiled):\n", len(result.Kernels), plural(len(result.Kernels)))
	for _, kc := range result.Kernels {
		fmt.Fprintf(out, "\n  %s  (%d launches replayed)\n", shortKernel(kc.Kernel), kc.Launches)
		if k, ok := timeline[kc.Kernel]; ok {
			fmt.Fprintf(out, "    capture said: %dx, mean %s, theoretical occupancy %.0f%%\n",
				k.Count, dur(k.MeanNS), k.TheoreticalOccupancyPct)
		}
		for _, m := range kc.Metrics {
			fmt.Fprintf(out, "    %-56s median %10.2f %s\n", m.Metric, m.Median, m.Unit)
		}
		if len(kc.Unsupported) > 0 {
			fmt.Fprintf(out, "    unsupported on this part: %s\n", strings.Join(kc.Unsupported, ", "))
		}
	}
}

func init() {
	rootCmd.AddCommand(ncuCmd)
}
