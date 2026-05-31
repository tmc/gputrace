package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestVersionCommandWritesToCommandOutput(t *testing.T) {
	oldJSON := versionJSON
	defer func() {
		versionJSON = oldJSON
		versionCmd.SetOut(nil)
	}()

	var out bytes.Buffer
	versionJSON = false
	versionCmd.SetOut(&out)

	if err := versionCmd.RunE(versionCmd, nil); err != nil {
		t.Fatalf("version RunE: %v", err)
	}
	if got := out.String(); !strings.HasPrefix(got, "gputrace ") {
		t.Fatalf("version output = %q, want gputrace prefix", got)
	}
}

func TestVersionCommandJSONWritesToCommandOutput(t *testing.T) {
	oldJSON := versionJSON
	defer func() {
		versionJSON = oldJSON
		versionCmd.SetOut(nil)
	}()

	var out bytes.Buffer
	versionJSON = true
	versionCmd.SetOut(&out)

	if err := versionCmd.RunE(versionCmd, nil); err != nil {
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
