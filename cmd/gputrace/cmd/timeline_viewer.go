package cmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/tmc/gputrace/internal/perfettoviewer"
)

func validateTimelineViewerOptions(opts *timelineOptions, output string) error {
	if !opts.openViewer && !opts.serveViewer {
		if opts.uiDir != "" || opts.remoteUI || opts.listen != "127.0.0.1:0" {
			return fmt.Errorf("--ui-dir, --remote-ui, and --listen require --open or --serve")
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
	if commandOutputPathIsStdout(output) {
		return fmt.Errorf("--open and --serve require a file output")
	}
	if opts.remoteUI == (opts.uiDir != "") {
		return fmt.Errorf("choose exactly one of --ui-dir or --remote-ui")
	}
	if !loopbackListenAddress(opts.listen) {
		return fmt.Errorf("Perfetto viewer listen address must be loopback: %s", opts.listen)
	}
	return nil
}

func serveTimelinePerfetto(cmd *cobra.Command, tracePath, output string, opts *timelineOptions) error {
	if !opts.openViewer && !opts.serveViewer {
		return nil
	}
	handler, err := perfettoviewer.NewHandler(perfettoviewer.Config{
		TracePath: output,
		UIPath:    opts.uiDir,
		RemoteUI:  opts.remoteUI,
		Title:     filepath.Base(tracePath),
	})
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", opts.listen)
	if err != nil {
		return fmt.Errorf("listen for Perfetto viewer: %w", err)
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	url := "http://" + listener.Addr().String() + "/"
	fmt.Fprintf(cmd.ErrOrStderr(), "Perfetto viewer: %s\n", url)
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

func loopbackListenAddress(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
