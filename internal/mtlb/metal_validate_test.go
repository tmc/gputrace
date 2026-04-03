//go:build darwin

package mtlb

import (
	"os"
	"sort"
	"testing"

	"github.com/tmc/apple/metal"
	"github.com/tmc/apple/objc"
)

// TestMTLBParserAgainstMetal validates that our MTLB parser extracts the same
// function names as Metal's native library loading APIs.
func TestMTLBParserAgainstMetal(t *testing.T) {
	// Path to a real MTLB file from a GPU trace
	mtlbPath := "/tmp/mlx-lm-generate_tokens_8_to_9.gputrace/671438C4BF69309E"

	// Skip if file doesn't exist
	if _, err := os.Stat(mtlbPath); os.IsNotExist(err) {
		t.Skipf("MTLB test file not found: %s", mtlbPath)
	}

	// Read the file
	data, err := os.ReadFile(mtlbPath)
	if err != nil {
		t.Fatalf("Failed to read MTLB file: %v", err)
	}

	// Parse with our parser
	mtlb, err := ParseMTLB(data)
	if err != nil {
		t.Fatalf("ParseMTLB failed: %v", err)
	}

	parserFunctions, err := mtlb.ListFunctions()
	if err != nil {
		t.Fatalf("ListFunctions failed: %v", err)
	}

	t.Logf("Parser found %d functions", len(parserFunctions))

	// Load with Metal API
	metalFunctions, err := loadMTLBWithMetal(data)
	if err != nil {
		t.Fatalf("Failed to load MTLB with Metal: %v", err)
	}

	t.Logf("Metal found %d functions", len(metalFunctions))

	// Sort both lists for comparison
	sort.Strings(parserFunctions)
	sort.Strings(metalFunctions)

	// Compare
	parserSet := make(map[string]bool)
	for _, f := range parserFunctions {
		parserSet[f] = true
	}

	metalSet := make(map[string]bool)
	for _, f := range metalFunctions {
		metalSet[f] = true
	}

	// Find functions in Metal but not in parser
	var missingFromParser []string
	for _, f := range metalFunctions {
		if !parserSet[f] {
			missingFromParser = append(missingFromParser, f)
		}
	}

	// Find functions in parser but not in Metal
	var extraInParser []string
	for _, f := range parserFunctions {
		if !metalSet[f] {
			extraInParser = append(extraInParser, f)
		}
	}

	if len(missingFromParser) > 0 {
		t.Errorf("Functions found by Metal but missing from parser (%d):", len(missingFromParser))
		for i, f := range missingFromParser {
			if i < 10 {
				t.Errorf("  - %s", f)
			}
		}
		if len(missingFromParser) > 10 {
			t.Errorf("  ... and %d more", len(missingFromParser)-10)
		}
	}

	if len(extraInParser) > 0 {
		t.Errorf("Functions found by parser but not by Metal (%d):", len(extraInParser))
		for i, f := range extraInParser {
			if i < 10 {
				t.Errorf("  - %s", f)
			}
		}
		if len(extraInParser) > 10 {
			t.Errorf("  ... and %d more", len(extraInParser)-10)
		}
	}

	// Log match rate
	matchCount := 0
	for _, f := range metalFunctions {
		if parserSet[f] {
			matchCount++
		}
	}
	t.Logf("Match rate: %d/%d (%.1f%%)", matchCount, len(metalFunctions), float64(matchCount)/float64(len(metalFunctions))*100)
}

// loadMTLBWithMetal uses Metal APIs to load an MTLB and extract function names.
func loadMTLBWithMetal(data []byte) ([]string, error) {
	// Get the default Metal device
	device := metal.MTLCreateSystemDefaultDevice()
	if device.GetID() == 0 {
		return nil, nil // No Metal device available
	}

	// Create dispatch_data_t from the MTLB bytes
	// Use NSData as intermediary
	nsDataClass := objc.GetClass("NSData")
	nsData := objc.Send[objc.ID](objc.ID(uintptr(nsDataClass)), objc.Sel("dataWithBytes:length:"), &data[0], uint(len(data)))
	if nsData == 0 {
		return nil, nil
	}

	// Create library from data using objc.Send (to avoid interface return type issues)
	var libErr objc.ID
	libraryID := objc.Send[objc.ID](device.GetID(), objc.Sel("newLibraryWithData:error:"), nsData, &libErr)
	if libraryID == 0 {
		// Library creation failed - might be incompatible with current device
		return nil, nil
	}
	library := metal.MTLLibraryObjectFromID(libraryID)

	// Get function names
	return library.FunctionNames(), nil
}
