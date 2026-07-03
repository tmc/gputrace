package gputrace

import (
	"fmt"
	"os"
	"path/filepath"
)

func ExampleOpen() {
	dir, err := os.MkdirTemp("", "gputrace-example-*")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer os.RemoveAll(dir)

	tracePath := filepath.Join(dir, "minimal.gputrace")
	if err := writeMinimalTraceBundle(tracePath); err != nil {
		fmt.Println(err)
		return
	}

	trace, err := Open(tracePath)
	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println(trace.Metadata.UUID)
	fmt.Println(len(trace.CaptureData))

	// Output:
	// minimal-test-trace
	// 4
}

func writeMinimalTraceBundle(path string) error {
	if err := os.Mkdir(path, 0o777); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(path, "metadata"), []byte(minimalMetadataPlist), 0o666); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(path, "capture"), []byte(MagicMTSP), 0o666)
}

const minimalMetadataPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>(uuid)</key>
	<string>minimal-test-trace</string>
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
