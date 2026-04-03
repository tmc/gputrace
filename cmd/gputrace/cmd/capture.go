package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/metal"
	"github.com/tmc/apple/objc"
	"github.com/tmc/macgo"
)

// MTLCaptureDestinationGPUTraceDocument is the correct value (2) for GPU trace document output.
// The generated enum has a bug where both values are 0.
const MTLCaptureDestinationGPUTraceDocumentValue metal.MTLCaptureDestination = 2

var (
	captureOutput   string
	captureNoBundle bool
)

var captureCmd = &cobra.Command{
	Use:   "capture -- command [args...]",
	Short: "Capture GPU trace while running a command",
	Long: `Captures Metal GPU activity to a .gputrace file while running the specified command.

The captured trace can be opened in Xcode for analysis.

Example:
  gputrace capture -o trace.gputrace -- ./my-metal-app --arg1 --arg2

Requirements:
  - The target application must use Metal
  - GPU Frame Capture must be enabled (MTL_CAPTURE_ENABLED=1)
  - May require entitlements for some applications`,
	Args: cobra.MinimumNArgs(1),
	RunE: runCapture,
}

func init() {
	captureCmd.Flags().StringVarP(&captureOutput, "output", "o", "capture.gputrace", "Output .gputrace file path")
	captureCmd.Flags().BoolVar(&captureNoBundle, "no-bundle", false, "Skip macgo app bundle (may limit capture capabilities)")
	rootCmd.AddCommand(captureCmd)
}

// setupCaptureBundle configures macgo with entitlements needed for GPU capture.
func setupCaptureBundle() error {
	if captureNoBundle || os.Getenv("GPUTRACE_SKIP_MACGO") != "" {
		return nil
	}

	cfg := &macgo.Config{
		AppName:  "gputrace",
		BundleID: "com.tmc.gputrace",
		Permissions: []macgo.Permission{
			macgo.Accessibility,
		},
		Custom: []string{
			"com.apple.security.get-task-allow",          // Enable debugger attach (GPU capture)
			"com.apple.security.automation.apple-events", // AppleScript automation
		},
		AutoSign: true,
		UIMode:    macgo.UIModeBackground,
		Info: map[string]interface{}{
			"NSAppleEventsUsageDescription": "gputrace needs to control applications for GPU trace capture.",
		},
	}

	if err := macgo.Start(cfg); err != nil {
		// Don't fail hard - we can still try environment-based capture
		fmt.Printf("Note: macgo bundle setup failed: %v\n", err)
		fmt.Println("Proceeding with environment-based capture...")
		return nil
	}

	return nil
}

