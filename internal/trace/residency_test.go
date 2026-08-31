package trace

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// residencyTrace builds a trace whose capture holds the buffer-creation records
// described by buffers, one per entry, plus the residency records asked for.
//
// ParseAPICallList reads the capture from disk rather than from CaptureData, so
// the file has to exist; CaptureData is set as well because BufferStorageModes
// reads that instead, and the two are compared below.
func residencyTrace(t *testing.T, buffers []struct {
	options uint64
	length  uint64
}, newSets, requests int) *Trace {
	t.Helper()
	data := make([]byte, 0x400+0x200*(len(buffers)+newSets+requests+2))

	off := 0x100
	for i, b := range buffers {
		putCululBufferRecord(data, off, 0x106da56b0, b.length, 256, uint64(0x200000000+i*0x1000))
		binary.LittleEndian.PutUint64(data[off+0x18:], b.options)
		off += 0x100
	}
	// A residency set address the request records can refer back to.
	const setAddr = 0x9df0ec000
	for range newSets {
		copy(data[off:], []byte("CUt\x00"))
		binary.LittleEndian.PutUint64(data[off+0x04:], setAddr)
		off += 0x40
	}
	for range requests {
		copy(data[off:], []byte("Ct\x00\x00"))
		binary.LittleEndian.PutUint64(data[off+0x04:], setAddr)
		off += 0x40
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "capture"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return &Trace{Path: dir, CaptureData: data}
}

func TestResidencyReport(t *testing.T) {
	type buf = struct {
		options uint64
		length  uint64
	}
	tests := []struct {
		name        string
		buffers     []buf
		newSets     int
		requests    int
		wantModes   map[string]uint64 // mode -> bytes
		wantBuffers int
		wantExplict bool
	}{
		{
			name:        "all shared",
			buffers:     []buf{{0x00, 1024}, {0x00, 2048}},
			wantModes:   map[string]uint64{"shared": 3072},
			wantBuffers: 2,
		},
		{
			name: "mixed modes are separated and ordered by bytes",
			// 0x20 is StorageModePrivate, 0x10 StorageModeManaged.
			buffers:     []buf{{0x00, 512}, {0x20, 4096}, {0x10, 1024}},
			wantModes:   map[string]uint64{"shared": 512, "private": 4096, "managed": 1024},
			wantBuffers: 3,
		},
		{
			name:        "created but never requested is not explicit",
			buffers:     []buf{{0x00, 64}},
			newSets:     1,
			wantModes:   map[string]uint64{"shared": 64},
			wantBuffers: 1,
		},
		{
			name:        "no buffer records at all",
			wantModes:   map[string]uint64{},
			wantBuffers: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := residencyTrace(t, tt.buffers, tt.newSets, tt.requests)
			r, err := tr.ResidencyReport()
			if err != nil {
				t.Fatal(err)
			}
			if r.Buffers != tt.wantBuffers {
				t.Errorf("Buffers = %d, want %d", r.Buffers, tt.wantBuffers)
			}
			got := map[string]uint64{}
			for _, f := range r.Storage {
				got[f.Mode] = f.Bytes
			}
			for mode, want := range tt.wantModes {
				if got[mode] != want {
					t.Errorf("%s bytes = %d, want %d (all: %v)", mode, got[mode], want, got)
				}
			}
			if len(got) != len(tt.wantModes) {
				t.Errorf("storage modes = %v, want %v", got, tt.wantModes)
			}
			// Largest first, so the mode that dominates a capture is the one
			// read first.
			for i := 1; i < len(r.Storage); i++ {
				if r.Storage[i-1].Bytes < r.Storage[i].Bytes {
					t.Errorf("storage not ordered by bytes: %+v", r.Storage)
				}
			}
			if r.Residency.NewResidencySet != tt.newSets {
				t.Errorf("newResidencySet = %d, want %d", r.Residency.NewResidencySet, tt.newSets)
			}
			if r.Residency.Explicit() != tt.wantExplict {
				t.Errorf("Explicit() = %v, want %v", r.Residency.Explicit(), tt.wantExplict)
			}
		})
	}
}

