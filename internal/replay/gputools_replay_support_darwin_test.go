//go:build darwin && metal

package replay

import (
	"testing"

	"github.com/tmc/apple/metal"
)

func TestGPUToolsReplayInitializeSupport(t *testing.T) {
	replay, err := OpenGPUToolsReplay()
	if err != nil {
		t.Skipf("GPUToolsReplay is unavailable: %v", err)
	}
	device := metal.MTLCreateSystemDefaultDevice()
	if device.GetID() == 0 {
		t.Skip("Metal device is unavailable")
	}
	if err := replay.InitializeSupport(device.GetID()); err != nil {
		t.Fatal(err)
	}
}

func TestMetalReplayEnableGPUToolsReplay(t *testing.T) {
	engine, err := NewMetalReplayEngine(&Trace{})
	if err != nil {
		t.Skipf("Metal is unavailable: %v", err)
	}
	defer engine.Close()
	if err := engine.EnableGPUToolsReplay(); err != nil {
		t.Skipf("GPUToolsReplay is unavailable: %v", err)
	}
	if engine.GPUToolsReplay == nil {
		t.Fatal("EnableGPUToolsReplay did not retain the loader")
	}
}
