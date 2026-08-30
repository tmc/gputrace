package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/pprof/profile"
	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/cudagraphdot"
	"github.com/tmc/gputrace/internal/cupticapture"
	"github.com/tmc/gputrace/internal/cuptiprofile"
	"github.com/tmc/gputrace/internal/cuptitrace"
	"github.com/tmc/gputrace/internal/gpuevent"
)

// runCuptiPprof converts a CUDA capture (bundle or JSONL) into a pprof
// profile: one sample per kernel launch carrying GPU time, launch count,
// queue delay, and per-stream idle, stacked under the application spans
// that enclosed it.
func runCuptiPprof(cmd *cobra.Command, args []string, opts *pprofOptions) error {
	path := args[0]
	r, closers, err := cupticapture.OpenEvents(path)
	if err != nil {
		return err
	}
	defer closers()
	cap, err := gpuevent.DecodeJSONL(r)
	if err != nil {
		return err
	}
	// Demangle for readable pprof function names, keeping each mangled
	// symbol so it can be restored as the function's SystemName.
	mangled := map[string]string{}
	for i := range cap.Events {
		e := &cap.Events[i]
		if e.Kind != gpuevent.KindKernel || e.RawSymbol == "" {
			continue
		}
		if e.Name == "" {
			e.Name = cuptitrace.Demangle(e.RawSymbol)
		}
		mangled[e.Name] = e.RawSymbol
	}

	var buildOpts cuptiprofile.Options
	if opts.dot != "" {
		nodes, commits, err := readGraphStructure(opts.dot, mangled)
		if err != nil {
			return err
		}
		buildOpts.Structure = nodes
		buildOpts.Commits = commits
	}

	prof, stats, err := cuptiprofile.Build(cap, buildOpts)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	cuptiprofile.SetSystemNames(prof, mangled)

	outPath := opts.output
	if outPath == "" {
		base := filepath.Base(path)
		base = strings.TrimSuffix(base, filepath.Ext(base))
		outPath = base + ".pprof"
	}
	if err := writeProfile(prof, outPath); err != nil {
		return err
	}

	status := pprofStatusWriter(outPath)
	fmt.Fprintf(status, "Wrote %d kernel samples -> %s\n", stats.Kernels, outPath)
	fmt.Fprint(status, formatCuptiPprofStats(stats))
	fmt.Fprintf(cmd.ErrOrStderr(), "View with: go tool pprof -top %s\n", outPath)
	return nil
}

// readGraphStructure loads a CUDA-graph dump directory into the structure
// nodes a joined profile carries. Symbols are demangled through the same
// table the activity records use, because the join is on the kernel name:
// a mangled node symbol and a demangled activity symbol are two functions,
// and the profile would show the counts and the time side by side without
// ever adding them up.
func readGraphStructure(path string, mangled map[string]string) ([]cuptiprofile.StructureNode, []string, error) {
	files, err := loadGraphDumps(path)
	if err != nil {
		return nil, nil, err
	}
	var nodes []cuptiprofile.StructureNode
	var commits []string
	for _, f := range files {
		for _, k := range f.Kernels() {
			name := cuptitrace.Demangle(k.Symbol)
			mangled[name] = k.Symbol
			nodes = append(nodes, cuptiprofile.StructureNode{GraphPath: k.Path, Symbol: name})
		}
		commits = append(commits, f.Roots...)
	}
	if len(nodes) == 0 {
		return nil, nil, fmt.Errorf("dot: %s declares no kernel nodes", path)
	}
	return nodes, commits, nil
}

// loadGraphDumps accepts a directory of dumps or a single dump file.
func loadGraphDumps(path string) ([]*cudagraphdot.File, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return cudagraphdot.ParseDir(path)
	}
	f, err := cudagraphdot.ParseFile(path)
	if err != nil {
		return nil, err
	}
	return []*cudagraphdot.File{f}, nil
}

// formatCuptiPprofStats reports what the profile's numbers are backed by.
// Span attribution and queue timing are both partial on real captures —
// CUDA-graph launches carry no queue timestamps at all — so the coverage
// is printed rather than left for the reader to assume.
func formatCuptiPprofStats(stats cuptiprofile.Stats) string {
	var b strings.Builder
	fmt.Fprintf(&b, "  gpu_time:     %.2f ms summed over %d launches (not wall time; streams overlap)\n",
		float64(stats.GPUTimeNS)/1e6, stats.Kernels)
	if stats.Spans == 0 {
		fmt.Fprintf(&b, "  spans:        none in capture; stacks are the kernel name alone\n")
	} else {
		fmt.Fprintf(&b, "  spans:        %d, enclosing %d of %d kernels (%.1f%%)\n",
			stats.Spans, stats.SpanAttributed, stats.Kernels, stats.SpanAttributedPct())
	}
	if stats.QueueTimed == 0 {
		fmt.Fprintf(&b, "  queue_delay:  no kernel carries usable queue timestamps (CUDA-graph launches report none); all samples are 0\n")
	} else {
		fmt.Fprintf(&b, "  queue_delay:  median %s over the %d of %d kernels that carry queue timestamps\n",
			formatDurationNS(stats.MedianQueueNS), stats.QueueTimed, stats.Kernels)
	}
	if stats.StructureNodes > 0 {
		fmt.Fprintf(&b, "  structure:    %d graph kernel nodes across %d commits, joined on kernel name\n",
			stats.StructureNodes, stats.Commits)
	}
	return b.String()
}

// formatDurationNS renders a duration at a scale that makes a wrong clock
// domain obvious: the failure this guards against reports seconds where
// microseconds belong.
func formatDurationNS(ns uint64) string {
	switch {
	case ns >= 1e9:
		return fmt.Sprintf("%.2f s", float64(ns)/1e9)
	case ns >= 1e6:
		return fmt.Sprintf("%.2f ms", float64(ns)/1e6)
	case ns >= 1e3:
		return fmt.Sprintf("%.2f us", float64(ns)/1e3)
	default:
		return fmt.Sprintf("%d ns", ns)
	}
}

// writeProfile writes a profile to a path, or to stdout for "-" and
// /dev/stdout so the profile can be piped straight into pprof.
func writeProfile(prof *profile.Profile, outPath string) error {
	if outputPathIsExplicitStdout(outPath) {
		return prof.Write(os.Stdout)
	}
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	if err := prof.Write(f); err != nil {
		f.Close()
		return fmt.Errorf("write pprof: %w", err)
	}
	return f.Close()
}
