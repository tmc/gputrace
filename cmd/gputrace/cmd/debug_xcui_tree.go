//go:build darwin

package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var debugTreeVerbose bool

func init() {
	debugTreeCmd := &cobra.Command{
		Use:    "debug-tree [trace_file]",
		Short:  "Print UI tree to find key elements",
		Hidden: true,
		Long:   `Prints the Accessibility tree structure showing paths to key buttons like Replay, Stop, Show Performance.`,
		Args:   cobra.MaximumNArgs(1),
		RunE:   runDebugTree,
	}
	debugTreeCmd.Flags().BoolVarP(&debugTreeVerbose, "verbose", "v", false, "Print verbose progress info")
	collectXcodeProfileCmd.AddCommand(debugTreeCmd)
}

func runDebugTree(cmd *cobra.Command, args []string) error {
	traceFile := ""
	if len(args) > 0 {
		traceFile = args[0]
	}

	if err := setupMacgo(); err != nil {
		return err
	}

	appAX, err := FindXcodeApp()
	if err != nil {
		return fmt.Errorf("Xcode not running: %w", err)
	}
	defer cfRelease(appAX)

	// Find target window (prefer trace windows)
	windowAX, err := findTargetWindow(appAX, traceFile)
	if err != nil {
		return err
	}

	fmt.Println("=== Finding key elements with path info ===")
	fmt.Println()

	// Track key buttons we want to find (state-dependent buttons, not always-visible ones)
	keyButtons := []string{"Replay", "Open Performance", "Show Performance", "Show Dependencies", "Show Memory", "Export"}

	// BFS with path tracking
	type queueItem struct {
		el    uintptr
		path  []string
		depth int
	}

	queue := []queueItem{{el: windowAX, path: []string{"Window"}, depth: 0}}
	visited := 0
	maxVisit := 3000
	foundCount := 0
	cycleCount := 0
	seen := make(map[uintptr]bool) // Cycle detection

	// Track groups/containers to understand structure
	fmt.Println("--- UI Structure (groups/toolbars at depth 1-3) ---")
	for len(queue) > 0 && visited < maxVisit {
		item := queue[0]
		queue = queue[1:]

		// Cycle detection
		if seen[item.el] {
			cycleCount++
			if debugTreeVerbose {
				fmt.Printf("[CYCLE] Skipping already-seen element at depth %d\n", item.depth)
			}
			continue
		}
		seen[item.el] = true

		role := axString(item.el, "AXRole")
		title := axString(item.el, "AXTitle")
		if title == "" {
			title = axString(item.el, "AXDescription")
		}
		identifier := axString(item.el, "AXIdentifier")

		// Skip RuntimeIssue buttons entirely
		if role == "AXButton" && title == "RuntimeIssue" {
			continue
		}

		visited++

		// Verbose progress
		if debugTreeVerbose && visited%100 == 0 {
			fmt.Printf("[PROGRESS] Visited %d, Queue: %d, Depth: %d, Cycles: %d\n", visited, len(queue), item.depth, cycleCount)
		}

		// Print structure elements at shallow depths
		if item.depth <= 3 && (role == "AXGroup" || role == "AXToolbar" || role == "AXSplitGroup" || role == "AXScrollArea") {
			indent := strings.Repeat("  ", item.depth)
			label := title
			if label == "" && identifier != "" {
				label = fmt.Sprintf("[id=%s]", identifier)
			}
			if label == "" {
				label = "(unnamed)"
			}
			fmt.Printf("%s%s: %s\n", indent, role, label)
		}

		// Check for key buttons
		if role == "AXButton" {
			for _, key := range keyButtons {
				if title == key {
					fmt.Printf("\n*** FOUND: %s ***\n", key)
					fmt.Printf("    Path: %s\n", strings.Join(item.path, " > "))
					fmt.Printf("    Depth: %d, Visited: %d\n", item.depth, visited)
					fmt.Printf("    Enabled: %v\n", IsElementEnabled(item.el))
					foundCount++
				}
			}
		}

		// Add children
		children := axChildren(item.el)
		for _, child := range children {
			// Skip if already seen (preemptive cycle check)
			if seen[child] {
				continue
			}
			childRole := axString(child, "AXRole")
			childTitle := axString(child, "AXTitle")
			if childTitle == "" {
				childTitle = axString(child, "AXDescription")
			}
			childLabel := childRole
			if childTitle != "" {
				childLabel = fmt.Sprintf("%s(%s)", childRole, childTitle)
			}
			newPath := make([]string, len(item.path)+1)
			copy(newPath, item.path)
			newPath[len(item.path)] = childLabel
			queue = append(queue, queueItem{el: child, path: newPath, depth: item.depth + 1})
		}
	}

	fmt.Printf("\n--- Summary ---\n")
	fmt.Printf("Visited: %d elements\n", visited)
	fmt.Printf("Cycles detected: %d\n", cycleCount)
	fmt.Printf("Queue remaining: %d\n", len(queue))
	fmt.Printf("Found %d key buttons\n", foundCount)

	return nil
}
