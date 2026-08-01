package counter

import (
	"encoding/binary"
	"testing"
)

// buildBlob assembles a GPRWCNTR blob of records with ncols columns each.
func buildBlob(ncols int, records [][]uint64) []byte {
	var out []byte
	for _, rec := range records {
		out = append(out, GPRWCNTRMagic...)
		for i := 0; i < ncols; i++ {
			var v uint64
			if i < len(rec) {
				v = rec[i]
			}
			out = binary.LittleEndian.AppendUint64(out, v)
		}
	}
	return out
}

func TestGPRWCNTRStride(t *testing.T) {
	tests := []struct {
		name  string
		ncols int
		recs  int
		want  int
	}{
		// Widths measured across two archives: ShaderProfilerData uses 20
		// columns for RDE_0, 17 for BMPR_RDE_0 and 9 for Firmware, while the
		// per-pass blobs under "Derived Counter Sample Data" range 7..43.
		{"firmware", 9, 4, 80},
		{"grc_only", 7, 3, 64},
		{"bmpr_rde_0", 17, 2, 144},
		{"rde_0", 20, 5, 168},
		{"widest_pass", 43, 2, 352},
		{"single_record", 20, 1, 168},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blob := buildBlob(tt.ncols, make([][]uint64, tt.recs))
			got, err := GPRWCNTRStride(blob)
			if err != nil {
				t.Fatalf("GPRWCNTRStride: %v", err)
			}
			if got != tt.want {
				t.Errorf("stride = %d, want %d", got, tt.want)
			}
			if len(blob)%got != 0 {
				t.Errorf("stride %d does not divide blob length %d", got, len(blob))
			}
			samples, _, err := ParseGPRWCNTR(blob)
			if err != nil {
				t.Fatalf("ParseGPRWCNTR: %v", err)
			}
			// The fixed-size parse computed (len-8)/168 and silently dropped
			// the final record; every record must survive.
			if len(samples) != tt.recs {
				t.Errorf("decoded %d records, want %d", len(samples), tt.recs)
			}
		})
	}
}

func TestGPRWCNTRColumnOrder(t *testing.T) {
	want := []string{
		"GRC_TIMESTAMP", "GRC_GPU_CYCLES", "GRC_SAMPLE_TYPE", "GRC_ENCODER_ID",
		"GRC_KICK_TRACE_ID", "GRC_KICK_SLOT_IDX", "GRC_SOURCE_ID",
	}
	if len(GRCColumnNames) != len(want) {
		t.Fatalf("GRCColumnNames has %d entries, want %d", len(GRCColumnNames), len(want))
	}
	for i, name := range want {
		if GRCColumnNames[i] != name {
			t.Errorf("column %d = %q, want %q", i, GRCColumnNames[i], name)
		}
	}

	// A record decodes its columns in that order, not the old
	// timestamp/size/count/flags reading.
	blob := buildBlob(9, [][]uint64{{1, 2, 3, 4, 5, 6, 7, 8, 9}})
	samples, _, err := ParseGPRWCNTR(blob)
	if err != nil {
		t.Fatalf("ParseGPRWCNTR: %v", err)
	}
	got := samples[0]
	for i, v := range []uint64{
		got.Timestamp, got.GPUCycles, got.SampleType, got.EncoderID,
		got.KickTraceID, got.KickSlotIdx, got.SourceID,
	} {
		if v != uint64(i+1) {
			t.Errorf("%s = %d, want %d", GRCColumnNames[i], v, i+1)
		}
	}
	if len(got.Counters) != 2 || got.Counters[0] != 8 || got.Counters[1] != 9 {
		t.Errorf("Counters = %v, want [8 9]", got.Counters)
	}
}

func TestGPRWCNTRRejectsBadStride(t *testing.T) {
	// A blob whose stride does not divide its length must fail loudly. The old
	// fixed-size parse accepted such blobs and produced plausible garbage.
	blob := buildBlob(20, make([][]uint64, 3))
	blob = append(blob, 0, 0, 0, 0)
	if _, err := GPRWCNTRStride(blob); err == nil {
		t.Fatal("GPRWCNTRStride accepted a blob its stride does not divide")
	}
	if _, _, err := ParseGPRWCNTR(blob); err == nil {
		t.Fatal("ParseGPRWCNTR accepted a blob its stride does not divide")
	}
	if _, err := GPRWCNTRStride([]byte("NOTMAGIC")); err == nil {
		t.Fatal("GPRWCNTRStride accepted a blob without the magic")
	}
	// 168 bytes is the RDE_0 width, but assuming it for a 43-column pass blob
	// is exactly the defect being fixed: the derived stride must win.
	wide := buildBlob(43, make([][]uint64, 2))
	stride, err := GPRWCNTRStride(wide)
	if err != nil {
		t.Fatalf("GPRWCNTRStride: %v", err)
	}
	if stride == 168 {
		t.Fatal("43-column blob decoded with the RDE_0 stride")
	}
}

func TestGPRWCNTRMachineWide(t *testing.T) {
	blob := buildBlob(7, [][]uint64{
		{100, 1, GRCMachineWideSampleType, GRCMachineWideID, GRCMachineWideID, 0, 1},
		{200, 2, 4, 0x2E00EE27, 0x2F020FA9, 2, 1},
	})
	samples, _, err := ParseGPRWCNTR(blob)
	if err != nil {
		t.Fatalf("ParseGPRWCNTR: %v", err)
	}
	if !samples[0].MachineWide() {
		t.Error("sample with encoder id 0xFFFFFFFF is not reported machine-wide")
	}
	if samples[1].MachineWide() {
		t.Error("sample naming an encoder is reported machine-wide")
	}
}
