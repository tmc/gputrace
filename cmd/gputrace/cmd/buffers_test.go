package cmd

import (
	"encoding/binary"
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/gputrace"
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
			got, err := validateBuffersOptions(tt.format, tt.sort, tt.minSize, "", -1, "not-used")
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
			got, err := validateBuffersOptions("table", "size", "", "MTLBuffer-1-0", 256, format)
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
			_, err := validateBuffersOptions("table", "size", "", "MTLBuffer-1-0", 256, tt.format)
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
		inspectBytes  int
		inspectFormat string
		want          string
	}{
		{
			name:          "invalid format",
			format:        "xml",
			sort:          "size",
			inspectBytes:  256,
			inspectFormat: "hex",
			want:          `invalid buffers format "xml" (must be table, json, or csv)`,
		},
		{
			name:          "invalid sort",
			format:        "table",
			sort:          "created",
			inspectBytes:  256,
			inspectFormat: "hex",
			want:          `invalid buffers sort "created" (must be size, id, or name)`,
		},
		{
			name:          "invalid inspect format",
			format:        "table",
			sort:          "size",
			inspect:       "MTLBuffer-1-0",
			inspectBytes:  256,
			inspectFormat: "raw",
			want:          `invalid inspect format "raw" (must be hex, float32, int32, uint32, or float16)`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &buffersCommandOptions{
				format:        tt.format,
				sort:          tt.sort,
				inspect:       tt.inspect,
				inspectBytes:  tt.inspectBytes,
				inspectFormat: tt.inspectFormat,
			}

			missingTrace := filepath.Join(t.TempDir(), "missing.gputrace")
			err := runBuffers(nil, []string{missingTrace}, opts)
			if err == nil {
				t.Fatal("runBuffers succeeded, want error")
			}
			if err.Error() != tt.want {
				t.Fatalf("error = %q, want %q", err.Error(), tt.want)
			}
		})
	}
}

