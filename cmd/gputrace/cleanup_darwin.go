//go:build darwin

package main

import "github.com/tmc/macgo"

func cleanupMacgo() {
	macgo.Cleanup()
}
