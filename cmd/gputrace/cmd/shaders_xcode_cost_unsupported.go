//go:build !darwin

package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func runShadersXcodeCost(*cobra.Command, string, *shadersOptions) error {
	return fmt.Errorf("--xcode-cost requires macOS and Xcode")
}