func TestRunBuffersRejectsInspectBytesBeforeTraceIO(t *testing.T) {
	tests := []struct {
		name  string
		bytes int
	}{
		{name: "negative", bytes: -1},
		{name: "zero", bytes: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &buffersCommandOptions{
				format:        "table",
				sort:          "size",
				inspect:       "MTLBuffer-1-0",
				inspectBytes:  tt.bytes,
				inspectFormat: "hex",
			}

			missingTrace := filepath.Join(t.TempDir(), "missing.gputrace")
			err := runBuffers(nil, []string{missingTrace}, opts)
			if err == nil {
				t.Fatal("runBuffers succeeded, want error")
			}
			if err.Error() != "inspect bytes must be greater than zero" {
				t.Fatalf("error = %q, want %q", err.Error(), "inspect bytes must be greater than zero")
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

func TestExtractBufferResourceInventoryCountsSidecarBuffers(t *testing.T) {
	dir := t.TempDir()
	tracePath := filepath.Join(dir, "trace.gputrace")
	if err := os.Mkdir(tracePath, 0o755); err != nil {
		t.Fatalf("mkdir trace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tracePath, "MTLBuffer-1-0"), make([]byte, 64), 0o644); err != nil {
		t.Fatalf("write buffer: %v", err)
	}
	data := appendBufferResourceRecord(nil, "MTLBuffer-1-0", 64)
	data = appendBufferResourceRecord(data, "MTLBuffer-2-0", 128)
	if err := os.WriteFile(filepath.Join(tracePath, "device-resources-0x1"), data, 0o644); err != nil {
		t.Fatalf("write resources: %v", err)
	}

	inventory, err := extractBufferResourceInventory(tracePath, nil)
	if err != nil {
		t.Fatalf("extractBufferResourceInventory: %v", err)
	}
	if got := inventory.FinalBuffers; got != 2 {
		t.Fatalf("FinalBuffers = %d, want 2", got)
	}
	if got := inventory.FinalBytes; got != 192 {
		t.Fatalf("FinalBytes = %d, want 192", got)
	}
	if got := inventory.FileBackedBuffers; got != 1 {
		t.Fatalf("FileBackedBuffers = %d, want 1", got)
	}
	if got := inventory.FileBackedBytes; got != 64 {
		t.Fatalf("FileBackedBytes = %d, want 64", got)
	}
	if len(inventory.Files) != 1 {
		t.Fatalf("len(Files) = %d, want 1", len(inventory.Files))
	}
	file := inventory.Files[0]
	if file.Records != 2 || file.FinalNameRecords != 1 || file.NoFinalFile != 1 {
		t.Fatalf("resource file summary = %+v, want 2 records, 1 final name, 1 missing final file", file)
	}
}

func TestExtractBufferLifetimeReportAttributesCommands(t *testing.T) {
	dir := t.TempDir()
	tracePath := filepath.Join(dir, "trace.gputrace")
	if err := os.Mkdir(tracePath, 0o755); err != nil {
		t.Fatalf("mkdir trace: %v", err)
	}
	const addr = uint64(0x123456780000)
	if err := os.WriteFile(filepath.Join(tracePath, "MTLBuffer-1-0"), make([]byte, 64), 0o644); err != nil {
		t.Fatalf("write buffer: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tracePath, "device-resources-0x1"), appendBufferResourceRecord(nil, "MTLBuffer-1-0", 64), 0o644); err != nil {
		t.Fatalf("write resources: %v", err)
	}
	capture := syntheticLifetimeCapture(addr, "MTLBuffer-1-0", "kernel_a")
	if err := os.WriteFile(filepath.Join(tracePath, "capture"), capture, 0o644); err != nil {
		t.Fatalf("write capture: %v", err)
	}
	trace := &gputrace.Trace{
		Path:         tracePath,
		CaptureData:  capture,
		DeviceLabels: map[uint64]string{addr: "weights"},
	}

	report, err := extractBufferLifetimeReport(tracePath, trace)
	if err != nil {
		t.Fatalf("extractBufferLifetimeReport: %v", err)
	}
	if report.Resources != 1 {
		t.Fatalf("Resources = %d, want 1", report.Resources)
	}
	row := report.Rows[0]
	if row.Name != "MTLBuffer-1-0" || row.Address != addr || row.Size != 64 {
		t.Fatalf("row identity = %+v", row)
	}
	if row.Label != "weights" {
		t.Fatalf("Label = %q, want weights", row.Label)
	}
	if row.BindingRecords != 1 {
		t.Fatalf("BindingRecords = %d, want 1", row.BindingRecords)
	}
	if len(row.CommandBuffers) != 1 || row.CommandBuffers[0] != 0 {
		t.Fatalf("CommandBuffers = %+v, want [0]", row.CommandBuffers)
	}
	if len(row.Encoders) != 1 || row.Encoders[0] != 0 {
		t.Fatalf("Encoders = %+v, want [0]", row.Encoders)
	}
	if len(row.EncoderLabels) != 1 || row.EncoderLabels[0] != "kernel_a" {
		t.Fatalf("EncoderLabels = %+v, want [kernel_a]", row.EncoderLabels)
	}
}

func TestExtractBufferLifetimeReportFailsClosedWithoutCapture(t *testing.T) {
	dir := t.TempDir()
	tracePath := filepath.Join(dir, "trace.gputrace")
	if err := os.Mkdir(tracePath, 0o755); err != nil {
		t.Fatalf("mkdir trace: %v", err)
	}

	_, err := extractBufferLifetimeReport(tracePath, &gputrace.Trace{Path: tracePath})
	if err == nil {
		t.Fatal("extractBufferLifetimeReport succeeded without capture")
	}
	if !strings.Contains(err.Error(), "requires full trace capture data") {
		t.Fatalf("error = %q, want full trace capture message", err.Error())
	}
}

func appendBufferResourceRecord(dst []byte, name string, size uint64) []byte {
	dst = append(dst, []byte("CU<b>ulul")...)
	dst = append(dst, 0, 0, 0, 0)
	dst = append(dst, []byte(name)...)
	dst = append(dst, 0)
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], size)
	dst = append(dst, buf[:]...)
	return dst
}

func syntheticLifetimeCapture(addr uint64, name, label string) []byte {
	var out []byte
	ctu := make([]byte, 0x40)
	copy(ctu, []byte("CtU<b>ulul"))
	binary.LittleEndian.PutUint64(ctu[0x14:0x1c], addr)
	copy(ctu[0x1c:], []byte(name))
	out = append(out, ctu...)

	cuuu := make([]byte, 0x10)
	copy(cuuu, []byte("CUUU"))
	out = append(out, cuuu...)

	cs := make([]byte, 12+len(label)+1)
	copy(cs, []byte("CS\x00\x00"))
	binary.LittleEndian.PutUint64(cs[4:12], 0xbeef)
	copy(cs[12:], []byte(label))
	out = append(out, cs...)

	binding := make([]byte, 0x20)
	copy(binding, []byte("Ctulul"))
	binary.LittleEndian.PutUint64(binding[0x10:0x18], addr)
	out = append(out, binding...)
	return out
}
