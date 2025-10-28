package gputrace_test

import (
	"fmt"
	"log"

	"github.com/tmc/mlx-go/experiments/gputrace"
)

// ExampleValidate demonstrates trace validation.
func ExampleValidate() {
	// Validate a trace file
	result, err := gputrace.Validate("testdata/sample.gputrace")
	if err != nil {
		log.Fatal(err)
	}

	// Print validation report
	fmt.Println(result.String())

	if !result.Valid {
		fmt.Println("Trace is invalid, cannot proceed")
		return
	}

	fmt.Printf("Found %d kernels and %d encoders\n",
		result.Info.NumKernels, result.Info.NumEncoders)
}

// ExampleTimingExtractorV2 demonstrates improved timing extraction.
func ExampleTimingExtractorV2() {
	// Open trace
	trace, err := gputrace.Open("testdata/sample.gputrace")
	if err != nil {
		log.Fatal(err)
	}

	// Create timing extractor
	extractor := gputrace.NewTimingExtractor(trace)

	// Extract timing with multiple strategies
	timings, err := extractor.ExtractTimingV2()
	if err != nil {
		log.Fatal(err)
	}

	// Generate detailed report
	report := extractor.ImprovedTimingReport(timings)
	fmt.Println(report)
}

// ExampleTrace_ParseMTSPRecords demonstrates MTSP record parsing.
func ExampleTrace_ParseMTSPRecords() {
	trace, err := gputrace.Open("testdata/sample.gputrace")
	if err != nil {
		log.Fatal(err)
	}

	// Parse MTSP records
	records, err := trace.ParseMTSPRecords()
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Found %d MTSP records\n", len(records))

	// Filter for CS (kernel submission) records
	kernelRecords := 0
	for _, record := range records {
		if record.Type == "CS" && record.Label != "" {
			fmt.Printf("Kernel: %s (offset=0x%x, size=%d)\n",
				record.Label, record.Offset, record.Size)
			kernelRecords++
		}
	}

	fmt.Printf("Total kernel records: %d\n", kernelRecords)
}

// ExampleEnhancedMetadata demonstrates detailed metadata extraction.
func ExampleEnhancedMetadata() {
	trace, err := gputrace.Open("testdata/sample.gputrace")
	if err != nil {
		log.Fatal(err)
	}

	// Extract enhanced metadata
	meta, err := trace.ExtractEnhancedMetadata()
	if err != nil {
		log.Fatal(err)
	}

	// Print summary
	fmt.Printf("Command Buffers: %d\n", len(meta.CommandBuffers))
	fmt.Printf("Encoders: %d\n", len(meta.Encoders))
	fmt.Printf("Total Kernels: %d\n", meta.TotalKernels)

	// Print buffer information
	var totalMemory uint64
	for _, buf := range meta.BufferBindings {
		totalMemory += buf.Size
	}
	fmt.Printf("Buffers: %d (%.2f MB total)\n",
		len(meta.BufferBindings),
		float64(totalMemory)/(1024*1024))

	// Show largest buffers
	fmt.Println("\nLargest Buffers:")
	// Sort and show top 5...
}

// Example_completePipeline demonstrates a complete analysis pipeline.
func Example_completePipeline() {
	fmt.Println("=== GPU Trace Analysis Pipeline ===")

	tracePath := "testdata/sample.gputrace"

	// Step 1: Quick validation
	fmt.Println("Step 1: Quick Validation")
	if err := gputrace.QuickValidate(tracePath); err != nil {
		log.Fatal("Validation failed:", err)
	}
	fmt.Println("✅ Trace format is valid")

	// Step 2: Open and parse
	fmt.Println("Step 2: Opening Trace")
	trace, err := gputrace.Open(tracePath)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("✅ Opened: %d kernels, %d encoders\n\n", len(trace.KernelNames), len(trace.EncoderLabels))

	// Step 3: Extract timing
	fmt.Println("Step 3: Extracting Timing")
	extractor := gputrace.NewTimingExtractor(trace)
	timings, err := extractor.ExtractTimingV2()
	if err != nil {
		log.Printf("Warning: %v", err)
	}
	fmt.Printf("✅ Extracted %d timing entries\n\n", len(timings))

	// Step 4: Generate reports
	fmt.Println("Step 4: Generating Reports")

	// Timing report
	timingReport := extractor.ImprovedTimingReport(timings)
	fmt.Println(timingReport)

	// Structure analysis
	structReport := trace.AnalyzeTraceStructure()
	fmt.Println(structReport)

	// Step 5: Convert to pprof
	fmt.Println("Step 5: Converting to Pprof")
	prof, err := trace.ToPprof(timings)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("✅ Generated pprof with %d samples\n", len(prof.Sample))

	fmt.Println("\n=== Analysis Complete ===")
}

// Example_bufferAnalysis demonstrates buffer usage analysis.
func Example_bufferAnalysis() {
	trace, err := gputrace.Open("testdata/sample.gputrace")
	if err != nil {
		log.Fatal(err)
	}

	meta, err := trace.ExtractEnhancedMetadata()
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("=== Buffer Analysis ===")

	// Calculate size distribution
	sizeRanges := map[string]struct {
		count int
		total uint64
	}{
		"<1KB":    {},
		"1KB-1MB": {},
		"1MB-10MB": {},
		">10MB":   {},
	}

	for _, buf := range meta.BufferBindings {
		size := buf.Size
		switch {
		case size < 1024:
			r := sizeRanges["<1KB"]
			r.count++
			r.total += size
			sizeRanges["<1KB"] = r
		case size < 1024*1024:
			r := sizeRanges["1KB-1MB"]
			r.count++
			r.total += size
			sizeRanges["1KB-1MB"] = r
		case size < 10*1024*1024:
			r := sizeRanges["1MB-10MB"]
			r.count++
			r.total += size
			sizeRanges["1MB-10MB"] = r
		default:
			r := sizeRanges[">10MB"]
			r.count++
			r.total += size
			sizeRanges[">10MB"] = r
		}
	}

	// Print distribution
	fmt.Println("Buffer Size Distribution:")
	for _, rangeLabel := range []string{"<1KB", "1KB-1MB", "1MB-10MB", ">10MB"} {
		r := sizeRanges[rangeLabel]
		if r.count > 0 {
			fmt.Printf("  %-10s: %4d buffers, %.2f MB total\n",
				rangeLabel, r.count, float64(r.total)/(1024*1024))
		}
	}
}

// Example_errorHandling demonstrates robust error handling.
func Example_errorHandling() {
	tracePath := "nonexistent.gputrace"

	// Try quick validation first
	if err := gputrace.QuickValidate(tracePath); err != nil {
		fmt.Printf("Quick validation failed: %v\n", err)
		// Continue with full validation for detailed report
	}

	// Full validation
	result, err := gputrace.Validate(tracePath)
	if err != nil {
		fmt.Printf("Validation error: %v\n", err)
		return
	}

	if !result.Valid {
		fmt.Println("Trace is invalid:")
		for _, err := range result.Errors {
			fmt.Printf("  - %v\n", err)
		}
		return
	}

	// If valid, proceed with analysis
	trace, err := gputrace.Open(tracePath)
	if err != nil {
		fmt.Printf("Open failed: %v\n", err)
		return
	}

	_ = trace // Use the trace...
}
