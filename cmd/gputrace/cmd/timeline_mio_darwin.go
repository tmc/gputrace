//go:build darwin

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/tmc/gputrace/internal/profilerraw"
	"github.com/tmc/gputrace/internal/xcodebindings"
)

var xcodeGPUTimeStderrMu sync.Mutex

func readXcodeGPUTime(tracePath string) (uint64, error) {
	profilerDir := profilerraw.FindDir(tracePath)
	if profilerDir == "" {
		return 0, fmt.Errorf("find profiler archive")
	}
	var summary xcodebindings.ProcessedStreamData
	err := withDiscardedXcodeGPUTimeStderr(func() error {
		var err error
		summary, err = xcodebindings.ProcessStreamData(filepath.Join(profilerDir, "streamData"))
		return err
	})
	if err != nil {
		return 0, err
	}
	return summary.GPUTime, nil
}

// withDiscardedXcodeGPUTimeStderr hides diagnostic spam emitted by Xcode's
// GTLLVMHelper while it builds the requested model. Errors still return through
// ProcessStreamData and are reported by the command.
func withDiscardedXcodeGPUTimeStderr(fn func() error) error {
	xcodeGPUTimeStderrMu.Lock()
	defer xcodeGPUTimeStderrMu.Unlock()

	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer null.Close()

	savedStdout, err := syscall.Dup(int(os.Stdout.Fd()))
	if err != nil {
		return err
	}
	defer syscall.Close(savedStdout)
	if err := syscall.Dup2(int(null.Fd()), int(os.Stdout.Fd())); err != nil {
		return err
	}
	defer syscall.Dup2(savedStdout, int(os.Stdout.Fd()))

	savedStderr, err := syscall.Dup(int(os.Stderr.Fd()))
	if err != nil {
		return err
	}
	defer syscall.Close(savedStderr)
	if err := syscall.Dup2(int(null.Fd()), int(os.Stderr.Fd())); err != nil {
		return err
	}
	defer syscall.Dup2(savedStderr, int(os.Stderr.Fd()))

	return fn()
}