// A capture with no buffer records must not be reported as a program that
// allocates nothing. It is the difference between a measurement and its
// absence, and the reader cannot recover it from "0".
func TestResidencyFindingDistinguishesAbsence(t *testing.T) {
	tr := residencyTrace(t, nil, 0, 0)
	r, err := tr.ResidencyReport()
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Finding(); !strings.Contains(got, "says nothing") {
		t.Errorf("Finding() = %q, want it to disclaim rather than report zero", got)
	}
}

// The finding a swarm of agents converged on, stated in one line: all-shared
// allocation and an uncommitted residency set are the same observation.
func TestResidencyFindingAllSharedUncommitted(t *testing.T) {
	type buf = struct {
		options uint64
		length  uint64
	}
	tr := residencyTrace(t, []buf{{0x00, 4096}, {0x00, 8192}}, 1, 0)
	r, err := tr.ResidencyReport()
	if err != nil {
		t.Fatal(err)
	}
	got := r.Finding()
	for _, want := range []string{"shared", "driver"} {
		if !strings.Contains(got, want) {
			t.Errorf("Finding() = %q, want it to mention %q", got, want)
		}
	}
}

// A zero-length buffer record is a record the scan matched and did not decode,
// so the byte total understates. Reporting the total without saying so would
// present a floor as a measurement.
func TestResidencyReportsUnsizedRecords(t *testing.T) {
	type buf = struct {
		options uint64
		length  uint64
	}
	tr := residencyTrace(t, []buf{{0x00, 0}, {0x00, 128}}, 0, 0)
	r, err := tr.ResidencyReport()
	if err != nil {
		t.Fatal(err)
	}
	if r.Unsized != 1 {
		t.Errorf("Unsized = %d, want 1", r.Unsized)
	}
}

// ResidencyReport decodes init calls and BufferStorageModes rescans the capture
// bytes. The gate reads the second and this command the first, so a divergence
// would put two different storage-mode counts in front of the same reader.
func TestResidencyAgreesWithBufferStorageModes(t *testing.T) {
	type buf = struct {
		options uint64
		length  uint64
	}
	tr := residencyTrace(t,
		[]buf{{0x00, 512}, {0x20, 4096}, {0x20, 8192}, {0x10, 1024}}, 1, 1)
	r, err := tr.ResidencyReport()
	if err != nil {
		t.Fatal(err)
	}
	scanned := tr.BufferStorageModes()
	for _, f := range r.Storage {
		if scanned[f.Mode] != f.Buffers {
			t.Errorf("%s: report says %d buffers, BufferStorageModes says %d",
				f.Mode, f.Buffers, scanned[f.Mode])
		}
	}
	if len(scanned) != len(r.Storage) {
		t.Errorf("BufferStorageModes has %d modes, report has %d", len(scanned), len(r.Storage))
	}
}

// Observing that a program does not manage residency and decoding nothing about
// residency are different results. Only the first is a finding.
func TestResidencyFindingSeparatesAbsentRecordsFromAbsentResidency(t *testing.T) {
	type buf = struct {
		options uint64
		length  uint64
	}
	nothing := residencyTrace(t, nil, 0, 0)
	rn, err := nothing.ResidencyReport()
	if err != nil {
		t.Fatal(err)
	}
	if got := rn.Finding(); !strings.Contains(got, "property of the capture") {
		t.Errorf("empty capture Finding() = %q, want it to blame the capture", got)
	}

	observed := residencyTrace(t, []buf{{0x00, 4096}}, 1, 0)
	ro, err := observed.ResidencyReport()
	if err != nil {
		t.Fatal(err)
	}
	if got := ro.Finding(); strings.Contains(got, "property of the capture") {
		t.Errorf("capture with records Finding() = %q, want a finding", got)
	}
	if !ro.Residency.Any() {
		t.Error("Any() = false with a residency set recorded")
	}
	if rn.Residency.Any() {
		t.Error("Any() = true with no residency records")
	}
}
