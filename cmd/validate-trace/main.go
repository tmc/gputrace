// Command validate-trace validates .gputrace files.
//
// This tool performs comprehensive validation of Metal GPU trace files,
// checking for required files, correct format, and data integrity.
//
// Usage:
//
//	validate-trace <trace.gputrace>
//
// Example:
//
//	validate-trace benchmark.gputrace
package main

import (
	"fmt"
	"log"
	"os"

	"github.com/tmc/mlx-go/experiments/gputrace"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <trace.gputrace>\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "\nValidates Metal GPU trace files.\n")
		fmt.Fprintf(os.Stderr, "\nExample:\n")
		fmt.Fprintf(os.Stderr, "  %s benchmark.gputrace\n", os.Args[0])
		os.Exit(1)
	}

	tracePath := os.Args[1]

	// Perform validation
	result, err := gputrace.Validate(tracePath)
	if err != nil {
		log.Fatalf("Validation error: %v", err)
	}

	// Print report
	fmt.Println(result.String())

	// Exit with error code if invalid
	if !result.Valid {
		os.Exit(1)
	}
}
