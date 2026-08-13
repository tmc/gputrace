package hostevents

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validHost = `{"clock_domain":"cpu_uptime_ns","duration_ns":20,"id":"event-1","kind":"interval","name":"Generation","run_id":"run-1","schema":"gputrace.host-event/v1","timestamp_ns":120}
`

const validTiming = `{"kind":"clock_sample","cpu_ticks":100,"gpu_ticks":200,"run_id":"run-1"}
{"kind":"clock_sample","cpu_ticks":200,"gpu_ticks":300,"run_id":"run-1"}
{"kind":"clock_sample","cpu_ticks":300,"gpu_ticks":400,"run_id":"run-1"}
{"kind":"command_buffer","id":1,"capture_label":"gputrace.live.cb.1","final_label":"decode","gpu_start_seconds":0.00000025,"gpu_end_seconds":0.00000035,"kernel_start_seconds":0.00000024,"kernel_end_seconds":0.00000034,"status":4,"run_id":"run-1"}
{"kind":"artifact","run_id":"run-1","trace_digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
`

func TestReceipt(t *testing.T) {
	host := write(t, "host.jsonl", validHost)
	timing := write(t, "timing.jsonl", validTiming)
	receipt, err := Receipt(host, timing)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Host.RunID != "run-1" || receipt.GPU.ClockDomain != "live" || len(receipt.Events) != 1 {
		t.Fatalf("Receipt() = %+v", receipt)
	}
	projected, err := receipt.Project()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := projected[0].TimestampNS, int64(220); got != want {
		t.Fatalf("timestamp = %d, want %d", got, want)
	}
}

func TestReadRefusesMalformedEvidence(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{"unknown field", strings.Replace(validHost, `"timestamp_ns":120`, `"extra":1,"timestamp_ns":120`, 1)},
		{"zero duration", strings.Replace(validHost, `"duration_ns":20`, `"duration_ns":0`, 1)},
		{"wrong clock", strings.Replace(validHost, ClockDomain, "xctrace-relative", 1)},
		{"duplicate", validHost + validHost},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Read(write(t, "host.jsonl", test.data))
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("Read() error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestReceiptRefusesDifferentRun(t *testing.T) {
	host := write(t, "host.jsonl", validHost)
	timing := write(t, "timing.jsonl", strings.ReplaceAll(validTiming, "run-1", "run-2"))
	_, err := Receipt(host, timing)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Receipt() error = %v, want ErrInvalid", err)
	}
}

func write(t *testing.T, name, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
