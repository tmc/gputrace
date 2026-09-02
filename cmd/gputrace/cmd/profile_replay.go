package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tmc/gputrace/internal/profilereplay"
)

var profileReplayCmd = newProfileReplayCommand(&profileReplayOptions{})

type profileReplayOptions struct {
	output       string
	embed        bool
	profilerOnly bool
	wait         bool
}

func newProfileReplayCommand(opts *profileReplayOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profile-replay <trace.gputrace>",
		Short: "Replay a captured trace under the profiler to add performance data",
		Long: `Replay a captured .gputrace under Apple's MTLReplayer with the profiler
attached, writing a bundle that carries measured performance data.

A capture records what a Metal workload did and carries no timing. This replays
it on the GPU and collects streamData plus the Counters, Profiling and Timeline
shards. It is headless -- MTLReplayer is an agent process, so no window opens
and the frontmost application does not change. A small trace takes a few seconds.

The default output is a self-contained -perfdata.gputrace bundle containing the
original capture and resources plus the profiler payload. Xcode can open it,
and capture-dependent commands such as kernels, buffer bindings, and grid and
threadgroup sizes remain available.

Use --profiler-only only when the smaller raw payload is sufficient. It writes
a .gpuprofiler_raw directory for profiler, timing, timeline, and pprof. It is
not a .gputrace bundle and cannot be opened by Xcode.

Only one MTLReplayer profiling job runs at a time. By default, a concurrent
invocation fails with a busy error. Use --wait to queue behind the active job.
This prevents separate replay processes from overlapping; it does not change
the command-buffer or encoder concurrency recorded inside one capture.

This does not produce derived counters. Utilization, limiter and occupancy
values are not available on this GPU generation; MTLReplayer's counter flags
reach a dispatch branch with no writer, and its raw-counter writer is preempted
by the profiler flags used here.

Examples:
  gputrace profile-replay run.gputrace                    # run-perfdata.gputrace
  gputrace profile-replay run.gputrace -o profiled.gputrace
  gputrace profile-replay run.gputrace --profiler-only    # .gpuprofiler_raw
  gputrace profile-replay run.gputrace --wait             # queue serially`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.embed && opts.profilerOnly {
				return fmt.Errorf("--embed and --profiler-only are mutually exclusive")
			}
			out, err := profilereplay.Profile(cmd.Context(), args[0], profilereplay.Options{
				Output:       opts.output,
				ProfilerOnly: opts.profilerOnly,
				Wait:         opts.wait,
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", out)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVarP(&opts.output, "output", "o", "", "path of the bundle to write (default <trace>-perfdata.gputrace)")
	f.BoolVar(&opts.profilerOnly, "profiler-only", false, "write only a .gpuprofiler_raw payload, not an Xcode-openable trace")
	f.BoolVar(&opts.embed, "embed", false, "deprecated compatibility flag; self-contained output is now the default")
	_ = f.MarkDeprecated("embed", "self-contained output is now the default; omit --embed")
	f.BoolVar(&opts.wait, "wait", false, "wait for another replay instead of reporting that MTLReplayer is busy")
	return cmd
}

func init() {
	rootCmd.AddCommand(profileReplayCmd)
}
