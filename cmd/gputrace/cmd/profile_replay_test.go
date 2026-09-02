package cmd

import (
	"strings"
	"testing"
)

func TestProfileReplayEmbedCompatibility(t *testing.T) {
	cmd := newProfileReplayCommand(new(profileReplayOptions))
	cmd.SetArgs([]string{"trace.gputrace", "--embed", "--profiler-only"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("error = %v, want mutually exclusive", err)
	}
}

func TestProfileReplayHelpExplainsOutputShapes(t *testing.T) {
	cmd := newProfileReplayCommand(new(profileReplayOptions))
	for _, text := range []string{"self-contained", ".gpuprofiler_raw", "cannot be opened by Xcode"} {
		if !strings.Contains(cmd.Long, text) {
			t.Errorf("help does not contain %q", text)
		}
	}
}
