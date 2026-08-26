package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/cupticapture"
	"github.com/tmc/gputrace/internal/cuptiprofile"
	"github.com/tmc/gputrace/internal/cuptitrace"
	"github.com/tmc/gputrace/internal/gpuevent"
)

// runCuptiPprof converts a CUDA capture (bundle or JSONL) into a pprof
// profile: one sample per kernel launch, valued at GPU execution time.
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
	// Demangle for readable pprof function names; raw symbols stay in the
	// function's SystemName via the name fallback chain.
	for i := range cap.Events {
		if cap.Events[i].Kind == gpuevent.KindKernel && cap.Events[i].Name == "" {
			cap.Events[i].Name = cuptitrace.Demangle(cap.Events[i].RawSymbol)
		}
	}
	prof, err := cuptiprofile.Build(cap)
	if err != nil {
		return err
	}

	outPath := opts.output
	if outPath == "" {
		base := filepath.Base(path)
		base = strings.TrimSuffix(base, filepath.Ext(base))
		outPath = base + ".pprof"
	}
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	if err := cuptiprofile.Write(prof, f); err != nil {
		f.Close()
		return fmt.Errorf("write pprof: %w", err)
	}
	if err := f.Close(); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Wrote %d kernel samples -> %s\n", len(prof.Sample), outPath)
	fmt.Fprintf(cmd.ErrOrStderr(), "View with: go tool pprof -top %s\n", outPath)
	return nil
}
