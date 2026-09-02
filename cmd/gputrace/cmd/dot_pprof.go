package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/cudagraphdot"
	"github.com/tmc/gputrace/internal/cuptiprofile"
	"github.com/tmc/gputrace/internal/cuptitrace"
)

type dotPprofOptions struct {
	output string
}

var dotPprofOpts = &dotPprofOptions{}

var dotPprofCmd = newDotPprofCommand(dotPprofOpts)

func newDotPprofCommand(opts *dotPprofOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dot-pprof <dot-dir-or-file>...",
		Short: "Convert CUDA-graph DOT dumps to a pprof structure profile",
		Long: `Convert CUDA-graph DOT dumps to a pprof structure profile.

Reads the dumps MLX writes when MLX_SAVE_CUDA_GRAPHS_DOT_FILE is set (one
file per graph commit) and emits a profile of what the graphs commit, with
no timing attached:

  sample_type: kernel_count (count), graph_commits (count)
  stack:       graph_<n> -> [child graph...] -> <kernel name>

The dumps are written by the same libmlx code whichever language binding
drove it, so diffing two of them is a same-instrument comparison: no
cross-stack calibration, no GPU hold, and no run-to-run variance to average
away. One dump per side is the whole measurement, unlike timing.

  gputrace dot-pprof py-dots -o py.pb.gz
  gputrace dot-pprof go-dots -o go.pb.gz
  go tool pprof -top -diff_base=py.pb.gz go.pb.gz

That last line is a signed multiset difference over kernel signatures: it
names which kernels one side commits and the other does not, which an
aggregate count comparison cannot. Diff at -top granularity — graph ids are
assigned per run and do not match across two runs, so stack-level diffs
compare labels, not structure.

Counting rule: a node drawn as a rectangle whose label names another graph
is a child-graph node, not a kernel. It is flattened, and its kernels are
counted once per instantiation.

Join the structure to measured cost with:

  gputrace pprof <bundle>.gpucapture --dot <dot-dir> -o joined.pb.gz`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDotPprof(cmd, args, opts)
		},
	}
	cmd.Flags().StringVarP(&opts.output, "output", "o", opts.output, "Output pprof file path (default: <input>.pprof)")
	return cmd
}

func runDotPprof(cmd *cobra.Command, args []string, opts *dotPprofOptions) error {
	var files []*cudagraphdot.File
	for _, arg := range args {
		parsed, err := loadGraphDumps(arg)
		if err != nil {
			return err
		}
		files = append(files, parsed...)
	}

	var nodes []cuptiprofile.StructureNode
	var commits []string
	mangled := map[string]string{}
	for _, f := range files {
		for _, k := range f.Kernels() {
			name := cuptitrace.Demangle(k.Symbol)
			mangled[name] = k.Symbol
			nodes = append(nodes, cuptiprofile.StructureNode{GraphPath: k.Path, Symbol: name})
		}
		commits = append(commits, f.Roots...)
	}

	prof, stats, err := cuptiprofile.BuildStructure(nodes, commits)
	if err != nil {
		return fmt.Errorf("%s: %w", strings.Join(args, " "), err)
	}
	cuptiprofile.SetSystemNames(prof, mangled)

	outPath := opts.output
	if outPath == "" {
		outPath = stripExt(args[0]) + ".pprof"
	}
	if err := writeProfile(prof, outPath); err != nil {
		return err
	}

	status := pprofStatusWriter(outPath)
	fmt.Fprintf(status, "Wrote %d kernel nodes from %d dumps (%d graph commits) -> %s\n",
		stats.StructureNodes, len(files), stats.Commits, outPath)
	fmt.Fprintf(cmd.ErrOrStderr(), "View with: go tool pprof -top %s\n", outPath)
	return nil
}

func init() {
	rootCmd.AddCommand(dotPprofCmd)
}
