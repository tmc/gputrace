package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tmc/gputrace/internal/profilereplay"
)

var profileReplayCmd = newProfileReplayCommand(&profileReplayOptions{})

type profileReplayOptions struct {
	output string
	embed  bool
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

The output defaults to the input's name with a -perfdata suffix and holds the
profiler payload, which is what profiler, timing, timeline and pprof read. Add
--embed to copy the capture stream in as well, for the commands that need it:
kernels, buffer bindings, and grid and threadgroup sizes.

This does not produce derived counters. Utilization, limiter and occupancy
values are not available on this GPU generation; MTLReplayer's counter flags
reach a dispatch branch with no writer, and its raw-counter writer is preempted
by the profiler flags used here.

Examples:
  gputrace profile-replay run.gputrace                    # run-perfdata.gputrace
  gputrace profile-replay run.gputrace -o profiled.gputrace
  gputrace profile-replay run.gputrace --embed`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out, err := profilereplay.Profile(cmd.Context(), args[0], profilereplay.Options{
				Output: opts.output,
				Embed:  opts.embed,
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
	f.BoolVar(&opts.embed, "embed", false, "copy the capture stream in too, for a self-contained trace")
	return cmd
}

func init() {
	rootCmd.AddCommand(profileReplayCmd)
}
