package gputrace

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// MTSP Record Types observed in capture files
const (
	RecordTypeCS     = "CS"     // Command submission with kernel name
	RecordTypeCt     = "Ct"     // Command type/transition?
	RecordTypeCU     = "CU"     // Command unknown?
	RecordTypeCulul  = "Culul"  // Command buffer marker
	RecordTypeCuw    = "Cuw"    // Command write?
	RecordTypeCi     = "Ci"     // Command info?
	RecordTypeCul    = "Cul"    // Command?
	RecordTypeCut    = "Cut"    // Command type extended?
)

// MTSPRecord represents a parsed MTSP record from the capture file.
type MTSPRecord struct {
	Type   string  // Record type (CS, CU, Culul, etc.)
	Offset int     // Offset in file where record starts
	Size   int     // Size of record in bytes
	Data   []byte  // Raw record data

	// Parsed fields (type-specific)
	Label      string   // For CS records: kernel/stream name
	Address    uint64   // Memory address
	Pointers   []uint64 // Referenced pointers
	Values     []uint32 // Embedded values
}

// CtRecord represents a parsed Ct (Command) record containing
// pipeline state, function, and buffer binding information.
type CtRecord struct {
	RecordSize     uint32   // Total record size in bytes
	CommandFlags   uint32   // Command type/flags
	PipelineAddr   uint64   // Pipeline state object address
	FunctionAddr   uint64   // Metal function address
	BindingCount   uint32   // Number of resource bindings
	Stride         uint32   // Binding array stride (always 8)
	BufferBindings []uint64 // Array of buffer addresses
}

// CiRecord represents a parsed Ci (Compute Indirect / ICB) record.
// These records appear to reference indirect command buffers or command groups.
// Always 52 bytes in size.
type CiRecord struct {
	RecordSize   uint32 // Total record size (always 52)
	CommandFlags uint32 // Command type/flags
	Field1       uint32 // Unknown field at offset 0x20
	ICBAddr      uint64 // Indirect command buffer address at offset 0x28
	Count        uint32 // Dispatch count or index at offset 0x30
	Field2       uint32 // Unknown field at offset 0x34
}

// CululRecord represents a parsed Culul record.
// These appear to be command buffer or indirect command buffer definitions.
// Usually 160 bytes (sometimes 168).
type CululRecord struct {
	RecordSize     uint32   // Total record size (usually 160)
	CommandFlags   uint32   // Command type/flags
	MarkerCount    uint32   // Count at offset 0x20
	ICBAddr        uint64   // ICB or buffer address at offset 0x28
	Field1         uint32   // Unknown at offset 0x30
	Field2         uint32   // Unknown at offset 0x34
	Field3         uint32   // Unknown at offset 0x38
	PayloadSize    uint32   // Size field at offset 0x40
	PayloadAddr    uint64   // Payload address at offset 0x48
	ArrayCount     uint32   // Number of array elements at offset 0x50
	ArrayStride    uint32   // Array stride at offset 0x54
	ArrayAddresses []uint64 // Array of addresses starting at offset 0x58
}

// CulRecord represents a parsed Cul record.
// Variable size, appears to contain buffer or resource bindings.
type CulRecord struct {
	RecordSize     uint32   // Total record size
	CommandFlags   uint32   // Command type/flags
	MarkerCount    uint32   // Count at offset 0x20
	BufferAddr     uint64   // Buffer address at offset 0x28
	Field1         uint32   // Unknown at offset 0x30
	Field2         uint32   // Unknown at offset 0x34
	PayloadSize    uint32   // Size field (when present)
	PayloadAddr    uint64   // Payload address (when present)
	ArrayCount     uint32   // Number of array elements
	ArrayStride    uint32   // Array stride
	ArrayAddresses []uint64 // Array of addresses
}

