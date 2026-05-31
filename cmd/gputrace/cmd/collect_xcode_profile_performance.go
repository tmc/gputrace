package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// PerformanceInfo represents performance data extracted from Xcode.
type PerformanceInfo struct {
	Available bool   `json:"available"`
	Status    string `json:"status"` // "ready", "not_available", "already_shown"
}

// PerformanceSummary represents metrics read from Xcode's visible Performance
// view through Accessibility.
type PerformanceSummary struct {
	Available bool                `json:"available"`
	Status    string              `json:"status"`
	Source    string              `json:"source,omitempty"`
	View      string              `json:"view,omitempty"`
	TextCount int                 `json:"text_count"`
	Metrics   []PerformanceMetric `json:"metrics,omitempty"`
}

// PerformanceMetric is a numeric label/value pair exposed by Xcode's
// Performance view.
type PerformanceMetric struct {
	Name         string  `json:"name"`
	Value        float64 `json:"value"`
	Unit         string  `json:"unit,omitempty"`
	DisplayValue string  `json:"display_value"`
}

type performanceSummaryError struct {
	Code       string
	Message    string
	Suggestion string
}

func (e performanceSummaryError) Error() string {
	return e.Message
}

// PerformanceMemoryInfo represents memory metrics extracted from Xcode's
// Performance/Memory accessibility tree.
type PerformanceMemoryInfo struct {
	Available bool                      `json:"available"`
	Status    string                    `json:"status"`
	Source    string                    `json:"source,omitempty"`
	Metrics   []PerformanceMemoryMetric `json:"metrics,omitempty"`
	Message   string                    `json:"message,omitempty"`
}

// PerformanceMemoryMetric is a byte-sized memory value found in Xcode UI text.
type PerformanceMemoryMetric struct {
	Name     string `json:"name"`
	Label    string `json:"label"`
	Bytes    uint64 `json:"bytes"`
	RawValue string `json:"raw_value,omitempty"`
}

var (
	performanceMemorySizeRE   = regexp.MustCompile(`(?i)([0-9]+(?:\.[0-9]+)?)\s*(bytes?|b|kib|kb|mib|mb|gib|gb|tib|tb)\b`)
	metricNameCleanRE         = regexp.MustCompile(`[^a-z0-9]+`)
	performanceInlineMetricRE = regexp.MustCompile(`^\s*([^:=]{2,96}?)\s*[:=]\s*(.+?)\s*$`)
	performanceMetricValueRE  = regexp.MustCompile(`^\s*([+-]?(?:(?:[0-9]{1,3}(?:,[0-9]{3})+)|[0-9]+)(?:\.[0-9]+)?|\.[0-9]+)\s*([A-Za-z%][A-Za-z0-9%./-]*)?\s*$`)
)

var performanceCmd = &cobra.Command{
	Use:   "performance",
	Short: "Performance data commands",
	Long: `Commands for working with GPU performance data in Xcode.

Subcommands:
  show      Click the "Show Performance" button to reveal performance data
  status    Check if performance data is available
  summary   Extract visible summary statistics
  counters  Select the Counters tab
  memory    Extract memory usage info when visible

Example:
  gputrace xp performance show
  gputrace xp performance status --json
`,
}

