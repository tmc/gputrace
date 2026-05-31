package trace

import (
	"encoding/binary"
	"os"
	"testing"
)

func TestParseCSRecordsSyntheticFixture(t *testing.T) {
	const (
		kernelAddr = uint64(0xafcc88580)
		uuidAddr   = uint64(0xafcc88600)
	)

	tr := &Trace{
		CaptureData: syntheticCSCapture(
			csFixtureRecord{Address: kernelAddr, Identifier: "vv_Addfloat32"},
			csFixtureRecord{Address: uuidAddr, Identifier: "01234567-89ab-cdef-0123-456789abcdef"},
		),
	}

	records, err := tr.ParseCSRecords()
	if err != nil {
		t.Fatalf("ParseCSRecords failed: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("got %d CS records, want 2: %#v", len(records), records)
	}

	if records[0].Offset != 7 {
		t.Fatalf("first offset = %d, want 7", records[0].Offset)
	}
	if records[0].Address != kernelAddr {
		t.Fatalf("first address = 0x%x, want 0x%x", records[0].Address, kernelAddr)
	}
	if records[0].Identifier != "vv_Addfloat32" {
		t.Fatalf("first identifier = %q", records[0].Identifier)
	}
	if !records[0].IsKernelName {
		t.Fatalf("first record IsKernelName = false, want true")
	}

	if records[1].Address != uuidAddr {
		t.Fatalf("second address = 0x%x, want 0x%x", records[1].Address, uuidAddr)
	}
	if records[1].Identifier != "01234567-89ab-cdef-0123-456789abcdef" {
		t.Fatalf("second identifier = %q", records[1].Identifier)
	}
	if records[1].IsKernelName {
		t.Fatalf("second record IsKernelName = true, want false")
	}
}

func TestCSRecordHelpersSyntheticFixture(t *testing.T) {
	tr := &Trace{
		CaptureData: syntheticCSCapture(
			csFixtureRecord{Address: 0x1000, Identifier: "block_softmax_float32"},
			csFixtureRecord{Address: 0x2000, Identifier: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"},
			csFixtureRecord{Address: 0x3000, Identifier: "g3_copyfloat32float32"},
		),
	}

	kernels, err := tr.GetKernelNameCSRecords()
	if err != nil {
		t.Fatalf("GetKernelNameCSRecords failed: %v", err)
	}
	if got, want := len(kernels), 2; got != want {
		t.Fatalf("kernel record count = %d, want %d", got, want)
	}
	if kernels[0].Identifier != "block_softmax_float32" || kernels[1].Identifier != "g3_copyfloat32float32" {
		t.Fatalf("kernel identifiers = %q, %q", kernels[0].Identifier, kernels[1].Identifier)
	}

	uuids, err := tr.GetUUIDCSRecords()
	if err != nil {
		t.Fatalf("GetUUIDCSRecords failed: %v", err)
	}
	if got, want := len(uuids), 1; got != want {
		t.Fatalf("UUID record count = %d, want %d", got, want)
	}
	if uuids[0].Identifier != "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" {
		t.Fatalf("UUID identifier = %q", uuids[0].Identifier)
	}

	count, err := tr.CountCSRecords()
	if err != nil {
		t.Fatalf("CountCSRecords failed: %v", err)
	}
	if got, want := count, 3; got != want {
		t.Fatalf("CS record count = %d, want %d", got, want)
	}
}

func TestParseCSRecordsSkipsInvalidSyntheticRecords(t *testing.T) {
	capture := syntheticCSCapture(csFixtureRecord{Address: 0x1000, Identifier: "valid_kernel"})

	capture = append(capture, []byte("CS\x00\x00")...)
	capture = binary.LittleEndian.AppendUint64(capture, 0x2000)
	capture = append(capture, 'b', 'a', 'd', 0x01, 0)

	capture = append(capture, []byte("CS\x00\x00")...)
	capture = binary.LittleEndian.AppendUint64(capture, 0x3000)

	tr := &Trace{CaptureData: capture}
	records, err := tr.ParseCSRecords()
	if err != nil {
		t.Fatalf("ParseCSRecords failed: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1: %#v", len(records), records)
	}
	if records[0].Identifier != "valid_kernel" {
		t.Fatalf("identifier = %q, want valid_kernel", records[0].Identifier)
	}
}

func TestParseCSRecordsRealTraceIntegration(t *testing.T) {
	tracePath := os.Getenv("GPUTRACE_CS_TEST_TRACE")
	if tracePath == "" {
		t.Skip("set GPUTRACE_CS_TEST_TRACE to run real-trace CS parser integration test")
	}

	tr, err := Open(tracePath)
	if err != nil {
		t.Fatalf("open trace: %v", err)
	}

	records, err := tr.ParseCSRecords()
	if err != nil {
		t.Fatalf("ParseCSRecords failed: %v", err)
	}
	if len(records) == 0 {
		t.Fatal("expected at least one CS record")
	}

	kernels, err := tr.GetKernelNameCSRecords()
	if err != nil {
		t.Fatalf("GetKernelNameCSRecords failed: %v", err)
	}
	if len(kernels) == 0 {
		t.Fatal("expected at least one kernel-name CS record")
	}
}

type csFixtureRecord struct {
	Address    uint64
	Identifier string
}

func syntheticCSCapture(records ...csFixtureRecord) []byte {
	capture := []byte{0xde, 0xad, 0xbe, 0xef}
	for _, record := range records {
		capture = append(capture, 0xaa, 0xbb, 0xcc)
		capture = append(capture, []byte("CS\x00\x00")...)
		capture = binary.LittleEndian.AppendUint64(capture, record.Address)
		capture = append(capture, record.Identifier...)
		capture = append(capture, 0)
	}
	return capture
}
