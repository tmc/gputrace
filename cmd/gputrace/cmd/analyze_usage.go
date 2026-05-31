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
	Use:    "analyze-usage [trace-path]",
	Short:  "Analyze buffer usage across kernels",
	Hidden: true,
	Args:   cobra.ExactArgs(1),
	RunE:   runAnalyzeUsage,
}

var analyzeFormat string

func init() {
	analyzeUsageCmd.Flags().StringVar(&analyzeFormat, "format", "text", "Output format (text, dot, json)")
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

func runAnalyzeUsage(cmd *cobra.Command, args []string) error {
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

	if analyzeFormat == "json" {
		type kernelUsage struct {
			Name  string `json:"name"`
			Count int    `json:"count"`
		}
		type bufferUsageJSON struct {
			Address    string        `json:"address"`
			Name       string        `json:"name"`
			Dispatches int           `json:"dispatches"`
			Kernels    []kernelUsage `json:"kernels"`
		}
		var out []bufferUsageJSON
		for _, stats := range sortedUsageStats(bufferUsage) {
			entry := bufferUsageJSON{
				Address:    fmt.Sprintf("0x%x", stats.Address),
				Name:       stats.Name,
				Dispatches: stats.Dispatches,
			}
			for _, kernel := range sortedKernelUsageStats(stats.Kernels) {
				entry.Kernels = append(entry.Kernels, kernelUsage{Name: kernel.Name, Count: kernel.Accesses})
			}
			out = append(out, entry)
		}
		data, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal json: %w", err)
		}
		fmt.Println(string(data))
		return nil
	}

	if analyzeFormat == "dot" {
		return writeAnalyzeUsageDOT(cmd.OutOrStdout(), bufferUsage)
	}

	fmt.Printf("Trace Buffer Usage Analysis\n")
	fmt.Printf("=============================\n")

	for _, stats := range sortedUsageStats(bufferUsage) {
		name := fmt.Sprintf("%s (0x%x)", stats.Name, stats.Address)

		fmt.Printf("\n%s: Used in %d dispatches\n", name, stats.Dispatches)
		for _, kernel := range sortedKernelUsageStats(stats.Kernels) {
			fmt.Printf("  - %s: %d\n", kernel.Name, kernel.Accesses)
		}
	}

	return nil
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