func init() {
	collectXcodeProfileCmd.AddCommand(performanceCmd)

	// performance show
	showCmd := &cobra.Command{
		Use:   "show",
		Short: "Click the Show Performance button",
		Long:  `Clicks the "Show Performance" button in Xcode to reveal GPU performance data.`,
		RunE:  runPerformanceShow,
	}
	performanceCmd.AddCommand(showCmd)

	// performance status
	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Check if performance data is available",
		Long:  `Checks whether the "Show Performance" button is available and enabled.`,
		RunE:  runPerformanceStatus,
	}
	performanceCmd.AddCommand(statusCmd)

	// Performance view navigation commands
	viewCommands := []struct {
		name  string
		short string
	}{
		{"overview", "Select the Overview tab"},
		{"timeline", "Select the Timeline tab"},
		{"shaders", "Select the Shaders tab"},
		{"counters", "Select the Counters tab"},
		{"cost-graph", "Select the Cost Graph tab"},
		{"heat-map", "Select the Heat Map tab"},
		{"encoders", "Select the Encoders tab"},
		{"cost", "Select the Cost tab"},
	}

	for _, vc := range viewCommands {
		vcName := vc.name
		cmd := &cobra.Command{
			Use:   vcName,
			Short: vc.short,
			RunE: func(cmd *cobra.Command, args []string) error {
				return runPerformanceView(vcName)
			},
		}
		performanceCmd.AddCommand(cmd)
	}

	// performance summary
	summaryCmd := &cobra.Command{
		Use:    "summary",
		Short:  "Extract summary statistics",
		Long:   `Extracts visible label/value metrics from Xcode's Performance view.`,
		Hidden: true,
		RunE:   runPerformanceSummary,
	}
	performanceCmd.AddCommand(summaryCmd)

	// performance memory
	memoryCmd := &cobra.Command{
		Use:    "memory",
		Short:  "Extract memory usage info",
		Long:   `Extracts memory allocation and usage information from Xcode when visible.`,
		Hidden: true,
		RunE:   runPerformanceMemory,
	}
	performanceCmd.AddCommand(memoryCmd)
}

func runPerformanceShow(cmd *cobra.Command, args []string) error {
	status := xcodeProfileStatusWriter()

	if err := setupMacgo(); err != nil {
		return err
	}

	appAX, err := FindXcodeApp()
	if err != nil {
		if collectProfileJSON {
			return outputJSONError("XCODE_NOT_RUNNING", "Xcode not running", "Start Xcode first")
		}
		return fmt.Errorf("Xcode not running: %w", err)
	}
	defer cfRelease(appAX)

	windowAX, err := findTargetWindow(appAX, "")
	if err != nil {
		if collectProfileJSON {
			return outputJSONError("NO_WINDOWS", "no trace window found", "Open a trace file first")
		}
		return err
	}

	btn := findShowPerformanceButton(windowAX)
	if btn == 0 {
		if collectProfileJSON {
			return outputJSONError("NOT_AVAILABLE", "Show Performance button not found", "Replay may not be complete")
		}
		return fmt.Errorf("Show Performance button not found (replay may not be complete)")
	}

	if !IsElementEnabled(btn) {
		if collectProfileJSON {
			return outputJSONError("DISABLED", "Show Performance button is disabled", "Wait for replay to complete")
		}
		return fmt.Errorf("Show Performance button is disabled")
	}

	fmt.Fprintln(status, "Clicking Show Performance...")

	if err := axAction(btn, "AXPress"); err != nil {
		if collectProfileJSON {
			return outputJSONError("CLICK_FAILED", fmt.Sprintf("failed to click: %v", err), "Try again")
		}
		return fmt.Errorf("failed to click: %w", err)
	}

	if collectProfileJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]interface{}{
			"success": true,
			"action":  "show_performance",
		})
	}

	fmt.Fprintln(status, "Done")
	return nil
}

func runPerformanceStatus(cmd *cobra.Command, args []string) error {
	if err := setupMacgo(); err != nil {
		return err
	}

	appAX, err := FindXcodeApp()
	if err != nil {
		if collectProfileJSON {
			return outputJSONError("XCODE_NOT_RUNNING", "Xcode not running", "Start Xcode first")
		}
		return fmt.Errorf("Xcode not running: %w", err)
	}
	defer cfRelease(appAX)

	windowAX, err := findTargetWindow(appAX, "")
	if err != nil {
		if collectProfileJSON {
			return outputJSONError("NO_WINDOWS", "no trace window found", "Open a trace file first")
		}
		return err
	}

	btn := findShowPerformanceButton(windowAX)
	info := PerformanceInfo{}

	if btn == 0 {
		info.Available = false
		info.Status = "not_available"
	} else if !IsElementEnabled(btn) {
		info.Available = false
		info.Status = "disabled"
	} else {
		info.Available = true
		info.Status = "ready"
	}

	if collectProfileJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(info)
	}

	switch info.Status {
	case "ready":
		fmt.Println("Performance data available - use 'performance show' to view")
	case "disabled":
		fmt.Println("Performance button disabled - replay may be in progress")
	case "not_available":
		fmt.Println("Performance data not available - complete replay first")
	}
	return nil
}

