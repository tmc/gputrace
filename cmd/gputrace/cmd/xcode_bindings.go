//go:build darwin

package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/xcodebindings"
)

type xcodeBindingsOptions struct {
	json bool
}

func runXcodeBindings(cmd *cobra.Command, args []string, opts *xcodeBindingsOptions) error {
	report := xcodebindings.Probe()
	w := cmd.OutOrStdout()
	if opts.json {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}

	fmt.Fprintf(w, "Framework: %s\n", report.FrameworkPath)
	if report.Framework {
		fmt.Fprintln(w, "Status: available")
	} else {
		fmt.Fprintln(w, "Status: missing")
	}
	fmt.Fprintf(w, "Classes: %d present, %d missing\n",
		report.Summary["classes_present"], report.Summary["classes_missing"])
	fmt.Fprintf(w, "Selectors: %d present, %d missing\n\n",
		report.Summary["selectors_present"], report.Summary["selectors_missing"])

	for _, class := range report.Classes {
		status := "missing"
		if class.Present {
			status = "present"
		}
		fmt.Fprintf(w, "%s: %s\n", class.Name, status)
		for _, sel := range class.Selectors {
			marker := "missing"
			if sel.Present {
				marker = "present"
			}
			fmt.Fprintf(w, "  %-8s %-7s %s\n", sel.Kind, marker, sel.Name)
		}
	}

	fmt.Fprintln(w, "\nXcode parity gaps")
	for _, gap := range report.Gaps {
		fmt.Fprintf(w, "  %-20s %-33s %s\n", gap.Metric, gap.Status, gap.Binding)
		if gap.Signature != "" {
			fmt.Fprintf(w, "    signature: %s\n", gap.Signature)
		}
		fmt.Fprintf(w, "    next: %s\n", gap.Next)
	}
	for _, note := range report.Notes {
		fmt.Fprintf(w, "\nNote: %s\n", note)
	}
	return nil
}
