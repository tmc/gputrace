package cmd

import "testing"

func TestGPRWCNTREventArgs(t *testing.T) {
	rec := GPRWCNTRRecord{
		Timestamp:    1234,
		Size:         168,
		Count:        6,
		Flags:        7,
		EncoderIndex: 3,
	}
	args := gprwcntrEventArgs(rec)
	checks := map[string]interface{}{
		"stream_index":     3,
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
