package trace

import (
	"encoding/binary"
	"reflect"
	"testing"
)

// ctuRecord builds a CtU<b>ulul record with the given address and name,
// preceded by lead bytes of padding.
func ctuRecord(lead int, addr uint64, name string) []byte {
	rec := make([]byte, lead+ctuNameOffset+len(name)+1)
	copy(rec[lead:], ctuMarker)
	binary.LittleEndian.PutUint64(rec[lead+ctuAddrOffset:], addr)
	copy(rec[lead+ctuNameOffset:], name)
	return rec
}

func TestParseCtUAt(t *testing.T) {
	rec := ctuRecord(0, 0xdeadbeef, "MTLBuffer-93-0")
	addr, name, ok := ParseCtUAt(rec, 0)
	if !ok {
		t.Fatal("ParseCtUAt reported a well-formed record as invalid")
	}
	if addr != 0xdeadbeef {
		t.Errorf("addr = %#x, want 0xdeadbeef", addr)
	}
	if name != "MTLBuffer-93-0" {
		t.Errorf("name = %q, want %q", name, "MTLBuffer-93-0")
	}
}

func TestParseCtUAtRejectsBadRecords(t *testing.T) {
	full := ctuRecord(0, 1, "MTLBuffer-1-0")

	tests := []struct {
		name string
		data []byte
		pos  int
	}{
		{name: "truncated before the name", data: full[:ctuNameOffset-1], pos: 0},
		{name: "truncated before the address", data: full[:4], pos: 0},
		{name: "empty name", data: ctuRecord(0, 1, ""), pos: 0},
		{name: "negative position", data: full, pos: -1},
		{name: "position past the end", data: full, pos: len(full)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, ok := ParseCtUAt(tt.data, tt.pos); ok {
				t.Error("ParseCtUAt accepted a malformed record")
			}
		})
	}
}

// A name with no terminator must not run off into the rest of the capture.
func TestParseCtUAtBoundsUnterminatedName(t *testing.T) {
	data := make([]byte, ctuNameOffset+ctuMaxNameLen*4)
	copy(data, ctuMarker)
	for i := ctuNameOffset; i < len(data); i++ {
		data[i] = 'A'
	}
	if _, _, ok := ParseCtUAt(data, 0); ok {
		t.Error("ParseCtUAt accepted a name with no terminator")
	}
}

func TestScanBufferNames(t *testing.T) {
	var data []byte
	data = append(data, ctuRecord(8, 0x1000, "MTLBuffer-1-0")...)
	data = append(data, ctuRecord(4, 0x2000, "MTLHeap-2-0")...)
	data = append(data, ctuRecord(0, 0x3000, "unnamed-thing")...)

	want := map[uint64]string{
		0x1000: "MTLBuffer-1-0",
		0x2000: "MTLHeap-2-0",
		0x3000: "unnamed-thing",
	}
	if got := ScanBufferNames(data); !reflect.DeepEqual(got, want) {
		t.Errorf("ScanBufferNames() = %v, want %v", got, want)
	}
}

func TestScanBufferNamesNoRecords(t *testing.T) {
	if got := ScanBufferNames([]byte("nothing to see here")); len(got) != 0 {
		t.Errorf("ScanBufferNames() = %v, want empty", got)
	}
}
