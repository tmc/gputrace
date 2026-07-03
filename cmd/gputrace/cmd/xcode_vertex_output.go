//go:build darwin

package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

type vertexOutputResult struct {
	Status        string   `json:"status"`
	Trace         string   `json:"trace"`
	DrawCall      int      `json:"draw_call"`
	Mode          string   `json:"mode,omitempty"`
	Data          string   `json:"data,omitempty"`
	NavigatorRows []string `json:"navigator_rows,omitempty"`
	Message       string   `json:"message,omitempty"`
	Suggestion    string   `json:"suggestion,omitempty"`
}

type drawCallNotFoundError struct {
	DrawCall    int
	Rows        []string
	ComputeOnly bool
}

func (e *drawCallNotFoundError) Error() string {
	if e.ComputeOnly {
		return fmt.Sprintf("draw call #%d not found: trace shows compute shader work, not vertex draw calls\n%s", e.DrawCall, formatNavigatorRows(e.Rows))
	}
	return fmt.Sprintf("draw call #%d not found in outline (%d rows)", e.DrawCall, len(e.Rows))
}

func init() {
	collectXcodeProfileCmd.AddCommand(newVertexOutputCommand(&vertexOutputOptions{
		drawCall: 21,
		format:   "text",
	}))
}

type vertexOutputOptions struct {
	drawCall int
	output   string
	format   string
}

func newVertexOutputCommand(opts *vertexOutputOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "vertex-output <trace.gputrace>",
		Short: "Extract vertex shader output from Xcode GPU debugger",
		Long: `Opens a .gputrace in Xcode, navigates to a specific draw call,
and extracts the vertex shader output table via Accessibility APIs.

This automates what you'd normally do manually:
  1. Open trace in Xcode
  2. Switch to Debug navigator
  3. Expand the draw call tree
  4. Click the target draw call
  5. Read the vertex output table from the editor area`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runVertexOutput(cmd, args, opts)
		},
	}
	cmd.Flags().IntVar(&opts.drawCall, "draw", opts.drawCall, "Draw call number to inspect")
	cmd.Flags().StringVarP(&opts.output, "output", "o", opts.output, "Output file path (default: stdout)")
	cmd.Flags().StringVar(&opts.format, "format", opts.format, "Output format: text, json")
	return cmd
}

