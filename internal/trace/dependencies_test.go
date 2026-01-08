package trace

import (
	"encoding/binary"
	"testing"
)

func TestParseDependencyEvents(t *testing.T) {
	// Create minimal MTSP data with Ct records (compute dispatches)
	// Ct marker: 43 74 00 00 ... (Ct\x00\x00)
	// Structure: +0 marker, +4 pipeline_addr, +12 func_addr, +20 binding_count, +24 stride, +28 bindings

	buf := make([]byte, 2048)
	offset := 0

	// Add some padding to avoid false positives
	offset += 16

	// 1. Ct dispatch for "Op1" with one buffer binding
	copy(buf[offset:], []byte{0x43, 0x74, 0x00, 0x00}) // "Ct\x00\x00"
	offset += 4
	binary.LittleEndian.PutUint64(buf[offset:], 0x1000) // pipeline_addr
	offset += 8
	binary.LittleEndian.PutUint64(buf[offset:], 0xAAAA1111) // func_addr for "Op1"
	offset += 8
	binary.LittleEndian.PutUint32(buf[offset:], 1) // binding_count = 1
	offset += 4
	binary.LittleEndian.PutUint32(buf[offset:], 8) // stride = 8
	offset += 4
	binary.LittleEndian.PutUint64(buf[offset:], 0x2000) // buffer binding
	offset += 8

	// Add padding between records
	offset += 32

	// 2. Ct dispatch for "Op2" with one buffer binding
	copy(buf[offset:], []byte{0x43, 0x74, 0x00, 0x00}) // "Ct\x00\x00"
	offset += 4
	binary.LittleEndian.PutUint64(buf[offset:], 0x1000) // pipeline_addr
	offset += 8
	binary.LittleEndian.PutUint64(buf[offset:], 0xBBBB2222) // func_addr for "Op2"
	offset += 8
	binary.LittleEndian.PutUint32(buf[offset:], 1) // binding_count = 1
	offset += 4
	binary.LittleEndian.PutUint32(buf[offset:], 8) // stride = 8
	offset += 4
	binary.LittleEndian.PutUint64(buf[offset:], 0x2000) // same buffer binding (creates dependency)
	offset += 8

	trace := &Trace{
		CaptureData: buf[:offset],
		DeviceLabels: map[uint64]string{
			0xAAAA1111: "Op1",
			0xBBBB2222: "Op2",
		},
	}

	events, err := trace.ParseDependencyEvents()
	if err != nil {
		t.Fatalf("ParseDependencyEvents failed: %v", err)
	}

	// Should have: 2 CS events + 2 Bind events = 4 events
	if len(events) < 4 {
		t.Errorf("Expected at least 4 events, got %d", len(events))
	}

	// Verify we got CS events
	csCount := 0
	bindCount := 0
	for _, ev := range events {
		switch ev.Type {
		case EventCS:
			csCount++
		case EventBind:
			bindCount++
		}
	}

	if csCount < 2 {
		t.Errorf("Expected at least 2 CS events, got %d", csCount)
	}
	if bindCount < 2 {
		t.Errorf("Expected at least 2 Bind events, got %d", bindCount)
	}

	// Test Graph Construction
	graph, err := trace.BuildDependencyGraph()
	if err != nil {
		t.Fatalf("BuildDependencyGraph failed: %v", err)
	}

	if len(graph.Nodes) < 2 {
		t.Errorf("Expected at least 2 nodes, got %d", len(graph.Nodes))
	}

	// Since both ops use the same buffer with ReadWrite access,
	// we should have dependencies (RAW, WAW, or WAR)
	if len(graph.Edges) < 1 {
		t.Errorf("Expected at least 1 edge for shared buffer dependency, got %d", len(graph.Edges))
	}
}

func TestHazardTypes(t *testing.T) {
	// Test that HazardType String() works correctly
	tests := []struct {
		hazard HazardType
		want   string
	}{
		{HazardRAW, "RAW"},
		{HazardWAW, "WAW"},
		{HazardWAR, "WAR"},
		{HazardType(99), "Unknown"},
	}

	for _, tt := range tests {
		got := tt.hazard.String()
		if got != tt.want {
			t.Errorf("HazardType(%d).String() = %q, want %q", tt.hazard, got, tt.want)
		}
	}
}
