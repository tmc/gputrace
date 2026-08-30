package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGateCommandHelp(t *testing.T) {
	var buf bytes.Buffer
	cmd := newGateCommand(&gateOptions{})
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("gate --help failed: %v", err)
	}

	out := buf.String()
	for _, expected := range []string{
		"Gate a GPU capture before trusting anything in it.",
		"--tokens",
		"--invariant",
		"--slack",
		"--stationarity-threshold",
		"--block-size",
		"--json",
		"Exit status:",
	} {
		if !strings.Contains(out, expected) {
			t.Errorf("gate --help output missing %q, got:\n%s", expected, out)
		}
	}
}

func TestGateCommandCUDAEvents(t *testing.T) {
	// Create a synthetic events.jsonl
	dir := t.TempDir()
	eventsPath := filepath.Join(dir, "events.jsonl")

	var content strings.Builder
	// 33 arg_reduce kernels, 10ms apart
	for i := 0; i < 33; i++ {
		start := uint64(1000000000 + i*10000000)
		end := start + 1000000
		content.WriteString(`{"kind":"kernel","raw_symbol":"_Z18arg_reduce_generali","start_ns":` +
			strings.TrimSpace(string(intToBytes(start))) + `,"end_ns":` +
			strings.TrimSpace(string(intToBytes(end))) + `}` + "\n")
	}
	// 5 HtoD memcpys
	for i := 0; i < 5; i++ {
		content.WriteString(`{"kind":"memcpy","src_kind":"host","dst_kind":"device","bytes":1048576,"start_ns":500000,"end_ns":600000}` + "\n")
	}

	if err := os.WriteFile(eventsPath, []byte(content.String()), 0o644); err != nil {
		t.Fatalf("write events: %v", err)
	}

	var buf bytes.Buffer
	cmd := newGateCommand(&gateOptions{
		tokens:                32,
		invariant:             "arg_reduce",
		slack:                 2,
		stationarityThreshold: 0.15,
		blockSize:             8,
	})
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"-t", "32", "-k", "arg_reduce", "--block-size", "8", dir})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("gate execution failed: %v, output: %s", err, buf.String())
	}

	out := buf.String()
	if !strings.Contains(out, "completeness ok") {
		t.Errorf("expected completeness ok in output, got:\n%s", out)
	}
	if !strings.Contains(out, "5 HtoD transfers") {
		t.Errorf("expected staging info in output, got:\n%s", out)
	}
	if !strings.Contains(out, "stationarity ok") {
		t.Errorf("expected stationarity ok in output, got:\n%s", out)
	}
}

func intToBytes(n uint64) []byte {
	return []byte(strings.TrimSpace(string(fmtInt(n))))
}

func fmtInt(n uint64) string {
	var buf [32]byte
	i := len(buf)
	for n >= 10 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	i--
	buf[i] = byte('0' + n)
	return string(buf[i:])
}
