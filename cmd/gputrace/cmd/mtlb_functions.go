package cmd

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/mtlb"
	"github.com/tmc/gputrace/internal/trace"
)

var (
	filterPattern string
	showAll       bool
	usedOnly      bool
	withUsage     bool
)

var mtlbFunctionsCmd = &cobra.Command{
	Use:   "functions [trace]",
	Short: "List all functions in MTLB files",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		tracePath := args[0]

		files, err := mtlb.FindMTLBFiles(tracePath)
		if err != nil {
			return err
		}

		var allFuncs []string
		funcSizes := make(map[string]int64)

		for _, f := range files {
			data, err := os.ReadFile(f.Path)
			if err != nil {
				continue
			}

			lib, err := mtlb.ParseMTLB(data)
			if err != nil {
				continue
			}

			funcs, err := lib.ListFunctions()
			if err != nil {
				continue
			}
			allFuncs = append(allFuncs, funcs...)

			// Estimate size if possible (not implemented in parser yet, so 0)
			for _, fn := range funcs {
				funcSizes[fn] = 0 // Placeholder
			}
		}

		// Collect usage data if requested
		usageCounts := make(map[string]int)
		if usedOnly || withUsage {
			tr, err := trace.Open(tracePath)
			if err != nil {
				return fmt.Errorf("open trace: %w", err)
			}

			pipelineMap := tr.BuildPipelineFunctionMap()
			records, err := tr.ParseMTSPRecords()
			if err != nil {
				// Fallback to KernelNames if parsing fails
				for _, name := range tr.KernelNames {
					usageCounts[name] = 1 // Just indicate presence
				}
			} else {
				for _, rec := range records {
					if rec.Type == trace.RecordTypeCt {
						ct, err := rec.ParseCtRecord()
						if err == nil {
							if name, ok := pipelineMap[ct.PipelineAddr]; ok {
								usageCounts[name]++
							}
						}
					}
				}
			}
		}

		filteredFuncs := allFuncs
		if filterPattern != "" {
			filteredFuncs = nil
			for _, fn := range allFuncs {
				if strings.Contains(fn, filterPattern) {
					filteredFuncs = append(filteredFuncs, fn)
				}
			}
		}

		if usedOnly {
			var kept []string
			for _, fn := range filteredFuncs {
				if usageCounts[fn] > 0 {
					kept = append(kept, fn)
				}
			}
			filteredFuncs = kept
		}

		fmt.Printf("\n=== Kernel Functions (%d total) ===\n\n", len(filteredFuncs))

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 4, ' ', 0)

		if withUsage {
			fmt.Fprintln(w, "Name\tSize\tDispatches")
			fmt.Fprintln(w, "───────────────────────────────────────────────\t────\t──────────")
		} else {
			fmt.Fprintln(w, "Name\tSize")
			fmt.Fprintln(w, "───────────────────────────────────────────────\t────")
		}

		limit := 50
		if showAll {
			limit = len(filteredFuncs)
		}

		sort.Strings(filteredFuncs)

		for i, fn := range filteredFuncs {
			if i >= limit {
				fmt.Fprintf(w, "...\n")
				break
			}

			sizeStr := "-"
			if s := funcSizes[fn]; s > 0 {
				sizeStr = formatSize(s)
			}

			if withUsage {
				fmt.Fprintf(w, "%s\t%s\t%d\n", fn, sizeStr, usageCounts[fn])
			} else {
				fmt.Fprintf(w, "%s\t%s\n", fn, sizeStr)
			}
		}

		w.Flush()

		if !showAll && len(filteredFuncs) > limit {
			fmt.Printf("\n(showing %d of %d, use --all to show all)\n", limit, len(filteredFuncs))
		}

		return nil
	},
}

func init() {
	mtlbCmd.AddCommand(mtlbFunctionsCmd)
	mtlbFunctionsCmd.Flags().StringVar(&filterPattern, "filter", "", "Filter by name pattern")
	mtlbFunctionsCmd.Flags().BoolVar(&showAll, "all", false, "Show all functions")
	mtlbFunctionsCmd.Flags().BoolVar(&usedOnly, "used-only", false, "Show only functions used in the trace")
	mtlbFunctionsCmd.Flags().BoolVar(&withUsage, "with-usage", false, "Show dispatch counts")
}
