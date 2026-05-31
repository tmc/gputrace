package cmd

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/mtlb"
	"github.com/tmc/gputrace/internal/trace"
)

var (
	exportFormat   string
	exportUsedOnly bool
	exportUsage    bool
)

var mtlbExportFunctionsCmd = &cobra.Command{
	Use:   "export-functions <trace>",
	Short: "Export function list to various formats",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		format, err := validateMTLBExportFormat(exportFormat)
		if err != nil {
			return err
		}

		tracePath := args[0]

		files, err := mtlb.FindMTLBFiles(tracePath)
		if err != nil {
			return err
		}

		var allFuncs []string

		for _, f := range files {
			data, err := os.ReadFile(f.Path)
			if err != nil {
				continue
			}
			lib, err := mtlb.ParseMTLB(data)
			if err == nil {
				funcs, _ := lib.ListFunctions()
				allFuncs = append(allFuncs, funcs...)
			}
		}

		// Collect usage if needed
		usageCounts := make(map[string]int)
		if exportUsedOnly || exportUsage {
			tr, err := trace.Open(tracePath)
			if err != nil {
				return fmt.Errorf("open trace: %w", err)
			}

			// Try accurate counting
			pipelineMap := tr.BuildPipelineFunctionMap()
			records, err := tr.ParseMTSPRecords()
			if err == nil {
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
			} else {
				// Fallback to KernelNames
				for _, kn := range tr.KernelNames {
					usageCounts[kn] = 1 // Just indicate presence
				}
			}
		}

		// Filter
		var finalFuncs []string
		if exportUsedOnly {
			for _, fn := range allFuncs {
				if usageCounts[fn] > 0 {
					finalFuncs = append(finalFuncs, fn)
				}
			}
		} else {
			finalFuncs = allFuncs
		}

		if format == "json" {
			type funcData struct {
				Name       string `json:"name"`
				Dispatches int    `json:"dispatches,omitempty"`
			}
			var output []funcData
			for _, fn := range finalFuncs {
				item := funcData{Name: fn}
				if exportUsage {
					item.Dispatches = usageCounts[fn]
				}
				output = append(output, item)
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(output)
		} else if format == "csv" {
			w := csv.NewWriter(os.Stdout)
			header := []string{"Name"}
			if exportUsage {
				header = append(header, "Dispatches")
			}
			w.Write(header)
			for _, fn := range finalFuncs {
				row := []string{fn}
				if exportUsage {
					row = append(row, fmt.Sprintf("%d", usageCounts[fn]))
				}
				w.Write(row)
			}
			w.Flush()
			return w.Error()
		}

		return nil
	},
}

func validateMTLBExportFormat(format string) (string, error) {
	switch format {
	case "json", "csv":
		return format, nil
	default:
		return "", fmt.Errorf("invalid mtlb export format %q (must be json or csv)", format)
	}
}

func init() {
	mtlbCmd.AddCommand(mtlbExportFunctionsCmd)
	mtlbExportFunctionsCmd.Flags().StringVar(&exportFormat, "format", "json", "Output format (json, csv)")
	mtlbExportFunctionsCmd.Flags().BoolVar(&exportUsedOnly, "used-only", false, "Export only used functions")
	mtlbExportFunctionsCmd.Flags().BoolVar(&exportUsage, "with-usage", false, "Include usage stats")
}
