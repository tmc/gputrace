// +build ignore

package main

import (
	"encoding/binary"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <path-to-.gputrace>\n", os.Args[0])
		os.Exit(1)
	}

	tracePath := os.Args[1]

	// Read capture file
	capturePath := filepath.Join(tracePath, "capture")
	data, err := os.ReadFile(capturePath)
	if err != nil {
		log.Fatalf("Failed to read capture: %v", err)
	}

	fmt.Printf("=== Trace Analysis: %s ===\n\n", tracePath)
	fmt.Printf("Capture Data Size: %d bytes\n\n", len(data))

	// Find all strings/labels in the data
	labels := findStrings(data)
	fmt.Printf("Found %d potential labels:\n", len(labels))
	for i, label := range labels {
		if i < 20 { // Show first 20
			fmt.Printf("  [%d] %q at offset 0x%x\n", i, label.text, label.offset)
		}
	}
	if len(labels) > 20 {
		fmt.Printf("  ... and %d more\n", len(labels)-20)
	}
	fmt.Println()

	// Analyze each label for timing patterns
	fmt.Println("=== Timestamp Pattern Analysis ===")
	for i, label := range labels {
		if !strings.Contains(label.text, "Stage") && !strings.Contains(label.text, "Label") {
			continue
		}

		fmt.Printf("\n[%d] Analyzing: %q at offset 0x%x (%d)\n", i, label.text, label.offset, label.offset)

		// Show hex dump around label
		fmt.Printf("\nContext (128 bytes before/after):\n")
		dumpContext(data, label.offset, label.text)

		// Scan for timestamps
		fmt.Printf("\nPotential timestamps:\n")
		scanForTimestamps(data, label.offset)
	}
}

type labelInfo struct {
	text   string
	offset int
}

func findStrings(data []byte) []labelInfo {
	var labels []labelInfo

	// Look for null-terminated strings that are printable
	for i := 0; i < len(data)-4; i++ {
		// Skip if not start of potential string
		if data[i] < 32 || data[i] > 126 {
			continue
		}

		// Find end of string
		end := i
		for end < len(data) && end-i < 128 {
			if data[end] == 0 {
				break
			}
			if data[end] < 32 || data[end] > 126 {
				break
			}
			end++
		}

		if end > i && end-i >= 4 && end-i < 128 {
			text := string(data[i:end])
			// Filter for reasonable strings
			if isReasonableLabel(text) {
				labels = append(labels, labelInfo{text: text, offset: i})
				i = end // Skip past this string
			}
		}
	}

	return labels
}

func isReasonableLabel(s string) bool {
	if len(s) < 3 {
		return false
	}

	// Must have some letters
	hasLetter := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			hasLetter = true
			break
		}
	}

	return hasLetter
}

func dumpContext(data []byte, labelOffset int, label string) {
	start := labelOffset - 128
	if start < 0 {
		start = 0
	}
	end := labelOffset + len(label) + 128
	if end > len(data) {
		end = len(data)
	}

	labelBytes := []byte(label)

	for i := start; i < end; i += 16 {
		lineEnd := i + 16
		if lineEnd > end {
			lineEnd = end
		}

		fmt.Printf("  %08x: ", i)

		// Hex bytes
		for j := i; j < lineEnd; j++ {
			// Highlight label bytes
			isLabel := j >= labelOffset && j < labelOffset+len(labelBytes)
			if isLabel {
				fmt.Printf("[%02x]", data[j])
			} else {
				fmt.Printf("%02x ", data[j])
			}
		}

		// Padding
		for j := lineEnd; j < i+16; j++ {
			fmt.Printf("   ")
		}

		// ASCII
		fmt.Printf(" |")
		for j := i; j < lineEnd; j++ {
			if data[j] >= 32 && data[j] <= 126 {
				fmt.Printf("%c", data[j])
			} else {
				fmt.Printf(".")
			}
		}
		fmt.Printf("|\n")
	}
}

func scanForTimestamps(data []byte, labelOffset int) {
	// Mach absolute time is typically > 1e15
	// Scan various offsets from the label

	offsets := []int{
		-256, -240, -224, -208, -192, -176, -160, -144, -128,
		-112, -96, -88, -80, -72, -68, -64, -56, -48, -40, -32,
		-24, -16, -8, 0, 8, 16, 24, 32, 40, 48, 56, 64, 72, 80,
		88, 96, 104, 112, 128, 144, 160, 192, 224, 256,
	}

	for _, delta := range offsets {
		offset := labelOffset + delta
		if offset < 0 || offset+8 > len(data) {
			continue
		}

		val := binary.LittleEndian.Uint64(data[offset : offset+8])

		// Check if it looks like a Mach timestamp
		if val > 1000000000000000 && val < 10000000000000000000 {
			fmt.Printf("  [%+5d] 0x%08x: %20d (0x%016x)\n", delta, offset, val, val)
		}
	}
}
