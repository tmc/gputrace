//go:build darwin

package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/osa"
)

// PermissionsOutput represents the JSON output for check-permissions.
type PermissionsOutput struct {
	Accessibility   bool `json:"accessibility"`
	ScreenRecording bool `json:"screen_recording"`
	AllGranted      bool `json:"all_granted"`
}

func init() {
	checkPermissionsCmd := &cobra.Command{
		Use:   "check-permissions",
		Short: "Check required permissions",
		Long: `Check if gputrace has the required permissions:
  - Accessibility: Required for UI automation via AX APIs
  - Screen Recording: Required for screenshots

Use --json for machine-readable output.
Use --no-prompt to check without triggering permission dialogs.`,
		// Override PersistentPreRunE to only setup macgo, skip permission checks
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return setupMacgo()
		},
		RunE: runCheckPermissions,
	}
	collectXcodeProfileCmd.AddCommand(checkPermissionsCmd)
}

func runCheckPermissions(cmd *cobra.Command, args []string) error {
	output := PermissionsOutput{}

	// Check Accessibility
	output.Accessibility = osa.HasAccessibilityPermission()
	if !output.Accessibility && !collectProfileNoPrompt {
		osa.PromptAccessibilityPermission()
		output.Accessibility = osa.HasAccessibilityPermission()
	}

	// Check Screen Recording by attempting a screenshot
	output.ScreenRecording = checkScreenRecordingPermission()

	// Determine if all required permissions are granted
	output.AllGranted = output.Accessibility && output.ScreenRecording

	if collectProfileJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(output)
	}

	// Human-readable output
	fmt.Printf("Permissions status:\n")
	fmt.Printf("  Accessibility:    %s\n", permissionStatus(output.Accessibility))
	fmt.Printf("  Screen Recording: %s\n", permissionStatus(output.ScreenRecording))
	fmt.Println()

	if output.AllGranted {
		fmt.Println("All permissions granted.")
		return nil
	}

	fmt.Println("Some permissions are missing. Run without --no-prompt to trigger dialogs,")
	fmt.Println("or use 'axperms -enable gputrace.app' to grant permissions.")
	if collectProfileNoPrompt {
		return fmt.Errorf("missing permissions")
	}
	return nil
}

func permissionStatus(granted bool) string {
	if granted {
		return Colorize("✓ granted", ColorGreen)
	}
	return Colorize("✗ denied", ColorRed)
}

func checkScreenRecordingPermission() bool {
	return cgPreflightScreenCaptureAccess()
}
