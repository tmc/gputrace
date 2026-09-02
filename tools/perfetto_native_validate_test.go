package tools

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPerfettoNativeValidateCountsRecordedDispatches(t *testing.T) {
	for _, test := range []struct {
		name     string
		gpu      int
		recorded int
	}{
		{name: "profiled", gpu: 1166},
		{name: "capture only", recorded: 1166},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			trace := filepath.Join(dir, "trace.pftrace")
			sql := filepath.Join(dir, "gputrace.sql")
			processor := filepath.Join(dir, "trace_processor_shell")
			for _, path := range []string{trace, sql} {
				if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			stub := fmt.Sprintf(`#!/bin/sh
query=${3-}
case "$query" in
  *"from stats"*) value=0 ;;
  *"from gpu_slice"*) value=%d ;;
  *"where category='dispatch'"*) value=%d ;;
  *"debug.schema"*) value=gputrace.perfetto/v1 ;;
  *"from slice"*) value=1 ;;
  *) cat >/dev/null; value=%d ;;
esac
printf '"value"\n"%%s"\n' "$value"
`, test.gpu, test.recorded, test.gpu+test.recorded)
			if err := os.WriteFile(processor, []byte(stub), 0o700); err != nil {
				t.Fatal(err)
			}

			cmd := exec.Command("sh", "perfetto-native-validate.sh", "--sql", sql, trace)
			cmd.Env = append(os.Environ(), "TRACE_PROCESSOR_SHELL="+processor)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("validator failed: %v\n%s", err, output)
			}
			if !strings.Contains(string(output), "native Perfetto validation passed") {
				t.Fatalf("validator output = %q", output)
			}
		})
	}
}
