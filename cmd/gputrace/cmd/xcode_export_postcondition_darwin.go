//go:build darwin

package cmd

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func exportPathFromSheetState(state exportSheetState) (string, error) {
	if state.Filename == "" || filepath.Base(state.Filename) != state.Filename {
		return "", fmt.Errorf("save filename is not established")
	}
	for _, candidate := range state.DirectoryCandidates {
		value := candidate
		if strings.HasPrefix(value, "file://") {
			parsed, err := url.Parse(value)
			if err != nil {
				continue
			}
			value = parsed.Path
		}
		if decoded, err := url.PathUnescape(value); err == nil {
			value = decoded
		}
		if filepath.IsAbs(value) {
			return filepath.Join(filepath.Clean(value), state.Filename), nil
		}
	}
	return "", fmt.Errorf("save directory is not exposed as an absolute path")
}

func waitForExportFile(ctx context.Context, path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var previousSize int64 = -1
	stable := 0
	for {
		info, err := os.Stat(path)
		if err == nil && !info.IsDir() && info.Size() > 0 {
			if info.Size() == previousSize {
				stable++
				if stable >= 2 {
					return nil
				}
			} else {
				stable = 0
				previousSize = info.Size()
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("saved export was not verified at %s within %s", path, timeout.Round(time.Second))
		}
		if err := waitForAutomation(ctx, 200*time.Millisecond); err != nil {
			return err
		}
	}
}
