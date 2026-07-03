package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tmc/gputrace"
)

type insightsOptions struct {
	json     bool
	minLevel string
}

var insightsCmd = newInsightsCommand(&insightsOptions{})

func newInsightsCommand(opts *insightsOptions) *cobra.Command {
	if opts.minLevel == "" {
		opts.minLevel = "low"
	}
	cmd := &cobra.Command{
		Use:   "insights <trace.gputrace>",
		Short: "Generate actionable performance insights from GPU trace",
		Long: `Analyze GPU trace and generate actionable performance insights.

This command performs comprehensive analysis to identify:
  - BOTTLENECKS: Shaders consuming significant GPU time
    * Memory-bound vs compute-bound classification
    * Dominant shader detection
  - OPTIMIZATIONS: Opportunities to improve performance
    * Low occupancy issues
    * Excessive dispatch overhead
    * Work distribution imbalance
  - ANTI-PATTERNS: Common performance pitfalls
    * Unbalanced threadgroup configurations
    * Execution time variability (branch divergence)
  - INFO: General observations about the trace

Each insight includes:
  - Severity level (CRITICAL, HIGH, MEDIUM, LOW, INFO)
  - Detailed description with metrics
  - Actionable recommendations
  - Expected impact

Examples:
  # Show all insights
  gputrace insights trace.gputrace

  # Show only critical and high severity insights
  gputrace insights trace.gputrace --min-level high

  # Export insights as JSON
  gputrace insights trace.gputrace --json > insights.json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInsights(cmd, args, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.json, "json", false, "Output in JSON format")
	cmd.Flags().StringVar(&opts.minLevel, "min-level", "low",
		"Minimum severity level to display (critical, high, medium, low, info)")
	return cmd
}

func init() {
	rootCmd.AddCommand(insightsCmd)
}

func runInsights(cmd *cobra.Command, args []string, opts *insightsOptions) error {
	tracePath := args[0]

	// Verify trace file exists
	if err := checkTraceFile(tracePath); err != nil {
		return err
	}

	// Open trace
	trace, err := gputrace.Open(tracePath)
	if err != nil {
		return fmt.Errorf("failed to open trace: %w", err)
	}
	defer trace.Close()

	// Generate insights
	report, err := gputrace.GenerateInsights(trace)
	if err != nil {
		return fmt.Errorf("failed to generate insights: %w", err)
	}

	// Filter by severity level if requested
	if opts.minLevel != "low" {
		report = filterInsightsBySeverity(report, opts.minLevel)
	}

	// Output based on format
	if opts.json {
		return writeInsightsJSON(cmd.OutOrStdout(), report)
	}

	// Print formatted report
	return writeInsightsText(cmd.OutOrStdout(), report)
}

func writeInsightsJSON(w io.Writer, report *gputrace.InsightsReport) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		return fmt.Errorf("encode insights json: %w", err)
	}
	return nil
}

func writeInsightsText(w io.Writer, report *gputrace.InsightsReport) error {
	var out strings.Builder

	out.WriteString(gputrace.FormatInsightsReport(report))

	// Print summary at the end
	if len(report.Insights) == 0 {
		out.WriteString("✓ No performance issues detected!\n")
	} else {
		out.WriteString("\n=== Summary ===\n")
		if report.CriticalCount > 0 {
			fmt.Fprintf(&out, "⚠️  %d CRITICAL issues require immediate attention\n", report.CriticalCount)
		}
		if report.HighCount > 0 {
			fmt.Fprintf(&out, "⚠️  %d HIGH priority optimizations recommended\n", report.HighCount)
		}
		if report.MediumCount > 0 {
			fmt.Fprintf(&out, "ℹ️  %d MEDIUM priority suggestions available\n", report.MediumCount)
		}
		if report.LowCount > 0 {
			fmt.Fprintf(&out, "ℹ️  %d LOW priority observations noted\n", report.LowCount)
		}
	}

	if _, err := io.WriteString(w, out.String()); err != nil {
		return fmt.Errorf("write insights report: %w", err)
	}
	return nil
}

// filterInsightsBySeverity filters insights by minimum severity level.
func filterInsightsBySeverity(report *gputrace.InsightsReport, minLevel string) *gputrace.InsightsReport {
	// Map severity levels to numeric values
	severityValue := map[string]int{
		"critical": 0,
		"high":     1,
		"medium":   2,
		"low":      3,
		"info":     4,
	}

	minValue, ok := severityValue[minLevel]
	if !ok {
		minValue = 3 // Default to "low"
	}

	// Create filtered report
	filtered := &gputrace.InsightsReport{
		Insights:       make([]*gputrace.PerformanceInsight, 0),
		TotalGPUTimeMs: report.TotalGPUTimeMs,
		TopBottlenecks: report.TopBottlenecks,
	}

	for _, insight := range report.Insights {
		var insightValue int
		switch insight.Severity {
		case gputrace.SeverityCritical:
			insightValue = 0
		case gputrace.SeverityHigh:
			insightValue = 1
		case gputrace.SeverityMedium:
			insightValue = 2
		case gputrace.SeverityLow:
			insightValue = 3
		case gputrace.SeverityInfo:
			insightValue = 4
		default:
			insightValue = 4
		}

		if insightValue <= minValue {
			filtered.Insights = append(filtered.Insights, insight)
			switch insight.Severity {
			case gputrace.SeverityCritical:
				filtered.CriticalCount++
			case gputrace.SeverityHigh:
				filtered.HighCount++
			case gputrace.SeverityMedium:
				filtered.MediumCount++
			case gputrace.SeverityLow:
				filtered.LowCount++
			}
		}
	}

	return filtered
}
