package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/gputrace/internal/hostcorrelation"
)

func TestHostReceiptCommand(t *testing.T) {
	dir := t.TempDir()
	host := filepath.Join(dir, "host.jsonl")
	timing := filepath.Join(dir, "timing.jsonl")
	if err := os.WriteFile(host, []byte(`{"clock_domain":"cpu_uptime_ns","duration_ns":20,"id":"event-1","kind":"interval","name":"Generation","run_id":"run-1","schema":"gputrace.host-event/v1","timestamp_ns":120}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(timing, []byte(`{"kind":"clock_sample","cpu_ticks":100,"gpu_ticks":200,"run_id":"run-1"}
{"kind":"clock_sample","cpu_ticks":200,"gpu_ticks":300,"run_id":"run-1"}
{"kind":"clock_sample","cpu_ticks":300,"gpu_ticks":400,"run_id":"run-1"}
{"kind":"command_buffer","id":1,"capture_label":"gputrace.live.cb.1","final_label":"decode","gpu_start_seconds":0.00000025,"gpu_end_seconds":0.00000035,"kernel_start_seconds":0.00000024,"kernel_end_seconds":0.00000034,"status":4,"run_id":"run-1"}
{"kind":"artifact","run_id":"run-1","trace_digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	cmd := newHostReceiptCommand(&hostReceiptOptions{})
	cmd.SetOut(&output)
	cmd.SetArgs([]string{host, timing})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	receiptPath := filepath.Join(dir, "receipt.json")
	if err := os.WriteFile(receiptPath, output.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := hostcorrelation.Read(receiptPath); err != nil {
		t.Fatal(err)
	}
}
