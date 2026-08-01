package gputrace

import (
	"io"

	"github.com/tmc/gputrace/internal/command"
	"github.com/tmc/gputrace/internal/trace"
)

// IsLibraryUUID reports whether label identifies a Metal library rather than a
// function.
func IsLibraryUUID(label string) bool {
	return trace.IsLibraryUUID(label)
}

// IsArchiveFunctionName reports whether name identifies a function only by the
// shader archive it came from. A capture records an archive's content id where
// it records a function name for a library the capture describes, so such a
// kernel has a distinct, stable identity but no readable name. Only the
// profiler's streamData carries the name.
func IsArchiveFunctionName(name string) bool {
	return trace.IsArchiveFunctionName(name)
}

// ParseDetailedCommandBuffer parses command buffer cbIndex from t.
//
// It reads and rescans the whole capture file on every call. Use OpenCapture
// when walking more than one command buffer.
func ParseDetailedCommandBuffer(t *Trace, cbIndex int) (*command.DetailedCommandBuffer, error) {
	return command.ParseDetailedCommandBuffer(t, cbIndex)
}

// Capture holds a capture file and its command-buffer index for repeated
// detailed parses.
type Capture = command.Capture

// OpenCapture reads t's capture file and command-buffer index once, so that
// walking every command buffer does not reread the file per buffer.
func OpenCapture(t *Trace) (*Capture, error) {
	return command.OpenCapture(t)
}

// DumpCommandBuffer writes command buffer cbIndex from t to w.
func DumpCommandBuffer(t *Trace, w io.Writer, cbIndex int) error {
	return command.DumpCommandBuffer(t, w, cbIndex)
}
