package cmd

import (
	"fmt"
	"io"
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

type mtlbFunctionsOptions struct {
	FilterPattern string
	ShowAll       bool
	UsedOnly      bool
	WithUsage     bool
}

type mtlbFunctionSize struct {
	Bytes int64
	Known bool
}

const maxFunctionSizeForDisplay = uint64(1<<63 - 1)

var mtlbFunctionsCmd = &cobra.Command{
	Use:   "functions [trace]",
	Short: "List all functions in MTLB files",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		opts := mtlbFunctionsOptions{
			FilterPattern: filterPattern,
			ShowAll:       showAll,
			UsedOnly:      usedOnly,
			WithUsage:     withUsage,
		}
		return runMTLBFunctions(args[0], opts, cmd.OutOrStdout())
	},
}

func runMTLBFunctions(tracePath string, opts mtlbFunctionsOptions, out io.Writer) error {
	files, err := mtlb.FindMTLBFiles(tracePath)
	if err != nil {
		return err
	}

	var allFuncs []string
	funcSizes := make(map[string]mtlbFunctionSize)

	for _, f := range files {
		data, err := os.ReadFile(f.Path)
		if err != nil {
			continue
		}

		lib, err := mtlb.ParseMTLB(data)
		if err != nil {
			continue
		}

		funcs, err := lib.ListFunctionMetadata()
		if err != nil {
			continue
		}
		for _, fn := range funcs {
			allFuncs = append(allFuncs, fn.Name)
			if fn.SizeKnown && fn.Size <= maxFunctionSizeForDisplay {
				funcSizes[fn.Name] = mtlbFunctionSize{Bytes: int64(fn.Size), Known: true}
				continue
			}
			if _, ok := funcSizes[fn.Name]; !ok {
				funcSizes[fn.Name] = mtlbFunctionSize{}
			}
		}
	}

	// Collect usage data if requested
	usageCounts := make(map[string]int)
	if opts.UsedOnly || opts.WithUsage {
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
	if opts.FilterPattern != "" {
		filteredFuncs = nil
		for _, fn := range allFuncs {
			if strings.Contains(fn, opts.FilterPattern) {
				filteredFuncs = append(filteredFuncs, fn)
			}
		}
	}

	if opts.UsedOnly {
		var kept []string
		for _, fn := range filteredFuncs {
			if usageCounts[fn] > 0 {
				kept = append(kept, fn)
			}
		}
		filteredFuncs = kept
	}

	unknownSizes := 0
	for _, fn := range filteredFuncs {
		if !funcSizes[fn].Known {
			unknownSizes++
		}
	}

	fmt.Fprintf(out, "\n=== Kernel Functions (%d total) ===\n\n", len(filteredFuncs))

	w := tabwriter.NewWriter(out, 0, 0, 4, ' ', 0)

	if opts.WithUsage {
		fmt.Fprintln(w, "Name\tSize\tDispatches")
		fmt.Fprintln(w, "───────────────────────────────────────────────\t────\t──────────")
	} else {
		fmt.Fprintln(w, "Name\tSize")
		fmt.Fprintln(w, "───────────────────────────────────────────────\t────")
	}

	limit := 50
	if opts.ShowAll {
		limit = len(filteredFuncs)
	}

	sort.Strings(filteredFuncs)

	for i, fn := range filteredFuncs {
		if i >= limit {
			fmt.Fprintf(w, "...\n")
			break
		}

		sizeStr := "unknown"
		if size := funcSizes[fn]; size.Known {
			sizeStr = formatSize(size.Bytes)
		}

		if opts.WithUsage {
			fmt.Fprintf(w, "%s\t%s\t%d\n", fn, sizeStr, usageCounts[fn])
		} else {
			fmt.Fprintf(w, "%s\t%s\n", fn, sizeStr)
		}
	}

	w.Flush()

	if unknownSizes > 0 {
		fmt.Fprintf(out, "\n(size unknown for %d function(s); MTLB metadata did not include a per-function size)\n", unknownSizes)
	}

	if !opts.ShowAll && len(filteredFuncs) > limit {
		fmt.Fprintf(out, "\n(showing %d of %d, use --all to show all)\n", limit, len(filteredFuncs))
	}

	return nil
}

func init() {
	mtlbCmd.AddCommand(mtlbFunctionsCmd)
	mtlbFunctionsCmd.Flags().StringVar(&filterPattern, "filter", "", "Filter by name pattern")
	mtlbFunctionsCmd.Flags().BoolVar(&showAll, "all", false, "Show all functions")
	mtlbFunctionsCmd.Flags().BoolVar(&usedOnly, "used-only", false, "Show only functions used in the trace")
	mtlbFunctionsCmd.Flags().BoolVar(&withUsage, "with-usage", false, "Show dispatch counts")
}
