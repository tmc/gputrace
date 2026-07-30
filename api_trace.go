package gputrace

import (
	"io"

	"github.com/tmc/gputrace/internal/command"
)

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
