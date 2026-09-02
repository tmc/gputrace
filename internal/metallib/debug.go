package metallib

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"unicode/utf8"
)

// DebugRecord describes one source record in a metallib debug table. Records
// are returned in table order; this type does not assert a function binding.
type DebugRecord struct {
	Source     string
	Line       uint32
	Dependency string
}

// FunctionDebug binds a function-list entry to the private metadata entry at
// the same metallib table index.
type FunctionDebug struct {
	Function Function
	Debug    DebugRecord
}

// ListFunctionDebug returns function/debug pairs from the parallel metallib
// function and private-metadata tables. It refuses absent sections, malformed
// entries, count mismatches, and trailing private metadata.
func (m *File) ListFunctionDebug() ([]FunctionDebug, error) {
	if m == nil {
		return nil, errors.New("metallib: nil file")
	}
	functions, err := m.ListFunctionMetadata()
	if err != nil {
		return nil, err
	}
	private, ok := metallibSection(m.Data, m.Header.PrivateMetadata, m.Header.PrivateMetadataSize)
	if !ok {
		return nil, errors.New("metallib: missing private metadata section")
	}
	pairs := make([]FunctionDebug, 0, len(functions))
	pos := 0
	for i, function := range functions {
		if len(private)-pos < 4 {
			return nil, fmt.Errorf("metallib: private metadata has %d entries, want %d", i, len(functions))
		}
		size := int(binary.LittleEndian.Uint32(private[pos : pos+4]))
		if size < 8 || size > len(private)-pos {
			return nil, fmt.Errorf("metallib: invalid private metadata entry %d size %d", i, size)
		}
		entry := private[pos+4 : pos+size]
		record, end, ok := parseDebugRecord(entry, 0)
		if !ok || len(entry)-end != 4 || string(entry[end:]) != "ENDT" {
			return nil, fmt.Errorf("metallib: invalid private metadata entry %d", i)
		}
		pairs = append(pairs, FunctionDebug{Function: function, Debug: record})
		pos += size
	}
	if pos != len(private) {
		return nil, fmt.Errorf("metallib: %d trailing private metadata bytes", len(private)-pos)
	}
	return pairs, nil
}

// ListDebugRecords returns structurally valid DEBI/DEPF records found in the
// metallib. It does not associate records with functions.
func (m *File) ListDebugRecords() []DebugRecord {
	if m == nil {
		return nil
	}
	data := m.Data
	if section, ok := metallibSection(data, m.Header.PrivateMetadata, m.Header.PrivateMetadataSize); ok {
		data = section
	}
	var records []DebugRecord
	for at := 0; at < len(data); {
		i := bytes.Index(data[at:], []byte("DEBI"))
		if i < 0 {
			break
		}
		i += at
		record, end, ok := parseDebugRecord(data, i)
		if ok {
			records = append(records, record)
			at = end
			continue
		}
		at = i + 4
	}
	return records
}

func parseDebugRecord(data []byte, at int) (DebugRecord, int, bool) {
	const maxField = 4096
	if at < 0 || len(data)-at < 6 || string(data[at:at+4]) != "DEBI" {
		return DebugRecord{}, at, false
	}
	sourceLen := int(binary.LittleEndian.Uint16(data[at+4 : at+6]))
	if sourceLen < 5 || sourceLen > maxField || len(data)-(at+6) < sourceLen {
		return DebugRecord{}, at, false
	}
	sourcePayload := data[at+6 : at+6+sourceLen]
	next := at + 6 + sourceLen
	if len(data)-next < 6 || string(data[next:next+4]) != "DEPF" {
		return DebugRecord{}, at, false
	}
	dependencyLen := int(binary.LittleEndian.Uint16(data[next+4 : next+6]))
	if dependencyLen < 1 || dependencyLen > maxField || len(data)-(next+6) < dependencyLen {
		return DebugRecord{}, at, false
	}
	dependencyPayload := data[next+6 : next+6+dependencyLen]
	source, ok := debugString(sourcePayload[4:])
	if !ok {
		return DebugRecord{}, at, false
	}
	dependency, ok := debugString(dependencyPayload)
	if !ok {
		return DebugRecord{}, at, false
	}
	return DebugRecord{
		Source:     source,
		Line:       binary.LittleEndian.Uint32(sourcePayload[:4]),
		Dependency: dependency,
	}, next + 6 + dependencyLen, true
}

func debugString(p []byte) (string, bool) {
	end := bytes.IndexByte(p, 0)
	if end < 0 || end == 0 || !utf8.Valid(p[:end]) {
		return "", false
	}
	return string(p[:end]), true
}
