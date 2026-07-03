package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/trace"
)

var analyzeUsageCmd = &cobra.Command{
	Use:    "analyze-usage <trace-path>",
	Short:  "Analyze buffer usage across kernels",
	Hidden: true,
	Args:   cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		format, err := cmd.Flags().GetString("format")
		if err != nil {
			return err
		}
		return runAnalyzeUsage(cmd, args, format)
	},
}

func init() {
	analyzeUsageCmd.Flags().String("format", "text", "Output format (text, dot, json)")
	rootCmd.AddCommand(analyzeUsageCmd)
}

type usageStats struct {
	Address    uint64
	Name       string
	Dispatches int
	Kernels    map[string]*kernelUsageStats
}

type kernelUsageStats struct {
	ID       string
	Name     string
	Accesses int
	Reads    int
	Writes   int
}

type analyzeKernelUsageJSON struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type analyzeBufferUsageJSON struct {
	Address    string                   `json:"address"`
	Name       string                   `json:"name"`
	Dispatches int                      `json:"dispatches"`
	Kernels    []analyzeKernelUsageJSON `json:"kernels"`
}

func runAnalyzeUsage(cmd *cobra.Command, args []string, outputFormat string) error {
	format, err := normalizeAnalyzeFormat(outputFormat)
	if err != nil {
		return err
	}

	tracePath := args[0]
	t, err := trace.Open(tracePath)
	if err != nil {
		return fmt.Errorf("open trace: %w", err)
	}
	defer t.Close()

	events, err := t.ParseDependencyEvents()
	if err != nil {
		return fmt.Errorf("parse dependency events: %w", err)
	}
	bufferUsage := collectAnalyzeUsage(events)

	if format == "json" {
		return writeAnalyzeUsageJSON(cmd.OutOrStdout(), bufferUsage)
	}

	if format == "dot" {
		return writeAnalyzeUsageDOT(cmd.OutOrStdout(), bufferUsage)
	}

	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "Trace Buffer Usage Analysis\n")
	fmt.Fprintf(w, "=============================\n")

	for _, stats := range sortedUsageStats(bufferUsage) {
		name := fmt.Sprintf("%s (0x%x)", stats.Name, stats.Address)

		fmt.Fprintf(w, "\n%s: Used in %d dispatches\n", name, stats.Dispatches)
		for _, kernel := range sortedKernelUsageStats(stats.Kernels) {
			fmt.Fprintf(w, "  - %s: %d\n", kernel.Name, kernel.Accesses)
		}
	}

	return nil
}

func writeAnalyzeUsageJSON(w io.Writer, bufferUsage map[uint64]*usageStats) error {
	var out []analyzeBufferUsageJSON
	for _, stats := range sortedUsageStats(bufferUsage) {
		entry := analyzeBufferUsageJSON{
			Address:    fmt.Sprintf("0x%x", stats.Address),
			Name:       stats.Name,
			Dispatches: stats.Dispatches,
		}
		for _, kernel := range sortedKernelUsageStats(stats.Kernels) {
			entry.Kernels = append(entry.Kernels, analyzeKernelUsageJSON{Name: kernel.Name, Count: kernel.Accesses})
		}
		out = append(out, entry)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return fmt.Errorf("marshal json: %w", err)
	}
	return nil
}

func normalizeAnalyzeFormat(format string) (string, error) {
	switch format {
	case "text", "dot", "json":
		return format, nil
	default:
		return "", fmt.Errorf("unknown analyze-usage format %q", format)
	}
}

