// Command axperms automates granting accessibility permissions.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/tmc/apple/x/axuiautomation"
	"github.com/tmc/macgo"
)

var noPrompt bool

func main() {
	runtime.LockOSThread()
	macgo.Start(&macgo.Config{Permissions: []macgo.Permission{macgo.Accessibility}})

	// Parse flags
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		if args[i] == "--no-prompt" || args[i] == "-n" {
			noPrompt = true
			args = append(args[:i], args[i+1:]...)
			i--
		}
	}

	// If no args, run "test" mode which writes to a file (for Finder launch testing)
	if len(args) < 1 {
		runTestMode()
		return
	}

	var err error
	switch args[0] {
	case "reset", "r":
		// reset doesn't need accessibility permissions
		if len(args) < 2 {
			err = fmt.Errorf("usage: axperms reset <bundle-id|app>")
		} else {
			err = reset(args[1])
		}
	default:
		// all other commands need accessibility permissions
		// Skip check if AXPERMS_SKIP_CHECK is set (for debugging)
		if os.Getenv("AXPERMS_SKIP_CHECK") == "" && !axuiautomation.IsProcessTrusted() {
			if noPrompt {
				fmt.Fprintf(os.Stderr, "error: no accessibility permission (use System Settings to grant)\n")
				os.Exit(1)
			}
			if err := waitForPermissions(); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
		}
		switch args[0] {
		case "grant", "g":
			if len(args) < 2 {
				err = fmt.Errorf("usage: axperms grant <app>")
			} else {
				err = grant(args[1])
			}
		case "list", "l":
			err = list()
		default:
			err = grant(args[0])
		}
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func waitForPermissions() error {
	fmt.Println("axperms needs accessibility permissions to work.")
	fmt.Println("Opening System Settings...")
	openPane()
	time.Sleep(1 * time.Second)

	fmt.Println("\nPlease enable axperms in the list, then press Enter to continue...")
	fmt.Println("(You may need to click the + button to add it first)")

	// Wait for user input
	var input string
	fmt.Scanln(&input)

	// Check again
	for i := 0; i < 5; i++ {
		if axuiautomation.IsProcessTrusted() {
			fmt.Println("Permissions granted!")
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}

	return fmt.Errorf("permissions not granted - please enable axperms in Accessibility settings")
}

func openPane() error {
	return exec.Command("open", "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility").Run()
}

func getApp() (*axuiautomation.Application, error) {
	out, err := exec.Command("pgrep", "-x", "System Settings").Output()
	if err != nil {
		return nil, fmt.Errorf("System Settings not running")
	}
	var pid int
	fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &pid)

	app := axuiautomation.NewApplicationFromPID(int32(pid))
	if app == nil {
		return nil, fmt.Errorf("cannot connect to System Settings")
	}
	return app, nil
}

func grant(name string) error {
	name = strings.TrimSuffix(name, ".app")
	fmt.Printf("Granting accessibility for %q...\n", name)

	openPane()
	time.Sleep(3 * time.Second)

	app, err := getApp()
	if err != nil {
		return err
	}
	defer app.Close()

	// Find checkbox with matching app name via parent/sibling
	for retry := 0; retry < 10; retry++ {
		checkboxes := app.Descendants().WithLimit(5000).ByRole("AXCheckBox").AllElements()
		for _, cb := range checkboxes {
			parent := cb.Parent()
			if parent == nil {
				continue
			}
			var appName string
			for _, sib := range parent.Children() {
				if sib.Role() == "AXStaticText" {
					appName = sib.Value()
					if appName == "" {
						appName = sib.Title()
					}
					break
				}
			}
			if !strings.Contains(strings.ToLower(appName), strings.ToLower(name)) {
				continue
			}
			if cb.Value() == "1" {
				fmt.Printf("%q already enabled\n", appName)
				return nil
			}
			fmt.Printf("Clicking checkbox for %q\n", appName)
			return cb.Click()
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("%q not found - add it manually first", name)
}

func list() error {
	openPane()
	time.Sleep(3 * time.Second)

	app, err := getApp()
	if err != nil {
		return err
	}
	defer app.Close()

	// Use ByRole filter to efficiently find checkboxes
	checkboxes := app.Descendants().WithLimit(5000).ByRole("AXCheckBox").AllElements()

	fmt.Println("Apps with Accessibility permission:")
	for _, cb := range checkboxes {
		// Try to get name from AXIdentifier first (e.g., "gputrace.app_Toggle" -> "gputrace.app")
		var name string
		identifier := cb.Identifier()
		// Strip null bytes that may be embedded
		if idx := strings.IndexByte(identifier, 0); idx >= 0 {
			identifier = identifier[:idx]
		}
		if strings.Contains(identifier, "_Toggle") {
			name = strings.Split(identifier, "_Toggle")[0]
		}

		// Fall back to sibling static text lookup
		if name == "" {
			parent := cb.Parent()
			if parent != nil {
				for _, sib := range parent.Children() {
					if sib.Role() == "AXStaticText" {
						name = sib.Value()
						if name == "" {
							name = sib.Title()
						}
						break
					}
				}
			}
		}
		if name == "" || len(name) < 3 {
			continue
		}
		status := "[ ]"
		// Checkboxes use AXValue as a Boolean
		if cb.IsChecked() {
			status = "[x]"
		}
		fmt.Printf("  %s %s\n", status, name)
	}
	return nil
}

func reset(name string) error {
	name = strings.TrimSuffix(name, ".app")
	fmt.Printf("Resetting TCC permissions for %q...\n", name)
	cmd := exec.Command("tccutil", "reset", "All", name)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// runTestMode writes TCC status to a file for testing when launched from Finder
func runTestMode() {
	f, err := os.Create("/tmp/axperms_finder_test.txt")
	if err != nil {
		return
	}
	defer f.Close()

	fmt.Fprintf(f, "=== axperms TCC Test ===\n")
	fmt.Fprintf(f, "Time: %s\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(f, "PID: %d\n", os.Getpid())
	fmt.Fprintf(f, "Executable: %s\n", os.Args[0])
	fmt.Fprintf(f, "\n")
	fmt.Fprintf(f, "IsProcessTrusted: %v\n", axuiautomation.IsProcessTrusted())

	// Try to access System Settings
	out, _ := exec.Command("pgrep", "-x", "System Settings").Output()
	var pid int
	fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &pid)
	fmt.Fprintf(f, "\nSystem Settings PID: %d\n", pid)

	if pid > 0 {
		code, msg := axuiautomation.CheckAccessibilityAccess(int32(pid))
		fmt.Fprintf(f, "CheckAccessibilityAccess: code=%d msg=%q\n", code, msg)

		app := axuiautomation.NewApplicationFromPID(int32(pid))
		if app != nil {
			fmt.Fprintf(f, "App.IsRunning: %v\n", app.IsRunning())
			windows := app.Windows().AllElements()
			fmt.Fprintf(f, "Windows: %d\n", len(windows))
			app.Close()
		}
	}

	fmt.Fprintf(f, "\n=== Done ===\n")
}