func runPerformanceSummary(cmd *cobra.Command, args []string) error {
	if err := setupMacgo(); err != nil {
		return err
	}

	appAX, err := FindXcodeApp()
	if err != nil {
		if collectProfileJSON {
			return outputJSONError("XCODE_NOT_RUNNING", "Xcode not running", "Start Xcode first")
		}
		return fmt.Errorf("Xcode not running: %w", err)
	}
	defer cfRelease(appAX)

	windowAX, err := findTargetWindow(appAX, "")
	if err != nil {
		if collectProfileJSON {
			return outputJSONError("NO_WINDOWS", "no trace window found", "Open a trace file first")
		}
		return err
	}

	summary, err := extractPerformanceSummary(windowAX)
	if err != nil {
		if summaryErr, ok := err.(performanceSummaryError); ok {
			if collectProfileJSON {
				return outputJSONError(summaryErr.Code, summaryErr.Message, summaryErr.Suggestion)
			}
			if summaryErr.Suggestion != "" {
				return fmt.Errorf("%s\nHint: %s", summaryErr.Message, summaryErr.Suggestion)
			}
			return fmt.Errorf("%s", summaryErr.Message)
		}
		return err
	}

	if collectProfileJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(summary)
	}

	printPerformanceSummary(summary)
	return nil
}

func extractPerformanceSummary(windowAX uintptr) (PerformanceSummary, error) {
	if err := tryOpenPerformanceOverviewView(windowAX); err != nil {
		return PerformanceSummary{Available: false, Status: "not_ready"}, err
	}

	root := windowAX
	if editorArea := findGroupByTitle(windowAX, "editor area", 100); editorArea != 0 {
		root = editorArea
	}
	if !hasPerformanceViewIndicators(root) {
		return PerformanceSummary{Available: false, Status: "view_not_found"}, performanceSummaryError{
			Code:       "PERFORMANCE_VIEW_NOT_FOUND",
			Message:    "could not find visible Performance view controls in the Xcode trace window",
			Suggestion: "Open the trace's Performance view before running performance summary",
		}
	}

	texts := collectPerformanceSummaryTexts(root, 5000)
	return performanceSummaryFromTexts(texts, "xcode_accessibility")
}

func tryOpenPerformanceOverviewView(windowAX uintptr) error {
	if btn := findShowPerformanceButton(windowAX); btn != 0 {
		if !IsElementEnabled(btn) {
			return performanceSummaryError{
				Code:       "PERFORMANCE_NOT_READY",
				Message:    "performance data is not ready; Show Performance is visible but disabled",
				Suggestion: "Wait for replay to finish, then run performance summary again",
			}
		}
		if err := axAction(btn, "AXPress"); err != nil {
			return performanceSummaryError{
				Code:       "PERFORMANCE_SHOW_FAILED",
				Message:    fmt.Sprintf("failed to open the Performance view: %v", err),
				Suggestion: "Run performance show and retry performance summary",
			}
		}
		time.Sleep(300 * time.Millisecond)
	}

	candidates := []uintptr{
		findButtonBFS(windowAX, "Overview", 2000),
		findTabByName(windowAX, "Overview"),
		findOutlineRowByName(windowAX, "Performance"),
	}
	for _, el := range candidates {
		if el == 0 {
			continue
		}
		if axAction(el, "AXPress") == nil || axAction(el, "AXOpen") == nil {
			time.Sleep(300 * time.Millisecond)
			return nil
		}
	}
	return nil
}

func hasPerformanceViewIndicators(root uintptr) bool {
	for _, buttonName := range []string{
		"Overview",
		"Timeline",
		"Shaders",
		"Counters",
		"Cost Graph",
		"Heat Map",
		"Encoders",
		"Cost",
	} {
		if findButtonBFS(root, buttonName, 2000) != 0 || findTabByName(root, buttonName) != 0 {
			return true
		}
	}
	return false
}

