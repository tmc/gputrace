// Copyright © 2026 gputrace authors. All rights reserved.
// Use of this source code is governed by a BSD-style license.

package exp

import (
	"fmt"
	"os"

	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/metal"
)

// CapturePureGo starts programmatic Metal frame capture for a given device using 100% Pure Go bindings.
// It writes the resulting .gputrace archive directly to outputTracePath.
func CapturePureGo(outputTracePath string, device metal.MTLDevice) (func() error, error) {
	if outputTracePath == "" {
		return nil, fmt.Errorf("outputTracePath cannot be empty")
	}

	mgr := metal.GetMTLCaptureManagerClass().SharedCaptureManager()
	desc := metal.GetMTLCaptureDescriptorClass().Alloc().Init()
	desc.SetCaptureObject(device)
	desc.SetDestination(metal.MTLCaptureDestinationGPUTraceDocument)

	url := foundation.GetNSURLClass().FileURLWithPath(outputTracePath)
	desc.SetOutputURL(url)

	if ok, err := mgr.StartCaptureWithDescriptorError(desc); !ok || err != nil {
		return nil, fmt.Errorf("start capture failed: %w", err)
	}

	stopFunc := func() error {
		mgr.StopCapture()
		if info, err := os.Stat(outputTracePath); err != nil || !info.IsDir() {
			return fmt.Errorf("capture output verification failed for %s: %w", outputTracePath, err)
		}
		return nil
	}

	return stopFunc, nil
}
