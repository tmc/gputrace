package cmd

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/metallib"
	"github.com/tmc/gputrace/internal/trace"
)

type mtlbStatsOptions struct{}

var mtlbStatsCmd = newMTLBStatsCommand(new(mtlbStatsOptions))

func newMTLBStatsCommand(opts *mtlbStatsOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "stats <trace>",
		Short: "Analyze MTLB composition",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMTLBStats(cmd, args, opts)
		},
	}
}

func runMTLBStats(cmd *cobra.Command, args []string, _ *mtlbStatsOptions) error {
	tracePath := args[0]

	files, err := metallib.FindFiles(tracePath)
	if err != nil {
		return err
	}

	tr, err := trace.Open(tracePath)
	if err != nil {
		return fmt.Errorf("open trace: %w", err)
	}

	fmt.Println("\n=== Metal Library Statistics ===")

	// Collect all functions
	var allFuncs []string
	for _, f := range files {
		data, err := os.ReadFile(f.Path)
		if err != nil {
			continue
		}
		lib, err := metallib.Parse(data)
		if err == nil {
			funcs, _ := lib.ListFunctions()
			allFuncs = append(allFuncs, funcs...)
		}
	}

	// Categorize functions
	categories := map[string]int{
		"GEMM variants":   0,
		"Copy operations": 0,
		"Elementwise":     0,
		"Normalization":   0,
		"Other":           0,
	}

	dataTypes := map[string]int{
		"float32":  0,
		"bfloat16": 0,
		"float16":  0,
		"int32":    0,
	}

	for _, fn := range allFuncs {
		lowerFn := strings.ToLower(fn)

		// Categories
		if strings.Contains(lowerFn, "gemm") {
			categories["GEMM variants"]++
		} else if strings.Contains(lowerFn, "copy") {
			categories["Copy operations"]++
		} else if strings.Contains(lowerFn, "norm") {
			categories["Normalization"]++
		} else if strings.Contains(lowerFn, "element") || strings.Contains(lowerFn, "add") || strings.Contains(lowerFn, "mul") {
			// Very rough heuristic for elementwise
			categories["Elementwise"]++
		} else {
			categories["Other"]++
		}

		// Data types
		if strings.Contains(lowerFn, "float32") {
			dataTypes["float32"]++
		}
		if strings.Contains(lowerFn, "bfloat16") {
			dataTypes["bfloat16"]++
		}
		if strings.Contains(lowerFn, "float16") {
			dataTypes["float16"]++
		}
		if strings.Contains(lowerFn, "int32") {
			dataTypes["int32"]++
		}
	}

	fmt.Println("\nFunction Categories:")
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 4, ' ', 0)

	// Sort categories for consistent output (except Other last)
	var cats []string
	for c := range categories {
		if c != "Other" {
			cats = append(cats, c)
		}
	}
	sort.Strings(cats)
	cats = append(cats, "Other")

	for _, c := range cats {
		fmt.Fprintf(w, "  %s:\t%d functions\n", c, categories[c])
	}
	w.Flush()

	fmt.Println("\nData Type Coverage:")
	var types []string
	for t := range dataTypes {
		types = append(types, t)
	}
	sort.Strings(types)

	for _, t := range types {
		fmt.Fprintf(w, "  %s:\t%d functions\n", t, dataTypes[t])
	}
	w.Flush()

	// Usage Analysis
	usedMap := make(map[string]bool)

	// Try accurate counting first
	pipelineMap := tr.BuildPipelineFunctionMap()
	records, err := tr.ParseMTSPRecords()
	if err == nil {
		for _, rec := range records {
			if rec.Type == trace.RecordTypeCt {
				ct, err := rec.ParseCtRecord()
				if err == nil {
					if name, ok := pipelineMap[ct.PipelineAddr]; ok {
						usedMap[name] = true
					}
				}
			}
		}
	} else {
		// Fallback to KernelNames
		for _, kn := range tr.KernelNames {
			usedMap[kn] = true
		}
	}

	usedCount := 0
	// Count how many of allFuncs are in usedMap
	for _, fn := range allFuncs {
		if usedMap[fn] {
			usedCount++
		}
	}

	totalFuncs := len(allFuncs)
	unusedCount := totalFuncs - usedCount
	usedPct := 0.0
	if totalFuncs > 0 {
		usedPct = float64(usedCount) / float64(totalFuncs) * 100
	}

	fmt.Println("\nUsage Analysis:")
	fmt.Fprintf(w, "  Functions used:\t%d (%.1f%%)\n", usedCount, usedPct)
	fmt.Fprintf(w, "  Functions unused:\t%d (%.1f%%)\n", unusedCount, 100-usedPct)
	w.Flush()

	return nil
}

func init() {
	mtlbCmd.AddCommand(mtlbStatsCmd)
}
