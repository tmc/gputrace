package counter

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/gputrace/internal/trace"
)

func TestParseXcodeCountersCSVUsesHeaderColumns(t *testing.T) {
	tests := []struct {
		name              string
		csv               string
		wantIndex         int
		wantFunctionIndex int
		wantCommandBuffer string
		wantEncoderLabel  string
		wantALU           float64
		wantInvocations   float64
	}{
		{
			name: "debug_group",
			csv: "Index,Encoder FunctionIndex,CommandBuffer Label,Debug Group,Encoder Label,,ALU Utilization,Kernel Invocations\n" +
				"1,7,Command Buffer 0,root/group,kernel_add,,12.5,64\n",
			wantIndex:         1,
			wantFunctionIndex: 7,
			wantCommandBuffer: "Command Buffer 0",
			wantEncoderLabel:  "kernel_add",
			wantALU:           12.5,
			wantInvocations:   64,
		},
		{
			name: "legacy_without_debug_group",
			csv: "Index,Encoder FunctionIndex,CommandBuffer Label,Encoder Label,,ALU Utilization,Kernel Invocations\n" +
				"2,8,Command Buffer 1,kernel_mul,,25,128\n",
			wantIndex:         2,
			wantFunctionIndex: 8,
			wantCommandBuffer: "Command Buffer 1",
			wantEncoderLabel:  "kernel_mul",
			wantALU:           25,
			wantInvocations:   128,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "Counters.csv")
			if err := os.WriteFile(path, []byte(tt.csv), 0o666); err != nil {
				t.Fatal(err)
			}

			data, err := ParseXcodeCountersCSV(nil, path)
			if err != nil {
				t.Fatalf("ParseXcodeCountersCSV: %v", err)
			}
			if len(data.Encoders) != 1 {
				t.Fatalf("len(Encoders) = %d, want 1", len(data.Encoders))
			}
			if len(data.Metrics) != 2 || data.Metrics[0] != "ALU Utilization" || data.Metrics[1] != "Kernel Invocations" {
				t.Fatalf("Metrics = %#v, want ALU Utilization and Kernel Invocations", data.Metrics)
			}

			enc := data.Encoders[0]
			if enc.Index != tt.wantIndex || enc.FunctionIndex != tt.wantFunctionIndex {
				t.Fatalf("index fields = (%d, %d), want (%d, %d)", enc.Index, enc.FunctionIndex, tt.wantIndex, tt.wantFunctionIndex)
			}
			if enc.CommandBufferLabel != tt.wantCommandBuffer {
				t.Fatalf("CommandBufferLabel = %q, want %q", enc.CommandBufferLabel, tt.wantCommandBuffer)
			}
			if enc.EncoderLabel != tt.wantEncoderLabel {
				t.Fatalf("EncoderLabel = %q, want %q", enc.EncoderLabel, tt.wantEncoderLabel)
			}
			if enc.Counters["ALU Utilization"] != tt.wantALU {
				t.Fatalf("ALU Utilization = %v, want %v", enc.Counters["ALU Utilization"], tt.wantALU)
			}
			if enc.Counters["Kernel Invocations"] != tt.wantInvocations {
				t.Fatalf("Kernel Invocations = %v, want %v", enc.Counters["Kernel Invocations"], tt.wantInvocations)
			}
			if _, ok := enc.Counters["Debug Group"]; ok {
				t.Fatalf("Counters contains Debug Group metadata: %#v", enc.Counters)
			}
			if _, ok := enc.Counters[""]; ok {
				t.Fatalf("Counters contains empty separator column: %#v", enc.Counters)
			}
		})
	}
}

func TestXcodeCountersCSVParsing(t *testing.T) {
	tr := openCountersCSVTrace(t)

	// Try to parse the Xcode Counters.csv
	csvData, err := ImportCountersCSV(tr)
	if err != nil {
		t.Fatalf("Failed to parse Counters.csv: %v", err)
	}

	t.Logf("Parsed %d encoders", len(csvData.Encoders))

	if len(csvData.Encoders) == 0 {
		t.Fatal("Expected at least one encoder")
	}

	// Check first encoder
	enc := csvData.Encoders[0]
	t.Logf("Encoder 0:")
	t.Logf("  Index: %d", enc.Index)
	t.Logf("  FunctionIndex: %d", enc.EncoderFunctionIndex)
	t.Logf("  CommandBuffer: %s", enc.CommandBufferLabel)
	t.Logf("  Encoder: %s", enc.EncoderLabel)
	t.Logf("  ALU Utilization: %.2f", enc.ALUUtilization)
	t.Logf("  Kernel Occupancy: %.2f", enc.KernelOccupancy)
	t.Logf("  Kernel Invocations: %d", enc.KernelInvocations)
	t.Logf("  Device Memory Bandwidth: %.2f GB/s", enc.DeviceMemoryBandwidth)
}

func TestXcodeCSVMemoryBandwidth(t *testing.T) {
	tr := openCountersCSVTrace(t)

	csvData, err := ImportCountersCSV(tr)
	if err != nil {
		t.Skipf("Counters.csv not available: %v", err)
	}

	// Check memory bandwidth fields
	for i, enc := range csvData.Encoders {
		t.Logf("Encoder %d: %s", i, enc.EncoderLabel)
		t.Logf("  Bytes Read: %d", enc.BytesReadFromDeviceMemory)
		t.Logf("  Bytes Written: %d", enc.BytesWrittenToDeviceMemory)
		t.Logf("  Device Memory BW: %.2f GB/s", enc.DeviceMemoryBandwidth)
		t.Logf("  GPU Read BW: %.2f GB/s", enc.GPUReadBandwidth)
		t.Logf("  GPU Write BW: %.2f GB/s", enc.GPUWriteBandwidth)
	}
}

func openCountersCSVTrace(t *testing.T) *trace.Trace {
	t.Helper()

	tracePath := os.Getenv("GPUTRACE_COUNTERS_CSV_TRACE")
	if tracePath == "" {
		t.Skip("set GPUTRACE_COUNTERS_CSV_TRACE to run Xcode Counters.csv fixture tests")
	}
	tracePath = filepath.Clean(tracePath)

	info, err := os.Stat(tracePath)
	if err != nil {
		t.Fatalf("GPUTRACE_COUNTERS_CSV_TRACE=%q is not accessible: %v", tracePath, err)
	}
	if !info.IsDir() {
		t.Fatalf("GPUTRACE_COUNTERS_CSV_TRACE=%q must point to a .gputrace directory", tracePath)
	}
	if filepath.Ext(tracePath) != ".gputrace" {
		t.Fatalf("GPUTRACE_COUNTERS_CSV_TRACE=%q must point to a .gputrace directory", tracePath)
	}

	tr, err := trace.Open(tracePath)
	if err != nil {
		t.Fatalf("open trace from GPUTRACE_COUNTERS_CSV_TRACE=%q: %v", tracePath, err)
	}
	return tr
}
