package perfettosql

import (
	_ "embed"
	"fmt"
	"io"
)

// Module is the versioned PerfettoSQL projection for gputrace traces.
//
//go:embed module.sql
var Module string

// Write writes Module to w.
func Write(w io.Writer) error {
	if _, err := io.WriteString(w, Module); err != nil {
		return fmt.Errorf("write PerfettoSQL module: %w", err)
	}
	return nil
}
