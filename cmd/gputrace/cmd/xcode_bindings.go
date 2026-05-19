//go:build darwin

package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/xcodebindings"
)

var xcodeBindingsJSON bool

var xcodeBindingsCmd = &cobra.Command{
	Use:   "xcode-bindings",
	Short: "Inspect private Xcode GTShaderProfiler bindings",
	Long: `Inspect the private GTShaderProfiler binding surface used for Xcode parity.

The command checks class and selector availability only. It does not construct
GTShaderProfiler objects or parse trace data, so it is safe to run as a
capability probe before enabling deeper profiler adapters.`,
	RunE: runXcodeBindings,
}

func init() {
	rootCmd.AddCommand(xcodeBindingsCmd)
	xcodeBindingsCmd.Flags().BoolVar(&xcodeBindingsJSON, "json", false, "Output in JSON format")
}

func runXcodeBindings(cmd *cobra.Command, args []string) error {
	report := xcodebindings.Probe()
	if xcodeBindingsJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}

	fmt.Printf("Framework: %s\n", report.FrameworkPath)
	if report.Framework {
		fmt.Println("Status: available")
	} else {
		fmt.Println("Status: missing")
	}
	fmt.Printf("Classes: %d present, %d missing\n",
		report.Summary["classes_present"], report.Summary["classes_missing"])
	fmt.Printf("Selectors: %d present, %d missing\n\n",
		report.Summary["selectors_present"], report.Summary["selectors_missing"])

	for _, class := range report.Classes {
		status := "missing"
		if class.Present {
			status = "present"
		}
		fmt.Printf("%s: %s\n", class.Name, status)
		for _, sel := range class.Selectors {
			marker := "missing"
			if sel.Present {
				marker = "present"
			}
			fmt.Printf("  %-8s %-7s %s\n", sel.Kind, marker, sel.Name)
		}
	}

	fmt.Println("\nXcode parity gaps")
	for _, gap := range report.Gaps {
		fmt.Printf("  %-20s %-33s %s\n", gap.Metric, gap.Status, gap.Binding)
		if gap.Signature != "" {
			fmt.Printf("    signature: %s\n", gap.Signature)
		}
		fmt.Printf("    next: %s\n", gap.Next)
	}
	for _, note := range report.Notes {
		fmt.Printf("\nNote: %s\n", note)
	}
	return nil
}
