package main

import (
	"encoding/binary"
	"fmt"
	"log"
	"math"
	"os"
	"sort"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatalf("Usage: %s <profiling_file.raw>", os.Args[0])
	}

	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		log.Fatalf("Failed to read file: %v", err)
	}

	// Extract all float32 values in occupancy range
	valueCounts := make(map[float32]int)

	for i := 0; i < len(data)-4; i += 4 {
		bits := binary.LittleEndian.Uint32(data[i : i+4])
		val := math.Float32frombits(bits)

		if val >= 0.01 && val <= 1.0 && !math.IsNaN(float64(val)) && !math.IsInf(float64(val), 0) {
			valueCounts[val]++
		}
	}

	// Sort by frequency
	type valFreq struct {
		value float32
		count int
	}

	var sorted []valFreq
	for v, c := range valueCounts {
		sorted = append(sorted, valFreq{v, c})
	}

	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].count > sorted[j].count
	})

	fmt.Printf("=== Float32 Value Frequency Analysis ===\n\n")
	fmt.Printf("File: %s\n", os.Args[1])
	fmt.Printf("Total unique values: %d\n\n", len(sorted))

	fmt.Printf("Top 20 most frequent values:\n")
	fmt.Printf("%-12s %10s %10s\n", "Value", "Count", "Percent")
	fmt.Printf("%s\n", repeatStr("-", 35))

	totalSamples := 0
	for _, vf := range sorted {
		totalSamples += vf.count
	}

	for i := 0; i < len(sorted) && i < 20; i++ {
		vf := sorted[i]
		percent := float64(vf.count) / float64(totalSamples) * 100
		fmt.Printf("%-12.6f %10d %9.2f%%\n", vf.value, vf.count, percent)
	}

	fmt.Printf("\nBottom 20 least frequent values:\n")
	fmt.Printf("%-12s %10s\n", "Value", "Count")
	fmt.Printf("%s\n", repeatStr("-", 25))

	start := len(sorted) - 20
	if start < 0 {
		start = 0
	}
	for i := start; i < len(sorted); i++ {
		vf := sorted[i]
		fmt.Printf("%-12.6f %10d\n", vf.value, vf.count)
	}
}

func repeatStr(s string, n int) string {
	result := ""
	for i := 0; i < n; i++ {
		result += s
	}
	return result
}
