package trace

import (
	"encoding/binary"
	"testing"
)

func TestParseCtRecord(t *testing.T) {
	// Construct a synthetic Ct record
	// Header (RecordSize=0, Flags=0) - assuming first 8 bytes.
	// We need 64 bytes total for minimum size check + marker.

	// Format based on mtsp.go implementation:
	// ... "Ct\0\0" (marker)
	// +4: PipelineAddr (8 bytes)
	// +12: FunctionAddr (8 bytes)
	// +20: BindingCount (4 bytes)
	// +24: Stride (4 bytes) - MUST be 8
	// +28: Bindings...

	data := make([]byte, 100)

	// Create marker "Ct\0\0" at offset 16
	markerOffset := 16
	copy(data[markerOffset:], []byte("Ct\000\000"))

	base := markerOffset
	pipelineAddr := uint64(0x1122334455667788)
	functionAddr := uint64(0x8877665544332211)
	bindingCount := uint32(2)
	stride := uint32(8)

	binary.LittleEndian.PutUint64(data[base+4:], pipelineAddr)
	binary.LittleEndian.PutUint64(data[base+12:], functionAddr)
	binary.LittleEndian.PutUint32(data[base+20:], bindingCount)
	binary.LittleEndian.PutUint32(data[base+24:], stride)

	// Bindings at base+28
	binding1 := uint64(0xAABBCCDDEEFF0011)
	binding2 := uint64(0x1100FFEEDDCCBBAA)
	binary.LittleEndian.PutUint64(data[base+28:], binding1)
	binary.LittleEndian.PutUint64(data[base+36:], binding2)

	rec := MTSPRecord{
		Type: RecordTypeCt,
		Data: data,
	}

	ct, err := rec.ParseCtRecord()
	if err != nil {
		t.Fatalf("ParseCtRecord failed: %v", err)
	}

	if ct.PipelineAddr != pipelineAddr {
		t.Errorf("expected PipelineAddr 0x%x, got 0x%x", pipelineAddr, ct.PipelineAddr)
	}
	if ct.FunctionAddr != functionAddr {
		t.Errorf("expected FunctionAddr 0x%x, got 0x%x", functionAddr, ct.FunctionAddr)
	}
	if ct.BindingCount != bindingCount {
		t.Errorf("expected BindingCount %d, got %d", bindingCount, ct.BindingCount)
	}
	if len(ct.BufferBindings) != int(bindingCount) {
		t.Errorf("expected %d bindings, got %d", bindingCount, len(ct.BufferBindings))
	}
	if ct.BufferBindings[0] != binding1 {
		t.Errorf("expected binding[0] 0x%x, got 0x%x", binding1, ct.BufferBindings[0])
	}
}

func TestParseCSuwuwRecord(t *testing.T) {
	// CSuwuw record: Label extraction
	// ... [CSuwuw] (6 bytes) [pad] ... [address] [string]

	data := make([]byte, 100)

	markerOffset := 10
	copy(data[markerOffset:], []byte("CSuwuw"))

	// Based on implementation line 393: addressStart := i + 9
	addrOffset := markerOffset + 9
	funcAddr := uint64(0xCAFEBABE112233)
	binary.LittleEndian.PutUint64(data[addrOffset:], funcAddr)

	// String follows address (addrOffset + 8), skipping nulls
	stringStart := addrOffset + 8
	// Add some nulls
	data[stringStart] = 0
	data[stringStart+1] = 0

	label := "MyKernelFunc"
	copy(data[stringStart+2:], []byte(label))
	data[stringStart+2+len(label)] = 0 // Null terminator

	rec := MTSPRecord{
		Type: RecordTypeCSuwuw,
		Data: data,
	}

	rec.parseCSuwuwRecord()

	if rec.Address != funcAddr {
		t.Errorf("expected Address 0x%x, got 0x%x", funcAddr, rec.Address)
	}
	if rec.Label != label {
		t.Errorf("expected Label %q, got %q", label, rec.Label)
	}
}

func TestParseCiulSlRecord(t *testing.T) {
	// CiulSl record: Function Address
	// "CiulSl" at some offset
	// +8: Address (8 bytes)

	data := make([]byte, 64)
	offset := 10
	copy(data[offset:], []byte("CiulSl"))
	funcAddr := uint64(0xDEADBEEF)
	binary.LittleEndian.PutUint64(data[offset+8:], funcAddr)

	rec := MTSPRecord{
		Type: RecordTypeCiulSl,
		Data: data,
	}

	// This method updates rec.FunctionAddr in place
	rec.parseCiulSlRecord()

	if rec.FunctionAddr != funcAddr {
		t.Errorf("expected FunctionAddr 0x%x, got 0x%x", funcAddr, rec.FunctionAddr)
	}
}
