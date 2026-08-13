package metallib

import (
	"bytes"
	"encoding/binary"
	"unicode/utf8"
)

// DebugRecord describes one source record in a metallib debug table. Records
// are returned in table order; this type does not assert a function binding.
type DebugRecord struct {
	Source     string
	Line       uint32
	Dependency string
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
