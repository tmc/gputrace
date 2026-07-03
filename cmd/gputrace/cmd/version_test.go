package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestVersionCommandWritesToCommandOutput(t *testing.T) {
	var out bytes.Buffer
	cmd := newVersionCommand(&versionOptions{})
	cmd.SetOut(&out)

	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("version RunE: %v", err)
	}
	if got := out.String(); !strings.HasPrefix(got, "gputrace ") {
		t.Fatalf("version output = %q, want gputrace prefix", got)
	}
}

func TestVersionCommandJSONWritesToCommandOutput(t *testing.T) {
	var out bytes.Buffer
	cmd := newVersionCommand(&versionOptions{json: true})
	cmd.SetOut(&out)

	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("version --json RunE: %v", err)
	}
	var got versionInfo
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("version --json output is not JSON: %v\n%s", err, out.String())
	}
	if got.Version == "" {
		t.Fatalf("version --json version is empty: %+v", got)
	}
	if !strings.HasSuffix(out.String(), "\n") {
		t.Fatalf("version --json output missing trailing newline: %q", out.String())
	}
}
