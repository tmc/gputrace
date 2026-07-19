package gputrace

import (
	"io"

	"github.com/tmc/gputrace/internal/command"
)

// ParseDetailedCommandBuffer parses command buffer cbIndex from t.
func ParseDetailedCommandBuffer(t *Trace, cbIndex int) (*command.DetailedCommandBuffer, error) {
	return command.ParseDetailedCommandBuffer(t, cbIndex)
}

// DumpCommandBuffer writes command buffer cbIndex from t to w.
func DumpCommandBuffer(t *Trace, w io.Writer, cbIndex int) error {
	return command.DumpCommandBuffer(t, w, cbIndex)
}
