package cmd

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunMTLBFunctionsUsesParserSizeMetadata(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "library"), testTaggedMTLB("known_kernel", 4096), 0666); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	var out bytes.Buffer
	err := runMTLBFunctions(dir, mtlbFunctionsOptions{ShowAll: true}, &out)
	if err != nil {
		t.Fatalf("runMTLBFunctions failed: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "known_kernel") {
		t.Fatalf("output missing function name:\n%s", got)
	}
	if !strings.Contains(got, "4.0 KB") {
		t.Fatalf("output missing parsed function size:\n%s", got)
	}
	if strings.Contains(got, "size unknown") {
		t.Fatalf("output reported unknown size despite MDSZ metadata:\n%s", got)
	}
}

func TestRunMTLBFunctionsReportsUnknownSizeWhenMetadataMissing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "library"), testLegacyMTLB("legacy_kernel"), 0666); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	var out bytes.Buffer
	err := runMTLBFunctions(dir, mtlbFunctionsOptions{ShowAll: true}, &out)
	if err != nil {
		t.Fatalf("runMTLBFunctions failed: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "legacy_kernel") {
		t.Fatalf("output missing function name:\n%s", got)
	}
	if !strings.Contains(got, "unknown") {
		t.Fatalf("output should fail closed with unknown size:\n%s", got)
	}
	if !strings.Contains(got, "MTLB metadata did not include a per-function size") {
		t.Fatalf("output missing unknown-size explanation:\n%s", got)
	}
}

func testTaggedMTLB(name string, size uint64) []byte {
	entry := make([]byte, 0)
	entry = append(entry, testTaggedField("NAME", append([]byte(name), 0))...)
	entry = append(entry, testTaggedField("MDSZ", testLittleUint64(size))...)
	entry = append(entry, []byte("ENDT")...)

	data := make([]byte, 56+len(entry))
	copy(data[0:4], []byte("MTLB"))
	binary.LittleEndian.PutUint32(data[4:8], 1)
	binary.LittleEndian.PutUint64(data[16:24], uint64(len(data)))
	binary.LittleEndian.PutUint64(data[24:32], 48)
	binary.LittleEndian.PutUint64(data[32:40], 48)
	binary.LittleEndian.PutUint64(data[40:48], uint64(len(data)))
	binary.LittleEndian.PutUint32(data[48:52], 1)
	binary.LittleEndian.PutUint32(data[52:56], uint32(len(entry)))
	copy(data[56:], entry)
	return data
}

func testLegacyMTLB(name string) []byte {
	payload := append([]byte("NAMED\x00"), []byte(name)...)
	payload = append(payload, 0)

	data := make([]byte, 48+len(payload))
	copy(data[0:4], []byte("MTLB"))
	binary.LittleEndian.PutUint32(data[4:8], 1)
	binary.LittleEndian.PutUint64(data[16:24], uint64(len(data)))
	binary.LittleEndian.PutUint64(data[24:32], 48)
	binary.LittleEndian.PutUint64(data[32:40], 48)
	binary.LittleEndian.PutUint64(data[40:48], uint64(len(data)))
	copy(data[48:], payload)
	return data
}

func testTaggedField(tag string, payload []byte) []byte {
	out := make([]byte, 6+len(payload))
	copy(out[0:4], []byte(tag))
	binary.LittleEndian.PutUint16(out[4:6], uint16(len(payload)))
	copy(out[6:], payload)
	return out
}

func testLittleUint64(v uint64) []byte {
	out := make([]byte, 8)
	binary.LittleEndian.PutUint64(out, v)
	return out
}
