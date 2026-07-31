package trace

import (
	"bytes"
	"encoding/binary"
)

// A CtU<b>ulul record defines a buffer: its address and the name Metal gave
// it, such as "MTLBuffer-93-0" or "MTLHeap-2-0". It is how an address seen in
// a dispatch's bindings is resolved to a buffer file in the bundle.
//
// Layout, from the start of the marker:
//
//	+0x00  "CtU<b>ulul"  (10 bytes)
//	+0x0a  padding       (2 bytes)
//	+0x0c  first address (8 bytes, little-endian)
//	+0x14  buffer address
//	+0x1c  buffer name, NUL-terminated
const CtUMarker = "CtU<b>ulul"

var ctuMarker = []byte(CtUMarker)

const (
	ctuAddrOffset = 0x14
	ctuNameOffset = 0x1c

	// ctuMaxNameLen bounds the name scan so a record whose terminator was
	// lost cannot swallow the rest of the capture.
	ctuMaxNameLen = 128
)

// ParseCtUAt decodes the buffer definition whose marker starts at pos. It
// reports false if the record is truncated or carries no name.
func ParseCtUAt(data []byte, pos int) (addr uint64, name string, ok bool) {
	if pos < 0 || pos+ctuNameOffset > len(data) {
		return 0, "", false
	}
	addr = binary.LittleEndian.Uint64(data[pos+ctuAddrOffset : pos+ctuNameOffset])

	nameStart := pos + ctuNameOffset
	limit := nameStart + ctuMaxNameLen
	if limit > len(data) {
		limit = len(data)
	}
	end := bytes.IndexByte(data[nameStart:limit], 0)
	if end <= 0 {
		return 0, "", false
	}
	return addr, string(data[nameStart : nameStart+end]), true
}

// ScanBufferNames returns the buffer address to name mapping defined by every
// CtU<b>ulul record in data.
func ScanBufferNames(data []byte) map[uint64]string {
	names := make(map[uint64]string)
	for offset := 0; offset < len(data); {
		pos := bytes.Index(data[offset:], ctuMarker)
		if pos == -1 {
			break
		}
		pos += offset
		if addr, name, ok := ParseCtUAt(data, pos); ok {
			names[addr] = name
		}
		offset = pos + len(ctuMarker)
	}
	return names
}
