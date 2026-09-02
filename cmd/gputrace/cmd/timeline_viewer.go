package cmd

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/tmc/gputrace/internal/perfettoviewer"
)

func validateTimelineViewerOptions(opts *timelineOptions, output string) error {
	if opts.maxOutputBytes < 0 {
		return fmt.Errorf("--max-output-bytes must not be negative")
	}
	if opts.maxOutputBytes > 0 && opts.format != "perfetto" {
		return fmt.Errorf("--max-output-bytes requires --format perfetto")
	}
	timeUnset := (opts.timeStart < 0 && opts.timeEnd < 0) || (opts.timeStart == 0 && opts.timeEnd == 0)
	viewerSelection := opts.kernel != "" || opts.kernelOccurrence >= 0 || !timeUnset
	if !opts.openViewer && !opts.serveViewer {
		if opts.uiDir != "" || opts.remoteUI || opts.listen != "127.0.0.1:0" || viewerSelection {
			return fmt.Errorf("viewer selection and serving flags require --open or --serve")
		}
		return nil
	}
	if opts.openViewer && opts.serveViewer {
		return fmt.Errorf("--open and --serve are mutually exclusive")
	}
	if opts.format != "perfetto" {
		return fmt.Errorf("--open and --serve require --format perfetto")
	}
	if opts.clock != timelineClockBusy && opts.clock != timelineClockWall {
		return fmt.Errorf("--open and --serve require --clock busy or wall")
	}
	if opts.kernelOccurrence >= 0 && opts.kernel == "" {
		return fmt.Errorf("--kernel-occurrence requires --kernel")
	}
	if opts.kernel != "" && opts.clock != timelineClockBusy {
		return fmt.Errorf("--kernel requires --clock busy")
	}
	hasStart, hasEnd := opts.timeStart >= 0, opts.timeEnd >= 0
	if !timeUnset && hasStart != hasEnd {
		return fmt.Errorf("--time-start and --time-end must be provided together")
	}
	if !timeUnset && (!isFiniteSeconds(opts.timeStart) || !isFiniteSeconds(opts.timeEnd) || opts.timeEnd <= opts.timeStart) {
		return fmt.Errorf("viewer time range must be finite, non-negative, and increasing")
	}
	if opts.kernel != "" && !timeUnset {
		return fmt.Errorf("--kernel and --time-start/--time-end are mutually exclusive")
	}
	if commandOutputPathIsStdout(output) {
		return fmt.Errorf("--open and --serve require a file output")
	}
	if opts.remoteUI == (opts.uiDir != "") {
		return fmt.Errorf("choose exactly one of --ui-dir or --remote-ui")
	}
	if opts.uiDir != "" {
		manifest, err := perfettoviewer.ReadUIManifest(opts.uiDir)
		if err != nil {
			return err
		}
		opts.uiRevision = manifest.Revision
	}
	if !loopbackListenAddress(opts.listen) {
		return fmt.Errorf("Perfetto viewer listen address must be loopback: %s", opts.listen)
	}
	return nil
}

func isFiniteSeconds(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0) && value <= float64(math.MaxUint64)/1e9
}

func resolveTimelineNavigation(timeline *Timeline, opts *timelineOptions) error {
	opts.navigationStartNS = 0
	opts.navigationEndNS = 0
	opts.selectionStartNS = 0
	opts.selectionDurationNS = 0
	if !opts.openViewer && !opts.serveViewer {
		return nil
	}
	timeUnset := (opts.timeStart < 0 && opts.timeEnd < 0) || (opts.timeStart == 0 && opts.timeEnd == 0)
	if !timeUnset {
		opts.navigationStartNS = uint64(opts.timeStart * 1e9)
		opts.navigationEndNS = uint64(opts.timeEnd * 1e9)
		return nil
	}
	if opts.kernel == "" {
		return nil
	}
	var matches []TimelineEvent
	for _, event := range timeline.Events {
		if event.Category == "kernel" && event.Name == opts.kernel {
			matches = append(matches, event)
		}
	}
	if len(matches) == 0 {
		return fmt.Errorf("kernel %q does not occur in the selected timeline", opts.kernel)
	}
	if opts.kernelOccurrence < 0 && len(matches) != 1 {
		return fmt.Errorf("kernel %q has %d occurrences; specify --kernel-occurrence", opts.kernel, len(matches))
	}
	occurrence := opts.kernelOccurrence
	if occurrence < 0 {
		occurrence = 0
	}
	if occurrence >= len(matches) {
		return fmt.Errorf("kernel %q occurrence %d is out of range (have %d)", opts.kernel, occurrence, len(matches))
	}
	event := matches[occurrence]
	start := event.Timestamp * 1000
	duration := event.Duration * 1000
	if duration == 0 {
		duration = 1
	}
	padding := duration
	if padding < 1_000 {
		padding = 1_000
	}
	viewStart := uint64(0)
	if start > padding {
		viewStart = start - padding
	}
	opts.navigationStartNS = viewStart
	opts.navigationEndNS = start + duration + padding
	opts.selectionStartNS = start
	opts.selectionDurationNS = duration
	return nil
}

func serveTimelinePerfetto(cmd *cobra.Command, tracePath, output string, opts *timelineOptions) error {
	if !opts.openViewer && !opts.serveViewer {
		return nil
	}
	handler, err := perfettoviewer.NewHandler(perfettoviewer.Config{
		TracePath:  output,
		UIPath:     opts.uiDir,
		UIRevision: opts.uiRevision,
		RemoteUI:   opts.remoteUI,
		Title:      filepath.Base(tracePath),
		Navigation: timelineViewerNavigation(opts),
	})
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", opts.listen)
	if err != nil {
		return fmt.Errorf("listen for Perfetto viewer: %w", err)
	}
	defer listener.Close()
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	url := "http://" + listener.Addr().String() + "/"
	fmt.Fprintf(cmd.ErrOrStderr(), "Perfetto viewer: %s\n", url)
	if opts.remoteUI {
		fmt.Fprintln(cmd.ErrOrStderr(), "Perfetto UI: https://ui.perfetto.dev (mutable remote release)")
	} else {
		fmt.Fprintf(cmd.ErrOrStderr(), "Perfetto UI revision: %s\n", opts.uiRevision)
	}
	if opts.openViewer {
		if err := exec.Command("open", url).Run(); err != nil {
			listener.Close()
			return fmt.Errorf("open Perfetto viewer: %w", err)
		}
	}

	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(listener) }()
	select {
	case <-cmd.Context().Done():
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			return fmt.Errorf("shut down Perfetto viewer: %w", err)
		}
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve Perfetto viewer: %w", err)
	}
}

func timelineViewerNavigation(opts *timelineOptions) *perfettoviewer.Navigation {
	if opts.navigationEndNS <= opts.navigationStartNS {
		return nil
	}
	return &perfettoviewer.Navigation{
		ViewStartNS:      opts.navigationStartNS,
		ViewEndNS:        opts.navigationEndNS,
		SelectionStartNS: opts.selectionStartNS,
		SelectionDurNS:   opts.selectionDurationNS,
		HasSelection:     opts.kernel != "",
	}
}

func loopbackListenAddress(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
