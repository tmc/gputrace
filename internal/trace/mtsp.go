package trace

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// MTSP Record Types observed in capture files
const (
	RecordTypeCS      = "CS"      // Command submission with kernel name
	RecordTypeCt      = "Ct"      // Command type/transition?
	RecordTypeCtt     = "Ctt"     // Command type extended?
	RecordTypeCU      = "CU"      // Command unknown?
	RecordTypeCulul   = "Culul"   // Command buffer marker
	RecordTypeCiulul  = "Ciulul"  // Compute Indirect ulul?
	RecordTypeCtulul  = "Ctulul"  // Command type ulul?
	RecordTypeCuw     = "Cuw"     // Command write?
	RecordTypeCi      = "Ci"      // Command info?
	RecordTypeCul     = "Cul"     // Command?
	RecordTypeCut     = "Cut"     // Command type extended?
	RecordTypeCSuwuw  = "CSuwuw"  // Command Submission uwuw?
	RecordTypeCiulSl  = "CiulSl"  // Command info ul Sl?
	RecordTypeUnknown = "Unknown" // Fallback for valid-looking records
)

// MTSPRecord represents a parsed MTSP record from the capture file.
type MTSPRecord struct {
	Type   string // Record type (CS, CU, Culul, etc.)
	Offset int    // Offset in file where record starts
	Size   int    // Size of record in bytes
	Data   []byte // Raw record data

	// Parsed fields (type-specific)
	Label        string   // For CS records: kernel/stream name
	Address      uint64   // Memory address
	FunctionAddr uint64   // Metal function address (for CiulSl)
	Pointers     []uint64 // Referenced pointers
	Values       []uint32 // Embedded values
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

// CttRecord parsed from Ctt record.
// Matches structure expected by existing ParseCttRecords manually:
// +0x04: device addr
// +0x0C: function addr
// +0x20: pipeline addr
type CttRecord struct {
	RecordSize   uint32
	DeviceAddr   uint64
	FunctionAddr uint64
	PipelineAddr uint64
}

// ParseCttRecord parses a Ctt (Command Type Transfer?) record.
func (r *MTSPRecord) ParseCttRecord() (*CttRecord, error) {
	if r.Type != RecordTypeCtt {
		return nil, fmt.Errorf("not a Ctt record (type=%s)", r.Type)
	}

	// Ctt\x00 (4 bytes) at different offsets?
	// Based on manual scan in trace.go: "Ctt\x00" is found.
	// In MTSP parsing, we look for "Ctt" string.
	// Hex dump showed: [Size 4B] "Ctt\0" ...
	// So data starts with "Ctt\0".

	cttOffset := bytes.Index(r.Data, []byte("Ctt\000"))
	if cttOffset == -1 {
		return nil, fmt.Errorf("Ctt marker not found in data")
	}

	// Based on trace.go manual parsing:
	// +0x04 (relative to Ctt): device address
	// +0x0C (relative to Ctt): function address
	// +0x20 (relative to Ctt): pipeline state address

	base := cttOffset
	if base+0x28 > len(r.Data) {
		return nil, fmt.Errorf("Ctt record data too short")
	}

	ctt := &CttRecord{
		RecordSize:   uint32(r.Size),
		DeviceAddr:   binary.LittleEndian.Uint64(r.Data[base+4 : base+12]),
		FunctionAddr: binary.LittleEndian.Uint64(r.Data[base+12 : base+20]),
		PipelineAddr: binary.LittleEndian.Uint64(r.Data[base+0x20 : base+0x28]),
	}

	return ctt, nil
}

// CiululRecord parsed from Ciulul record.
type CiululRecord struct {
	RecordSize uint32
	// Add fields as we discover them
}

func (r *MTSPRecord) ParseCiululRecord() (*CiululRecord, error) {
	// For now just return basic info
	return &CiululRecord{RecordSize: uint32(r.Size)}, nil
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
	return t.ParseMTSPFromData(t.CaptureData)
}

// ParseMTSPFromData parses records from a byte slice.
func (t *Trace) ParseMTSPFromData(data []byte) ([]MTSPRecord, error) {
	// Skip MTSP header if present (check magic)
	offset := 0
	if len(data) >= 4 && string(data[0:4]) == MagicMTSP {
		if len(data) < 16 {
			return nil, fmt.Errorf("capture data too small for header")
		}
		_, err := ReadMTSPHeader(data)
		if err != nil {
			return nil, fmt.Errorf("read header: %w", err)
		}
		offset = 16
	}

	var records []MTSPRecord

	// Standard scan loop
	for offset < len(data)-8 {
		// Read potential record size
		recordSize := int(binary.LittleEndian.Uint32(data[offset : offset+4]))

		// Validate size looks reasonable
		// Note: recursive records inside CS might be smaller or larger,
		// but we still expect valid uint32 size.
		// We use 0x400000 (4MB) as a generous upper bound for individual records.
		if recordSize == 0 || recordSize > 0x400000 || offset+recordSize > len(data) {
			offset += 4
			continue
		}

		// Extract potential record data
		recordData := data[offset : offset+recordSize]

		// Try to detect record type
		recordType := detectRecordType(recordData)

		// Only accept records with known types or valid-looking unknown records
		if recordType != "unknown" {
			// Known type
		} else if recordSize >= 16 && recordSize <= 0x400000 {
			// Unknown type but valid size - treat as Unknown record
			// We only do this if it looks like a record (valid size header)
			// But wait, recordSize is just uint32. Any 4 bytes is a valid uint32.
			// We need a heuristic.
			// If we are in a container, maybe assumes stream is contiguous?
			// Let's assume contiguous for now and see what we get.
			recordType = RecordTypeUnknown
		}

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
			case RecordTypeCSuwuw:
				record.parseCSuwuwRecord()
			case RecordTypeCiulSl:
				record.parseCiulSlRecord()
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
	// Extended scan range to 128 to catch CS records with larger padding
	// Started scan at 4 to catch CS records that appear immediately after size
	for i := 4; i < min(len(data), 128); i++ {
		if i+5 <= len(data) && bytes.Equal(data[i:i+5], []byte("Culul")) {
			return RecordTypeCulul
		}
		if i+3 <= len(data) && bytes.Equal(data[i:i+3], []byte("Cuw")) {
			return RecordTypeCuw
		}
		if i+3 <= len(data) && bytes.Equal(data[i:i+3], []byte("Cut")) {
			return RecordTypeCut
		}
		if i+6 <= len(data) && bytes.Equal(data[i:i+6], []byte("Ciulul")) {
			return RecordTypeCiulul
		}
		if i+6 <= len(data) && bytes.Equal(data[i:i+6], []byte("Ctulul")) {
			return RecordTypeCtulul
		}
		// Check CS before Ct/Cul to avoid false matches
		if i+6 <= len(data) && bytes.Equal(data[i:i+6], []byte("CSuwuw")) {
			return RecordTypeCSuwuw
		}
		if i+2 <= len(data) && bytes.Equal(data[i:i+2], []byte("CS")) {
			// Check that it's not part of another word
			if i+3 < len(data) && data[i+2] == 0 {
				return RecordTypeCS
			}
		}
		// Ctt check
		if i+3 <= len(data) && bytes.Equal(data[i:i+3], []byte("Ctt")) {
			return RecordTypeCtt
		}
		// Ct needs to be checked carefully to not match "Cut" or "Ctulul" or "Ctt"
		if i+2 <= len(data) && bytes.Equal(data[i:i+2], []byte("Ct")) {
			// Must not be followed by 't' or 'u'
			if i+3 < len(data) && data[i+2] != 't' && data[i+2] != 'u' {
				// Check for null terminator or other non-letter to confirm it's likely just Ct
				if data[i+2] == 0 {
					return RecordTypeCt
				}
			}
		}
		if i+6 <= len(data) && bytes.Equal(data[i:i+6], []byte("CiulSl")) {
			return RecordTypeCiulSl
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

// parseCSuwuwRecord parses a CSuwuw record.
// Seem to be similar to CS records but with "CSuwuw" marker.
func (r *MTSPRecord) parseCSuwuwRecord() {
	// Format seems to be:
	// ... [CSuwuw] [padding] [address] [string]

	for i := 0; i < len(r.Data)-14; i++ {
		if i+6 <= len(r.Data) && bytes.Equal(r.Data[i:i+6], []byte("CSuwuw")) {
			// Found marker
			// Address starts at i + 6 + 2 (padding?) or just after?
			// Dump showed: 43 53 75 77 75 77 00 00 00 c0 57 2c 0a
			// "CSuwuw" is 6 bytes.
			// Then 00 00 00 (3 bytes? align to 8?)
			// Let's assume address starts at i+8 or i+12?
			// In dump: offset 0x88 is "F... CSuwuw..."
			// CSuwuw at 0x8C.
			// Address at 0x98? (12 bytes after start of marker, similar to CS)

			// Let's skip marker (6) + padding (unknown)
			// Scan for non-zero bytes?
			// Or just assume +12 bytes from start of marker matching CS?
			// CS was: +4 (marker) + 8 (padding/whatever) -> +12?
			// CS parser used `i+12` for string start, `i+4` for address start.
			// CS marker is 4 bytes (CS\0\0).
			// CSuwuw is 6 bytes.
			// Address likely 8-byte aligned.

			// Dump again:
			// 43 53 75 77 75 77 (CSuwuw)
			// 00 00 00 (3 nulls)
			// c0 57 2c 0a ... (Address)
			// 6 + 3 = 9 bytes? Alignment usually 4 or 8.
			// If marker starts at 0x8C. 0x8C + 9 = 0x95. Not aligned.
			// 0x98 is aligned.

			// Let's try to find address 8 bytes after marker start?
			// Marker at 0. Address at 8? (skip 2 bytes padding)
			// 6 + 2 = 8.
			// Dump: 00 00 00. 3 bytes padding.
			// Wait, previous byte before marker was 00?
			// Let's look at dump again.
			// 00000080  46 00 00 00 43 53 75 77  75 77 00 00 00 c0 57 2c
			// Offset 0x84 is C (43).
			// 0x84: CSuwuw (6 bytes) -> ends 0x8A.
			// 0x8A: 00 00 00 (3 bytes) -> ends 0x8D.
			// 0x8D: c0 57 2c (Address?)
			// This is unaligned.

			// Maybe address is at offset 0x90?
			// 0x90 is 0a ...
			// c0 57 2c 0a 00 00 00 00 is 0x0000000a2c57c0 (valid address)
			// So address starts at 0x8D.
			// Offset 0x8D relative to 0x84 is +9.
			// Only 9 bytes?

			// Let's search for string null terminator?
			// String "root" is at 0x99 (72 6f 6f 74).

			// Address 8D to 95 (8 bytes).
			// 0x95 is 00. 0x96 is 00. 0x97 is 00. 0x98 is 00.
			// 0x99 is 'r'.
			// So string starts after address.

			addressStart := i + 9
			if addressStart+8 <= len(r.Data) {
				r.Address = binary.LittleEndian.Uint64(r.Data[addressStart : addressStart+8])
			}

			// String usually follows.
			stringStart := addressStart + 8
			// Skip nulls
			for stringStart < len(r.Data) && r.Data[stringStart] == 0 {
				stringStart++
			}
			if stringStart < len(r.Data) {
				if end := bytes.IndexByte(r.Data[stringStart:], 0); end != -1 {
					r.Label = string(r.Data[stringStart : stringStart+end])
				}
			}
			break
		}
	}
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

			// Extract address (8 bytes after CS marker + 2 bytes padding/zero)
			// CS marker is 2 bytes + 2 zero bytes = 4 bytes total
			addressStart := i + 4
			if addressStart+8 <= len(r.Data) {
				r.Address = binary.LittleEndian.Uint64(r.Data[addressStart : addressStart+8])
			}

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
		if isHexString(r.Data[i:min(i+32, len(r.Data))]) {
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
//
//	Offset | Size | Type    | Field Name
//	-------|------|---------|------------------
//	0x00   | 4    | uint32  | record_size
//	0x04   | 4    | uint32  | command_flags
//	0x08   | 24   | bytes   | reserved
//	0x20   | 4    | uint32  | marker1 (0x00000008)
//	0x24   | 4    | char[4] | marker2 ("Ct\0\0")
//	0x28   | 8    | uint64  | pipeline_addr
//	0x30   | 8    | uint64  | function_addr
//	0x38   | 4    | uint32  | binding_count
//	0x3c   | 4    | uint32  | stride (always 8)
//	0x40   | 8*N  | uint64[]| buffer_bindings
func (r *MTSPRecord) ParseCtRecord() (*CtRecord, error) {
	if r.Type != RecordTypeCt {
		return nil, fmt.Errorf("not a Ct record (type=%s)", r.Type)
	}

	if len(r.Data) < 0x40 {
		return nil, fmt.Errorf("Ct record too small: %d bytes (need at least 64)", len(r.Data))
	}

	// Find Ct marker
	ctOffset := bytes.Index(r.Data, []byte("Ct\000\000"))
	if ctOffset == -1 {
		// Try just Ct\0 if 4-byte aligned?
		// The dump showed 43 74 00 00 which is Ct\0\0
		ctOffset = bytes.Index(r.Data, []byte("Ct\000"))
	}
	if ctOffset == -1 {
		return nil, fmt.Errorf("Ct marker not found")
	}
	base := ctOffset

	if base+28 > len(r.Data) {
		return nil, fmt.Errorf("Ct record too small")
	}

	ct := &CtRecord{
		RecordSize:   binary.LittleEndian.Uint32(r.Data[0x00:0x04]),
		CommandFlags: binary.LittleEndian.Uint32(r.Data[0x04:0x08]),
		PipelineAddr: binary.LittleEndian.Uint64(r.Data[base+4 : base+12]),
		FunctionAddr: binary.LittleEndian.Uint64(r.Data[base+12 : base+20]),
		BindingCount: binary.LittleEndian.Uint32(r.Data[base+20 : base+24]),
		Stride:       binary.LittleEndian.Uint32(r.Data[base+24 : base+28]),
	}

	// Validate stride (should always be 8 for uint64 addresses)
	if ct.Stride != 8 && ct.Stride != 0 {
		return nil, fmt.Errorf("unexpected stride value: %d (expected 8)", ct.Stride)
	}

	// Parse bindings
	if ct.BindingCount > 0 {
		bindingsOffset := base + 28
		size := int(ct.BindingCount) * 8
		if bindingsOffset+size <= len(r.Data) {
			ct.BufferBindings = make([]uint64, ct.BindingCount)
			for i := 0; i < int(ct.BindingCount); i++ {
				ct.BufferBindings[i] = binary.LittleEndian.Uint64(r.Data[bindingsOffset+i*8 : bindingsOffset+(i+1)*8])
			}
		}
	}

	return ct, nil
}

// ParseCiRecord parses a Ci (Compute Indirect / ICB) record.
//
// Ci Record Structure (52 bytes):
//
//	Offset | Size | Type    | Field Name
//	-------|------|---------|------------------
//	0x00   | 4    | uint32  | record_size (always 52)
//	0x04   | 4    | uint32  | command_flags
//	0x08   | 24   | bytes   | reserved
//	0x20   | 4    | uint32  | field1
//	0x24   | 4    | char[4] | marker ("Ci\0\0")
//	0x28   | 8    | uint64  | icb_addr
//	0x30   | 4    | uint32  | count
//	0x34   | 4    | uint32  | field2
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
//
//	Offset | Size | Type    | Field Name
//	-------|------|---------|------------------
//	0x00   | 4    | uint32  | record_size (160 or 168)
//	0x04   | 4    | uint32  | command_flags
//	0x08   | 24   | bytes   | reserved
//	0x20   | 4    | uint32  | marker_count
//	0x24   | 8    | char[]  | marker ("Culul\0\0\0")
//	0x28   | 8    | uint64  | icb_addr
//	0x30   | 4    | uint32  | field1
//	0x34   | 4    | uint32  | field2
//	0x38   | 4    | uint32  | field3
//	0x40   | 4    | uint32  | payload_size
//	0x48   | 8    | uint64  | payload_addr
//	0x50   | 4    | uint32  | array_count
//	0x54   | 4    | uint32  | array_stride
//	0x58   | 8*N  | uint64[]| array_addresses
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
//
//	Similar to Culul but with variable payload structure
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
//
//	Offset | Size | Type    | Field Name
//	-------|------|---------|------------------
//	0x00   | 4    | uint32  | record_size
//	0x04   | 4    | uint32  | command_flags
//	0x08   | 24   | bytes   | reserved
//	0x20   | 4    | uint32  | marker_count
//	0x24   | ?    | char[]  | marker ("Cuw" or "Cuwuw")
//	0x28   | 8    | uint64  | buffer_addr
//	0x30   | 8    | uint64  | field1 (size 68+)
//	0x38   | 4    | uint32  | field2 (size 68+)
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

// parseCiulSlRecord parses a CiulSl record.
// Maps Function Address (at base+8) to previous CS record.
func (r *MTSPRecord) parseCiulSlRecord() {
	for i := 0; i < len(r.Data)-16; i++ {
		if bytes.Equal(r.Data[i:i+6], []byte("CiulSl")) {
			// Address at i+8
			r.FunctionAddr = binary.LittleEndian.Uint64(r.Data[i+8 : i+16])
			break
		}
	}
}
