package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/gputrace/internal/profilereplay"
)

func TestProfileReplayHint(t *testing.T) {
	if err := profilereplay.Available(); err != nil {
		t.Skip(err)
	}
	tests := []struct {
		name    string
		entries []string
		want    bool
	}{
		{"capture without profiler data", []string{"capture"}, true},
		{"unsorted-capture without profiler data", []string{"unsorted-capture"}, true},
		// Advice that cannot work is worse than silence: a profiler-only
		// bundle has no capture stream left to replay, so the hint would send
		// the reader to a command that refuses the trace.
		{"profiler-only", []string{"trace.gpuprofiler_raw"}, false},
		{"empty", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bundle := filepath.Join(t.TempDir(), "trace.gputrace")
			if err := os.Mkdir(bundle, 0o755); err != nil {
				t.Fatal(err)
			}
			for _, entry := range tt.entries {
				path := filepath.Join(bundle, entry)
				if filepath.Ext(entry) == ".gpuprofiler_raw" {
					if err := os.Mkdir(path, 0o755); err != nil {
						t.Fatal(err)
					}
					continue
				}
				if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			hint := profileReplayHint(bundle)
			if got := hint != ""; got != tt.want {
				t.Fatalf("profileReplayHint(%v) = %q, want hint = %v", tt.entries, hint, tt.want)
			}
			if tt.want && !strings.Contains(hint, "gputrace profile-replay "+bundle) {
				t.Fatalf("hint does not name a runnable command: %q", hint)
			}
		})
	}
}
