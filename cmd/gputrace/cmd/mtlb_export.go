package cmd

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/metallib"
	"github.com/tmc/gputrace/internal/trace"
)

var mtlbExportFunctionsCmd = newMTLBExportFunctionsCommand(&mtlbExportOptions{
	format: "json",
})

type mtlbExportOptions struct {
	format   string
	usedOnly bool
	usage    bool
}

func newMTLBExportFunctionsCommand(opts *mtlbExportOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "export-functions <trace>",
		Short: "Export function list to various formats",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMTLBExportFunctions(cmd, args, opts)
		},
	}

	cmd.Flags().StringVar(&opts.format, "format", opts.format, "Output format (json, csv)")
	cmd.Flags().BoolVar(&opts.usedOnly, "used-only", opts.usedOnly, "Export only used functions")
	cmd.Flags().BoolVar(&opts.usage, "with-usage", opts.usage, "Include usage stats")
	return cmd
}

func runMTLBExportFunctions(cmd *cobra.Command, args []string, opts *mtlbExportOptions) error {
	format, err := validateMTLBExportFormat(opts.format)
	if err != nil {
		return err
	}

	tracePath := args[0]

	files, err := metallib.FindFiles(tracePath)
	if err != nil {
		return err
	}

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

	usageCounts := make(map[string]int)
	if opts.usedOnly || opts.usage {
		tr, err := trace.Open(tracePath)
		if err != nil {
			return fmt.Errorf("open trace: %w", err)
		}

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
			for _, kn := range tr.KernelNames {
				usageCounts[kn] = 1
			}
		}
	}

	var finalFuncs []string
	if opts.usedOnly {
		for _, fn := range allFuncs {
			if usageCounts[fn] > 0 {
				finalFuncs = append(finalFuncs, fn)
			}
		}
	} else {
		finalFuncs = allFuncs
	}

	switch format {
	case "json":
		type funcData struct {
			Name       string `json:"name"`
			Dispatches int    `json:"dispatches,omitempty"`
		}
		var output []funcData
		for _, fn := range finalFuncs {
			item := funcData{Name: fn}
			if opts.usage {
				item.Dispatches = usageCounts[fn]
			}
			output = append(output, item)
		}
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(output)
	case "csv":
		w := csv.NewWriter(cmd.OutOrStdout())
		header := []string{"Name"}
		if opts.usage {
			header = append(header, "Dispatches")
		}
		w.Write(header)
		for _, fn := range finalFuncs {
			row := []string{fn}
			if opts.usage {
				row = append(row, fmt.Sprintf("%d", usageCounts[fn]))
			}
			w.Write(row)
		}
		w.Flush()
		return w.Error()
	}

	return nil
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
}
