// Copyright © 2026 gputrace authors. All rights reserved.
// Use of this source code is governed by a BSD-style license.

package exp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/apple/metal"
)

func TestCapturePureGo(t *testing.T) {
	dev := metal.MTLCreateSystemDefaultDevice()
	if dev.ID == 0 {
		t.Skip("Metal device unavailable in this environment")
	}

	mgr := metal.GetMTLCaptureManagerClass().SharedCaptureManager()
	if !mgr.SupportsDestination(metal.MTLCaptureDestinationGPUTraceDocument) {
		t.Skip("GPUTraceDocument destination not supported in test environment without Metal frame capture enabled")
	}

	tmpDir := t.TempDir()
	tracePath := filepath.Join(tmpDir, "purego_test.gputrace")

	stopFunc, err := CapturePureGo(tracePath, dev)
	if err != nil {
		t.Fatalf("CapturePureGo() unexpected error = %v", err)
	}

	// Submit a minimal command buffer to device
	queue := dev.NewCommandQueue()
	cb := queue.CommandBuffer()
	cb.Commit()
	cb.WaitUntilCompleted()

	if err := stopFunc(); err != nil {
		t.Fatalf("stopFunc() unexpected error = %v", err)
	}

	if _, err := os.Stat(tracePath); err != nil {
		t.Errorf("expected trace directory at %s, got error: %v", tracePath, err)
	}
}
