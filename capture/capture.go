// Package capture runs a Metal workload under the GPUToolsCapture interposer.
package capture

import (
	"context"
	"io"

	internal "github.com/tmc/gputrace/internal/capture"
)

// ErrNotInterposable reports that dyld will not load the capture interposer
// into the target executable.
var ErrNotInterposable = internal.ErrNotInterposable

// Options configure a capture run.
type Options struct {
	// Output is the .gputrace bundle to create.
	Output string
	// Dir is the workload's working directory. Empty inherits the caller's.
	Dir string
	// Env adds environment entries in KEY=value form.
	Env []string
	// Stdout and Stderr receive workload output. Nil discards it.
	Stdout io.Writer
	Stderr io.Writer
}

// Eligible reports whether dyld will honor the capture interposer for path.
func Eligible(path string) error { return internal.Eligible(path) }

// Run executes argv under the capture interposer and returns the trace path.
func Run(ctx context.Context, opts Options, argv ...string) (string, error) {
	return internal.Run(ctx, internal.Options{
		Output: opts.Output,
		Dir:    opts.Dir,
		Env:    opts.Env,
		Stdout: opts.Stdout,
		Stderr: opts.Stderr,
	}, argv...)
}
