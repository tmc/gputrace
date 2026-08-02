//go:build !darwin

package cmd

import "fmt"

func readXcodeGPUTime(string) (uint64, error) {
	return 0, fmt.Errorf("Xcode GPU Time is only available on Darwin")
}
