package cmd

import (
	"os"
	"strings"
	"testing"
)

func TestVertexOutputJSON(t *testing.T) {
	tests := []struct {
		name       string
		globalJSON bool
		format     string
		want       bool
		wantErr    string
	}{
		{
			name:   "default text",
			format: "text",
		},
		{
			name:       "global json",
			globalJSON: true,
			format:     "text",
			want:       true,
		},
		{
			name:   "format json",
			format: "json",
			want:   true,
		},
		{
			name:       "format json with global json",
			globalJSON: true,
			format:     "json",
			want:       true,
		},
		{
			name:    "unknown format",
			format:  "yaml",
			wantErr: `unknown vertex output format "yaml"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := vertexOutputJSON(tt.globalJSON, tt.format)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("vertexOutputJSON returned nil error, want %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error %q does not contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("vertexOutputJSON returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("vertexOutputJSON() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestVertexOutputStatusWriter(t *testing.T) {
	tests := []struct {
		name       string
		jsonOutput bool
		outputPath string
		want       *os.File
	}{
		{name: "text stdout default", want: os.Stderr},
		{name: "text stdout path", outputPath: "/dev/stdout", want: os.Stderr},
		{name: "json stdout", jsonOutput: true, want: os.Stderr},
		{name: "text file", outputPath: "vertex.txt", want: os.Stdout},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := vertexOutputStatusWriter(tt.jsonOutput, tt.outputPath); got != tt.want {
				t.Fatalf("vertexOutputStatusWriter() = %v, want %v", got, tt.want)
			}
		})
	}
}
