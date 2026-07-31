package trace

import (
	"encoding/binary"
	"testing"
)

// csRecord builds a CS record with the layout scanFunctionNames expects:
// "CS\0\0" | object address (8) | label (NUL) | pad to 4 | tag (4) | function
// address (8).
func csRecord(objAddr uint64, label string, tag uint32, funcAddr uint64) []byte {
	rec := []byte("CS\x00\x00")
	rec = binary.LittleEndian.AppendUint64(rec, objAddr)
	rec = append(rec, label...)
	rec = append(rec, 0)
	for len(rec)%4 != 0 {
		rec = append(rec, 0)
	}
	rec = binary.LittleEndian.AppendUint32(rec, tag)
	return binary.LittleEndian.AppendUint64(rec, funcAddr)
}

func TestScanFunctionNames(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want map[uint64]string
	}{
		{
			name: "function record",
			data: csRecord(0xb8ccacd00, "simple_add", csTagFunction, 0x1035d52f0),
			want: map[uint64]string{0x1035d52f0: "simple_add"},
		},
		// An odd-length label pads to the next 4-byte boundary; an
		// aligned one does not. Both must find the same field.
		{
			name: "label length forces padding",
			data: csRecord(0xb8ccacd00, "simple_multiply", csTagFunction, 0x1035cfe90),
			want: map[uint64]string{0x1035cfe90: "simple_multiply"},
		},
		// A command encoder stores something other than a function
		// address in the same position. Reading it would name a
		// pipeline after an encoder.
		{
			name: "encoder record ignored",
			data: csRecord(0xb8cc48280, "Encoder_1_simple_add", 0x04, 0x49ab6a000000008),
			want: map[uint64]string{},
		},
		{
			name: "library uuid record ignored",
			data: csRecord(0xb8ccacd00, "369CB11B-DC04-3E8B-A356-4BB0656C5D0D", 0x34, 0xffffc05c),
			want: map[uint64]string{},
		},
		{
			name: "empty label skipped",
			data: csRecord(0xb8ccacd00, "", csTagFunction, 0x1035d52f0),
			want: map[uint64]string{},
		},
		{
			name: "zero function address skipped",
			data: csRecord(0xb8ccacd00, "simple_add", csTagFunction, 0),
			want: map[uint64]string{},
		},
		{
			name: "truncated record does not panic",
			data: []byte("CS\x00\x00\x01\x02"),
			want: map[uint64]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := make(map[uint64]string)
			scanFunctionNames(tt.data, got)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for addr, name := range tt.want {
				if got[addr] != name {
					t.Errorf("0x%x = %q, want %q", addr, got[addr], name)
				}
			}
		})
	}
}

// TestScanFunctionNamesMixed pins that the function record is still found when
// the encoder and library records that share the CS marker surround it.
func TestScanFunctionNamesMixed(t *testing.T) {
	var data []byte
	data = append(data, csRecord(0xb8cc48280, "Encoder_1_simple_add", 0x04, 0x49ab6a000000008)...)
	data = append(data, csRecord(0xb8ccacd00, "simple_add", csTagFunction, 0x1035d52f0)...)
	data = append(data, csRecord(0xb8ccacd00, "369CB11B-DC04", 0x34, 0xffffc05c)...)

	got := make(map[uint64]string)
	scanFunctionNames(data, got)

	if len(got) != 1 || got[0x1035d52f0] != "simple_add" {
		t.Errorf("got %v, want only the function record", got)
	}
}
