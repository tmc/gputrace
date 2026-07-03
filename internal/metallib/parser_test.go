package metallib

import (
	"encoding/binary"
	"reflect"
	"testing"
)

func TestParseMTLB(t *testing.T) {
	// Create a dummy MTLB file
	// Header is 48 bytes
	data := make([]byte, 150)

	copy(data[0:4], []byte("MTLB"))
	binary.LittleEndian.PutUint32(data[4:8], 1)     // Version
	binary.LittleEndian.PutUint64(data[16:24], 150) // TotalSize

	// Function table at offset 48 (where ListFunctions starts scanning)
	binary.LittleEndian.PutUint64(data[24:32], 48)  // FunctionTable
	binary.LittleEndian.PutUint64(data[32:40], 48)  // StringTable
	binary.LittleEndian.PutUint64(data[40:48], 120) // BytecodeOffset

	// Add function entries with NAMED tags (ListFunctions looks for these)
	// Format: "NAMED\x00" + function_name + "\x00"
	offset := 48
	copy(data[offset:], []byte("NAMED\x00function1\x00"))
	offset += 6 + 10 // "NAMED\x00" (6) + "function1\x00" (10)
	copy(data[offset:], []byte("NAMED\x00function2\x00"))

	lib, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
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

func TestListFunctionMetadataTaggedTable(t *testing.T) {
	data := buildTaggedMTLBForTest(
		taggedFunctionForTest("tiny_add", 0, 4096, 0),
		taggedFunctionForTest("tiny_mul", 2048, 8192, 0),
	)

	lib, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	funcs, err := lib.ListFunctionMetadata()
	if err != nil {
		t.Fatalf("ListFunctionMetadata failed: %v", err)
	}
	if len(funcs) != 2 {
		t.Fatalf("Expected 2 functions, got %d", len(funcs))
	}

	if funcs[0].Name != "tiny_add" || !funcs[0].SizeKnown || funcs[0].Size != 4096 {
		t.Fatalf("First function metadata = %+v", funcs[0])
	}
	if funcs[1].Name != "tiny_mul" || funcs[1].Offset != 2048 || !funcs[1].SizeKnown || funcs[1].Size != 8192 {
		t.Fatalf("Second function metadata = %+v", funcs[1])
	}

	names, err := lib.ListFunctions()
	if err != nil {
		t.Fatalf("ListFunctions failed: %v", err)
	}
	if want := []string{"tiny_add", "tiny_mul"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("ListFunctions = %v, want %v", names, want)
	}
}

func TestListFunctionMetadataFallsBackAfterZeroTaggedCount(t *testing.T) {
	name := "legacy_after_zero_prefix"
	payload := append(make([]byte, 8), []byte("NAMED\x00")...)
	payload = append(payload, []byte(name)...)
	payload = append(payload, 0)

	data := make([]byte, 48+len(payload))
	copy(data[0:4], []byte("MTLB"))
	binary.LittleEndian.PutUint32(data[4:8], 1)
	binary.LittleEndian.PutUint64(data[16:24], uint64(len(data)))
	binary.LittleEndian.PutUint64(data[24:32], 48)
	binary.LittleEndian.PutUint64(data[32:40], 48)
	binary.LittleEndian.PutUint64(data[40:48], uint64(len(data)))
	copy(data[48:], payload)

	lib, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	funcs, err := lib.ListFunctions()
	if err != nil {
		t.Fatalf("ListFunctions failed: %v", err)
	}
	if want := []string{name}; !reflect.DeepEqual(funcs, want) {
		t.Fatalf("ListFunctions = %v, want %v", funcs, want)
	}
}

func buildTaggedMTLBForTest(entries ...[]byte) []byte {
	tableLen := 8
	for i := range entries {
		if i+1 < len(entries) {
			entries[i] = append(entries[i], littleUint32ForTest(uint32(len(entries[i+1])))...)
		}
		tableLen += len(entries[i])
	}

	data := make([]byte, 48+tableLen)
	copy(data[0:4], []byte("MTLB"))
	binary.LittleEndian.PutUint32(data[4:8], 1)
	binary.LittleEndian.PutUint64(data[16:24], uint64(len(data)))
	binary.LittleEndian.PutUint64(data[24:32], 48)
	binary.LittleEndian.PutUint64(data[32:40], 48)
	binary.LittleEndian.PutUint64(data[40:48], uint64(len(data)))

	pos := 48
	binary.LittleEndian.PutUint32(data[pos:pos+4], uint32(len(entries)))
	binary.LittleEndian.PutUint32(data[pos+4:pos+8], uint32(len(entries[0])))
	pos += 8
	for _, entry := range entries {
		copy(data[pos:], entry)
		pos += len(entry)
	}

	return data
}

func taggedFunctionForTest(name string, offset, size uint64, nextEntrySize uint32) []byte {
	entry := make([]byte, 0)
	entry = append(entry, taggedFieldForTest("NAME", append([]byte(name), 0))...)
	entry = append(entry, taggedFieldForTest("OFFT", littleUint64ForTest(offset))...)
	entry = append(entry, taggedFieldForTest("MDSZ", littleUint64ForTest(size))...)
	entry = append(entry, []byte("ENDT")...)
	if nextEntrySize > 0 {
		entry = append(entry, littleUint32ForTest(nextEntrySize)...)
	}
	return entry
}

func taggedFieldForTest(tag string, payload []byte) []byte {
	out := make([]byte, 6+len(payload))
	copy(out[0:4], []byte(tag))
	binary.LittleEndian.PutUint16(out[4:6], uint16(len(payload)))
	copy(out[6:], payload)
	return out
}

func littleUint64ForTest(v uint64) []byte {
	out := make([]byte, 8)
	binary.LittleEndian.PutUint64(out, v)
	return out
}

func littleUint32ForTest(v uint32) []byte {
	out := make([]byte, 4)
	binary.LittleEndian.PutUint32(out, v)
	return out
}
