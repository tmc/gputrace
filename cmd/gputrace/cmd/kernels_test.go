package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/tmc/gputrace"
)

func TestRunKernelsJSONWritesToCommandOutput(t *testing.T) {
	cmd := &cobra.Command{}
	var commandStdout bytes.Buffer
	cmd.SetOut(&commandStdout)

	tracePath := writeKernelsMinimalTraceBundle(t)
	osStdout, err := captureStdout(t, func() error {
		return runKernels(cmd, []string{tracePath}, &kernelsOptions{
			json: true,
		})
	})
	if err != nil {
		t.Fatalf("runKernels: %v", err)
	}
	if osStdout != "" {
		t.Fatalf("os stdout = %q, want empty", osStdout)
	}
	if got, want := commandStdout.String(), "[]\n"; got != want {
		t.Fatalf("command stdout = %q, want %q", got, want)
	}
}

func TestWriteKernelsJSON(t *testing.T) {
	kernels := []*gputrace.KernelStat{
		{
			Name:          "copy_kernel",
			PipelineAddr:  0x1234,
			DispatchCount: 4,
			DebugGroups: map[string]int{
				"dispatch": 3,
			},
			EncoderLabels: map[string]int{
				"encoder": 4,
			},
		},
		{
			Name:          "unknown",
			DispatchCount: 7,
		},
	}
	timingStats := map[string]*gputrace.TimingStat{
		"copy_kernel": {
			TotalTime: 10,
		},
	}

	var out bytes.Buffer
	if err := writeKernelsJSON(&out, kernels, timingStats); err != nil {
		t.Fatalf("writeKernelsJSON: %v", err)
	}

	const want = `[
  {
    "name": "copy_kernel",
    "row_kind": "named_inventory",
    "pipeline_addr": "0x1234",
    "dispatch_count": 4,
    "debug_groups": {
      "dispatch": 3
    },
    "encoder_labels": {
      "encoder": 4
    },
    "total_time_ms": 10,
    "avg_time_ms": 2.5
  },
  {
    "name": "unknown",
    "row_kind": "synthetic_unattributed_bucket",
    "pipeline_addr": "0x0",
    "dispatch_count": 7
  }
]
`
	if got := out.String(); got != want {
		t.Fatalf("json = %q, want %q", got, want)
	}
}

func TestSplitKernelRows(t *testing.T) {
	kernels := []*gputrace.KernelStat{
		{Name: "kernel_b", DispatchCount: 56},
		{Name: "unknown", DispatchCount: 435},
		{Name: "kernel_a", DispatchCount: 1},
	}

	rows := splitKernelRows(kernels)
	if len(rows.Executed) != 2 || rows.Executed[0].Name != "kernel_b" || rows.Executed[1].Name != "kernel_a" {
		t.Fatalf("executed rows = %#v, want kernel_b and kernel_a", rows.Executed)
	}
	if rows.Unknown == nil || rows.Unknown.DispatchCount != 435 {
		t.Fatalf("unknown bucket = %#v, want 435 dispatches", rows.Unknown)
	}
}

// The four zero-dispatch rows below are the ones read as "a whole kernel
// family that exists solely to support static capacity". They were created and
// fused away, and none of them ran.
func TestSplitKernelRowsSeparatesCreatedButUnrun(t *testing.T) {
	rows := splitKernelRows([]*gputrace.KernelStat{
		{Name: "g1_Selectbfloat16"},
		{Name: "gemm_bfloat16", DispatchCount: 96},
		{Name: "sv_GreaterEqualint32"},
		{Name: "E0A5F8B1-4C2D-4E7A-9F13-2B6C5D8E0A11"},
	})
	if len(rows.Executed) != 1 || rows.Executed[0].Name != "gemm_bfloat16" {
		t.Errorf("executed = %#v, want only the kernel that dispatched", rows.Executed)
	}
	if len(rows.Unrun) != 2 {
		t.Errorf("unrun = %#v, want both zero-dispatch pipelines", rows.Unrun)
	}
	if len(rows.Libraries) != 1 {
		t.Errorf("libraries = %#v, want the UUID row", rows.Libraries)
	}
}

func TestWriteInactiveKernelRowsSaysWhatEachIs(t *testing.T) {
	var out bytes.Buffer
	writeInactiveKernelRows(&out, splitKernelRows([]*gputrace.KernelStat{
		{Name: "g1_Selectbfloat16"},
		{Name: "E0A5F8B1-4C2D-4E7A-9F13-2B6C5D8E0A11"},
	}))
	got := out.String()
	if !strings.Contains(got, "created but never dispatched") {
		t.Errorf("output does not say unrun pipelines did not run:\n%s", got)
	}
	if !strings.Contains(got, "library UUID (not kernel names)") {
		t.Errorf("output does not distinguish library UUIDs:\n%s", got)
	}
}

// A row that ran must never be filed as inactive: that would remove evidence
// rather than label it.
func TestWriteInactiveKernelRowsOmitsExecuted(t *testing.T) {
	var out bytes.Buffer
	writeInactiveKernelRows(&out, splitKernelRows([]*gputrace.KernelStat{
		{Name: "gemm_bfloat16", DispatchCount: 96},
	}))
	if out.Len() != 0 {
		t.Errorf("executed kernel listed as inactive:\n%s", out.String())
	}
}

func TestWriteUnknownKernelBucket(t *testing.T) {
	var out bytes.Buffer
	writeUnknownKernelBucket(&out, &gputrace.KernelStat{
		Name:          "unknown",
		DispatchCount: 435,
	})
	const want = "\nSynthetic unattributed bucket: 435 dispatches\n"
	if got := out.String(); got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func writeKernelsMinimalTraceBundle(t *testing.T) string {
	t.Helper()

	tracePath := filepath.Join(t.TempDir(), "minimal.gputrace")
	if err := os.Mkdir(tracePath, 0o755); err != nil {
		t.Fatalf("mkdir trace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tracePath, "metadata"), []byte(kernelsMinimalMetadataPlist), 0o644); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tracePath, "capture"), []byte(gputrace.MagicMTSP), 0o644); err != nil {
		t.Fatalf("write capture: %v", err)
	}
	return tracePath
}

const kernelsMinimalMetadataPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>(uuid)</key>
	<string>kernels-minimal-test-trace</string>
	<key>DYCaptureSession.capture_version</key>
	<integer>1</integer>
	<key>DYCaptureSession.graphics_api</key>
	<integer>1</integer>
	<key>DYCaptureSession.deviceId</key>
	<integer>0</integer>
	<key>DYCaptureSession.nativePointerSize</key>
	<integer>8</integer>
	<key>DYCaptureEngine.captured_frames_count</key>
	<integer>1</integer>
</dict>
</plist>
`
