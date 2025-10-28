// +build ignore

package main

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <path-to-.gputrace>\n", os.Args[0])
		os.Exit(1)
	}

	tracePath := os.Args[1]
	capturePath := filepath.Join(tracePath, "capture")
	data, err := os.ReadFile(capturePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading capture: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Scanning for large 64-bit values that could be timestamps...\n\n")

	var found []struct {
		offset int
		value  uint64
	}

	// Scan every 8-byte aligned position
	for i := 0; i < len(data)-8; i += 8 {
		val := binary.LittleEndian.Uint64(data[i : i+8])

		// Look for values > 1e14 (could be timestamps or durations)
		if val > 100000000000000 {
			found = append(found, struct {
				offset int
				value  uint64
			}{i, val})
		}
	}

	fmt.Printf("Found %d candidates:\n", len(found))
	for i, f := range found {
		if i < 30 { // Show first 30
			fmt.Printf("  0x%06x: %20d (0x%016x)\n", f.offset, f.value, f.value)
		}
	}
	if len(found) > 30 {
		fmt.Printf("  ... and %d more\n", len(found)-30)
	}
}
