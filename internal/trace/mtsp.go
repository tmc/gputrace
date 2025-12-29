package trace

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// MTSP Record Types observed in capture files
const (
	RecordTypeCS      = "CS"        // Command submission with kernel name
	RecordTypeCt      = "Ct"        // Command type/transition?
	RecordTypeCtt     = "Ctt"       // Command type extended?
	RecordTypeCU      = "CU"        // Command unknown?
	RecordTypeCulul   = "Culul"     // Command buffer marker
	RecordTypeCiulul  = "Ciulul"    // Compute Indirect ulul?
	RecordTypeCtulul  = "Ctulul"    // Command type ulul?
	RecordTypeC       = "C"         // Generic Command (Pop, EndEncoding, etc.)
	RecordTypeC_3ul   = "C@3ul@3ul" // Dispatch threads
	RecordTypeCuw     = "Cuw"       // Command write?
	RecordTypeCi      = "Ci"        // Command info?
	RecordTypeCul     = "Cul"       // Command?
	RecordTypeCut     = "Cut"       // Command type extended?
	RecordTypeCSuwuw  = "CSuwuw"    // Command Submission uwuw?
	RecordTypeCiulSl  = "CiulSl"    // Command info ul Sl?
	RecordTypeCtU     = "CtU"       // Buffer definition (CtU<b>ulul)
	RecordTypeUnknown = "Unknown"   // Fallback for valid-looking records
)

