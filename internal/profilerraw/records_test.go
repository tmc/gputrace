package profilerraw

import (
	"bytes"
	"testing"
)

func TestRecords(t *testing.T) {
	tests := []struct {
		name        string
		data        []byte
		wantOffsets []int64
		wantSizes   []int
	}{
		{name: "empty"},
		{name: "no marker", data: []byte{1, 2, 3, 4}},
		{name: "one", data: append([]byte{1, 2}, append(recordMarker, 3, 4)...), wantOffsets: []int64{2}, wantSizes: []int{6}},
		{name: "two", data: append(append(bytes.Repeat([]byte{0}, 8), recordMarker...), append(bytes.Repeat([]byte{1}, 12), recordMarker...)...), wantOffsets: []int64{8, 24}, wantSizes: []int{16, 4}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			records := Records(tt.data)
			if len(records) != len(tt.wantOffsets) {
				t.Fatalf("Records returned %d records, want %d", len(records), len(tt.wantOffsets))
			}
			for i, record := range records {
				if record.Offset != tt.wantOffsets[i] || len(record.Data) != tt.wantSizes[i] {
					t.Errorf("record %d = offset %d, size %d; want offset %d, size %d", i, record.Offset, len(record.Data), tt.wantOffsets[i], tt.wantSizes[i])
				}
			}
		})
	}
}