// CuwRecord represents a parsed Cuw record.
// Two common sizes: 56 bytes (66.4%) and 68 bytes (33.2%).
// The 68-byte variant appears 4,397 times (same as Ci count).
type CuwRecord struct {
	RecordSize   uint32 // Total record size (56, 68, or 124)
	CommandFlags uint32 // Command type/flags
	MarkerCount  uint32 // Count at offset 0x20
	BufferAddr   uint64 // Buffer address at offset 0x28
	Field1       uint64 // Unknown at offset 0x30 (size 68+)
	Field2       uint32 // Unknown (size 68+)
}

// ParseMTSPRecords parses records from the capture file.
func (t *Trace) ParseMTSPRecords() ([]MTSPRecord, error) {
	data := t.CaptureData

	// Skip MTSP header
	if len(data) < 16 {
		return nil, fmt.Errorf("capture data too small")
	}

	_, err := ReadMTSPHeader(data)
	if err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}

	// Start parsing after header - records begin around offset 0x20 (32)
	// but we'll scan for them to be safe
	offset := 16
	var records []MTSPRecord

	for offset < len(data)-8 {
		// Read potential record size
		recordSize := int(binary.LittleEndian.Uint32(data[offset : offset+4]))

		// Validate size looks reasonable
		if recordSize == 0 || recordSize > 0x10000 || offset+recordSize > len(data) {
			offset += 4
			continue
		}

		// Extract potential record data
		recordData := data[offset : offset+recordSize]

		// Try to detect record type
		recordType := detectRecordType(recordData)

		// Only accept records with known types
		if recordType != "unknown" {
			record := MTSPRecord{
				Type:   recordType,
				Offset: offset,
				Size:   recordSize,
				Data:   recordData,
			}

			// Parse type-specific fields
			switch recordType {
			case RecordTypeCS:
				record.parseCSRecord()
			case RecordTypeCU, RecordTypeCut:
				record.parseCURecord()
			case RecordTypeCulul:
				record.parseCululRecord()
			}

			records = append(records, record)
			offset += recordSize
		} else {
			offset += 4
		}
	}

	return records, nil
}

// detectRecordType identifies the record type from its data.
func detectRecordType(data []byte) string {
	if len(data) < 16 {
		return "unknown"
	}

	// Check for known markers
	// Markers typically appear around offset 32 based on hex analysis
	for i := 8; i < min(len(data), 64); i++ {
		if i+5 <= len(data) && bytes.Equal(data[i:i+5], []byte("Culul")) {
			return RecordTypeCulul
		}
		if i+3 <= len(data) && bytes.Equal(data[i:i+3], []byte("Cuw")) {
			return RecordTypeCuw
		}
		if i+3 <= len(data) && bytes.Equal(data[i:i+3], []byte("Cut")) {
			return RecordTypeCut
		}
		// Check CS before Ct/Cul to avoid false matches
		if i+2 <= len(data) && bytes.Equal(data[i:i+2], []byte("CS")) {
			// Check that it's not part of another word
			if i+3 < len(data) && data[i+2] == 0 {
				return RecordTypeCS
			}
		}
		// Ct needs to be checked carefully to not match "Cut" or "Ctulul"
		if i+2 <= len(data) && bytes.Equal(data[i:i+2], []byte("Ct")) {
			if i+3 < len(data) && data[i+2] == 0 {
				return RecordTypeCt
			}
		}
		if i+3 <= len(data) && bytes.Equal(data[i:i+3], []byte("Cul")) {
			return RecordTypeCul
		}
		if i+2 <= len(data) && bytes.Equal(data[i:i+2], []byte("CU")) {
			if i+3 < len(data) && data[i+2] == 0 {
				return RecordTypeCU
			}
		}
		if i+2 <= len(data) && bytes.Equal(data[i:i+2], []byte("Ci")) {
			if i+3 < len(data) && data[i+2] == 0 {
				return RecordTypeCi
			}
		}
	}

	return "unknown"
}

