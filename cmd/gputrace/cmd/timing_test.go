package cmd

import (
	"os"
	"testing"
)

func TestTimingReportWriterUsesStderrForStdoutExports(t *testing.T) {
	tests := []struct {
		name string
		json string
		csv  string
		want *os.File
	}{
		{name: "no export", want: os.Stdout},
		{name: "json file", json: "timing.json", want: os.Stdout},
		{name: "csv file", csv: "timing.csv", want: os.Stdout},
		{name: "json stdout", json: "/dev/stdout", want: os.Stderr},
		{name: "csv stdout", csv: "/dev/stdout", want: os.Stderr},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldJSON, oldCSV := timingJSON, timingCSV
			timingJSON, timingCSV = tt.json, tt.csv
			t.Cleanup(func() {
				timingJSON, timingCSV = oldJSON, oldCSV
			})

			if got := timingReportWriter(); got != tt.want {
				t.Fatalf("timingReportWriter() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidateTimingOutputPathsRejectsMultipleStdoutExports(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		csv     string
		wantErr bool
	}{
		{name: "no export"},
		{name: "json stdout", json: "/dev/stdout"},
		{name: "csv stdout", csv: "/dev/stdout"},
		{name: "both stdout", json: "/dev/stdout", csv: "/dev/stdout", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldJSON, oldCSV := timingJSON, timingCSV
			timingJSON, timingCSV = tt.json, tt.csv
			t.Cleanup(func() {
				timingJSON, timingCSV = oldJSON, oldCSV
			})

			err := validateTimingOutputPaths()
			if tt.wantErr && err == nil {
				t.Fatal("validateTimingOutputPaths returned nil error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("validateTimingOutputPaths returned error: %v", err)
			}
		})
	}
}
