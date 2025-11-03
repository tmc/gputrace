package gputrace

import (
	"encoding/binary"
	"testing"
)

// TestParseCtRecord tests the CtRecord parser with the example from the spec.
func TestParseCtRecord(t *testing.T) {
	// Real example from trace (120-byte Ct record from spec):
	// Record #2 with 7 buffer bindings
	data := make([]byte, 120)

	// Header
	binary.LittleEndian.PutUint32(data[0x00:0x04], 120)              // record_size
	binary.LittleEndian.PutUint32(data[0x04:0x08], 0xffffc02f)       // command_flags

	// Reserved section (0x08-0x20) stays zero

	// Markers
	binary.LittleEndian.PutUint32(data[0x20:0x24], 0x00000008)       // marker1
	copy(data[0x24:0x28], []byte{'C', 't', 0, 0})                    // marker2

	// Addresses and counts
	binary.LittleEndian.PutUint64(data[0x28:0x30], 0x0b94c3dae0)     // pipeline_addr
	binary.LittleEndian.PutUint64(data[0x30:0x38], 0x0100f93c00)     // function_addr
	binary.LittleEndian.PutUint32(data[0x38:0x3c], 7)                // binding_count
	binary.LittleEndian.PutUint32(data[0x3c:0x40], 8)                // stride

	// Buffer bindings (7 addresses)
	buffers := []uint64{
		0x010dcff59c,
		0x010dcee01c,
		0x010d29b854,
		0x010d29c388,
		0x010bf3c29c,
		0x010036b06c,
		0x010017ce3c,
	}

	for i, addr := range buffers {
		offset := 0x40 + (i * 8)
		binary.LittleEndian.PutUint64(data[offset:offset+8], addr)
	}

	// Create MTSPRecord wrapper
	record := &MTSPRecord{
		Type:   RecordTypeCt,
		Offset: 0,
		Size:   120,
		Data:   data,
	}

	// Parse the record
	ct, err := record.ParseCtRecord()
	if err != nil {
		t.Fatalf("ParseCtRecord failed: %v", err)
	}

	// Validate fields
	if ct.RecordSize != 120 {
		t.Errorf("RecordSize = %d, want 120", ct.RecordSize)
	}

	if ct.CommandFlags != 0xffffc02f {
		t.Errorf("CommandFlags = 0x%08x, want 0xffffc02f", ct.CommandFlags)
	}

	if ct.PipelineAddr != 0x0b94c3dae0 {
		t.Errorf("PipelineAddr = 0x%016x, want 0x0b94c3dae0", ct.PipelineAddr)
	}

	if ct.FunctionAddr != 0x0100f93c00 {
		t.Errorf("FunctionAddr = 0x%016x, want 0x0100f93c00", ct.FunctionAddr)
	}

	if ct.BindingCount != 7 {
		t.Errorf("BindingCount = %d, want 7", ct.BindingCount)
	}

	if ct.Stride != 8 {
		t.Errorf("Stride = %d, want 8", ct.Stride)
	}

	if len(ct.BufferBindings) != 7 {
		t.Fatalf("BufferBindings length = %d, want 7", len(ct.BufferBindings))
	}

	// Validate each buffer binding
	for i, expected := range buffers {
		if ct.BufferBindings[i] != expected {
			t.Errorf("BufferBindings[%d] = 0x%016x, want 0x%016x",
				i, ct.BufferBindings[i], expected)
		}
	}
}

// TestParseCtRecord_WrongType tests error handling for non-Ct records.
func TestParseCtRecord_WrongType(t *testing.T) {
	record := &MTSPRecord{
		Type:   RecordTypeCS,
		Offset: 0,
		Size:   120,
		Data:   make([]byte, 120),
	}

	_, err := record.ParseCtRecord()
	if err == nil {
		t.Error("ParseCtRecord should fail for non-Ct record")
	}
}

// TestParseCtRecord_TooSmall tests error handling for undersized records.
func TestParseCtRecord_TooSmall(t *testing.T) {
	record := &MTSPRecord{
		Type:   RecordTypeCt,
		Offset: 0,
		Size:   32,
		Data:   make([]byte, 32),
	}

	_, err := record.ParseCtRecord()
	if err == nil {
		t.Error("ParseCtRecord should fail for record smaller than 64 bytes")
	}
}