func runVertexOutput(cmd *cobra.Command, args []string, opts *vertexOutputOptions) error {
	jsonOutput, err := vertexOutputJSON(collectProfileOpts.json, opts.format)
	if err != nil {
		return err
	}
	if err := validateVertexOutputDraw(opts.drawCall); err != nil {
		return err
	}
	statusOut := vertexOutputStatusWriter(jsonOutput, opts.output)

	inputPath, err := filepath.Abs(args[0])
	if err != nil {
		return fmt.Errorf("invalid input path: %w", err)
	}

	if err := setupMacgo(); err != nil {
		return err
	}

	fmt.Fprintf(statusOut, "Extracting vertex output for draw call #%d...\n", opts.drawCall)

	// Step 1: Open trace in Xcode
	fmt.Fprintln(statusOut, "  Step 1: Opening trace in Xcode...")
	openCmd := exec.Command("open", append(xcodeOpenArgs(), inputPath)...)
	if output, err := openCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to open trace: %w\n    output: %s", err, string(output))
	}
	time.Sleep(3 * time.Second)
	ActivateXcode()
	time.Sleep(2 * time.Second)

	appAX, err := FindXcodeApp()
	if err != nil {
		return fmt.Errorf("Xcode not found: %w", err)
	}
	defer cfRelease(appAX)

	traceFileName := filepath.Base(inputPath)
	windowAX, err := waitForWindow(appAX, traceFileName, 30*time.Second)
	if err != nil {
		return fmt.Errorf("window not found: %w", err)
	}

	// Ensure window is on screen
	x, y := axPosition(windowAX)
	if x < 0 || y < 0 || y > 5000 {
		setWindowPosition(windowAX, 100, 100)
		time.Sleep(500 * time.Millisecond)
	}

	// Step 2: Check if replay is needed and trigger it
	fmt.Fprintln(statusOut, "  Step 2: Checking replay status...")
	stopBtn := FindStopButton(windowAX)
	hasPerfData := hasShowPerformance(windowAX)
	if stopBtn != 0 && IsElementEnabled(stopBtn) && !hasPerfData {
		// Already replaying, wait for completion
		fmt.Fprintln(statusOut, "    Replay in progress, waiting...")
		if err := waitForReplayComplete(appAX, traceFileName, windowAX, 120*time.Second); err != nil {
			return fmt.Errorf("replay wait failed: %w", err)
		}
		time.Sleep(2 * time.Second)
	} else if !hasPerfData {
		// Need to start replay
		fmt.Fprintln(statusOut, "    Starting replay...")
		if err := clickReplayButton(windowAX); err != nil {
			return fmt.Errorf("failed to start replay: %w", err)
		}
		fmt.Fprintln(statusOut, "    Waiting for replay to complete...")
		time.Sleep(3 * time.Second)
		if err := waitForReplayComplete(appAX, traceFileName, windowAX, 120*time.Second); err != nil {
			return fmt.Errorf("replay wait failed: %w", err)
		}
		time.Sleep(2 * time.Second)
	} else {
		fmt.Fprintln(statusOut, "    Trace already replayed")
	}

	// Step 3: Switch to Debug navigator
	fmt.Fprintln(statusOut, "  Step 3: Switching to Debug navigator...")
	if err := switchToDebugNavigator(windowAX); err != nil {
		return fmt.Errorf("failed to switch navigator: %w", err)
	}
	time.Sleep(500 * time.Millisecond)

	// Step 4: Expand tree and find draw call
	fmt.Fprintf(statusOut, "  Step 4: Finding draw call #%d...\n", opts.drawCall)
	if err := navigateToDrawCall(windowAX, opts.drawCall); err != nil {
		if jsonOutput {
			var notFound *drawCallNotFoundError
			if errors.As(err, &notFound) {
				mode := "render"
				suggestion := "Use a render .gputrace with draw calls to extract vertex output."
				if notFound.ComputeOnly {
					mode = "compute"
					suggestion = "This trace contains compute shader work; use shader, profiler, timing, or API-call commands for compute traces."
				}
				return writeVertexOutput(vertexOutputResult{
					Status:        "unsupported",
					Trace:         inputPath,
					DrawCall:      opts.drawCall,
					Mode:          mode,
					NavigatorRows: notFound.Rows,
					Message:       notFound.Error(),
					Suggestion:    suggestion,
				}, "", opts)
			}
		}
		return fmt.Errorf("failed to navigate to draw call: %w", err)
	}
	time.Sleep(1 * time.Second)

	// Step 5: Read vertex output from editor area
	fmt.Fprintln(statusOut, "  Step 5: Reading vertex output...")
	data, err := readVertexOutput(windowAX)
	if err != nil {
		return fmt.Errorf("failed to read vertex output: %w", err)
	}

	if jsonOutput {
		return writeVertexOutput(vertexOutputResult{
			Status:   "ok",
			Trace:    inputPath,
			DrawCall: opts.drawCall,
			Mode:     "render",
			Data:     data,
		}, "", opts)
	}

	if err := writeVertexOutput(data, data, opts); err != nil {
		return err
	}
	if opts.output != "" {
		fmt.Fprintf(statusOut, "  Wrote vertex output to %s\n", opts.output)
	}
	return nil
}

func validateVertexOutputDraw(draw int) error {
	if draw <= 0 {
		return errors.New("--draw must be > 0")
	}
	return nil
}