// parseCSRecord parses a CS (Command Submission?) record.
// These often contain kernel/stream names.
func (r *MTSPRecord) parseCSRecord() {
	// CS records typically have format:
	// [size] [padding] [CS marker] [address] [string...]

	// Look for null-terminated string after CS marker
	for i := 0; i < len(r.Data)-4; i++ {
		if i+2 < len(r.Data) && r.Data[i] == 'C' && r.Data[i+1] == 'S' && r.Data[i+2] == 0 {
			// Found CS marker, look for string after address
			stringStart := i + 12 // Skip CS marker + padding + address
			if stringStart < len(r.Data) {
				if end := bytes.IndexByte(r.Data[stringStart:], 0); end != -1 {
					r.Label = string(r.Data[stringStart : stringStart+end])
				}
			}
			break
		}
	}
}

// parseCURecord parses a CU/Cut record.
// These may contain UUIDs or identifiers.
func (r *MTSPRecord) parseCURecord() {
	// Look for UUID-like strings (hexadecimal)
	for i := 0; i < len(r.Data)-16; i++ {
		// Check if we have a hex string
		if isHexString(r.Data[i : min(i+32, len(r.Data))]) {
			end := i
			for end < len(r.Data) && (isHex(r.Data[end]) || r.Data[end] == '-') {
				end++
			}
			if end > i {
				r.Label = string(r.Data[i:end])
				break
			}
		}
	}
}

// parseCululRecord parses a Culul (Command buffer) record.
func (r *MTSPRecord) parseCululRecord() {
	// Culul records mark command buffers
	// Format: [Culul marker] [padding] [address] [flags?]
	for i := 0; i < len(r.Data)-12; i++ {
		if i+5 <= len(r.Data) && bytes.Equal(r.Data[i:i+5], []byte("Culul")) {
			// Read address after marker
			if i+13 <= len(r.Data) {
				r.Address = binary.LittleEndian.Uint64(r.Data[i+5 : i+13])
			}
			break
		}
	}
}

// Helper functions

func isHexString(data []byte) bool {
	if len(data) < 8 {
		return false
	}
	count := 0
	for i := 0; i < min(len(data), 32); i++ {
		if isHex(data[i]) {
			count++
		} else if data[i] == 0 {
			break
		} else {
			return false
		}
	}
	return count >= 8 // At least 8 hex chars
}

func isHex(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'A' && b <= 'F') || (b >= 'a' && b <= 'f')
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// AnalyzeMTSPRecords provides a detailed analysis of MTSP records.
func (t *Trace) AnalyzeMTSPRecords() (string, error) {
	records, err := t.ParseMTSPRecords()
	if err != nil {
		return "", err
	}

	report := "=== MTSP Record Analysis ===\n\n"
	report += fmt.Sprintf("Total records: %d\n\n", len(records))

	// Count by type
	typeCounts := make(map[string]int)
	for _, record := range records {
		typeCounts[record.Type]++
	}

	report += "Record types:\n"
	for rtype, count := range typeCounts {
		report += fmt.Sprintf("  %-10s: %d records\n", rtype, count)
	}
	report += "\n"

	// Show first 20 records
	report += "First 20 records:\n"
	for i, record := range records {
		if i >= 20 {
			report += fmt.Sprintf("... and %d more\n", len(records)-20)
			break
		}

		info := fmt.Sprintf("  [%3d] offset=0x%06x size=%4d type=%-10s",
			i, record.Offset, record.Size, record.Type)

		if record.Label != "" {
			info += fmt.Sprintf(" label=%q", record.Label)
		}
		if record.Address != 0 {
			info += fmt.Sprintf(" addr=0x%x", record.Address)
		}

		report += info + "\n"
	}

	// Show CS records (kernel names)
	report += "\n=== CS Records (Kernel Names) ===\n"
	csCount := 0
	for _, record := range records {
		if record.Type == RecordTypeCS && record.Label != "" {
			if csCount < 30 {
				report += fmt.Sprintf("  %s\n", record.Label)
			}
			csCount++
		}
	}
	if csCount > 30 {
		report += fmt.Sprintf("... and %d more\n", csCount-30)
	}

	return report, nil
}

