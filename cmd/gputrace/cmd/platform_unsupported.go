//go:build !darwin

package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

const darwinOnly = "only supported on darwin"

func platformXcodeProfilePreRun(cmd *cobra.Command, args []string) error {
	return nil
}

func configurePlatformCommand(name string, cmd *cobra.Command) {}

func platformXcodeProfileRun(name string) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf("%s is %s", cmd.CommandPath(), darwinOnly)
	}
}
