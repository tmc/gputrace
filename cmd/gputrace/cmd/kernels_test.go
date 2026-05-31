package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"

	"github.com/tmc/gputrace"
)

func TestRunKernelsJSONWritesToCommandOutput(t *testing.T) {
	oldFilter := kernelsFilter
	oldVerbose := kernelsVerbose
	oldStats := kernelsStats
	oldJSON := kernelsJSON
	t.Cleanup(func() {
		kernelsFilter = oldFilter
		kernelsVerbose = oldVerbose
		kernelsStats = oldStats
		kernelsJSON = oldJSON
	})

	kernelsFilter = ""
	kernelsVerbose = false
	kernelsStats = false
	kernelsJSON = true

	cmd := &cobra.Command{}
	var commandStdout bytes.Buffer
	cmd.SetOut(&commandStdout)

	tracePath := writeKernelsMinimalTraceBundle(t)
	osStdout, err := captureStdout(t, func() error {
		return runKernels(cmd, []string{tracePath})
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
  }
]
`
	if got := out.String(); got != want {
		t.Fatalf("json = %q, want %q", got, want)
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
