package gputrace

import (
	"fmt"
)

// AnalyzeStoreStructure examines the decompressed store data for patterns.
func (t *Trace) AnalyzeStoreStructure() (string, error) {
	data, err := t.DecompressStore(0)
	if err != nil {
		return "", err
	}

	report := "=== store0 Analysis ===\n\n"
	report += fmt.Sprintf("Decompressed size: %d bytes (%.2f MB)\n\n", len(data), float64(len(data))/(1024*1024))

	// Show first 512 bytes as hex
	report += "First 512 bytes (hex):\n"
	dumpSize := 512
	if len(data) < dumpSize {
		dumpSize = len(data)
	}

	for i := 0; i < dumpSize; i += 16 {
		end := i + 16
		if end > dumpSize {
			end = dumpSize
		}

		report += fmt.Sprintf("%08x: ", i)

		// Hex bytes
		for j := i; j < end; j++ {
			report += fmt.Sprintf("%02x ", data[j])
		}

		// Padding
		for j := end; j < i+16; j++ {
			report += "   "
		}

		// ASCII
		report += " |"
		for j := i; j < end; j++ {
			if data[j] >= 32 && data[j] <= 126 {
				report += string(data[j])
			} else {
				report += "."
			}
		}
		report += "|\n"
	}

	// Look for large numbers that might be timestamps
	report += "\n=== Potential Timestamps (values > 1e15) ===\n"
	timestampCount := 0
	for i := 0; i < len(data)-8; i += 8 {
		// Try both little-endian and big-endian
		valLE := uint64(data[i]) | uint64(data[i+1])<<8 | uint64(data[i+2])<<16 | uint64(data[i+3])<<24 |
			uint64(data[i+4])<<32 | uint64(data[i+5])<<40 | uint64(data[i+6])<<48 | uint64(data[i+7])<<56

		// Check if it looks like a Mach timestamp
		if valLE > 1000000000000000 && valLE < 10000000000000000000 {
			if timestampCount < 20 { // Show first 20
				report += fmt.Sprintf("  Offset 0x%08x: %d (0x%016x)\n", i, valLE, valLE)
			}
			timestampCount++
		}
	}

	if timestampCount > 20 {
		report += fmt.Sprintf("  ... and %d more potential timestamps\n", timestampCount-20)
	} else if timestampCount == 0 {
		report += "  No timestamp-like values found\n"
	}

	// Look for small integers that might be kernel indices
	report += "\n=== Potential Kernel Indices (values 0-255) ===\n"
	indexCount := 0
	for i := 0; i < len(data)-4; i += 4 {
		val := uint32(data[i]) | uint32(data[i+1])<<8 | uint32(data[i+2])<<16 | uint32(data[i+3])<<24
		if val < 256 { // Likely kernel index range
			if indexCount < 20 {
				report += fmt.Sprintf("  Offset 0x%08x: %d (0x%02x)\n", i, val, val)
			}
			indexCount++
		}
	}

	if indexCount > 20 {
		report += fmt.Sprintf("  ... and %d more potential indices\n", indexCount-20)
	}

	return report, nil
}
