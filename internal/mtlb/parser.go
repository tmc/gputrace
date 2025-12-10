package mtlb

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// MTLBHeader represents the header of an MTLB file.
type MTLBHeader struct {
	Magic           [4]byte // "MTLB"
	Version         uint32
	Flags           uint32
	Reserved        uint32
	TotalSize       uint64
	FunctionTable   uint64 // Offset to function table
	StringTable     uint64 // Offset to string table
	BytecodeOffset  uint64 // Offset to bytecode
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
func (m *MTLBFile) ListFunctions() ([]string, error) {
	// If string table offset is valid, try to read from there
	// Otherwise scan for strings?

	// The user mentioned:
	// Function table offset
	// String table offset

	// Let's look at the string table offset
	if m.Header.StringTable >= uint64(len(m.Data)) {
		return nil, fmt.Errorf("string table offset %d out of bounds (size %d)", m.Header.StringTable, len(m.Data))
	}

	// We'll treat the string table as a sequence of null-terminated strings
	// until we hit the end of file or something that doesn't look like a string?
	// Or maybe the Function Table points to strings in the String Table.

	// For MVP, let's just scan for strings starting from the String Table offset
	// until the Bytecode offset or EOF.

	start := m.Header.StringTable
	end := uint64(len(m.Data))
	if m.Header.BytecodeOffset > start && m.Header.BytecodeOffset < end {
		end = m.Header.BytecodeOffset
	}

	if start >= end {
		return nil, nil // No string table or invalid range
	}

	var functions []string
	buf := bytes.NewBuffer(m.Data[start:end])

	// This is a naive implementation. The string table might have a structure.
	// But let's try reading null-terminated strings.

	for {
		s, err := buf.ReadString(0)
		if err != nil {
			if err == io.EOF {
				if len(s) > 0 {
					// Last string without null terminator? Unlikely but possible.
					cleanS := cleanString(s)
					if len(cleanS) > 0 {
						functions = append(functions, cleanS)
					}
				}
				break
			}
			return functions, err
		}

		// Remove the null terminator
		s = s[:len(s)-1]
		cleanS := cleanString(s)
		if len(cleanS) > 0 {
			functions = append(functions, cleanS)
		}
	}

	return functions, nil
}

func cleanString(s string) string {
	// Filter out non-printable or short strings if necessary
	// For now just return as is
	return s
}