func collectPerformanceSummaryTexts(root uintptr, maxVisit int) []string {
	var texts []string
	seenText := make(map[string]bool)
	seenElement := make(map[uintptr]bool)
	queue := []uintptr{root}
	visited := 0

	appendText := func(s string) {
		s = normalizePerformanceSummaryText(s)
		if s == "" || seenText[s] {
			return
		}
		seenText[s] = true
		texts = append(texts, s)
	}

	for len(queue) > 0 && visited < maxVisit {
		el := queue[0]
		queue = queue[1:]
		if seenElement[el] {
			continue
		}
		seenElement[el] = true
		visited++

		role := axString(el, "AXRole")
		switch role {
		case "AXStaticText", "AXTextField", "AXCell", "AXRow", "AXOutlineRow", "AXGroup":
			appendText(axString(el, "AXTitle"))
			appendText(axString(el, "AXValue"))
			appendText(axString(el, "AXDescription"))
		}

		queue = append(queue, axChildren(el)...)
	}

	return texts
}

func performanceSummaryFromTexts(texts []string, source string) (PerformanceSummary, error) {
	normalized := normalizePerformanceSummaryTexts(texts)
	metrics := parsePerformanceSummaryMetrics(normalized)
	summary := PerformanceSummary{
		Available: len(metrics) > 0,
		Status:    "ready",
		Source:    source,
		View:      "performance",
		TextCount: len(normalized),
		Metrics:   metrics,
	}
	if len(metrics) == 0 {
		summary.Status = "not_available"
		return summary, performanceSummaryError{
			Code:       "PERFORMANCE_SUMMARY_METRICS_NOT_FOUND",
			Message:    fmt.Sprintf("Performance view is visible, but no label/value metric pairs were exposed through Accessibility (%d text nodes found)", len(normalized)),
			Suggestion: "Use xcode-export-counters for source-backed counter data, or retry with the Performance Overview visible",
		}
	}
	return summary, nil
}

func printPerformanceSummary(summary PerformanceSummary) {
	fmt.Printf("Performance summary (%d metrics):\n", len(summary.Metrics))
	for _, metric := range summary.Metrics {
		fmt.Printf("  %s: %s\n", metric.Name, metric.DisplayValue)
	}
}

func normalizePerformanceSummaryTexts(texts []string) []string {
	seen := make(map[string]bool)
	normalized := make([]string, 0, len(texts))
	for _, text := range texts {
		text = normalizePerformanceSummaryText(text)
		if text == "" || seen[text] {
			continue
		}
		seen[text] = true
		normalized = append(normalized, text)
	}
	return normalized
}

func normalizePerformanceSummaryText(text string) string {
	text = strings.ReplaceAll(text, "\u00a0", " ")
	text = strings.Join(strings.Fields(text), " ")
	text = strings.TrimSpace(text)
	if text == "missing value" {
		return ""
	}
	return text
}

func parsePerformanceSummaryMetrics(texts []string) []PerformanceMetric {
	var metrics []PerformanceMetric
	seen := make(map[string]bool)

	addMetric := func(label, rawValue string) {
		label = normalizePerformanceSummaryText(label)
		rawValue = normalizePerformanceSummaryText(rawValue)
		if !looksLikePerformanceMetricLabel(label) {
			return
		}
		value, unit, ok := parsePerformanceMetricValue(rawValue)
		if !ok {
			return
		}
		key := strings.ToLower(label) + "\x00" + rawValue
		if seen[key] {
			return
		}
		seen[key] = true
		metrics = append(metrics, PerformanceMetric{
			Name:         label,
			Value:        value,
			Unit:         unit,
			DisplayValue: rawValue,
		})
	}

	for i, text := range texts {
		if label, rawValue, ok := parseInlinePerformanceMetric(text); ok {
			addMetric(label, rawValue)
		}
		if i+1 < len(texts) {
			addMetric(text, texts[i+1])
		}
	}
	return metrics
}