// ParseCtRecord parses a Ct (Command) record to extract pipeline state,
// function address, and buffer bindings.
//
// Ct Record Structure:
//   Offset | Size | Type    | Field Name
//   -------|------|---------|------------------
//   0x00   | 4    | uint32  | record_size
//   0x04   | 4    | uint32  | command_flags
//   0x08   | 24   | bytes   | reserved
//   0x20   | 4    | uint32  | marker1 (0x00000008)
//   0x24   | 4    | char[4] | marker2 ("Ct\0\0")
//   0x28   | 8    | uint64  | pipeline_addr
//   0x30   | 8    | uint64  | function_addr
//   0x38   | 4    | uint32  | binding_count
//   0x3c   | 4    | uint32  | stride (always 8)
//   0x40   | 8*N  | uint64[]| buffer_bindings
func (r *MTSPRecord) ParseCtRecord() (*CtRecord, error) {
	if r.Type != RecordTypeCt {
		return nil, fmt.Errorf("not a Ct record (type=%s)", r.Type)
	}

	if len(r.Data) < 0x40 {
		return nil, fmt.Errorf("Ct record too small: %d bytes (need at least 64)", len(r.Data))
	}

	ct := &CtRecord{
		RecordSize:   binary.LittleEndian.Uint32(r.Data[0x00:0x04]),
		CommandFlags: binary.LittleEndian.Uint32(r.Data[0x04:0x08]),
		PipelineAddr: binary.LittleEndian.Uint64(r.Data[0x28:0x30]),
		FunctionAddr: binary.LittleEndian.Uint64(r.Data[0x30:0x38]),
		BindingCount: binary.LittleEndian.Uint32(r.Data[0x38:0x3c]),
		Stride:       binary.LittleEndian.Uint32(r.Data[0x3c:0x40]),
	}

	// Validate stride (should always be 8 for uint64 addresses)
	if ct.Stride != 8 && ct.Stride != 0 {
		return nil, fmt.Errorf("unexpected stride value: %d (expected 8)", ct.Stride)
	}

	// Extract buffer bindings
	ct.BufferBindings = make([]uint64, ct.BindingCount)
	for i := uint32(0); i < ct.BindingCount; i++ {
		offset := 0x40 + (i * 8)
		if int(offset)+8 > len(r.Data) {
			return nil, fmt.Errorf("binding %d out of bounds (offset=0x%x, data_len=%d)",
				i, offset, len(r.Data))
		}
		ct.BufferBindings[i] = binary.LittleEndian.Uint64(r.Data[offset : offset+8])
	}

	// Validate record size matches expected size
	expectedSize := 0x40 + (ct.BindingCount * 8)
	if ct.RecordSize != expectedSize {
		return nil, fmt.Errorf("record size mismatch: header says %d, expected %d (bindings=%d)",
			ct.RecordSize, expectedSize, ct.BindingCount)
	}

	return ct, nil
}

// ParseCiRecord parses a Ci (Compute Indirect / ICB) record.
//
// Ci Record Structure (52 bytes):
//   Offset | Size | Type    | Field Name
//   -------|------|---------|------------------
//   0x00   | 4    | uint32  | record_size (always 52)
//   0x04   | 4    | uint32  | command_flags
//   0x08   | 24   | bytes   | reserved
//   0x20   | 4    | uint32  | field1
//   0x24   | 4    | char[4] | marker ("Ci\0\0")
//   0x28   | 8    | uint64  | icb_addr
//   0x30   | 4    | uint32  | count
//   0x34   | 4    | uint32  | field2
func (r *MTSPRecord) ParseCiRecord() (*CiRecord, error) {
	if r.Type != RecordTypeCi {
		return nil, fmt.Errorf("not a Ci record (type=%s)", r.Type)
	}

	if len(r.Data) < 52 {
		return nil, fmt.Errorf("Ci record too small: %d bytes (expected 52)", len(r.Data))
	}

	ci := &CiRecord{
		RecordSize:   binary.LittleEndian.Uint32(r.Data[0x00:0x04]),
		CommandFlags: binary.LittleEndian.Uint32(r.Data[0x04:0x08]),
		Field1:       binary.LittleEndian.Uint32(r.Data[0x20:0x24]),
		ICBAddr:      binary.LittleEndian.Uint64(r.Data[0x28:0x30]),
		Count:        binary.LittleEndian.Uint32(r.Data[0x30:0x34]),
		Field2:       binary.LittleEndian.Uint32(r.Data[0x34:0x38]),
	}

	if ci.RecordSize != 52 {
		return nil, fmt.Errorf("unexpected Ci record size: %d (expected 52)", ci.RecordSize)
	}

	return ci, nil
}

