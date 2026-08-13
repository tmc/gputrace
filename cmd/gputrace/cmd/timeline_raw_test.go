package cmd

import (
	"encoding/binary"
	"testing"
)

func TestGPRWCNTREventArgs(t *testing.T) {
	rec := GPRWCNTRRecord{
		Timestamp:    1234,
		Size:         168,
		Count:        6,
		Flags:        7,
		EncoderIndex: 3,
		RecordIndex:  11,
	}
	args := gprwcntrEventArgs(rec)
	checks := map[string]interface{}{
		"stream_index":     3,
		"record_index":     11,
		"timestamp_ticks":  uint64(1234),
		"timestamp_domain": "mach absolute ticks",
		"record_format":    "GPRWCNTR/168-byte record",
	}
	for key, want := range checks {
		if got := args[key]; got != want {
			t.Errorf("%s = %v, want %v", key, got, want)
		}
	}
	if got := args["counter_decode_status"]; got != "raw record header only; counter payload is not decoded" {
		t.Fatalf("counter_decode_status = %q", got)
	}
}

func TestParseGPRWCNTRRecordsHaveStreamLocalOrdinals(t *testing.T) {
	data := make([]byte, 8+2*168)
	copy(data, "GPRWCNTR")
	binary.LittleEndian.PutUint64(data[8:], 100)
	binary.LittleEndian.PutUint64(data[8+168:], 200)
	records, err := ParseGPRWCNTR(data, 4)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(records), 2; got != want {
		t.Fatalf("records = %d, want %d", got, want)
	}
	for i, record := range records {
		if record.EncoderIndex != 4 || record.RecordIndex != i {
			t.Errorf("record %d identity = stream %d record %d, want stream 4 record %d", i, record.EncoderIndex, record.RecordIndex, i)
		}
	}
}
