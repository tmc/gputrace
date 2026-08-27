package cmd

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/gpuevent"
)

// printSpanTable renders the per-span setup/launch/GPU/tail decomposition.
// Every row names its provenance: values from API records are [V] (measured
// host-side), device-fallback rows are [D] (derived, lower confidence).
func printSpanTable(cmd *cobra.Command, cap gpuevent.Capture, asJSON bool) error {
	spans := gpuevent.AttributeSpans(cap)
	decomp := gpuevent.Decompositions(spans, cap.APIs)
	out := cmd.OutOrStdout()

	if len(decomp) == 0 {
		fmt.Fprintln(out, "No span records in this capture.")
		fmt.Fprintln(out, "Spans come from app-events sidecars (GPUTRACE_APP_EVENTS) or in-process capture (mlx-go EvalWithLabel).")
		return nil
	}

	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(decomp)
	}

	w := tabwriter.NewWriter(out, 2, 4, 2, ' ', 0)
	fmt.Fprintln(w, "SPAN\tEVAL\tSETUP\tSRC\tLAUNCH LAT\tGPU TIME\tTAIL\tKERNELS\tCONF")
	for _, d := range decomp {
		src := "[V]"
		if d.SetupSource != "api" {
			src = "[D]"
		}
		fmt.Fprintf(w, "%s\t%d\t%s\t%s %s\t%s\t%s\t%s\t%d\t%s\n",
			d.SpanName, d.EvalSeq,
			dur(d.SetupNS), src, "setup",
			dur(uint64(max64(d.LaunchLatencyNS, 0))),
			dur(d.GPUTimeNS), dur(d.TailNS),
			d.KernelCount, d.Confidence)
	}
	w.Flush()
	return nil
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