// ParseCululRecord parses a Culul (Command Buffer / ICB Definition) record.
//
// Culul Record Structure (usually 160 bytes):
//   Offset | Size | Type    | Field Name
//   -------|------|---------|------------------
//   0x00   | 4    | uint32  | record_size (160 or 168)
//   0x04   | 4    | uint32  | command_flags
//   0x08   | 24   | bytes   | reserved
//   0x20   | 4    | uint32  | marker_count
//   0x24   | 8    | char[]  | marker ("Culul\0\0\0")
//   0x28   | 8    | uint64  | icb_addr
//   0x30   | 4    | uint32  | field1
//   0x34   | 4    | uint32  | field2
//   0x38   | 4    | uint32  | field3
//   0x40   | 4    | uint32  | payload_size
//   0x48   | 8    | uint64  | payload_addr
//   0x50   | 4    | uint32  | array_count
//   0x54   | 4    | uint32  | array_stride
//   0x58   | 8*N  | uint64[]| array_addresses
func (r *MTSPRecord) ParseCululRecord() (*CululRecord, error) {
	if r.Type != RecordTypeCulul {
		return nil, fmt.Errorf("not a Culul record (type=%s)", r.Type)
	}

	if len(r.Data) < 0x58 {
		return nil, fmt.Errorf("Culul record too small: %d bytes (need at least 88)", len(r.Data))
	}

	culul := &CululRecord{
		RecordSize:   binary.LittleEndian.Uint32(r.Data[0x00:0x04]),
		CommandFlags: binary.LittleEndian.Uint32(r.Data[0x04:0x08]),
		MarkerCount:  binary.LittleEndian.Uint32(r.Data[0x20:0x24]),
		ICBAddr:      binary.LittleEndian.Uint64(r.Data[0x28:0x30]),
		Field1:       binary.LittleEndian.Uint32(r.Data[0x30:0x34]),
		Field2:       binary.LittleEndian.Uint32(r.Data[0x34:0x38]),
		Field3:       binary.LittleEndian.Uint32(r.Data[0x38:0x3c]),
		PayloadSize:  binary.LittleEndian.Uint32(r.Data[0x40:0x44]),
		PayloadAddr:  binary.LittleEndian.Uint64(r.Data[0x48:0x50]),
		ArrayCount:   binary.LittleEndian.Uint32(r.Data[0x50:0x54]),
		ArrayStride:  binary.LittleEndian.Uint32(r.Data[0x54:0x58]),
	}

	// Extract array addresses
	culul.ArrayAddresses = make([]uint64, culul.ArrayCount)
	for i := uint32(0); i < culul.ArrayCount; i++ {
		offset := 0x58 + (i * 8)
		if int(offset)+8 > len(r.Data) {
			return nil, fmt.Errorf("array element %d out of bounds (offset=0x%x, data_len=%d)",
				i, offset, len(r.Data))
		}
		culul.ArrayAddresses[i] = binary.LittleEndian.Uint64(r.Data[offset : offset+8])
	}

	return culul, nil
}

