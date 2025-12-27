package mtlb

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

// MTLBHeader represents the header of an MTLB file.
type MTLBHeader struct {
	Magic          [4]byte // "MTLB"
	Version        uint32
	Flags          uint32
	Reserved       uint32
	TotalSize      uint64
	FunctionTable  uint64 // Offset to function table
	StringTable    uint64 // Offset to string table
	BytecodeOffset uint64 // Offset to bytecode
}

// MTLBFile represents a parsed Metal Library Binary file.
type MTLBFile struct {
	Header MTLBHeader
	Data   []byte
}

// MTLBFunction represents a function in the library.
type MTLBFunction struct {
	Name string
	// We'll add more fields as we reverse engineer them
	// Offset and Size might be available
}

// ParseMTLB parses the given data as an MTLB file.
func ParseMTLB(data []byte) (*MTLBFile, error) {
	if len(data) < 48 { // Header is 48 bytes
		return nil, errors.New("data too short for MTLB header")
	}

	header := MTLBHeader{}
	copy(header.Magic[:], data[0:4])

	if string(header.Magic[:]) != "MTLB" {
		return nil, fmt.Errorf("invalid magic: %s", string(header.Magic[:]))
	}

	header.Version = binary.LittleEndian.Uint32(data[4:8])
	header.Flags = binary.LittleEndian.Uint32(data[8:12])
	header.Reserved = binary.LittleEndian.Uint32(data[12:16])
	header.TotalSize = binary.LittleEndian.Uint64(data[16:24])
	header.FunctionTable = binary.LittleEndian.Uint64(data[24:32])
	header.StringTable = binary.LittleEndian.Uint64(data[32:40])
	header.BytecodeOffset = binary.LittleEndian.Uint64(data[40:48])

	return &MTLBFile{
		Header: header,
		Data:   data,
	}, nil
}

// ListFunctions returns a list of function names found in the MTLB file.
// This is a best-effort implementation based on the user's description
// that function names appear as null-terminated strings.
// ListFunctions returns a list of function names found in the MTLB file.
// It scans the file for "NAMED" tags which precede function names.
func (m *MTLBFile) ListFunctions() ([]string, error) {
	var functions []string

	// Start scanning from FunctionTable offset
	start := m.Header.FunctionTable
	if start >= uint64(len(m.Data)) {
		return nil, nil
	}

	data := m.Data[start:]

	// Identify "NAMED" tags (0x4E 41 4D 45 44 00)
	namedTag := []byte("NAMED\x00")

	idx := 0
	for idx < len(data) {
		// Search for tag
		pos := bytes.Index(data[idx:], namedTag)
		if pos == -1 {
			break
		}

		// Move to start of name
		nameStart := idx + pos + len(namedTag)

		// Find end of name (null terminator)
		nameEnd := bytes.IndexByte(data[nameStart:], 0)
		if nameEnd == -1 {
			break
		}

		// Extract name
		name := string(data[nameStart : nameStart+nameEnd])
		if len(name) > 0 {
			functions = append(functions, name)
		}

		// Advance index
		idx = nameStart + nameEnd + 1
	}

	return functions, nil
}

func cleanString(s string) string {
	// Filter out non-printable or short strings if necessary
	// For now just return as is
	return s
}
