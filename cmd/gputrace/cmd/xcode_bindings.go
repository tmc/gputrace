//go:build darwin

package cmd

import (
	"encoding/json"
	"fmt"
	"io"

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
	return writeXcodeBindingsText(w, report)
}

func writeXcodeBindingsText(w io.Writer, report xcodebindings.Report) error {
	fmt.Fprintf(w, "Framework: %s\n", report.FrameworkPath)
	if report.Framework {
		fmt.Fprintln(w, "Framework load: available")
	} else {
		fmt.Fprintln(w, "Framework load: unavailable")
	}
	fmt.Fprintln(w, "Probe scope: symbol availability only; this does not prove trace decoding or metric parity.")
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
	}
	fmt.Fprintln(w, "Selector details are available with --json.")

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
