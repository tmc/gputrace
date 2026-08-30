package cmd

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/cupticapture"
	"github.com/tmc/gputrace/internal/gate"
	"github.com/tmc/gputrace/internal/gpuevent"
)

// warnCrossHost labels a two-bundle comparison with its host provenance.
// Timing deltas between bundles from different hosts (or unverifiable
// sessions) are noise; saying so belongs to the tool, not the reader.
func warnCrossHost(out io.Writer, base, variant string) {
	a := gate.ReadHostProvenance(base)
	b := gate.ReadHostProvenance(variant)
	switch {
	case a.Recorded && b.Recorded && a.Hostname != b.Hostname:
		fmt.Fprintf(out, "warning: CROSS-HOST comparison: %s (%s) vs %s (%s) — timing deltas are noise; structural counts remain comparable\n\n",
			a.Hostname, a.Device, b.Hostname, b.Device)
	case !a.Recorded || !b.Recorded:
		fmt.Fprint(out, "warning: host provenance absent from at least one bundle: cross-session comparison cannot be verified\n\n")
	}
}

// isCaptureInput reports whether a diff argument names a CUDA capture: a
// .gpucapture bundle, or a bare JSONL activity file.
func isCaptureInput(path string) bool {
	if cupticapture.IsBundle(path) {
		return true
	}
	return hasSuffixFold(path, ".jsonl")
}

func hasSuffixFold(s, suffix string) bool {
	if len(s) < len(suffix) {
		return false
	}
	return equalFold(s[len(s)-len(suffix):], suffix)
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

// runCaptureDiff compares two CUDA captures kernel by kernel. It is the
// CUPTI counterpart of the Metal dispatch-alignment diff: matching is by
// kernel name rather than by dispatch position, because CUDA captures have
// no encoder structure to align against.
func runCaptureDiff(cmd *cobra.Command, base, variant string, opts diffOptions) error {
	baseReport, err := loadCaptureReport(base)
	if err != nil {
		return fmt.Errorf("load base capture: %w", err)
	}
	variantReport, err := loadCaptureReport(variant)
	if err != nil {
		return fmt.Errorf("load variant capture: %w", err)
	}
	cmp := gpuevent.CompareCaptures(baseReport, variantReport)
	out := cmd.OutOrStdout()
	if opts.JSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(cmp)
	}
	warnCrossHost(out, base, variant)
	writeCaptureDiff(out, cmp, base, variant, opts.Limit)
	return nil
}

func writeCaptureDiff(out io.Writer, c *gpuevent.CaptureComparison, base, variant string, limit int) {
	fmt.Fprintf(out, "base:    %s\nvariant: %s\n\n", base, variant)
	fmt.Fprintf(out, "verdict: %s — %s\n", c.Verdict, c.Summary)
	if c.Verdict == gpuevent.CaptureInconclusive {
		return
	}
	fmt.Fprintf(out, "kernel time: %s -> %s (%+.1f%%)\n",
		dur(c.BaseTotalNS), dur(c.VariantTotalNS), c.TotalDeltaPct)

	u := c.Utilization
	if u.BaseWallSpanNS > 0 || u.VariantWallSpanNS > 0 {
		fmt.Fprintf(out, "wall span:   %s -> %s\n", dur(u.BaseWallSpanNS), dur(u.VariantWallSpanNS))
		fmt.Fprintf(out, "occupancy:   %.1f%% -> %.1f%% (%+.1f points)\n",
			u.BaseOccupancyPct, u.VariantOccupancyPct, u.VariantOccupancyPct-u.BaseOccupancyPct)
		fmt.Fprintf(out, "idle budget: %s across %d gaps -> %s across %d gaps (mean gap %s -> %s)\n",
			dur(u.BaseIdleNS), u.BaseGapCount, dur(u.VariantIdleNS), u.VariantGapCount,
			dur(u.BaseMeanGapNS), dur(u.VariantMeanGapNS))
	}

	rows := c.KernelDeltas
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	fmt.Fprintf(out, "\nPer-kernel deltas (by GPU time moved):\n")
	fmt.Fprintf(out, "  %9s  %-12s  %-22s  %-14s  %s\n", "TOTAL Δ", "COUNT", "MEAN", "OCCUPANCY", "KERNEL")
	for _, d := range rows {
		mark := "  "
		switch d.OnlyIn {
		case "base":
			mark = "- "
		case "variant":
			mark = "+ "
		}
		fmt.Fprintf(out, "%s%9s  %5d->%-5d  %9s -> %-9s  %5s -> %-5s  %s\n",
			mark, signedDur(d.TotalDeltaNS),
			d.BaseCount, d.VariantCount,
			dur(d.BaseMeanNS), dur(d.VariantMeanNS),
			occupancyOrDash(d.BaseOccupancy), occupancyOrDash(d.VarOccupancy),
			shortKernel(d.Name))
	}
	if n := len(c.KernelDeltas) - len(rows); n > 0 {
		fmt.Fprintf(out, "  ... %d more (raise --limit)\n", n)
	}
	if len(c.OnlyInBase) > 0 {
		fmt.Fprintf(out, "\nOnly in base (%d): %s\n", len(c.OnlyInBase), joinShort(c.OnlyInBase, 3))
	}
	if len(c.OnlyInVariant) > 0 {
		fmt.Fprintf(out, "Only in variant (%d): %s\n", len(c.OnlyInVariant), joinShort(c.OnlyInVariant, 3))
	}
}

// signedDur renders a delta with its direction; negative means the
// variant spent less GPU time.
func signedDur(ns int64) string {
	if ns < 0 {
		return "-" + dur(uint64(-ns))
	}
	return "+" + dur(uint64(ns))
}

func occupancyOrDash(pct float64) string {
	if pct <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.0f%%", pct)
}

func joinShort(names []string, max int) string {
	shown := names
	if len(shown) > max {
		shown = shown[:max]
	}
	out := ""
	for i, n := range shown {
		if i > 0 {
			out += ", "
		}
		out += shortKernel(n)
	}
	if len(names) > len(shown) {
		out += fmt.Sprintf(", and %d more", len(names)-len(shown))
	}
	return out
}
