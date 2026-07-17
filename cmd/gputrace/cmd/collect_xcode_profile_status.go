//go:build darwin

package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

type checkStatusOptions struct {
	debug bool
}

// StatusOutput represents the JSON output for check-status.
type StatusOutput struct {
	Status                   string `json:"status"`
	ReplayAvailable          bool   `json:"replay_available"`
	ExportAvailable          bool   `json:"export_available"`
	ShowPerformanceAvailable bool   `json:"show_performance_available"`
	CurrentTab               string `json:"current_tab,omitempty"`
}

func runCheckStatus(cmd *cobra.Command, args []string, opts *checkStatusOptions) error {
	traceFile := ""
	if len(args) > 0 {
		traceFile = args[0]
	}
	debug := statusDebugEnabled(opts.debug)

	// Note: setupMacgo and checkPermissions are called by PersistentPreRunE

	if debug {
		fmt.Fprintln(os.Stderr, "[check-status] finding Xcode app...")
	}
	appAX, err := FindXcodeApp()
	if err != nil {
		if collectProfileOpts.json {
			return outputJSONError("XCODE_NOT_RUNNING", "Xcode not running", "Start Xcode first")
		}
		return fmt.Errorf("Xcode not running: %w", err)
	}
	if debug {
		fmt.Fprintln(os.Stderr, "[check-status] found Xcode app")
	}
	defer cfRelease(appAX)

	if debug {
		fmt.Fprintf(os.Stderr, "[check-status] finding target window (trace=%q)...\n", traceFile)
	}
	windowAX, err := findTargetWindow(appAX, traceFile)
	if err != nil {
		if collectProfileOpts.json {
			return outputJSONError("WINDOW_NOT_FOUND", err.Error(), "Check if the trace file is open")
		}
		return err
	}
	if debug {
		fmt.Fprintf(os.Stderr, "[check-status] got window: %v (title=%q)\n", windowAX, axString(windowAX, "AXTitle"))
	}

	if collectProfileOpts.json {
		if debug {
			fmt.Fprintln(os.Stderr, "[check-status] getting status output (JSON)...")
		}
		output := getStatusOutput(windowAX, debug)
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(output)
	}

	if debug {
		fmt.Fprintln(os.Stderr, "[check-status] getting profiling status...")
	}
	status := getProfilingStatusWithDebug(windowAX, debug)
	fmt.Println(status)
	return nil
}

func statusDebugEnabled(debug bool) bool {
	return debug || collectProfileOpts.debug || collectProfileOpts.verbose
}

// getStatusOutput returns a structured status output for JSON.
func getStatusOutput(window uintptr, debug bool) StatusOutput {
	status := getProfilingStatusWithDebug(window, debug)

	// Check for specific buttons
	replayBtn := FindReplayButton(window)
	exportBtn := findExportButton(window)
	showPerfBtn := findShowPerformanceButton(window)

	replayAvailable := replayBtn != 0 && IsElementEnabled(replayBtn)
	exportAvailable := exportBtn != 0 && IsElementEnabled(exportBtn)
	showPerfAvailable := showPerfBtn != 0 && IsElementEnabled(showPerfBtn)

	// Try to determine current tab
	currentTab := getCurrentTab(window)

	return StatusOutput{
		Status:                   status,
		ReplayAvailable:          replayAvailable,
		ExportAvailable:          exportAvailable,
		ShowPerformanceAvailable: showPerfAvailable,
		CurrentTab:               currentTab,
	}
}

// getCurrentTab tries to determine the currently selected tab.
func getCurrentTab(window uintptr) string {
	tabs := findAllTabs(window, 500)
	for _, tab := range tabs {
		if isTabSelected(tab) {
			title := axString(tab, "AXTitle")
			if title == "" {
				title = axString(tab, "AXDescription")
			}
			return title
		}
	}
	return ""
}

