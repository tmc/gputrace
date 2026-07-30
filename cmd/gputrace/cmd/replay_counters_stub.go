//go:build !darwin || !metal

package cmd

import "fmt"

func replayCountersRealAvailable() bool { return false }

func runReplayCountersReal(_ string, _ *replayCountersOptions) error {
	return fmt.Errorf("real replay counter collection requires macOS with the metal build tag; rerun with --simulate")
}
