package livetiming

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validInput = `{"kind":"clock_sample","cpu_ticks":100,"gpu_ticks":200,"run_id":"run-1"}
{"kind":"clock_sample","cpu_ticks":200,"gpu_ticks":300,"run_id":"run-1"}
{"kind":"clock_sample","cpu_ticks":300,"gpu_ticks":400,"run_id":"run-1"}
{"kind":"command_buffer","id":1,"capture_label":"gputrace.live.cb.1","final_label":"decode","gpu_start_seconds":0.00000025,"gpu_end_seconds":0.00000035,"kernel_start_seconds":0.00000024,"kernel_end_seconds":0.00000034,"status":4,"run_id":"run-1"}
{"kind":"artifact","run_id":"run-1","trace_digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
`

func TestRead(t *testing.T) {
	got, err := readString(t, validInput)
	if err != nil {
		t.Fatal(err)
	}
	if got.RunID != "run-1" || len(got.ClockSamples) != 3 || len(got.CommandBuffers) != 1 {
		t.Fatalf("Read() = %+v", got)
	}
	command := got.CommandBuffers[0]
	if command.GPUStartNS != 250 || command.GPUEndNS != 350 || command.CaptureLabel != "gputrace.live.cb.1" {
		t.Fatalf("command buffer = %+v", command)
	}
}

func TestReadRefusesMalformedEvidence(t *testing.T) {
	tests := []struct {
		name string
		edit func(string) string
	}{
		{"mixed run", func(s string) string { return strings.Replace(s, `"run_id":"run-1"`, `"run_id":"other"`, 1) }},
		{"unknown field", func(s string) string {
			return strings.Replace(s, `"kind":"clock_sample"`, `"kind":"clock_sample","extra":1`, 1)
		}},
		{"two samples", func(s string) string {
			return strings.Replace(s, `{"kind":"clock_sample","cpu_ticks":200,"gpu_ticks":300,"run_id":"run-1"}
`, "", 1)
		}},
		{"unordered sample", func(s string) string { return strings.Replace(s, `"cpu_ticks":200`, `"cpu_ticks":100`, 1) }},
		{"duplicate label", func(s string) string { return s + strings.Split(s, "\n")[3] + "\n" }},
		{"failed command", func(s string) string { return strings.Replace(s, `"status":4`, `"status":5`, 1) }},
		{"outside clock range", func(s string) string { return strings.Replace(s, `0.00000035`, `0.00000045`, 1) }},
		{"missing artifact", func(s string) string {
			return strings.Replace(s, `{"kind":"artifact","run_id":"run-1","trace_digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
`, "", 1)
		}},
		{"artifact not last", func(s string) string { return s + strings.Split(s, "\n")[0] + "\n" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := readString(t, test.edit(validInput)); !errors.Is(err, ErrInvalid) {
				t.Fatalf("Read() error = %v, want ErrInvalid", err)
			}
		})
	}
}

func readString(t *testing.T, input string) (Sidecar, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "timing.jsonl")
	if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	return Read(path)
}