func runCapture(cmd *cobra.Command, args []string) error {
	// Setup macgo bundle with GPU capture entitlements
	if err := setupCaptureBundle(); err != nil {
		return err
	}

	// Ensure output path has .gputrace extension
	if !strings.HasSuffix(captureOutput, ".gputrace") {
		captureOutput += ".gputrace"
	}

	// Get absolute path for output
	absOutput, err := filepath.Abs(captureOutput)
	if err != nil {
		return fmt.Errorf("failed to resolve output path: %w", err)
	}

	// Remove existing file if present
	if _, err := os.Stat(absOutput); err == nil {
		if err := os.RemoveAll(absOutput); err != nil {
			return fmt.Errorf("failed to remove existing trace: %w", err)
		}
	}

	// 1. Get Metal device
	devPtr := metal.MTLCreateSystemDefaultDevice()
	if devPtr == nil {
		return fmt.Errorf("failed to get default Metal device")
	}
	device := metal.MTLDeviceObjectFromID(objc.IDFrom(devPtr))
	nameID := objc.Send[objc.ID](device.GetID(), objc.Sel("name"))
	if nameID != 0 {
		nameCStr := objc.Send[*byte](nameID, objc.Sel("UTF8String"))
		if nameCStr != nil {
			fmt.Printf("Using Metal device: %s\n", objc.GoString(nameCStr))
		}
	}

	// 2. Get shared capture manager
	manager := metal.GetMTLCaptureManagerClass().SharedCaptureManager()
	if manager.GetID() == 0 {
		return fmt.Errorf("failed to get capture manager")
	}

	// 3. Check if GPU trace document destination is supported
	// Note: This typically returns false when running from Terminal because
	// programmatic capture requires specific entitlements:
	// - com.apple.security.get-task-allow (debug builds)
	// - com.apple.developer.kernel.extended-debugger
	//
	// For unsigned apps, we can try to proceed anyway and capture child processes
	// that have MTL_CAPTURE_ENABLED=1 set.
	gpuTraceSupported := manager.SupportsDestination(MTLCaptureDestinationGPUTraceDocumentValue)
	developerToolsSupported := manager.SupportsDestination(metal.MTLCaptureDestinationDeveloperTools)

	if !gpuTraceSupported {
		fmt.Println("Note: GPU trace document capture not directly supported (likely missing entitlements)")
		fmt.Println("Attempting capture via environment variable injection...")
		fmt.Println("")
		fmt.Println("For the child process to be captured, it must:")
		fmt.Println("  1. Use Metal APIs")
		fmt.Println("  2. Be built with GPU Frame Capture enabled")
		fmt.Println("  3. Or run: MTL_CAPTURE_ENABLED=1 <command>")
		fmt.Println("")

		// Try proceeding anyway - some configurations still work
		if !developerToolsSupported {
			// Neither destination is supported - capture won't work from this process
			// But we can still set up environment for child process
			return runWithEnvCapture(args, absOutput)
		}
	}

	// 4. Create capture descriptor
	desc := metal.NewMTLCaptureDescriptor()
	desc.SetCaptureObject(device)
	desc.SetDestination(MTLCaptureDestinationGPUTraceDocumentValue)

	// 5. Create NSURL for output path
	outputURL := foundation.NewURLFileURLWithPath(absOutput)
	desc.SetOutputURL(outputURL)

	fmt.Printf("Capture output: %s\n", absOutput)

	// 6. Start capture
	ok, captureErr := manager.StartCaptureWithDescriptorError(desc)
	if !ok || captureErr != nil {
		errMsg := "unknown error"
		if captureErr != nil {
			errMsg = captureErr.Error()
		}
		return fmt.Errorf("failed to start capture: %s", errMsg)
	}

	if !manager.IsCapturing() {
		return fmt.Errorf("capture did not start properly")
	}

	fmt.Println("GPU capture started...")

	// 7. Run user command
	userCmd := exec.Command(args[0], args[1:]...)
	userCmd.Stdout = os.Stdout
	userCmd.Stderr = os.Stderr
	userCmd.Stdin = os.Stdin

	// Set environment to enable Metal capture in child process
	userCmd.Env = append(os.Environ(),
		"MTL_CAPTURE_ENABLED=1",
		"METAL_DEVICE_WRAPPER_TYPE=1",
	)

	fmt.Printf("Running: %s\n", strings.Join(args, " "))
	cmdErr := userCmd.Run()

	// 8. Stop capture
	manager.StopCapture()
	fmt.Println("GPU capture stopped.")

	// Check if capture file was created
	if fi, err := os.Stat(absOutput); err == nil {
		fmt.Printf("Trace saved: %s (%.2f MB)\n", absOutput, float64(fi.Size())/(1024*1024))
	} else {
		fmt.Println("Warning: trace file may not have been created")
	}

	if cmdErr != nil {
		return fmt.Errorf("command failed: %w", cmdErr)
	}

	return nil
}

// runWithEnvCapture runs the command with environment variables set for Metal capture.
// This is a fallback when programmatic capture is not supported due to entitlements.
func runWithEnvCapture(args []string, outputPath string) error {
	fmt.Println("Using environment-based capture (MTL_CAPTURE_ENABLED)...")
	fmt.Println("")

	userCmd := exec.Command(args[0], args[1:]...)
	userCmd.Stdout = os.Stdout
	userCmd.Stderr = os.Stderr
	userCmd.Stdin = os.Stdin

	// Set environment variables for Metal capture
	// The child process will capture to a default location or specified path
	userCmd.Env = append(os.Environ(),
		"MTL_CAPTURE_ENABLED=1",
		"METAL_CAPTURE_ENABLED=1",
		"METAL_DEVICE_WRAPPER_TYPE=1",
		fmt.Sprintf("MTL_CAPTURE_OUTPUT=%s", outputPath),
	)

	fmt.Printf("Running: %s\n", strings.Join(args, " "))
	fmt.Println("Metal capture environment variables set.")
	fmt.Println("")

	if err := userCmd.Run(); err != nil {
		return fmt.Errorf("command failed: %w", err)
	}

	// Check if capture file was created
	if fi, err := os.Stat(outputPath); err == nil {
		fmt.Printf("\nTrace saved: %s (%.2f MB)\n", outputPath, float64(fi.Size())/(1024*1024))
	} else {
		// Check default Metal capture locations
		homeDir, _ := os.UserHomeDir()
		defaultPaths := []string{
			filepath.Join(homeDir, "Desktop", "*.gputrace"),
			"/tmp/*.gputrace",
		}
		fmt.Println("\nNote: Trace file not found at specified path.")
		fmt.Println("Metal may have saved the capture to a default location:")
		for _, p := range defaultPaths {
			matches, _ := filepath.Glob(p)
			for _, m := range matches {
				if fi, err := os.Stat(m); err == nil {
					fmt.Printf("  Found: %s (%.2f MB)\n", m, float64(fi.Size())/(1024*1024))
				}
			}
		}
	}

	return nil
}