func getProfilingStatusWithDebug(window uintptr, debug bool) string {
	debug = statusDebugEnabled(debug)
	var hasProfiling, hasReplay, hasProfile, hasPerfNotAvailable bool
	var replayEnabled, profileEnabled bool

	// BFS to find profiling indicators and Replay/Profile buttons
	queue := []uintptr{window}
	visited := 0
	maxNodes := 2000 // Increased to find elements deeper in tree

	// Progress reporting
	startTime := time.Now()
	lastProgress := startTime
	progressInterval := 2 * time.Second
	buttonsFound := 0
	textsFound := 0

	for len(queue) > 0 && visited < maxNodes {
		el := queue[0]
		queue = queue[1:]
		visited++

		// Print progress every 2 seconds
		now := time.Now()
		if debug && now.Sub(lastProgress) >= progressInterval {
			elapsed := now.Sub(startTime).Seconds()
			fmt.Fprintf(os.Stderr, "[check-status] %.1fs: visited=%d queue=%d buttons=%d texts=%d profiling=%v replay=%v profile=%v\n",
				elapsed, visited, len(queue), buttonsFound, textsFound, hasProfiling, hasReplay, hasProfile)
			lastProgress = now
		}

		role := axString(el, "AXRole")

		// Check for status text indicators
		if role == "AXStaticText" || role == "AXTextField" {
			textsFound++
			value := axString(el, "AXValue")
			if value == "" {
				value = axString(el, "AXTitle")
			}
			if strings.Contains(value, "Profiling GPU Trace") {
				hasProfiling = true
				if debug {
					fmt.Fprintf(os.Stderr, "[DEBUG] Found profiling indicator: %q\n", value)
				}
			}
			if strings.Contains(value, "Performance data not available") {
				hasPerfNotAvailable = true
				if debug {
					fmt.Fprintln(os.Stderr, "[DEBUG] Found 'Performance data not available' text")
				}
			}
		}

		// Check for Replay and Profile buttons
		if role == "AXButton" {
			buttonsFound++
			name := axString(el, "AXTitle")
			if name == "" {
				name = axString(el, "AXDescription")
			}
			switch name {
			case "Replay":
				hasReplay = true
				replayEnabled = IsElementEnabled(el)
				if debug {
					fmt.Fprintf(os.Stderr, "[DEBUG] Found Replay button (enabled=%v)\n", replayEnabled)
				}
			case "Profile":
				hasProfile = true
				profileEnabled = IsElementEnabled(el)
				if debug {
					fmt.Fprintf(os.Stderr, "[DEBUG] Found Profile button (enabled=%v)\n", profileEnabled)
				}
			}
		}

		children := axChildren(el)
		queue = append(queue, children...)
	}

	// Final progress summary
	if debug {
		elapsed := time.Since(startTime).Seconds()
		fmt.Fprintf(os.Stderr, "[check-status] done: %.1fs visited=%d buttons=%d texts=%d profiling=%v replay=%v profile=%v\n",
			elapsed, visited, buttonsFound, textsFound, hasProfiling, hasReplay, hasProfile)
	}

	if debug {
		fmt.Fprintf(os.Stderr, "[DEBUG] BFS: visited=%d, hasProfiling=%v, hasReplay=%v, hasProfile=%v, hasPerfNotAvailable=%v\n", visited, hasProfiling, hasReplay, hasProfile, hasPerfNotAvailable)
	}

	// Check if "Profiling GPU Trace..." text is visible
	if hasProfiling {
		if debug {
			fmt.Fprintln(os.Stderr, "[DEBUG] Profiling indicator found, returning 'running'")
		}
		return "running"
	}

	// Now do targeted traversal for "Show Performance"
	if hasShowPerformanceDebug(window, debug) {
		return "complete"
	}
	// Also check for "Timeline" or "Encoders" which indicate the trace is loaded and interactive
	if findButtonByNameInsensitive(window, "Timeline") != 0 || findButtonByNameInsensitive(window, "Encoders") != 0 {
		return "complete"
	}

	// "Performance data not available" means trace loaded but not profiled
	if hasPerfNotAvailable {
		if debug {
			fmt.Fprintln(os.Stderr, "[DEBUG] Performance data not available, returning 'replay-ready'")
		}
		return "replay-ready"
	}

	// Check Profile button state (preferred over Replay)
	if hasProfile {
		if profileEnabled {
			return "replay-ready"
		}
		return "initializing"
	}

	// Check Replay button state
	if hasReplay {
		if replayEnabled {
			return "replay-ready"
		}
		return "initializing"
	}

	return "unknown"
}

// getProfilingStatus returns "running", "replay-ready", "complete", or "unknown" based on button state.
func getProfilingStatus(window uintptr) string {
	return getProfilingStatusWithDebug(window, false)
}