// ParseCulRecord parses a Cul (Command / Resource Binding) record.
//
// Cul Record Structure (variable size):
//   Similar to Culul but with variable payload structure
func (r *MTSPRecord) ParseCulRecord() (*CulRecord, error) {
	if r.Type != RecordTypeCul {
		return nil, fmt.Errorf("not a Cul record (type=%s)", r.Type)
	}

	if len(r.Data) < 0x38 {
		return nil, fmt.Errorf("Cul record too small: %d bytes", len(r.Data))
	}

	cul := &CulRecord{
		RecordSize:   binary.LittleEndian.Uint32(r.Data[0x00:0x04]),
		CommandFlags: binary.LittleEndian.Uint32(r.Data[0x04:0x08]),
		MarkerCount:  binary.LittleEndian.Uint32(r.Data[0x20:0x24]),
		BufferAddr:   binary.LittleEndian.Uint64(r.Data[0x28:0x30]),
		Field1:       binary.LittleEndian.Uint32(r.Data[0x30:0x34]),
		Field2:       binary.LittleEndian.Uint32(r.Data[0x34:0x38]),
	}

	// For larger records, try to parse payload and array
	if len(r.Data) >= 0x48 {
		cul.PayloadSize = binary.LittleEndian.Uint32(r.Data[0x40:0x44])
		if len(r.Data) >= 0x50 {
			cul.PayloadAddr = binary.LittleEndian.Uint64(r.Data[0x48:0x50])
		}
	}

	// Try to find array section
	if len(r.Data) >= 0x58 {
		cul.ArrayCount = binary.LittleEndian.Uint32(r.Data[0x50:0x54])
		cul.ArrayStride = binary.LittleEndian.Uint32(r.Data[0x54:0x58])

		// Extract array addresses if present
		if cul.ArrayCount > 0 && cul.ArrayCount < 1024 {
			cul.ArrayAddresses = make([]uint64, 0, cul.ArrayCount)
			for i := uint32(0); i < cul.ArrayCount; i++ {
				offset := 0x58 + (i * 8)
				if int(offset)+8 <= len(r.Data) {
					addr := binary.LittleEndian.Uint64(r.Data[offset : offset+8])
					cul.ArrayAddresses = append(cul.ArrayAddresses, addr)
				}
			}
		}
	}

	return cul, nil
}

// ParseCuwRecord parses a Cuw (Command Update/Write) record.
//
// Cuw Record Structure (56, 68, or 124 bytes):
//   Offset | Size | Type    | Field Name
//   -------|------|---------|------------------
//   0x00   | 4    | uint32  | record_size
//   0x04   | 4    | uint32  | command_flags
//   0x08   | 24   | bytes   | reserved
//   0x20   | 4    | uint32  | marker_count
//   0x24   | ?    | char[]  | marker ("Cuw" or "Cuwuw")
//   0x28   | 8    | uint64  | buffer_addr
//   0x30   | 8    | uint64  | field1 (size 68+)
//   0x38   | 4    | uint32  | field2 (size 68+)
func (r *MTSPRecord) ParseCuwRecord() (*CuwRecord, error) {
	if r.Type != RecordTypeCuw {
		return nil, fmt.Errorf("not a Cuw record (type=%s)", r.Type)
	}

	if len(r.Data) < 0x30 {
		return nil, fmt.Errorf("Cuw record too small: %d bytes", len(r.Data))
	}

	cuw := &CuwRecord{
		RecordSize:   binary.LittleEndian.Uint32(r.Data[0x00:0x04]),
		CommandFlags: binary.LittleEndian.Uint32(r.Data[0x04:0x08]),
		MarkerCount:  binary.LittleEndian.Uint32(r.Data[0x20:0x24]),
		BufferAddr:   binary.LittleEndian.Uint64(r.Data[0x28:0x30]),
	}

	// For size 68+ records, extract additional fields
	if len(r.Data) >= 0x38 {
		cuw.Field1 = binary.LittleEndian.Uint64(r.Data[0x30:0x38])
	}
	if len(r.Data) >= 0x3c {
		cuw.Field2 = binary.LittleEndian.Uint32(r.Data[0x38:0x3c])
	}

	return cuw, nil
}
