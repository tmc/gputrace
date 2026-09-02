// Package profilereplay adds measured GPU profiler data to a Metal capture.
//
// Profile drives Apple's MTLReplayer headlessly. It does not open Xcode or
// change the frontmost application.
package profilereplay

import (
	"context"

	internal "github.com/tmc/gputrace/internal/profilereplay"
)

// AppPath is the system MTLReplayer application bundle.
const AppPath = internal.AppPath

var (
	// ErrUnavailable reports that MTLReplayer is not installed.
	ErrUnavailable = internal.ErrUnavailable
	// ErrNoCapture reports that the input has no capture stream to replay.
	ErrNoCapture = internal.ErrNoCapture
	// ErrNoProfilerData reports that replay produced no readable streamData.
	ErrNoProfilerData = internal.ErrNoProfilerData
	// ErrReplayerBusy reports that another replay is active.
	ErrReplayerBusy = internal.ErrReplayerBusy
)

// Options controls where a replay writes and what it assembles.
type Options struct {
	// Output is the destination bundle. Empty uses DefaultOutput.
	Output string

	// ProfilerOnly writes only a .gpuprofiler_raw payload. The default returns
	// a self-contained .gputrace containing the original capture and resources.
	ProfilerOnly bool

	// Wait queues behind another replay. The default reports ErrReplayerBusy.
	Wait bool
}

// Available reports whether MTLReplayer is installed.
func Available() error { return internal.Available() }

// Replayable reports whether path contains a capture stream.
func Replayable(path string) error { return internal.Replayable(path) }

// DefaultOutput returns the default profiler output path for in.
func DefaultOutput(in string) string { return internal.DefaultOutput(in) }

// DefaultProfilerOutput returns the default profiler-only path for in.
func DefaultProfilerOutput(in string) string { return internal.DefaultProfilerOutput(in) }

// Profile replays in under the profiler and returns the path it wrote.
func Profile(ctx context.Context, in string, opts Options) (string, error) {
	return internal.Profile(ctx, in, internal.Options{
		Output:       opts.Output,
		ProfilerOnly: opts.ProfilerOnly,
		Wait:         opts.Wait,
	})
}
