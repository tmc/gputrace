//go:build darwin

package cmd

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/tmc/gputrace/internal/xcodebindings"
)

func TestWriteShadersXcodeCostText(t *testing.T) {
	rows := []xcodebindings.ShaderCost{{Name: "kernel", ComputeTime: 3303, Cost: 33.03}}
	var out bytes.Buffer
	if err := writeShadersXcodeCost(&out, "text", rows, 10000); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "33.03%") || !strings.Contains(got, "kernel") {
		t.Fatalf("output = %q", got)
	}
}

func TestWriteShadersXcodeCostStructured(t *testing.T) {
	rows := []xcodebindings.ShaderCost{{Name: "kernel", ComputeTime: 3303, Cost: 33.03}}
	for _, format := range []string{"json", "csv"} {
		t.Run(format, func(t *testing.T) {
			var out bytes.Buffer
			if err := writeShadersXcodeCost(&out, format, rows, 10000); err != nil {
				t.Fatal(err)
			}
			if got := out.String(); !strings.Contains(got, "kernel") || !strings.Contains(got, "33.03") {
				t.Fatalf("output = %q", got)
			}
		})
	}
}

func TestReplaceEnv(t *testing.T) {
	got := replaceEnv([]string{"A=1", "B=2", "A=old"}, "A", "new")
	want := []string{"B=2", "A=new"}
	if !slices.Equal(got, want) {
		t.Fatalf("replaceEnv = %v, want %v", got, want)
	}
}
