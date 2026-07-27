package trace

import (
	"encoding/binary"
	"errors"
	"path/filepath"
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

	// The address follows the marker's two padding bytes.
	addrOffset := markerOffset + 8
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

func TestParseDeviceResourcesData(t *testing.T) {
	data := make([]byte, 16)
	copy(data, []byte("MTSP"))
	marker := []byte("CU<b>ulul")
	data = append(data, marker...)
	data = append(data, 0, 0, 0)
	address := uint64(0x12340000)
	var addressBytes [8]byte
	binary.LittleEndian.PutUint64(addressBytes[:], address)
	data = append(data, addressBytes[:]...)
	data = append(data, []byte("MTLBuffer-1-0")...)
	data = append(data, 0, 0, 0, 0, 0)
	var sizeBytes [8]byte
	binary.LittleEndian.PutUint64(sizeBytes[:], 4096)
	data = append(data, sizeBytes[:]...)

	got, err := ParseDeviceResourcesData("unused-device-resources-0xabc", data)
	if err != nil {
		t.Fatal(err)
	}
	if got.DeviceAddress != 0xabc {
		t.Fatalf("device address = 0x%x, want 0xabc", got.DeviceAddress)
	}
	if len(got.Resources) != 1 {
		t.Fatalf("resources = %d, want 1", len(got.Resources))
	}
	resource := got.Resources[0]
	if resource.Type != "buffer" || resource.Label != "MTLBuffer-1-0" || resource.Address != address || resource.Size != 4096 {
		t.Fatalf("resource = %+v", resource)
	}
	if len(got.OptimizationSuggestions) != 1 {
		t.Fatalf("suggestions = %d, want 1", len(got.OptimizationSuggestions))
	}
}

func TestParseDeviceResourcesDataRejectsInvalidMagic(t *testing.T) {
	_, err := ParseDeviceResourcesData("device-resources-0xabc", []byte("nope"))
	if !errors.Is(err, ErrInvalidMagic) {
		t.Fatalf("err = %v, want invalid magic", err)
	}
}

func TestParseDeviceResourcesRealSidecar(t *testing.T) {
	path := realDeviceResourcesPath()
	got, err := ParseDeviceResources(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.DeviceAddress != 0x997088000 {
		t.Fatalf("device address = %#x, want %#x", got.DeviceAddress, uint64(0x997088000))
	}
	if len(got.Resources) == 0 {
		t.Fatal("real sidecar yielded no resource schemas")
	}
	for _, want := range []string{"buffers", "compute-pipeline-states", "textures"} {
		found := false
		for _, resource := range got.Resources {
			if resource.Type == "schema" && resource.Label == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("resource schema %q not found in %+v", want, got.Resources)
		}
	}
}

func BenchmarkParseDeviceResources(b *testing.B) {
	path := realDeviceResourcesPath()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := ParseDeviceResources(path); err != nil {
			b.Fatal(err)
		}
	}
}

func realDeviceResourcesPath() string {
	return filepath.Join("..", "..", "testdata", "traces", "06-six-encoders", "06-six-encoders-run1.gputrace", "device-resources-0x997088000")
}