func writeVertexOutput(v any, text string, opts *vertexOutputOptions) error {
	jsonOutput, err := vertexOutputJSON(collectProfileOpts.json, opts.format)
	if err != nil {
		return err
	}
	writer, closeOutput, err := createCommandOutput(opts.output)
	if err != nil {
		return fmt.Errorf("create vertex output: %w", err)
	}
	if closeOutput != nil {
		defer closeOutput()
	}
	if jsonOutput {
		enc := json.NewEncoder(writer)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	if opts.output == "" {
		_, err = fmt.Fprintln(writer, text)
	} else {
		_, err = io.WriteString(writer, text)
	}
	if err != nil {
		return fmt.Errorf("write vertex output: %w", err)
	}
	return nil
}

func vertexOutputStatusWriter(jsonOutput bool, outputPath string) io.Writer {
	if jsonOutput || commandOutputPathIsStdout(outputPath) {
		return os.Stderr
	}
	return os.Stdout
}

func vertexOutputJSON(globalJSON bool, format string) (bool, error) {
	switch format {
	case "text":
		return globalJSON, nil
	case "json":
		return true, nil
	default:
		return false, fmt.Errorf("unknown vertex output format %q (use text or json)", format)
	}
}

func switchToDebugNavigator(windowAX uintptr) error {
	script := `
tell application "System Events"
	tell process "Xcode"
		set frontmost to true
		delay 0.3
		set w to first UI element whose role is "AXWindow"
		set allContents to entire contents of w
		repeat with elem in allContents
			try
				if role of elem is "AXRadioButton" and description of elem is "Debug" then
					click elem
					delay 0.5
					return "ok"
				end if
			end try
		end repeat
		return "not found"
	end tell
end tell`
	out, err := exec.Command("osascript", "-e", script).CombinedOutput()
	result := strings.TrimSpace(string(out))
	verboseLog("switchToDebugNavigator: result=%q err=%v", result, err)
	if result == "ok" {
		return nil
	}
	return fmt.Errorf("could not switch to Debug navigator: %s", result)
}

func navigateToDrawCall(windowAX uintptr, drawCallNum int) error {
	navGroup := findElement(windowAX, func(el uintptr) bool {
		return axString(el, "AXRole") == "AXGroup" && axString(el, "AXDescription") == "navigator"
	})
	if navGroup == 0 {
		return fmt.Errorf("navigator group not found")
	}

	// Find the outline - try by description first, then any outline
	var outline uintptr
	var allOutlines []uintptr
	findAllElements(navGroup, func(el uintptr) bool {
		return axString(el, "AXRole") == "AXOutline"
	}, &allOutlines, 500)

	verboseLog("navigateToDrawCall: found %d outlines in navigator", len(allOutlines))
	for i, ol := range allOutlines {
		desc := axString(ol, "AXDescription")
		rows := axRows(ol)
		children := axChildren(ol)
		verboseLog("navigateToDrawCall: outline[%d] desc=%q rows=%d children=%d", i, desc, len(rows), len(children))
		if desc == "Debug Navigator" || len(rows) > 1 {
			outline = ol
		}
	}
	if outline == 0 && len(allOutlines) > 0 {
		outline = allOutlines[0]
	}
	if outline == 0 {
		return fmt.Errorf("outline not found in navigator")
	}

	// Wait for outline to populate (may take time after navigator switch)
	var rows []uintptr
	for attempt := 0; attempt < 10; attempt++ {
		rows = axRows(outline)
		if len(rows) > 0 {
			break
		}
		// Also try AXChildren and filter for AXRow
		children := axChildren(outline)
		for _, c := range children {
			if axString(c, "AXRole") == "AXRow" {
				rows = append(rows, c)
			}
		}
		if len(rows) > 0 {
			break
		}
		verboseLog("navigateToDrawCall: outline empty, waiting... (attempt %d)", attempt)
		time.Sleep(500 * time.Millisecond)
	}

	verboseLog("navigateToDrawCall: outline has %d rows", len(rows))
	for i, row := range rows {
		rowText := extractRowText(row)
		if rowText != "" && i < 25 {
			verboseLog("navigateToDrawCall: row[%d] text=%q", i, rowText)
		}
	}

	// Expand tree nodes to reveal draw calls
	expandLabels := []string{"SFSymbolDemo", "RBLayer", "root-layer"}
	for _, label := range expandLabels {
		if err := expandOutlineRowByText(outline, label); err != nil {
			verboseLog("navigateToDrawCall: expand %q: %v", label, err)
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Find the target draw call
	target := fmt.Sprintf("%d [drawIndexedPrimitives", drawCallNum)
	altTarget := fmt.Sprintf("%d ", drawCallNum)

	// Re-read rows
	rows = axRows(outline)
	if len(rows) == 0 {
		children := axChildren(outline)
		for _, c := range children {
			if axString(c, "AXRole") == "AXRow" {
				rows = append(rows, c)
			}
		}
	}

	verboseLog("navigateToDrawCall: searching %d rows for draw call #%d", len(rows), drawCallNum)
	var rowTexts []string
	hasDrawCalls := false
	hasCompute := false
	for i, row := range rows {
		text := extractRowText(row)
		if text != "" {
			rowTexts = append(rowTexts, text)
		}
		if strings.Contains(text, "drawIndexedPrimitives") {
			hasDrawCalls = true
			verboseLog("navigateToDrawCall: row[%d] has drawIndexedPrimitives: %q", i, text)
			if strings.Contains(text, target) || (strings.Contains(text, altTarget) && strings.Contains(text, "drawIndexedPrimitives:Line indexCount:64")) {
				verboseLog("navigateToDrawCall: MATCH at row[%d]", i)
				return axPressWithFallback(row)
			}
		}
		if isComputeNavigatorRow(text) {
			hasCompute = true
		}
	}

	if !hasDrawCalls && hasCompute {
		return &drawCallNotFoundError{DrawCall: drawCallNum, Rows: rowTexts, ComputeOnly: true}
	}
	return &drawCallNotFoundError{DrawCall: drawCallNum, Rows: rowTexts}
}

func isComputeNavigatorRow(text string) bool {
	return strings.Contains(text, "newComputePipelineStateWithFunction:") ||
		strings.Contains(text, "newFunctionWithName:") ||
		strings.Contains(text, "Performance ") ||
		strings.Contains(text, "SingleEncoder")
}

func formatNavigatorRows(rows []string) string {
	if len(rows) == 0 {
		return "debug navigator had no readable rows"
	}
	var b strings.Builder
	b.WriteString("debug navigator rows:\n")
	for i, row := range rows {
		if i >= 20 {
			fmt.Fprintf(&b, "  ... %d more rows\n", len(rows)-i)
			break
		}
		fmt.Fprintf(&b, "  %s\n", row)
	}
	return strings.TrimRight(b.String(), "\n")
}

func expandOutlineRowByText(outline uintptr, label string) error {
	rows := axRows(outline)
	if len(rows) == 0 {
		children := axChildren(outline)
		for _, c := range children {
			if axString(c, "AXRole") == "AXRow" {
				rows = append(rows, c)
			}
		}
	}
	for _, row := range rows {
		text := extractRowText(row)
		if !strings.Contains(text, label) {
			continue
		}
		// Find disclosure triangle in this row
		var triangle uintptr
		var findTriangle func(uintptr, int)
		findTriangle = func(el uintptr, depth int) {
			if depth > 4 || triangle != 0 {
				return
			}
			if axString(el, "AXRole") == "AXDisclosureTriangle" {
				triangle = el
				return
			}
			for _, c := range axChildren(el) {
				findTriangle(c, depth+1)
			}
		}
		findTriangle(row, 0)
		if triangle != 0 {
			val := axString(triangle, "AXValue")
			if val == "0" {
				verboseLog("expandOutlineRowByText: expanding %q", label)
				return axPressWithFallback(triangle)
			}
			return nil // already expanded
		}
	}
	return fmt.Errorf("row with label %q not found", label)
}

func extractRowText(el uintptr) string {
	var texts []string
	var extract func(uintptr, int)
	extract = func(e uintptr, depth int) {
		if depth > 4 {
			return
		}
		role := axString(e, "AXRole")
		if role == "AXStaticText" {
			val := axString(e, "AXValue")
			if val != "" && val != "missing value" {
				texts = append(texts, val)
			}
		}
		children := axChildren(e)
		for _, c := range children {
			extract(c, depth+1)
		}
	}
	extract(el, 0)
	return strings.Join(texts, " ")
}

// axRows returns the AXRows attribute of an outline/table element.
func axRows(el uintptr) []uintptr {
	var ptr uintptr
	key := mkString("AXRows")
	defer cfRelease(key)
	if axCopyAttributeValue(el, key, &ptr) != kAXErrorSuccess {
		return nil
	}
	defer cfRelease(ptr)
	count := cfArrayGetCount(ptr)
	res := make([]uintptr, count)
	for i := 0; i < count; i++ {
		val := cfArrayGetValueAtIndex(ptr, i)
		res[i] = cfRetain(val)
	}
	return res
}

func expandOutlineRow(outline uintptr, label string) error {
	children := axRows(outline)
	for _, row := range children {
		cell := findFirstChild(row, "AXCell")
		if cell == 0 {
			continue
		}
		cellChildren := axChildren(cell)
		hasLabel := false
		var triangle uintptr
		for _, ck := range cellChildren {
			role := axString(ck, "AXRole")
			if role == "AXStaticText" {
				val := axString(ck, "AXValue")
				if val == label {
					hasLabel = true
				}
			}
			if role == "AXDisclosureTriangle" {
				triangle = ck
			}
		}
		if hasLabel && triangle != 0 {
			val := axString(triangle, "AXValue")
			if val == "0" {
				return axPressWithFallback(triangle)
			}
			return nil // already expanded
		}
	}
	return fmt.Errorf("row with label %q not found", label)
}

func findOutlineRowContaining(outline uintptr, prefix, substring string) uintptr {
	children := axRows(outline)
	for _, row := range children {
		if axString(row, "AXRole") != "AXRow" {
			continue
		}
		cell := findFirstChild(row, "AXCell")
		if cell == 0 {
			continue
		}
		cellChildren := axChildren(cell)
		for _, ck := range cellChildren {
			if axString(ck, "AXRole") == "AXStaticText" {
				val := axString(ck, "AXValue")
				if strings.HasPrefix(val, prefix) && strings.Contains(val, substring) {
					return row
				}
			}
		}
	}
	return 0
}

func findFirstChild(el uintptr, role string) uintptr {
	children := axChildren(el)
	for _, c := range children {
		if axString(c, "AXRole") == role {
			return c
		}
	}
	return 0
}

func readVertexOutput(windowAX uintptr) (string, error) {
	// After selecting a draw call, the editor area shows bound resources.
	// We need to find tables/outlines in the editor area that contain vertex data.
	editorArea := findElement(windowAX, func(el uintptr) bool {
		return axString(el, "AXRole") == "AXGroup" && axString(el, "AXDescription") == "editor area"
	})
	if editorArea == 0 {
		return "", fmt.Errorf("editor area not found")
	}

	// Find all tables in the editor area (vertex output is typically in a table)
	var tables []uintptr
	findAllElements(editorArea, func(el uintptr) bool {
		role := axString(el, "AXRole")
		return role == "AXTable" || role == "AXOutline"
	}, &tables, 3000)

	if len(tables) == 0 {
		// Try looking for scroll areas with content
		var scrollAreas []uintptr
		findAllElements(editorArea, func(el uintptr) bool {
			return axString(el, "AXRole") == "AXScrollArea"
		}, &scrollAreas, 2000)

		verboseLog("readVertexOutput: found %d scroll areas in editor", len(scrollAreas))
		for i, sa := range scrollAreas {
			children := axChildren(sa)
			for _, c := range children {
				role := axString(c, "AXRole")
				desc := axString(c, "AXDescription")
				verboseLog("readVertexOutput: scrollArea[%d] child: %s desc=%s", i, role, desc)
				if role == "AXTable" || role == "AXOutline" {
					tables = append(tables, c)
				}
			}
		}
	}

	verboseLog("readVertexOutput: found %d tables/outlines", len(tables))

	// Read data from each table
	var result strings.Builder
	for i, table := range tables {
		desc := axString(table, "AXDescription")
		role := axString(table, "AXRole")
		rows := axChildren(table)
		verboseLog("readVertexOutput: table[%d] role=%s desc=%s rows=%d", i, role, desc, len(rows))

		if len(rows) == 0 {
			continue
		}

		// Read column headers if available
		var columns []uintptr
		findAllElements(table, func(el uintptr) bool {
			return axString(el, "AXRole") == "AXColumn"
		}, &columns, 500)

		if len(columns) > 0 {
			for _, col := range columns {
				title := axString(col, "AXTitle")
				result.WriteString(title + "\t")
			}
			result.WriteString("\n")
		}

		// Read row data
		rowCount := 0
		for _, row := range rows {
			if axString(row, "AXRole") != "AXRow" {
				continue
			}
			cells := axChildren(row)
			for _, cell := range cells {
				val := axString(cell, "AXValue")
				if val == "" || val == "missing value" {
					// Try children
					cellKids := axChildren(cell)
					for _, ck := range cellKids {
						v := axString(ck, "AXValue")
						if v != "" && v != "missing value" {
							result.WriteString(v + "\t")
						}
					}
				} else {
					result.WriteString(val + "\t")
				}
			}
			result.WriteString("\n")
			rowCount++
		}
		verboseLog("readVertexOutput: read %d rows from table[%d]", rowCount, i)
	}

	if result.Len() == 0 {
		// Dump editor area structure for debugging
		var debugInfo strings.Builder
		debugInfo.WriteString("No vertex output table found. Editor area structure:\n")
		dumpElementTree(editorArea, &debugInfo, 0, 4)
		return debugInfo.String(), nil
	}

	return result.String(), nil
}

func findAllElements(root uintptr, match func(uintptr) bool, results *[]uintptr, maxVisit int) {
	queue := []uintptr{root}
	visited := 0
	for len(queue) > 0 && visited < maxVisit {
		el := queue[0]
		queue = queue[1:]
		visited++
		if match(el) {
			*results = append(*results, el)
		}
		children := axChildren(el)
		queue = append(queue, children...)
	}
}

func dumpElementTree(el uintptr, buf *strings.Builder, depth, maxDepth int) {
	if depth >= maxDepth {
		return
	}
	indent := strings.Repeat("  ", depth)
	role := axString(el, "AXRole")
	desc := axString(el, "AXDescription")
	title := axString(el, "AXTitle")
	val := axString(el, "AXValue")

	info := role
	if desc != "" && desc != "missing value" {
		info += " desc=" + desc
	}
	if title != "" && title != "missing value" {
		info += " title=" + title
	}
	if val != "" && val != "missing value" && len(val) < 80 {
		info += " val=" + val
	}
	children := axChildren(el)
	info += fmt.Sprintf(" ch=%d", len(children))
	buf.WriteString(indent + info + "\n")

	for _, c := range children {
		dumpElementTree(c, buf, depth+1, maxDepth)
	}
}
