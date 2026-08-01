package trace

import (
	"encoding/binary"
	"testing"
)

// cutRecord builds a CUt record with the layout scanArchiveFunctions expects:
// "CUt\0" | object id (8) | archive id (NUL) | pad to 4 | 8 unread bytes |
// tag (4) | function address (8).
func cutRecord(objID uint64, archiveID string, tag uint32, funcAddr uint64) []byte {
	rec := []byte("CUt\x00")
	rec = binary.LittleEndian.AppendUint64(rec, objID)
	rec = append(rec, archiveID...)
	rec = append(rec, 0)
	for len(rec)%4 != 0 {
		rec = append(rec, 0)
	}
	rec = append(rec, make([]byte, archiveFunctionTagSkip)...)
	rec = binary.LittleEndian.AppendUint32(rec, tag)
	return binary.LittleEndian.AppendUint64(rec, funcAddr)
}

func TestScanArchiveFunctions(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want map[uint64]string
	}{
		{
			name: "archive function record",
			data: cutRecord(0x9a6e54580, "277B1A8103415728", csTagFunction, 0x9a43dc540),
			want: map[uint64]string{0x9a43dc540: "archive:277B1A8103415728"},
		},
		// The tag check is the whole guard against the 8-byte skip being
		// wrong for a record this decoder has not seen.
		{
			name: "wrong tag rejected",
			data: cutRecord(0x9a6e54580, "277B1A8103415728", 0x34, 0x9a43dc540),
			want: map[uint64]string{},
		},
		{
			name: "empty archive id skipped",
			data: cutRecord(0x9a6e54580, "", csTagFunction, 0x9a43dc540),
			want: map[uint64]string{},
		},
		{
			name: "zero function address skipped",
			data: cutRecord(0x9a6e54580, "277B1A8103415728", csTagFunction, 0),
			want: map[uint64]string{},
		},
		{
			name: "truncated record does not panic",
			data: []byte("CUt\x00\x01\x02"),
			want: map[uint64]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := make(map[uint64]string)
			scanArchiveFunctions(tt.data, got)
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

// TestScanArchiveFunctionsKeepsRealName pins that an archive id never displaces
// a name a CS record already supplied for the same function address.
func TestScanArchiveFunctionsKeepsRealName(t *testing.T) {
	got := map[uint64]string{0x9a43dc540: "rope_single_bfloat16_"}
	scanArchiveFunctions(cutRecord(0x9a6e54580, "277B1A8103415728", csTagFunction, 0x9a43dc540), got)
	if got[0x9a43dc540] != "rope_single_bfloat16_" {
		t.Errorf("got %q, want the CS name to win", got[0x9a43dc540])
	}
}

// TestArchiveFunctionAttribution is the reason the scan exists: a pipeline
// whose function comes from an archive used to leave every one of its
// dispatches in the single unknown bucket, merged with every other such
// pipeline's.
func TestArchiveFunctionAttribution(t *testing.T) {
	const archiveFuncA, archiveFuncB = 0xb000, 0xc000
	const pipelineC, pipelineD = 0x3000, 0x4000

	var res []byte
	res = append(res, csRecord(0xdead0000, "kernel_a", csTagFunction, testFuncA)...)
	res = append(res, cttRecord(testFuncA, testPipelineA)...)
	res = append(res, cutRecord(0xdead0000, "277B1A8103415728", csTagFunction, archiveFuncA)...)
	res = append(res, cttRecord(archiveFuncA, pipelineC)...)
	res = append(res, cutRecord(0xdead0000, "F0BBD414E56C5B81", csTagFunction, archiveFuncB)...)
	res = append(res, cttRecord(archiveFuncB, pipelineD)...)

	var c []byte
	c = append(c, commandBufferHeader(1)...)
	c = append(c, pipelineStateRecord(testEncoder, testPipelineA)...)
	c = append(c, dispatchRecord()...)
	c = append(c, pipelineStateRecord(testEncoder, pipelineC)...)
	c = append(c, dispatchRecord()...)
	c = append(c, dispatchRecord()...)
	c = append(c, pipelineStateRecord(testEncoder, pipelineD)...)
	c = append(c, dispatchRecord()...)

	got := dispatchCounts(t, newSyntheticTrace(t, c, res))
	want := map[string]int{
		"kernel_a":                 1,
		"archive:277B1A8103415728": 2,
		"archive:F0BBD414E56C5B81": 1,
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for name, n := range want {
		if got[name] != n {
			t.Errorf("%s = %d, want %d (all: %v)", name, got[name], n, got)
		}
	}
}

func TestIsArchiveFunctionName(t *testing.T) {
	if !IsArchiveFunctionName("archive:277B1A8103415728") {
		t.Error("archive id not recognized")
	}
	if IsArchiveFunctionName("rope_single_bfloat16_") {
		t.Error("function name misread as an archive id")
	}
}
