package cmd

import (
	"encoding/csv"
	"encoding/json"
	"strings"
	"testing"
)

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
