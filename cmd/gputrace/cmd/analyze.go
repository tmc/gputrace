package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
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
		f, err := os.Open(args[0])
		if err != nil {
			return err
		}
		defer f.Close()
		cap, err := gpuevent.DecodeJSONL(f)
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
	if limit > len(rep.Kernels) {
		limit = len(rep.Kernels)
	}
	for _, k := range rep.Kernels[:limit] {
		fmt.Fprintf(out, "  %6.1f%%  %5dx  mean %-9s p95 %-9s [%s] %s\n",
			k.SharePct, k.Count, dur(k.MeanNS), dur(k.P95NS), k.Bound, shortKernel(k.Name))
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
