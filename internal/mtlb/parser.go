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
	Name      string
	NameOff   uint64
	Offset    uint64
	Size      uint64
	SizeKnown bool
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
func (m *MTLBFile) ListFunctions() ([]string, error) {
	metadata, err := m.ListFunctionMetadata()
	if err != nil {
		return nil, err
	}

	functions := make([]string, 0, len(metadata))
	for _, fn := range metadata {
		functions = append(functions, fn.Name)
	}
	return functions, nil
}

// ListFunctionMetadata returns function names and any per-function metadata the
// parser can prove from tagged MTLB function-table entries.
func (m *MTLBFile) ListFunctionMetadata() ([]MTLBFunction, error) {
	if functions, ok := m.parseTaggedFunctionTable(); ok {
		return functions, nil
	}

	return m.scanLegacyFunctionNames(), nil
}

func (m *MTLBFile) parseTaggedFunctionTable() ([]MTLBFunction, bool) {
	if m.Header.FunctionTable > uint64(len(m.Data)) || uint64(len(m.Data))-m.Header.FunctionTable < 8 {
		return nil, false
	}

	start := int(m.Header.FunctionTable)
	count := binary.LittleEndian.Uint32(m.Data[start : start+4])
	entrySize := binary.LittleEndian.Uint32(m.Data[start+4 : start+8])
	if count == 0 {
		return nil, false
	}
	if count > uint32(len(m.Data)) || entrySize < 4 {
		return nil, false
	}

	functions := make([]MTLBFunction, 0, count)
	entryStart := start + 8
	for i := uint32(0); i < count; i++ {
		size := int(entrySize)
		if size <= 0 || entryStart+size > len(m.Data) {
			return nil, false
		}

		entry := m.Data[entryStart : entryStart+size]
		fn, ok := parseTaggedFunctionEntry(entry, uint64(entryStart))
		if !ok {
			return nil, false
		}
		functions = append(functions, fn)

		if i+1 < count {
			if len(entry) < 4 {
				return nil, false
			}
			entrySize = binary.LittleEndian.Uint32(entry[len(entry)-4:])
			if entrySize < 4 {
				return nil, false
			}
		}
		entryStart += size
	}

	return functions, true
}

func parseTaggedFunctionEntry(entry []byte, base uint64) (MTLBFunction, bool) {
	var fn MTLBFunction
	for pos := 0; pos+4 <= len(entry); {
		tag := string(entry[pos : pos+4])
		pos += 4
		if tag == "ENDT" {
			return fn, fn.Name != ""
		}
		if pos+2 > len(entry) {
			return MTLBFunction{}, false
		}
		payloadLen := int(binary.LittleEndian.Uint16(entry[pos : pos+2]))
		pos += 2
		if payloadLen < 0 || pos+payloadLen > len(entry) {
			return MTLBFunction{}, false
		}

		payload := entry[pos : pos+payloadLen]
		payloadOff := base + uint64(pos)
		switch tag {
		case "NAME":
			if end := bytes.IndexByte(payload, 0); end >= 0 {
				fn.Name = string(payload[:end])
			} else {
				fn.Name = string(payload)
			}
			fn.NameOff = payloadOff
		case "OFFT":
			if len(payload) >= 8 {
				fn.Offset = binary.LittleEndian.Uint64(payload[:8])
			}
		case "MDSZ":
			if len(payload) >= 8 {
				fn.Size = binary.LittleEndian.Uint64(payload[:8])
				fn.SizeKnown = fn.Size > 0
			}
		}

		pos += payloadLen
	}

	return MTLBFunction{}, false
}

func (m *MTLBFile) scanLegacyFunctionNames() []MTLBFunction {
	var functions []MTLBFunction

	// Start scanning from FunctionTable offset
	start := m.Header.FunctionTable
	if start >= uint64(len(m.Data)) {
		return nil
	}

	data := m.Data[start:]

	// Identify "NAMED" tags (0x4E 41 4D 45 44 00) or "NAME;" (0x4E 41 4D 45 3B 00)
	tags := [][]byte{
		[]byte("NAMED\x00"),
		[]byte("NAME;\x00"),
	}

	idx := 0
	for idx < len(data) {
		// Search for any tag
		bestPos := -1
		tagLen := 0

		for _, tag := range tags {
			pos := bytes.Index(data[idx:], tag)
			if pos != -1 {
				if bestPos == -1 || pos < bestPos {
					bestPos = pos
					tagLen = len(tag)
				}
			}
		}

		if bestPos == -1 {
			break
		}

		pos := bestPos
		// Move to start of name (after tag)
		nameStart := idx + pos + tagLen

		// Find end of name (null terminator)
		nameEnd := bytes.IndexByte(data[nameStart:], 0)
		if nameEnd == -1 {
			break
		}

		// Extract name
		name := string(data[nameStart : nameStart+nameEnd])
		if len(name) > 0 {
			functions = append(functions, MTLBFunction{
				Name:    name,
				NameOff: start + uint64(nameStart),
			})
		}

		// Advance index
		idx = nameStart + nameEnd + 1
	}

	return functions
}