// TestParseCtRecord_BindingsOutOfBounds tests handling of truncated binding data.
func TestParseCtRecord_BindingsOutOfBounds(t *testing.T) {
	// Create a record that claims 10 bindings but only has space for 5
	data := make([]byte, 0x40+40) // Header + 5 bindings

	binary.LittleEndian.PutUint32(data[0x00:0x04], 144)         // claims 144 bytes
	binary.LittleEndian.PutUint32(data[0x04:0x08], 0xffffc02f)
	binary.LittleEndian.PutUint64(data[0x28:0x30], 0x0b94c3dae0)
	binary.LittleEndian.PutUint64(data[0x30:0x38], 0x0100f93c00)
	binary.LittleEndian.PutUint32(data[0x38:0x3c], 10) // Claims 10 bindings
	binary.LittleEndian.PutUint32(data[0x3c:0x40], 8)

	record := &MTSPRecord{
		Type:   RecordTypeCt,
		Offset: 0,
		Size:   len(data),
		Data:   data,
	}

	_, err := record.ParseCtRecord()
	if err == nil {
		t.Error("ParseCtRecord should fail when bindings exceed data length")
	}
}

// TestParseCtRecord_SizeMismatch tests validation of record size field.
func TestParseCtRecord_SizeMismatch(t *testing.T) {
	data := make([]byte, 120)

	// Set up valid record but with wrong size in header
	binary.LittleEndian.PutUint32(data[0x00:0x04], 200) // Wrong size!
	binary.LittleEndian.PutUint32(data[0x04:0x08], 0xffffc02f)
	binary.LittleEndian.PutUint64(data[0x28:0x30], 0x0b94c3dae0)
	binary.LittleEndian.PutUint64(data[0x30:0x38], 0x0100f93c00)
	binary.LittleEndian.PutUint32(data[0x38:0x3c], 7)
	binary.LittleEndian.PutUint32(data[0x3c:0x40], 8)

	record := &MTSPRecord{
		Type:   RecordTypeCt,
		Offset: 0,
		Size:   120,
		Data:   data,
	}

	_, err := record.ParseCtRecord()
	if err == nil {
		t.Error("ParseCtRecord should fail when record size doesn't match expected size")
	}
}

// TestParseCtRecord_VariousSizes tests parsing records with different binding counts.
func TestParseCtRecord_VariousSizes(t *testing.T) {
	testCases := []struct {
		name         string
		bindingCount uint32
		expectedSize uint32
	}{
		{"6 bindings", 6, 112},
		{"7 bindings", 7, 120},
		{"8 bindings", 8, 128},
		{"9 bindings", 9, 136},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			data := make([]byte, tc.expectedSize)

			binary.LittleEndian.PutUint32(data[0x00:0x04], tc.expectedSize)
			binary.LittleEndian.PutUint32(data[0x04:0x08], 0xffffc01c)
			binary.LittleEndian.PutUint64(data[0x28:0x30], 0x0b94c3dae0)
			binary.LittleEndian.PutUint64(data[0x30:0x38], 0x0100f93c00)
			binary.LittleEndian.PutUint32(data[0x38:0x3c], tc.bindingCount)
			binary.LittleEndian.PutUint32(data[0x3c:0x40], 8)

			// Fill in dummy buffer addresses
			for i := uint32(0); i < tc.bindingCount; i++ {
				offset := 0x40 + (i * 8)
				binary.LittleEndian.PutUint64(data[offset:offset+8], 0x010d000000+uint64(i)*0x1000)
			}

			record := &MTSPRecord{
				Type:   RecordTypeCt,
				Offset: 0,
				Size:   int(tc.expectedSize),
				Data:   data,
			}

			ct, err := record.ParseCtRecord()
			if err != nil {
				t.Fatalf("ParseCtRecord failed: %v", err)
			}

			if ct.RecordSize != tc.expectedSize {
				t.Errorf("RecordSize = %d, want %d", ct.RecordSize, tc.expectedSize)
			}

			if ct.BindingCount != tc.bindingCount {
				t.Errorf("BindingCount = %d, want %d", ct.BindingCount, tc.bindingCount)
			}

			if uint32(len(ct.BufferBindings)) != tc.bindingCount {
				t.Errorf("BufferBindings length = %d, want %d",
					len(ct.BufferBindings), tc.bindingCount)
			}
		})
	}
}