// MTSPRecord represents a parsed MTSP record from the capture file.
type MTSPRecord struct {
	Type   string // Record type (CS, CU, Culul, etc.)
	Offset int    // Offset in file where record starts
	Size   int    // Size of record in bytes
	Data   []byte // Raw record data

	// Parsed fields (type-specific)
	Label         string   // For CS records: kernel/stream name
	Address       uint64   // Memory address
	FunctionAddr  uint64   // Metal function address (for CiulSl)
	Pointers      []uint64 // Referenced pointers
	Values        []uint32 // Embedded values
	Name          string   // Buffer name (for CtU)
	SecondaryAddr uint64   // Function address (for CS Library records)
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
	RecordSize     uint32
	CommandFlags   uint32 // Command flags
	DeviceAddr     uint64
	FunctionAddr   uint64
	PipelineAddr   uint64
	BindingCount   uint32   // Number of resource bindings
	Stride         uint32   // Binding array stride (always 8)
	BufferBindings []uint64 // Array of buffer addresses
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

	// Parse bindings if present (similar to Ct records)
	// Base + 0x28 corresponds to offset 0x4C relative to start (if base=0x24)
	if base+0x30 <= len(r.Data) {
		ctt.BindingCount = binary.LittleEndian.Uint32(r.Data[base+0x28 : base+0x2c])
		ctt.Stride = binary.LittleEndian.Uint32(r.Data[base+0x2c : base+0x30])

		if ctt.BindingCount > 0 {
			bindingsOffset := base + 0x30
			size := int(ctt.BindingCount) * 8
			if bindingsOffset+size <= len(r.Data) {
				ctt.BufferBindings = make([]uint64, ctt.BindingCount)
				for i := 0; i < int(ctt.BindingCount); i++ {
					ctt.BufferBindings[i] = binary.LittleEndian.Uint64(r.Data[bindingsOffset+i*8 : bindingsOffset+(i+1)*8])
				}
			}
		}
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
		// Check for CtU (Buffer Definition)
		if i+10 <= len(data) && bytes.Equal(data[i:i+10], []byte("CtU<b>ulul")) {
			return RecordTypeCtU
		}
		if i+6 <= len(data) && bytes.Equal(data[i:i+6], []byte("Ctulul")) {
			return RecordTypeCtulul
		}
		if i+10 <= len(data) && bytes.Equal(data[i:i+10], []byte("C@3ul@3ul\x00")) {
			return RecordTypeC_3ul
		}
		// Check for C (Generic) - Check simpler marker first
		if i+4 <= len(data) && bytes.Equal(data[i:i+4], []byte("C\x00\x00\x00")) {
			return RecordTypeC
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
	// Format: ... [CSuwuw] [padding] [address] [string]
	// Marker is 6 bytes: "CSuwuw" (43 53 75 77 75 77)

	// Scan for marker
	marker := []byte("CSuwuw")
	idx := bytes.Index(r.Data, marker)
	if idx != -1 {
		// Based on analysis, address seems to effectively follow the marker,
		// possibly with alignment padding.
		// In examined traces, address starts 9 bytes after marker start?
		// 0x84: CSuwuw... 0x8D: Address. Difference is 9 bytes.
		// Address is 8 bytes.
		// String starts after address.

		addrStart := idx + 9
		if addrStart+8 <= len(r.Data) {
			r.Address = binary.LittleEndian.Uint64(r.Data[addrStart : addrStart+8])

			// String likely follows address, maybe with padding/nulls
			strStart := addrStart + 8
			// Skip nulls
			for strStart < len(r.Data) && r.Data[strStart] == 0 {
				strStart++
			}

			if strStart < len(r.Data) {
				if end := bytes.IndexByte(r.Data[strStart:], 0); end != -1 {
					r.Label = string(r.Data[strStart : strStart+end])
				}
			}
		}
	}
}

// parseCSRecord parses a CS (Command Submission) record.
// These contain kernel/stream names or UUIDs.
func (r *MTSPRecord) parseCSRecord() {
	// CS marker is "CS\x00\x00" (4 bytes)
	// Structure: [CS Marker] [Address (8 bytes)] [String] [Flags?] [SecondaryAddr]
	// Note: The marker might not be at offset 0 of Data due to unknown header fields.

	marker := []byte{0x43, 0x53, 0x00, 0x00}
	idx := bytes.Index(r.Data, marker)
	if idx != -1 {
		// Address is immediately after the 4-byte marker
		addrStart := idx + 4
		if addrStart+8 <= len(r.Data) {
			r.Address = binary.LittleEndian.Uint64(r.Data[addrStart : addrStart+8])

			// String starts immediately after address
			strStart := addrStart + 8
			if strStart < len(r.Data) {
				// Find null terminator
				if end := bytes.IndexByte(r.Data[strStart:], 0); end != -1 {
					r.Label = string(r.Data[strStart : strStart+end])

					// Heuristic: Check for Secondary Address after string
					// Check at aligned offsets after string end?
					// Dump analysis: StrEnd -> Padding -> 4 bytes -> Address
					// StrEnd is strStart + end + 1 (null)
					afterStr := strStart + end + 1
					// Align to 4 bytes?
					rem := afterStr % 4
					if rem != 0 {
						afterStr += (4 - rem)
					}

					// Skip 8 bytes (Flags/Count/Magic?)
					nextAddrStart := afterStr + 8
					if nextAddrStart+8 <= len(r.Data) {
						r.SecondaryAddr = binary.LittleEndian.Uint64(r.Data[nextAddrStart : nextAddrStart+8])
					}
				}
			}
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
	}

	// Cul marker at 0x24 (4 bytes)
	// Address at 0x28
	if len(r.Data) >= 0x30 {
		cul.BufferAddr = binary.LittleEndian.Uint64(r.Data[0x28:0x30])
	}
	// Value/Size at 0x30
	if len(r.Data) >= 0x34 {
		cul.Field1 = binary.LittleEndian.Uint32(r.Data[0x30:0x34])
	}

	return cul, nil
}

// ParseCuwRecord parses a Cuw (Command Update/Write) record.
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
	}

	// Check for "Cuwuw" (8 bytes marker?) or "Cuw" (4 bytes)
	// Markers at 0x24
	if len(r.Data) >= 0x2C && bytes.HasPrefix(r.Data[0x24:], []byte("Cuwuw")) {
		// Cuwuw variant
		// Marker: 0x24 (8 bytes approx? Cuwuw\0\0\0)
		// Address starts at 0x2C (0x24 + 8)
		if len(r.Data) >= 0x34 {
			cuw.BufferAddr = binary.LittleEndian.Uint64(r.Data[0x2c:0x34])
		}
		// Extract extra fields if needed
		if len(r.Data) >= 0x38 {
			val := binary.LittleEndian.Uint32(r.Data[0x34:0x38])
			cuw.Field2 = val
		}
	} else {
		// Standard Cuw variant
		// Marker: 0x24 "Cuw\0"
		// Address starts at 0x28
		cuw.BufferAddr = binary.LittleEndian.Uint64(r.Data[0x28:0x30])
		// Value at 0x30
		if len(r.Data) >= 0x38 {
			cuw.Field1 = binary.LittleEndian.Uint64(r.Data[0x30:0x38])
		}
	}

	return cuw, nil
}

// ParseCuwRecord parses a Cuw (Command Update/Write) record.
// ... (existing code) ...

// CtURecord represents a parsed CtU (Buffer Definition) record.
type CtURecord struct {
	RecordSize uint32
	Address    uint64
	Name       string
}

// ParseCtURecord parses a CtU record (CtU<b>ulul).
// Format: Marker at ~0x24/0x2C, followed by Address, then Name.
func (r *MTSPRecord) ParseCtURecord() (*CtURecord, error) {
	if r.Type != RecordTypeCtU {
		return nil, fmt.Errorf("not a CtU record (type=%s)", r.Type)
	}

	// Find marker "CtU<b>ulul"
	marker := []byte("CtU<b>ulul")
	idx := bytes.Index(r.Data, marker)
	if idx == -1 {
		return nil, fmt.Errorf("CtU marker not found")
	}

	// Address usually follows marker + padding?
	// In dependencies.go logic: base = idx, Address at base+8?
	// Let's look at dependencies.go:
	// base := absolutePos + 12 (where absolutePos was start of marker?)
	// No, dependencies.go finds marker, then says "Label starts at +12" for CS.
	// For Bind (CtU): ctBindMarker := "CtU<b>ulul\x00\x00" (12 bytes)
	// bindPos := bytes.Index(..., marker)
	// base := bindPos + 12
	// bufferAddr := data[base+8 : base+16] -> This implies Address is at Marker + 12 + 8 = Marker + 20?
	// string starts at base+16 -> Marker + 12 + 16 = Marker + 28?

	// Let's implement based on dependencies.go offsets relative to Marker start.
	// Marker len is 10 bytes "CtU<b>ulul". dependencies.go uses 12 bytes with nulls.

	addrOffset := idx + 20
	if addrOffset+8 > len(r.Data) {
		return nil, fmt.Errorf("CtU record too small for address")
	}
	addr := binary.LittleEndian.Uint64(r.Data[addrOffset : addrOffset+8])

	nameOffset := idx + 28
	if nameOffset >= len(r.Data) {
		return nil, fmt.Errorf("CtU record too small for name")
	}

	// Extract null-terminated string
	nameData := r.Data[nameOffset:]
	end := bytes.IndexByte(nameData, 0)
	var name string
	if end != -1 {
		name = string(nameData[:end])
	} else {
		name = string(nameData)
	}

	return &CtURecord{
		RecordSize: uint32(r.Size),
		Address:    addr,
		Name:       name,
	}, nil
}

// ParseCtululRecord parses a Ctulul record.
// Structure appears to be similar to Ctt/Ct (Binding info).
// Marker "Ctulul\0\0" at ~0x24.
func (r *MTSPRecord) ParseCtululRecord() (*CttRecord, error) {
	if r.Type != RecordTypeCtulul {
		return nil, fmt.Errorf("not a Ctulul record (type=%s)", r.Type)
	}

	// Find marker
	marker := []byte("Ctulul\x00")
	idx := bytes.Index(r.Data, marker[:6]) // Just match prefix
	if idx == -1 {
		return nil, fmt.Errorf("Ctulul marker not found")
	}

	// Based on analysis:
	// Marker at idx.
	// Pipeline Addr at idx + 8?
	// Count at idx + 44?
	// Buffer Array at idx + 52?

	base := idx
	if base+52 > len(r.Data) {
		return nil, fmt.Errorf("Ctulul record too small header")
	}

	count := binary.LittleEndian.Uint32(r.Data[base+44 : base+48])
	// stride := binary.LittleEndian.Uint32(r.Data[base+48 : base+52]) // Usually 8

	ctt := &CttRecord{
		RecordSize:   uint32(r.Size),
		CommandFlags: binary.LittleEndian.Uint32(r.Data[0x04:0x08]), // Assuming flags at 0x04
		PipelineAddr: binary.LittleEndian.Uint64(r.Data[base+8 : base+16]),
		BindingCount: count,
		// Function Addr? Maybe at base+16?
	}

	// Extract buffers
	bufferStart := base + 52
	if bufferStart+int(count)*8 <= len(r.Data) {
		ctt.BufferBindings = make([]uint64, count)
		for i := 0; i < int(count); i++ {
			offset := bufferStart + i*8
			ctt.BufferBindings[i] = binary.LittleEndian.Uint64(r.Data[offset : offset+8])
		}
	}

	return ctt, nil
}

// CRecord represents a generic command record (e.g. PopDebugGroup).
type CRecord struct {
	RecordSize   uint32
	CommandFlags uint32
	EncoderAddr  uint64
}

func (r *MTSPRecord) ParseCRecord() (*CRecord, error) {
	if r.Type != RecordTypeC {
		return nil, fmt.Errorf("not a C record (type=%s)", r.Type)
	}

	// Marker "C\0\0\0" at ~0x24
	marker := []byte("C\x00\x00\x00")
	idx := bytes.Index(r.Data, marker)
	if idx == -1 {
		return nil, fmt.Errorf("C marker not found")
	}

	// Encoder Addr usually at marker + 8
	addrStart := idx + 8
	if addrStart+8 > len(r.Data) {
		return nil, fmt.Errorf("C record too small for address")
	}

	return &CRecord{
		RecordSize:   uint32(r.Size),
		CommandFlags: binary.LittleEndian.Uint32(r.Data[0x04:0x08]),
		EncoderAddr:  binary.LittleEndian.Uint64(r.Data[addrStart : addrStart+8]),
	}, nil
}

// CDispatchRecord represents a compute dispatch record (C@3ul@3ul).
type CDispatchRecord struct {
	RecordSize   uint32
	CommandFlags uint32
	EncoderID    uint64 // Address at 0x30
	GridSize     [3]uint32
	GroupSize    [3]uint32
}

func (r *MTSPRecord) ParseDispatchRecord() (*CDispatchRecord, error) {
	if r.Type != RecordTypeC_3ul {
		return nil, fmt.Errorf("not a dispatch record (type=%s)", r.Type)
	}

	// Marker "C@3ul@3ul" at ~0x24.
	// 0x30: Encoder Addr (8 bytes)
	// 0x38: Grid X
	// 0x3C: Grid Y
	// 0x40: Grid Z
	// ... (0x44, 0x48, 0x4C reserved/unknown?)
	// 0x50: Group X
	// 0x54: Group Y
	// 0x58: Group Z

	if len(r.Data) < 0x60 {
		return nil, fmt.Errorf("dispatch record too small")
	}

	return &CDispatchRecord{
		RecordSize:   uint32(r.Size),
		CommandFlags: binary.LittleEndian.Uint32(r.Data[0x04:0x08]),
		EncoderID:    binary.LittleEndian.Uint64(r.Data[0x30:0x38]),
		GridSize: [3]uint32{
			uint32(binary.LittleEndian.Uint64(r.Data[0x38:0x40])),
			uint32(binary.LittleEndian.Uint64(r.Data[0x40:0x48])),
			uint32(binary.LittleEndian.Uint64(r.Data[0x48:0x50])),
		},
		GroupSize: [3]uint32{
			uint32(binary.LittleEndian.Uint64(r.Data[0x50:0x58])),
			uint32(binary.LittleEndian.Uint64(r.Data[0x58:0x60])),
			uint32(binary.LittleEndian.Uint64(r.Data[0x60:0x68])),
		},
	}, nil
}

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
