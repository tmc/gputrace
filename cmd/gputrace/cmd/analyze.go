package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/cupticapture"
	"github.com/tmc/gputrace/internal/cuptitrace"
	"github.com/tmc/gputrace/internal/gpuevent"
	"github.com/tmc/gputrace/internal/optimize"
)

var analyzeOpts = struct {
	json     bool
	suggest  bool
	samples  string
	demangle bool
	limit    int
}{demangle: true, limit: 10}

var analyzeCmd = &cobra.Command{
	Use:   "analyze <events.jsonl>",
	Short: "Report per-kernel metrics and optimization findings (Linux/NVIDIA)",
	Long: `Analyze a CUPTI activity capture and report optimization findings.

Aggregates kernel launches into per-kernel statistics (count, total,
mean/p50/p95 duration, share of GPU time, launch geometry) and classifies
each kernel as compute-, memory-, or latency-bound from launch shape.
Findings pair measured evidence with a concrete hypothesis an agent or
developer can act on. Classifications are heuristics from launch geometry,
not hardware-counter measurements.

--samples overlays concurrent NVML samples so findings can cite device
state during the capture window.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		f, closers, err := cupticapture.OpenEvents(args[0])
		if err != nil {
			return err
		}
		defer closers()
		samplesPath := cupticapture.ResolveSamples(args[0], analyzeOpts.samples)
		cap, err := readCapture(f, samplesPath)
		if err != nil {
			return err
		}
		if len(cap.Events) == 0 {
			return fmt.Errorf("no events in %s", args[0])
		}
		// Decode names for readable reports when demangling is available;
		// raw symbols always stay in RawSymbol for evidence.
		if analyzeOpts.demangle {
			for i := range cap.Events {
				if cap.Events[i].Kind == gpuevent.KindKernel && cap.Events[i].Name == "" {
					cap.Events[i].Name = cuptitrace.Demangle(cap.Events[i].RawSymbol)
				}
			}
		}
		rep := gpuevent.Analyze(cap.Events, cap.Samples)
		// Launch-overhead analysis needs API records; when present, attach
		// the host-vs-device split to the report.
		if len(cap.APIs) > 0 {
			rep.LaunchOverhead = gpuevent.LaunchOverheadAnalysis(cap)
		}
		if analyzeOpts.json {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(rep)
		}
		if analyzeOpts.suggest {
			entries := optimize.Suggest(rep.Findings)
			out := cmd.OutOrStdout()
			if len(entries) == 0 {
				fmt.Fprintln(out, "No findings to act on.")
				return nil
			}
			fmt.Fprintln(out, "Suggested actions (apply ONE, then re-measure):")
			fmt.Fprint(out, optimize.RenderSuggestions(entries))
			fmt.Fprintln(out, "\nLoop: gputrace optimize run -o base.json -- <workload>")
			fmt.Fprintln(out, "      ... apply one action ...")
			fmt.Fprintln(out, "      gputrace optimize run -o variant.json -- <workload>")
			fmt.Fprintln(out, "      gputrace optimize compare base.json variant.json")
			return nil
		}
		return printAnalysis(cmd, rep)
	},
}

func printAnalysis(cmd *cobra.Command, rep *gpuevent.Report) error {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Capture: %d launches, %d kernels, %.2f ms total kernel time\n",
		rep.KernelLaunches, len(rep.Kernels), float64(rep.TotalKernelNS)/1e6)
	if rep.MemcpyCount > 0 {
		fmt.Fprintf(out, "Transfers: %d memcpys (%.2f ms), %d memsets\n",
			rep.MemcpyCount, float64(rep.MemcpyNS)/1e6, rep.MemsetCount)
	}

	fmt.Fprintln(out, "\nKernels by GPU time:")
	limit := analyzeOpts.limit
	if limit == 0 || limit > len(rep.Kernels) {
		limit = len(rep.Kernels)
	}
	for _, k := range rep.Kernels[:limit] {
		occ := ""
		if k.TheoreticalOccupancyPct > 0 {
			occ = fmt.Sprintf("  occ ~%.0f%% (%s)", k.TheoreticalOccupancyPct, k.OccupancyLimiter)
		}
		fmt.Fprintf(out, "  %6.1f%%  %5dx  mean %-9s p95 %-9s [%s] %s%s\n",
			k.SharePct, k.Count, dur(k.MeanNS), dur(k.P95NS), k.Bound, shortKernel(k.Name), occ)
	}

	printUtilization(out, rep.Utilization)
	printGraphs(out, rep.Graphs)
	printLaunchLatency(out, rep.LaunchLatency)

	if rep.LaunchOverhead != nil && rep.LaunchOverhead.Joins > 0 {
		lo := rep.LaunchOverhead
		fmt.Fprintf(out, "\nLaunch overhead (host API vs GPU execution, %d joined launches):\n", lo.Joins)
		fmt.Fprintf(out, "  host cost/launch:  mean %s, p50 %s, p95 %s\n",
			dur(lo.MeanHostCostNS), dur(lo.P50HostCostNS), dur(lo.P95HostCostNS))
		if lo.TotalGPUNS > 0 {
			ratio := float64(lo.TotalHostNS) / float64(lo.TotalGPUNS) * 100
			fmt.Fprintf(out, "  host/GPU ratio:    %.1f%% (%s host vs %s GPU)\n",
				ratio, dur(lo.TotalHostNS), dur(lo.TotalGPUNS))
			if ratio >= 25 {
				fmt.Fprintf(out, "  => launch-bound: host submission is a material share of GPU time;\n     reduce launch count or batch work before tuning kernels\n")
			}
		}
	}

	if len(rep.Findings) == 0 {
		fmt.Fprintln(out, "\nNo optimization findings; the capture has no dominant pattern.")
		return nil
	}
	fmt.Fprintf(out, "\nFindings:\n")
	for _, f := range rep.Findings {
		fmt.Fprintf(out, "\n[%s] %s: %s\n", strings.ToUpper(string(f.Severity)), f.Kind, shortKernel(f.Subject))
		for _, ev := range f.Evidence {
			fmt.Fprintf(out, "  evidence:    %s\n", ev)
		}
		wrapped := wrapWords(f.Hypothesis, 76)
		for i, line := range wrapped {
			if i == 0 {
				fmt.Fprintf(out, "  hypothesis:  %s\n", line)
			} else {
				fmt.Fprintf(out, "               %s\n", line)
			}
		}
	}
	return nil
}

// printUtilization reports the busy/idle budget: how much of the capture's
// wall span the device spent executing anything.
func printUtilization(out io.Writer, u gpuevent.Utilization) {
	if u.WallSpanNS == 0 {
		return
	}
	fmt.Fprintf(out, "\nGPU budget: %s busy of %s wall span (%.1f%% occupancy), %s idle across %d gaps\n",
		dur(u.BusyNS), dur(u.WallSpanNS), u.OccupancyPct, dur(u.IdleNS), u.GapCount)
	if u.Concurrency > 1.05 {
		fmt.Fprintf(out, "  stream overlap:    %.2fx (summed activity time over busy wall time)\n", u.Concurrency)
	}
	if u.GapCount > 0 {
		fmt.Fprintf(out, "  idle gaps:         mean %s, p95 %s, max %s\n",
			dur(u.MeanGapNS), dur(u.P95GapNS), dur(u.MaxGapNS))
		for _, g := range u.TopGaps {
			fmt.Fprintf(out, "    %-9s after %s -> %s\n", dur(g.DurationNS),
				shortKernel(orUnknown(g.AfterName)), shortKernel(orUnknown(g.BeforeName)))
		}
	}
}

// printGraphs reports how much of the capture ran through CUDA graphs and
// which node of each graph owns its time.
func printGraphs(out io.Writer, g *gpuevent.GraphAnalysis) {
	if g == nil || len(g.Graphs) == 0 {
		return
	}
	fmt.Fprintf(out, "\nCUDA graphs: %d graph%s, %.1f%% of kernel time (%d graph kernels vs %d direct launches)\n",
		len(g.Graphs), plural(len(g.Graphs)), g.GraphSharePct, g.GraphKernels, g.DirectKernels)
	for _, gr := range g.Graphs {
		fmt.Fprintf(out, "  graph %d: %d launches x %d nodes, %s (%.1f%% of kernel time)\n",
			gr.GraphID, gr.Launches, gr.Nodes, dur(gr.TotalNS), gr.SharePct)
		for _, n := range gr.TopNodes {
			fmt.Fprintf(out, "    node #%-3d %5.1f%% of graph  %5dx  mean %-9s %s\n",
				n.Index, n.SharePct, n.Count, dur(n.MeanNS), shortKernel(n.Name))
		}
	}
}

// printLaunchLatency reports the queue -> submit -> start decomposition, or
// says why the capture cannot support one. It never prints a figure the
// analysis declared unusable.
func printLaunchLatency(out io.Writer, l *gpuevent.LaunchLatency) {
	if l == nil || l.Kernels == 0 {
		return
	}
	if !l.Usable {
		fmt.Fprintf(out, "\nLaunch latency: unavailable — %s\n", l.Reason)
		return
	}
	fmt.Fprintf(out, "\nLaunch latency (queue -> submit -> start, %d of %d launches timed):\n", l.Timed, l.Kernels)
	fmt.Fprintf(out, "  queued -> submitted: mean %s, p50 %s, p95 %s\n",
		dur(l.QueueToSubmitNS.MeanNS), dur(l.QueueToSubmitNS.P50NS), dur(l.QueueToSubmitNS.P95NS))
	fmt.Fprintf(out, "  submitted -> start:  mean %s, p50 %s, p95 %s\n",
		dur(l.SubmitToStartNS.MeanNS), dur(l.SubmitToStartNS.P50NS), dur(l.SubmitToStartNS.P95NS))
	fmt.Fprintf(out, "  queued -> start:     mean %s, p50 %s, p95 %s\n",
		dur(l.QueueToStartNS.MeanNS), dur(l.QueueToStartNS.P50NS), dur(l.QueueToStartNS.P95NS))
	if l.Reason != "" {
		fmt.Fprintf(out, "  coverage:          %s\n", l.Reason)
	}
}

func orUnknown(s string) string {
	if s == "" {
		return "(unknown)"
	}
	return s
}

func shortKernel(name string) string {
	const maxLen = 88
	if len(name) <= maxLen {
		return name
	}
	return name[:maxLen-3] + "..."
}

func wrapWords(s string, width int) []string {
	var lines []string
	var cur strings.Builder
	for _, word := range strings.Fields(s) {
		if cur.Len() > 0 && cur.Len()+1+len(word) > width {
			lines = append(lines, cur.String())
			cur.Reset()
		}
		if cur.Len() > 0 {
			cur.WriteByte(' ')
		}
		cur.WriteString(word)
	}
	if cur.Len() > 0 {
		lines = append(lines, cur.String())
	}
	return lines
}

// dur formats nanoseconds at human scale; shared with other command files.
func dur(ns uint64) string {
	switch {
	case ns >= 1e6:
		return fmt.Sprintf("%.2fms", float64(ns)/1e6)
	case ns >= 1e3:
		return fmt.Sprintf("%.1fus", float64(ns)/1e3)
	default:
		return fmt.Sprintf("%dns", ns)
	}
}

func init() {
	analyzeCmd.Flags().BoolVar(&analyzeOpts.json, "json", false, "Output machine-readable JSON report")
	analyzeCmd.Flags().BoolVar(&analyzeOpts.suggest, "suggest", false, "Print playbook actions for each finding instead of the report")
	analyzeCmd.Flags().StringVar(&analyzeOpts.samples, "samples", "", "Concurrent NVML sample file to join")
	analyzeCmd.Flags().BoolVar(&analyzeOpts.demangle, "demangle", true, "Demangle kernel symbols with c++filt when available")
	analyzeCmd.Flags().IntVar(&analyzeOpts.limit, "limit", 10, "Kernels listed in the text report")
	rootCmd.AddCommand(analyzeCmd)
}