func parseInlinePerformanceMetric(text string) (string, string, bool) {
	match := performanceInlineMetricRE.FindStringSubmatch(text)
	if len(match) != 3 {
		return "", "", false
	}
	if _, _, ok := parsePerformanceMetricValue(match[2]); !ok {
		return "", "", false
	}
	return match[1], match[2], true
}

func parsePerformanceMetricValue(raw string) (float64, string, bool) {
	match := performanceMetricValueRE.FindStringSubmatch(raw)
	if len(match) != 3 {
		return 0, "", false
	}
	value, err := strconv.ParseFloat(strings.ReplaceAll(match[1], ",", ""), 64)
	if err != nil {
		return 0, "", false
	}
	return value, strings.TrimSpace(match[2]), true
}

func looksLikePerformanceMetricLabel(label string) bool {
	label = normalizePerformanceSummaryText(label)
	if len(label) < 2 || len(label) > 96 {
		return false
	}
	if _, _, ok := parsePerformanceMetricValue(label); ok {
		return false
	}
	for _, r := range label {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
			return true
		}
	}
	return false
}

func runPerformanceMemory(cmd *cobra.Command, args []string) error {
	if err := setupMacgo(); err != nil {
		return err
	}

	appAX, err := FindXcodeApp()
	if err != nil {
		if collectProfileJSON {
			return outputJSONError("XCODE_NOT_RUNNING", "Xcode not running", "Start Xcode first")
		}
		return fmt.Errorf("Xcode not running: %w", err)
	}
	defer cfRelease(appAX)

	windowAX, err := findTargetWindow(appAX, "")
	if err != nil {
		if collectProfileJSON {
			return outputJSONError("NO_WINDOWS", "no trace window found", "Open a trace file first")
		}
		return err
	}

	info := extractPerformanceMemoryInfo(windowAX)
	if collectProfileJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(info)
	}

	if !info.Available {
		fmt.Printf("Performance memory stats not available: %s\n", info.Message)
		return nil
	}

	fmt.Println("Performance memory stats:")
	for _, metric := range info.Metrics {
		if metric.RawValue != "" {
			fmt.Printf("  %s: %d bytes (%s)\n", metric.Label, metric.Bytes, metric.RawValue)
		} else {
			fmt.Printf("  %s: %d bytes\n", metric.Label, metric.Bytes)
		}
	}
	return nil
}

func extractPerformanceMemoryInfo(windowAX uintptr) PerformanceMemoryInfo {
	tryOpenPerformanceMemoryView(windowAX)

	root := windowAX
	if editorArea := findGroupByTitle(windowAX, "editor area", 100); editorArea != 0 {
		root = editorArea
	}

	texts := collectPerformanceMemoryTexts(root, 3000)
	return performanceMemoryInfoFromTexts(texts, "xcode_accessibility")
}

func tryOpenPerformanceMemoryView(windowAX uintptr) {
	if btn := findShowPerformanceButton(windowAX); btn != 0 && IsElementEnabled(btn) {
		if err := axAction(btn, "AXPress"); err == nil {
			time.Sleep(300 * time.Millisecond)
		}
	}

	candidates := []uintptr{
		findButtonBFS(windowAX, "Memory", 2000),
		findButtonBFS(windowAX, "Show Memory", 2000),
		findTabByName(windowAX, "Memory"),
		findOutlineRowByName(windowAX, "Memory"),
	}
	for _, el := range candidates {
		if el == 0 {
			continue
		}
		if axAction(el, "AXPress") == nil || axAction(el, "AXOpen") == nil {
			time.Sleep(300 * time.Millisecond)
			return
		}
	}
}

