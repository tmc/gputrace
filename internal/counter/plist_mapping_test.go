//go:build darwin

package counter

import (
	"os"
	"testing"
)

func TestLoadGPUCounterGraph(t *testing.T) {
	// Skip if Xcode not installed
	if _, err := os.Stat(DefaultPlistPath()); os.IsNotExist(err) {
		t.Skip("Xcode not installed, skipping plist test")
	}

	graph, err := LoadGPUCounterGraph()
	if err != nil {
		t.Fatalf("LoadGPUCounterGraph() error = %v", err)
	}

	if graph == nil {
		t.Fatal("LoadGPUCounterGraph() returned nil")
	}

	if len(graph.Counters) == 0 {
		t.Error("LoadGPUCounterGraph() returned no counters")
	}

	t.Logf("Loaded %d counters from GPUCounterGraph.plist", len(graph.Counters))
}

func TestGetCounterMetadata(t *testing.T) {
	if _, err := os.Stat(DefaultPlistPath()); os.IsNotExist(err) {
		t.Skip("Xcode not installed, skipping plist test")
	}

	tests := []struct {
		name     string
		wantType CounterDataType
	}{
		{"ALU Utilization", DataTypePercentage},
		{"Buffer Device Memory Bytes Read", DataTypeBytes},
		{"GPU Write Bandwidth", DataTypeBandwidth},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			meta, ok := CounterMetadataForName(tt.name)
			if !ok {
				t.Errorf("CounterMetadataForName(%q) not found", tt.name)
				return
			}

			if meta.DataType != tt.wantType {
				t.Errorf("CounterMetadataForName(%q).DataType = %v, want %v", tt.name, meta.DataType, tt.wantType)
			}

			t.Logf("%s: datatype=%d, unit=%q, vendors=%v", tt.name, meta.DataType, meta.Unit, meta.VendorCounters)
		})
	}
}

func TestGetVendorCounterNames(t *testing.T) {
	if _, err := os.Stat(DefaultPlistPath()); os.IsNotExist(err) {
		t.Skip("Xcode not installed, skipping plist test")
	}

	vendors := VendorCounterNames("ALU Utilization")
	if len(vendors) == 0 {
		t.Error("VendorCounterNames(\"ALU Utilization\") returned empty")
	}

	t.Logf("ALU Utilization vendor counters: %v", vendors)
}

func TestGetCounterFileMappings(t *testing.T) {
	if _, err := os.Stat(DefaultPlistPath()); os.IsNotExist(err) {
		t.Skip("Xcode not installed, skipping plist test")
	}

	mappings := CounterFileMappings()
	if len(mappings) == 0 {
		t.Error("CounterFileMappings() returned empty")
	}

	// Check a few known mappings
	for _, m := range mappings {
		if m.FileIndex == 12 && m.UserName != "ALU Utilization" {
			t.Errorf("File 12 should be ALU Utilization, got %q", m.UserName)
		}
		if m.FileIndex == 21 && m.DataType != DataTypeBytes {
			t.Errorf("File 21 (Buffer Device Memory Bytes Read) should be DataTypeBytes, got %d", m.DataType)
		}
	}

	t.Logf("Got %d file mappings with metadata", len(mappings))
}

func TestListByteCounters(t *testing.T) {
	if _, err := os.Stat(DefaultPlistPath()); os.IsNotExist(err) {
		t.Skip("Xcode not installed, skipping plist test")
	}

	byteCounters := ListByteCounters()
	if len(byteCounters) == 0 {
		t.Error("ListByteCounters() returned empty")
	}

	t.Logf("Found %d byte counters", len(byteCounters))
	count := 5
	if len(byteCounters) < count {
		count = len(byteCounters)
	}
	for _, name := range byteCounters[:count] {
		t.Logf("  - %s", name)
	}
}
