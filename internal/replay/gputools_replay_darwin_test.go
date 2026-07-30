//go:build darwin

package replay

import (
	"strings"
	"testing"

	"github.com/tmc/apple/objc"
)

func TestGPUToolsReplayRequiresControllerABI(t *testing.T) {
	replay := &GPUToolsReplay{dispatch: 1, commitCommand: 1}
	for name, err := range map[string]error{
		"dispatch": func() error {
			_, err := replay.DefaultDispatchFunctionNoPinning()
			return err
		}(),
		"commit":  replay.CommitCommandBuffer(objc.ID(1)),
		"execute": replay.ExecuteCommandBuffer(objc.ID(1), nil, nil),
	} {
		if err == nil || !strings.Contains(err.Error(), "controller ABI arguments are unavailable") {
			t.Errorf("%s error = %v, want unresolved controller ABI error", name, err)
		}
	}
}

func TestDyldLocalGPUToolsReplaySymbols(t *testing.T) {
	replay, err := OpenGPUToolsReplay()
	if err != nil {
		t.Skipf("GPUToolsReplay is unavailable: %v", err)
	}
	if replay.supportInit == 0 || replay.dispatch == 0 || replay.commitCommand == 0 {
		t.Fatal("GPUToolsReplay opened without both entry points")
	}
}