// hasShowPerformance does targeted traversal to find the "Show Performance" button.
// Path: window > split > editor area > split > Summary > ... > Show Performance
func hasShowPerformance(window uintptr) bool {
	return hasShowPerformanceDebug(window, false)
}

func hasShowPerformanceDebug(window uintptr, debug bool) bool {
	// Find "editor area" group by title (BFS with visit limit)
	editorArea := findGroupByTitleDebug(window, "editor area", 100, debug)
	if editorArea == 0 {
		if debug {
			fmt.Fprintln(os.Stderr, "[DEBUG] editor area not found (visited 100)")
		}
		return false
	}
	if debug {
		fmt.Fprintln(os.Stderr, "[DEBUG] Found editor area")
	}

	// Find "Summary" group within editor area
	summary := findGroupByTitle(editorArea, "Summary", 500)
	if summary == 0 {
		if debug {
			fmt.Fprintln(os.Stderr, "[DEBUG] Summary not found in editor area (visited 500)")
		}
		return false
	}
	if debug {
		fmt.Fprintln(os.Stderr, "[DEBUG] Found Summary")
	}

	// Look for Show Performance within Summary subtree
	btn := findButtonBFS(summary, "Show Performance", 500)
	if debug {
		fmt.Fprintf(os.Stderr, "[DEBUG] Show Performance button: %v\n", btn != 0)
	}
	return btn != 0
}

// findGroupByTitle finds a group element by its AXTitle using BFS with visit limit.
func findGroupByTitle(root uintptr, title string, maxVisit int) uintptr {
	return findGroupByTitleDebug(root, title, maxVisit, false)
}

func findGroupByTitleDebug(root uintptr, title string, maxVisit int, debug bool) uintptr {
	queue := []uintptr{root}
	visited := 0
	seen := make(map[uintptr]bool)

	for len(queue) > 0 && visited < maxVisit {
		el := queue[0]
		queue = queue[1:]

		if seen[el] {
			continue
		}
		seen[el] = true
		visited++

		role := axString(el, "AXRole")
		if role == "AXGroup" || role == "AXSplitGroup" {
			t := axString(el, "AXTitle")
			if t == "" {
				t = axString(el, "AXDescription")
			}
			if t == title {
				return el
			}
		}

		children := axChildren(el)
		queue = append(queue, children...)
	}
	return 0
}

// findCheckboxByName finds a checkbox by name using BFS.
func findCheckboxByName(root uintptr, name string) uintptr {
	queue := []uintptr{root}
	visited := 0
	seen := make(map[uintptr]bool)
	maxVisit := 500

	for len(queue) > 0 && visited < maxVisit {
		el := queue[0]
		queue = queue[1:]

		if seen[el] {
			continue
		}
		seen[el] = true
		visited++

		role := axString(el, "AXRole")
		if role == "AXCheckBox" {
			title := axString(el, "AXTitle")
			if title == "" {
				title = axString(el, "AXDescription")
			}
			if title == name {
				return el
			}
		}

		children := axChildren(el)
		queue = append(queue, children...)
	}
	return 0
}

// findExportButton finds the Export button in the Summary panel.
// Path: window > editor area > Summary > Export
func findExportButton(window uintptr) uintptr {
	editorArea := findGroupByTitle(window, "editor area", 100)
	if editorArea == 0 {
		return 0
	}
	// Export is a direct child of the Summary area header, search broader
	return findButtonBFS(editorArea, "Export", 1000)
}

// findButtonBFS finds a button by name using BFS with visit limit.
func findButtonBFS(root uintptr, name string, maxVisit int) uintptr {
	queue := []uintptr{root}
	visited := 0
	seen := make(map[uintptr]bool)

	for len(queue) > 0 && visited < maxVisit {
		el := queue[0]
		queue = queue[1:]

		if seen[el] {
			continue
		}
		seen[el] = true
		visited++

		role := axString(el, "AXRole")
		if role == "AXButton" {
			title := axString(el, "AXTitle")
			if title == "" {
				title = axString(el, "AXDescription")
			}
			if title == name {
				return el
			}
		}

		children := axChildren(el)
		queue = append(queue, children...)
	}
	return 0
}
