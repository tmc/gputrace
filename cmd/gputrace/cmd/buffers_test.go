package cmd

import (
	"encoding/csv"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateBuffersOptionsAcceptsKnownValues(t *testing.T) {
	tests := []struct {
		name    string
		format  string
		sort    string
		minSize string
		wantMin uint64
	}{
		{name: "table size", format: "table", sort: "size"},
		{name: "json id", format: "json", sort: "id", minSize: "1KB", wantMin: 1024},
		{name: "csv name", format: "csv", sort: "name", minSize: "2MB", wantMin: 2 * 1024 * 1024},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := validateBuffersOptions(tt.format, tt.sort, tt.minSize, "", "not-used")
			if err != nil {
				t.Fatalf("validateBuffersOptions: %v", err)
			}
			if got.format != tt.format {
				t.Fatalf("format = %q, want %q", got.format, tt.format)
			}
			if got.sort != tt.sort {
				t.Fatalf("sort = %q, want %q", got.sort, tt.sort)
			}
			if got.minSize != tt.wantMin {
				t.Fatalf("minSize = %d, want %d", got.minSize, tt.wantMin)
			}
		})
	}
}

func TestValidateBuffersOptionsAcceptsInspectFormats(t *testing.T) {
	for _, format := range []string{"hex", "float32", "int32", "uint32", "float16"} {
		t.Run(format, func(t *testing.T) {
			got, err := validateBuffersOptions("table", "size", "", "MTLBuffer-1-0", format)
			if err != nil {
				t.Fatalf("validateBuffersOptions: %v", err)
			}
			if got.inspectFormat != format {
				t.Fatalf("inspectFormat = %q, want %q", got.inspectFormat, format)
			}
		})
	}
}

func TestValidateBuffersOptionsRejectsInvalidInspectFormat(t *testing.T) {
	tests := []struct {
		name   string
		format string
		want   string
	}{
		{
			name:   "empty",
			format: "",
			want:   `invalid inspect format "" (must be hex, float32, int32, uint32, or float16)`,
		},
		{
			name:   "raw",
			format: "raw",
			want:   `invalid inspect format "raw" (must be hex, float32, int32, uint32, or float16)`,
		},
		{
			name:   "uppercase",
			format: "FLOAT32",
			want:   `invalid inspect format "FLOAT32" (must be hex, float32, int32, uint32, or float16)`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := validateBuffersOptions("table", "size", "", "MTLBuffer-1-0", tt.format)
			if err == nil {
				t.Fatal("validateBuffersOptions succeeded, want error")
			}
			if err.Error() != tt.want {
				t.Fatalf("error = %q, want %q", err.Error(), tt.want)
			}
		})
	}
}

func TestRunBuffersValidatesOptionsBeforeTraceIO(t *testing.T) {
	tests := []struct {
		name          string
		format        string
		sort          string
		inspect       string
		inspectFormat string
		want          string
	}{
		{
			name:          "invalid format",
			format:        "xml",
			sort:          "size",
			inspectFormat: "hex",
			want:          `invalid buffers format "xml" (must be table, json, or csv)`,
		},
		{
			name:          "invalid sort",
			format:        "table",
			sort:          "created",
			inspectFormat: "hex",
			want:          `invalid buffers sort "created" (must be size, id, or name)`,
		},
		{
			name:          "invalid inspect format",
			format:        "table",
			sort:          "size",
			inspect:       "MTLBuffer-1-0",
			inspectFormat: "raw",
			want:          `invalid inspect format "raw" (must be hex, float32, int32, uint32, or float16)`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldFormat := buffersFormat
			oldSort := buffersSort
			oldMinSize := buffersMinSize
			oldInspect := buffersInspect
			oldInspectFormat := buffersInspectFormat
			buffersFormat = tt.format
			buffersSort = tt.sort
			buffersMinSize = ""
			buffersInspect = tt.inspect
			buffersInspectFormat = tt.inspectFormat
			t.Cleanup(func() {
				buffersFormat = oldFormat
				buffersSort = oldSort
				buffersMinSize = oldMinSize
				buffersInspect = oldInspect
				buffersInspectFormat = oldInspectFormat
			})

			missingTrace := filepath.Join(t.TempDir(), "missing.gputrace")
			err := runBuffers(nil, []string{missingTrace})
			if err == nil {
				t.Fatal("runBuffers succeeded, want error")
			}
			if err.Error() != tt.want {
				t.Fatalf("error = %q, want %q", err.Error(), tt.want)
			}
		})
	}
}

func TestFormatBuffersJSONEscapesFilenames(t *testing.T) {
	buffers := []BufferInfo{
		{
			ID:       "1",
			Filename: "MTLBuffer,\"quoted\"\nname",
			Size:     42,
			Aliases:  []string{"alias"},
		},
	}

	out, err := captureStdout(t, func() error {
		return formatBuffersJSON(buffers)
	})
	if err != nil {
		t.Fatalf("formatBuffersJSON: %v", err)
	}

	var got []bufferJSONInfo
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("JSON output did not decode: %v\n%s", err, out)
	}
	if len(got) != 1 {
		t.Fatalf("decoded %d buffers, want 1", len(got))
	}
	if got[0].Filename != buffers[0].Filename {
		t.Fatalf("filename = %q, want %q", got[0].Filename, buffers[0].Filename)
	}
}

func TestFormatBuffersCSVEscapesFilenames(t *testing.T) {
	buffers := []BufferInfo{
		{
			ID:       "1",
			Filename: "MTLBuffer,\"quoted\"\nname",
			Size:     42,
			Aliases:  []string{"alias"},
		},
	}

	out, err := captureStdout(t, func() error {
		return formatBuffersCSV(buffers)
	})
	if err != nil {
		t.Fatalf("formatBuffersCSV: %v", err)
	}

	records, err := csv.NewReader(strings.NewReader(out)).ReadAll()
	if err != nil {
		t.Fatalf("CSV output did not decode: %v\n%s", err, out)
	}
	if len(records) != 2 {
		t.Fatalf("decoded %d records, want 2", len(records))
	}
	if records[1][1] != buffers[0].Filename {
		t.Fatalf("filename = %q, want %q", records[1][1], buffers[0].Filename)
	}
}