func collectPerformanceMemoryTexts(root uintptr, maxVisit int) []string {
	var texts []string
	seenText := make(map[string]bool)
	seenElement := make(map[uintptr]bool)
	queue := []uintptr{root}
	visited := 0

	appendText := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || seenText[s] {
			return
		}
		seenText[s] = true
		texts = append(texts, s)
	}

	for len(queue) > 0 && visited < maxVisit {
		el := queue[0]
		queue = queue[1:]
		if seenElement[el] {
			continue
		}
		seenElement[el] = true
		visited++

		role := axString(el, "AXRole")
		switch role {
		case "AXStaticText", "AXTextField", "AXCell", "AXRow", "AXOutlineRow", "AXGroup":
			appendText(axString(el, "AXTitle"))
			appendText(axString(el, "AXValue"))
			appendText(axString(el, "AXDescription"))
		}

		queue = append(queue, axChildren(el)...)
	}

	return texts
}

func performanceMemoryInfoFromTexts(texts []string, source string) PerformanceMemoryInfo {
	metrics := parsePerformanceMemoryMetrics(texts)
	if len(metrics) == 0 {
		return PerformanceMemoryInfo{
			Available: false,
			Status:    "not_available",
			Source:    source,
			Message:   "no memory metrics found in Xcode accessibility tree",
		}
	}
	return PerformanceMemoryInfo{
		Available: true,
		Status:    "ready",
		Source:    source,
		Metrics:   metrics,
	}
}

func parsePerformanceMemoryMetrics(texts []string) []PerformanceMemoryMetric {
	var metrics []PerformanceMemoryMetric
	seen := make(map[string]bool)

	addMetric := func(label, raw string, bytes uint64) {
		label = cleanMemoryMetricLabel(label)
		if label == "" || !labelLooksMemoryRelated(label) {
			return
		}
		name := normalizeMemoryMetricName(label)
		key := fmt.Sprintf("%s/%d", name, bytes)
		if seen[key] {
			return
		}
		seen[key] = true
		metrics = append(metrics, PerformanceMemoryMetric{
			Name:     name,
			Label:    label,
			Bytes:    bytes,
			RawValue: strings.TrimSpace(raw),
		})
	}

	cleaned := make([]string, 0, len(texts))
	for _, text := range texts {
		text = strings.TrimSpace(text)
		if text != "" {
			cleaned = append(cleaned, text)
		}
	}

	for _, text := range cleaned {
		if label, raw, bytes, ok := parseInlineMemoryMetric(text); ok {
			addMetric(label, raw, bytes)
		}
	}

	for i, text := range cleaned {
		if performanceMemorySizeRE.MatchString(text) || !labelLooksMemoryRelated(text) {
			continue
		}
		for j := i + 1; j < len(cleaned) && j <= i+2; j++ {
			raw, bytes, ok := parseMemorySize(cleaned[j])
			if !ok {
				if labelLooksMemoryRelated(cleaned[j]) {
					break
				}
				continue
			}
			addMetric(text, raw, bytes)
			break
		}
	}

	return metrics
}

func parseInlineMemoryMetric(text string) (string, string, uint64, bool) {
	loc := performanceMemorySizeRE.FindStringSubmatchIndex(text)
	if loc == nil {
		return "", "", 0, false
	}

	label := strings.TrimSpace(text[:loc[0]])
	label = cleanMemoryMetricLabel(label)
	if label == "" {
		return "", "", 0, false
	}

	raw := text[loc[0]:loc[1]]
	bytes, ok := memorySizeToBytes(text[loc[2]:loc[3]], text[loc[4]:loc[5]])
	return label, raw, bytes, ok
}

func parseMemorySize(text string) (string, uint64, bool) {
	match := performanceMemorySizeRE.FindStringSubmatch(text)
	if len(match) != 3 {
		return "", 0, false
	}
	bytes, ok := memorySizeToBytes(match[1], match[2])
	return match[0], bytes, ok
}

