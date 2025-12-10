package mtlb

import (
	"encoding/binary"
	"testing"
)

func TestParseMTLB(t *testing.T) {
	// Create a dummy MTLB file
	// Header is 48 bytes
	data := make([]byte, 100)

	copy(data[0:4], []byte("MTLB"))
	binary.LittleEndian.PutUint32(data[4:8], 1) // Version
	binary.LittleEndian.PutUint64(data[16:24], 100) // TotalSize

	// String table at offset 48
	binary.LittleEndian.PutUint64(data[32:40], 48) // StringTable
	binary.LittleEndian.PutUint64(data[40:48], 80) // BytecodeOffset

	// Add some strings
	copy(data[48:], []byte("function1\x00function2\x00"))

	lib, err := ParseMTLB(data)
	if err != nil {
		t.Fatalf("ParseMTLB failed: %v", err)
	}

	if lib.Header.Version != 1 {
		t.Errorf("Expected version 1, got %d", lib.Header.Version)
	}

	funcs, err := lib.ListFunctions()
	if err != nil {
		t.Fatalf("ListFunctions failed: %v", err)
	}

	if len(funcs) != 2 {
		t.Errorf("Expected 2 functions, got %d", len(funcs))
	}

	if len(funcs) > 0 && funcs[0] != "function1" {
		t.Errorf("Expected function1, got %s", funcs[0])
	}
	if len(funcs) > 1 && funcs[1] != "function2" {
		t.Errorf("Expected function2, got %s", funcs[1])
	}
}
