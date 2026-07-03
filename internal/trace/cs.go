package trace

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// CSRecord represents a Command Submission record from the capture file.
// CS records mark encoder boundaries and associate them with pipeline states or kernel names.
type CSRecord struct {
	Offset       int64  // File offset where CS marker was found
	Address      uint64 // Pipeline state or encoder address
	Identifier   string // Either UUID (for pipeline states) or kernel name
	IsKernelName bool   // True if Identifier is a kernel name, false if UUID
}

// ParseCSRecords extracts all CS (Command Submission) records from the capture file.
// CS records come in two types:
//  1. With UUIDs (preceded by 0x04000000): Pipeline state identifiers
//  2. With kernel names (preceded by 0x09100000): Actual kernel function names
//
// Format:
//
//	[length: uint32] [CS marker: 0x43 0x53 0x00 0x00] [address: uint64] [identifier: null-terminated string]
func (t *Trace) ParseCSRecords() ([]*CSRecord, error) {
	data := t.CaptureData
	if len(data) == 0 {
		return nil, fmt.Errorf("no capture data available")
	}

	csMarker := []byte{0x43, 0x53, 0x00, 0x00} // "CS\x00\x00"
	var records []*CSRecord

	pos := 0
	for {
		// Find next CS marker
		idx := bytes.Index(data[pos:], csMarker)
		if idx == -1 {
			break
		}

		offset := int64(pos + idx)

		// Read address (8 bytes after CS marker)
		addrStart := pos + idx + 4
		if addrStart+8 > len(data) {
			break
		}

		address := binary.LittleEndian.Uint64(data[addrStart : addrStart+8])

		// Try to read identifier (starts after address)
		identStart := addrStart + 8
		if identStart >= len(data) {
			pos = pos + idx + 4
			continue
		}

		// Read null-terminated string
		identEnd := identStart
		maxLen := 256 // Maximum reasonable identifier length
		for identEnd < len(data) && identEnd-identStart < maxLen {
			if data[identEnd] == 0 {
				break
			}
			identEnd++
		}

		if identEnd > identStart {
			identifier := string(data[identStart:identEnd])

			// Determine if this is a kernel name or UUID
			// UUIDs have format like "XXXXXXXX-XXXX-XXXX-XXXX-XXXXXXXXXXXX" (with hyphens)
			// Kernel names are like "vs_Multiplyfloat32" (no hyphens, often with underscores)
			isKernelName := !isUUID(identifier)

			// Only add if identifier looks valid
			if isPrintableASCII(identifier) {
				records = append(records, &CSRecord{
					Offset:       offset,
					Address:      address,
					Identifier:   identifier,
					IsKernelName: isKernelName,
				})
			}
		}

		pos = pos + idx + 4
	}

	return records, nil
}

// KernelNameCSRecords returns only CS records that contain kernel names (not UUIDs).
func (t *Trace) KernelNameCSRecords() ([]*CSRecord, error) {
	allRecords, err := t.ParseCSRecords()
	if err != nil {
		return nil, err
	}

	var kernelRecords []*CSRecord
	for _, rec := range allRecords {
		if rec.IsKernelName {
			kernelRecords = append(kernelRecords, rec)
		}
	}

	return kernelRecords, nil
}

// UUIDCSRecords returns only CS records that contain pipeline state UUIDs.
func (t *Trace) UUIDCSRecords() ([]*CSRecord, error) {
	allRecords, err := t.ParseCSRecords()
	if err != nil {
		return nil, err
	}

	var uuidRecords []*CSRecord
	for _, rec := range allRecords {
		if !rec.IsKernelName {
			uuidRecords = append(uuidRecords, rec)
		}
	}

	return uuidRecords, nil
}

// CountCSRecords returns the total number of CS records in the trace.
func (t *Trace) CountCSRecords() (int, error) {
	records, err := t.ParseCSRecords()
	if err != nil {
		return 0, err
	}
	return len(records), nil
}

// isPrintableASCII checks if a string contains only printable ASCII characters.
func isPrintableASCII(s string) bool {
	for _, r := range s {
		if r < 32 || r > 126 {
			return false
		}
	}
	return len(s) > 0
}

// isUUID checks if a string looks like a UUID (XXXXXXXX-XXXX-XXXX-XXXX-XXXXXXXXXXXX).
func isUUID(s string) bool {
	// UUIDs are 36 characters: 8-4-4-4-12 with hyphens
	if len(s) != 36 {
		return false
	}

	// Check for hyphens at the right positions
	if s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return false
	}

	// Check that other characters are hex digits
	for i, r := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			continue // Skip hyphens
		}
		if !((r >= '0' && r <= '9') || (r >= 'A' && r <= 'F') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}

	return true
}