func collectAnalyzeUsage(events []trace.DependencyEvent) map[uint64]*usageStats {
	events = append([]trace.DependencyEvent(nil), events...)
	sort.SliceStable(events, func(i, j int) bool {
		return events[i].Offset < events[j].Offset
	})

	bufferUsage := make(map[uint64]*usageStats)
	currentKernelKey := ""
	currentKernelName := ""
	kernelSeq := 0

	for _, ev := range events {
		switch ev.Type {
		case trace.EventCS:
			currentKernelName = ev.Label
			if currentKernelName == "" {
				currentKernelName = fmt.Sprintf("Kernel %d", kernelSeq)
			}
			currentKernelKey = currentKernelName
			kernelSeq++
		case trace.EventBind, trace.EventUse:
			if currentKernelKey == "" || ev.Address == 0 {
				continue
			}
			read, write := analyzeUsageDirections(ev)
			if !read && !write {
				continue
			}

			stats := bufferUsage[ev.Address]
			if stats == nil {
				stats = &usageStats{
					Address: ev.Address,
					Name:    analyzeUsageBufferName(ev),
					Kernels: make(map[string]*kernelUsageStats),
				}
				bufferUsage[ev.Address] = stats
			}
			if stats.Name == "" || stats.Name == fmt.Sprintf("Buffer@0x%x", ev.Address) {
				stats.Name = analyzeUsageBufferName(ev)
			}
			stats.Dispatches++

			kernel := stats.Kernels[currentKernelKey]
			if kernel == nil {
				kernel = &kernelUsageStats{
					ID:   fmt.Sprintf("kernel:%s", currentKernelKey),
					Name: currentKernelName,
				}
				stats.Kernels[currentKernelKey] = kernel
			}
			kernel.Accesses++
			if read {
				kernel.Reads++
			}
			if write {
				kernel.Writes++
			}
		}
	}

	return bufferUsage
}

func analyzeUsageDirections(ev trace.DependencyEvent) (read, write bool) {
	if ev.Type == trace.EventUse {
		return true, false
	}
	if ev.Usage.IsRead() || ev.Usage&trace.MTLResourceUsageSample != 0 {
		read = true
	}
	if ev.Usage.IsWrite() {
		write = true
	}
	return read, write
}

func analyzeUsageBufferName(ev trace.DependencyEvent) string {
	if ev.Name != "" {
		return ev.Name
	}
	return fmt.Sprintf("Buffer@0x%x", ev.Address)
}

func sortedUsageStats(bufferUsage map[uint64]*usageStats) []*usageStats {
	stats := make([]*usageStats, 0, len(bufferUsage))
	for _, usage := range bufferUsage {
		stats = append(stats, usage)
	}
	sort.Slice(stats, func(i, j int) bool {
		if stats[i].Dispatches != stats[j].Dispatches {
			return stats[i].Dispatches > stats[j].Dispatches
		}
		return stats[i].Address < stats[j].Address
	})
	return stats
}

func sortedKernelUsageStats(kernels map[string]*kernelUsageStats) []*kernelUsageStats {
	stats := make([]*kernelUsageStats, 0, len(kernels))
	for _, kernel := range kernels {
		stats = append(stats, kernel)
	}
	sort.Slice(stats, func(i, j int) bool {
		if stats[i].Name != stats[j].Name {
			return stats[i].Name < stats[j].Name
		}
		return stats[i].ID < stats[j].ID
	})
	return stats
}

func writeAnalyzeUsageDOT(w io.Writer, bufferUsage map[uint64]*usageStats) error {
	var buf bytes.Buffer
	fmt.Fprintln(&buf, "digraph G {")
	fmt.Fprintln(&buf, "  rankdir=LR;")
	for _, stats := range sortedUsageStats(bufferUsage) {
		if stats.Dispatches < 2 {
			continue
		}

		bufferID := analyzeUsageBufferNodeID(stats.Address)
		bufferLabel := fmt.Sprintf("%s\n(0x%x)", stats.Name, stats.Address)
		fmt.Fprintf(&buf, "  %q [shape=box, label=%q];\n", bufferID, bufferLabel)

		for _, kernel := range sortedKernelUsageStats(stats.Kernels) {
			fmt.Fprintf(&buf, "  %q [shape=ellipse, label=%q];\n", kernel.ID, kernel.Name)
			switch {
			case kernel.Reads > 0 && kernel.Writes > 0:
				fmt.Fprintf(&buf, "  %q -> %q [label=\"ReadWrite\", dir=both];\n", kernel.ID, bufferID)
			case kernel.Reads > 0:
				fmt.Fprintf(&buf, "  %q -> %q [label=\"Read\"];\n", bufferID, kernel.ID)
			case kernel.Writes > 0:
				fmt.Fprintf(&buf, "  %q -> %q [label=\"Write\"];\n", kernel.ID, bufferID)
			}
		}
	}
	fmt.Fprintln(&buf, "}")
	_, err := w.Write(buf.Bytes())
	return err
}

func analyzeUsageBufferNodeID(address uint64) string {
	return fmt.Sprintf("buffer:0x%x", address)
}
