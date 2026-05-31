package cmd

import (
	"encoding/json"
	"testing"
)

func TestCommandOutputPathIsStdout(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{name: "empty", path: "", want: true},
		{name: "dash", path: "-", want: true},
		{name: "dev stdout", path: "/dev/stdout", want: true},
		{name: "file", path: "out.json", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := commandOutputPathIsStdout(tt.path); got != tt.want {
				t.Fatalf("commandOutputPathIsStdout(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestWriteOutputStdoutText(t *testing.T) {
	out, err := captureStdout(t, func() error {
		return writeOutput("/dev/stdout", "payload\n", nil)
	})
	if err != nil {
		t.Fatalf("writeOutput: %v", err)
	}
	if got, want := out, "payload\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestWriteOutputDashJSON(t *testing.T) {
	out, err := captureStdout(t, func() error {
		return writeOutput("-", "", map[string]string{"kind": "counter"})
	})
	if err != nil {
		t.Fatalf("writeOutput: %v", err)
	}

	var got map[string]string
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out)
	}
	if got["kind"] != "counter" {
		t.Fatalf("kind = %q, want counter", got["kind"])
	}
}
