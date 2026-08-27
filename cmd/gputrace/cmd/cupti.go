package cmd

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/cupticapture"
	"github.com/tmc/gputrace/internal/cuptitrace"
)

type cuptiOptions struct {
	output     string
	stats      bool
	spans      bool
	spansJSON  bool
	top        int
	perKernel  bool
	samples    string
}

var cuptiOpts = &cuptiOptions{}

var cuptiCmd = newCuptiCommand(cuptiOpts)

func newCuptiCommand(opts *cuptiOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cupti <events.jsonl>",
		Short: "Convert CUPTI activity captures to Perfetto traces (Linux/NVIDIA)",
		Long: `Convert CUPTI activity captures to Perfetto traces.

Reads newline-delimited JSON CUPTI activity records (kernels, memory copies)
as produced by a CUPTI activity tracer, summarizes them, and writes a native
Perfetto protobuf trace viewable at ui.perfetto.dev. Kernel symbols are
demangled with c++filt when available.

With --samples, a parallel newline-delimited NVML sample file (timestamp_ns,
power_mw, gpu_util_pct, mem_util_pct, temp_c, mem_used_bytes) is overlaid as
native counter tracks on the same normalized clock.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			capData, err := cuptitrace.ReadCapture(args[0])
			if err != nil {
				return err
			}
			events := capData.Events
			apis := capData.APIs
			if len(events) == 0 {
				return fmt.Errorf("no CUPTI events in %s", args[0])
			}
			if opts.spans {
				return printSpanTable(cmd, capData, opts.spansJSON)
			}
			if opts.stats {
				return printCuptiStats(cmd, events)
			}
			if opts.top > 0 {
				return printCuptiTop(cmd, events, opts.top)
			}

			samplesPath := cupticapture.ResolveSamples(args[0], opts.samples)
			samples, err := cuptitrace.ReadSamples(samplesPath)
			if err != nil {
				return fmt.Errorf("read NVML samples: %w", err)
			}
			trace, err := cuptitrace.Build(events, samples, apis, args[0], cuptitrace.Options{
				PerKernelTracks: opts.perKernel,
			})
			if err != nil {
				return err
			}

			outPath := opts.output
			if outPath == "" {
				outPath = stripExt(args[0]) + ".pftrace"
			}
			f, err := os.Create(outPath)
			if err != nil {
				return err
			}
			if err := cuptitrace.Write(trace, f); err != nil {
				f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Wrote %d events -> %s\n", len(trace.Events), outPath)
			fmt.Fprintf(cmd.ErrOrStderr(), "View with: open %s at https://ui.perfetto.dev\n", outPath)
			return nil
		},
	}
	cmd.Flags().StringVarP(&opts.output, "output", "o", opts.output, "Output Perfetto trace path")
	cmd.Flags().BoolVar(&opts.stats, "stats", opts.stats, "Print summary statistics instead of writing a trace")
	cmd.Flags().BoolVar(&opts.spans, "spans", opts.spans, "Print per-span setup/launch-latency/GPU/tail decomposition")
	cmd.Flags().BoolVar(&opts.spansJSON, "json", opts.spansJSON, "Output --spans table as JSON")
	cmd.Flags().IntVar(&opts.top, "top", opts.top, "Print the N slowest kernel launches instead of writing a trace")
	cmd.Flags().BoolVar(&opts.perKernel, "per-kernel-tracks", opts.perKernel, "Give each distinct kernel its own track")
	cmd.Flags().StringVar(&opts.samples, "samples", opts.samples, "Newline-delimited NVML sample file to overlay as counter tracks")
	return cmd
}

func printCuptiStats(cmd *cobra.Command, events []cuptitrace.Event) error {
	var kernels, memcpies int
	var totalNS uint64
	kernelTime := map[string]uint64{}
	counts := map[string]int{}
	for _, e := range events {
		switch e.Kind {
		case "kernel":
			kernels++
			d := e.EndNS - e.StartNS
			totalNS += d
			name := cuptitrace.Demangle(e.Name)
			kernelTime[name] += d
			counts[name]++
		case "memcpy", "memset":
			memcpies++
		}
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "CUPTI capture: %d kernels, %d memory transfers\n", kernels, memcpies)
	fmt.Fprintf(out, "Total kernel time: %.2f ms across %d distinct kernels\n\n", float64(totalNS)/1e6, len(kernelTime))

	type row struct {
		name  string
		count int
		ns    uint64
	}
	rows := make([]row, 0, len(kernelTime))
	for name, ns := range kernelTime {
		rows = append(rows, row{name, counts[name], ns})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ns > rows[j].ns })
	fmt.Fprintln(out, "Top kernels by total GPU time:")
	limit := 10
	if limit > len(rows) {
		limit = len(rows)
	}
	for _, r := range rows[:limit] {
		fmt.Fprintf(out, "  %8.2f ms  %5dx  %s\n", float64(r.ns)/1e6, r.count, r.name)
	}
	return nil
}

func printCuptiTop(cmd *cobra.Command, events []cuptitrace.Event, n int) error {
	sorted := make([]cuptitrace.Event, len(events))
	copy(sorted, events)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].EndNS-sorted[i].StartNS > sorted[j].EndNS-sorted[j].StartNS
	})
	out := cmd.OutOrStdout()
	if n > len(sorted) {
		n = len(sorted)
	}
	for _, e := range sorted[:n] {
		fmt.Fprintf(out, "%9.3f us  %-48s grid=%-12s block=%-10s regs=%d\n",
			float64(e.EndNS-e.StartNS)/1e3,
			cuptitrace.ShortName(cuptitrace.Demangle(e.Name)),
			e.Grid, e.Block, e.Registers)
	}
	return nil
}

func stripExt(path string) string {
	if i := strings.LastIndexByte(path, '.'); i > strings.LastIndexByte(path, '/') {
		return path[:i]
	}
	return path
}
