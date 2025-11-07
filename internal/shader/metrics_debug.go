package shader

import (
	"fmt"
	"os"
)

func init() {
	// Debug flag to enable verbose logging
	if os.Getenv("GPUTRACE_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "Debug mode enabled\n")
	}
}