func memorySizeToBytes(valueText, unit string) (uint64, bool) {
	value, err := strconv.ParseFloat(valueText, 64)
	if err != nil || value < 0 {
		return 0, false
	}

	switch strings.ToLower(unit) {
	case "b", "byte", "bytes":
		return uint64(value + 0.5), true
	case "kb":
		return uint64(value*1000 + 0.5), true
	case "mb":
		return uint64(value*1000*1000 + 0.5), true
	case "gb":
		return uint64(value*1000*1000*1000 + 0.5), true
	case "tb":
		return uint64(value*1000*1000*1000*1000 + 0.5), true
	case "kib":
		return uint64(value*1024 + 0.5), true
	case "mib":
		return uint64(value*1024*1024 + 0.5), true
	case "gib":
		return uint64(value*1024*1024*1024 + 0.5), true
	case "tib":
		return uint64(value*1024*1024*1024*1024 + 0.5), true
	default:
		return 0, false
	}
}

func cleanMemoryMetricLabel(label string) string {
	label = strings.TrimSpace(label)
	label = strings.TrimRight(label, ":- ")
	return strings.TrimSpace(label)
}

func labelLooksMemoryRelated(label string) bool {
	lower := strings.ToLower(label)
	if strings.Contains(lower, "bandwidth") ||
		strings.Contains(lower, "compression ratio") ||
		strings.Contains(lower, "/s") ||
		strings.Contains(lower, "per second") {
		return false
	}
	return strings.Contains(lower, "memory") ||
		strings.Contains(lower, "alloc") ||
		strings.Contains(lower, "resident") ||
		strings.Contains(lower, "heap") ||
		strings.Contains(lower, "buffer") ||
		strings.Contains(lower, "texture") ||
		strings.Contains(lower, "bytes")
}

func normalizeMemoryMetricName(label string) string {
	name := strings.ToLower(label)
	name = metricNameCleanRE.ReplaceAllString(name, "_")
	name = strings.Trim(name, "_")
	if name == "" {
		return "memory"
	}
	return name
}

// runPerformanceView clicks the appropriate tab button in the performance view.
func runPerformanceView(viewName string) error {
	status := xcodeProfileStatusWriter()

	if err := setupMacgo(); err != nil {
		return err
	}

	appAX, err := FindXcodeApp()
	if err != nil {
		if collectProfileJSON {
			return outputJSONError("XCODE_NOT_RUNNING", "Xcode not running", "Start Xcode first")
		}
		return fmt.Errorf("Xcode not running: %w", err)
	}
	defer cfRelease(appAX)

	windowAX, err := findTargetWindow(appAX, "")
	if err != nil {
		if collectProfileJSON {
			return outputJSONError("NO_WINDOWS", "no trace window found", "Open a trace file first")
		}
		return err
	}

	// Map view names to button names in Xcode UI
	buttonNames := map[string]string{
		"overview":   "Overview",
		"timeline":   "Timeline",
		"shaders":    "Shaders",
		"counters":   "Counters",
		"cost-graph": "Cost Graph",
		"heat-map":   "Heat Map",
		"encoders":   "Encoders",
		"cost":       "Cost",
	}

	buttonName, ok := buttonNames[viewName]
	if !ok {
		return fmt.Errorf("unknown view: %s", viewName)
	}

	// Find and click the button
	btn := findButtonBFS(windowAX, buttonName, 1000)
	if btn == 0 {
		if collectProfileJSON {
			return outputJSONError("NOT_FOUND", fmt.Sprintf("%s button not found", buttonName), "Open performance view first")
		}
		return fmt.Errorf("%s button not found (open performance view first)", buttonName)
	}

	if !IsElementEnabled(btn) {
		if collectProfileJSON {
			return outputJSONError("DISABLED", fmt.Sprintf("%s button is disabled", buttonName), "")
		}
		return fmt.Errorf("%s button is disabled", buttonName)
	}

	fmt.Fprintf(status, "Selecting %s view...\n", buttonName)

	if err := axAction(btn, "AXPress"); err != nil {
		if collectProfileJSON {
			return outputJSONError("CLICK_FAILED", fmt.Sprintf("failed to click: %v", err), "Try again")
		}
		return fmt.Errorf("failed to click: %w", err)
	}

	if collectProfileJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]interface{}{
			"success": true,
			"view":    viewName,
		})
	}

	fmt.Fprintln(status, "Done")
	return nil
}
