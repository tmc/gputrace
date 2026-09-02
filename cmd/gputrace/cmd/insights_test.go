package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/tmc/gputrace"
)

func TestRunInsightsJSONUsesCommandOutput(t *testing.T) {
	cmd := &cobra.Command{}
	var commandStdout bytes.Buffer
	cmd.SetOut(&commandStdout)
	opts := &insightsOptions{json: true, minLevel: "low"}

	tracePath := writeInsightsMinimalTraceBundle(t)
	osStdout, err := captureStdout(t, func() error {
		return runInsights(cmd, []string{tracePath}, opts)
	})
	if err != nil {
		t.Fatalf("runInsights: %v", err)
	}
	if osStdout != "" {
		t.Fatalf("os stdout = %q, want empty", osStdout)
	}
	if got := commandStdout.String(); !strings.HasSuffix(got, "\n") {
		t.Fatalf("json output does not end with newline: %q", got)
	}

	var got gputrace.InsightsReport
	if err := json.Unmarshal(commandStdout.Bytes(), &got); err != nil {
		t.Fatalf("json output is invalid: %v\n%s", err, commandStdout.String())
	}
	if got.Insights == nil {
		t.Fatalf("json insights = nil, want empty slice:\n%s", commandStdout.String())
	}
}

func TestWriteInsightsJSON(t *testing.T) {
	report := testInsightsReport()

	var out bytes.Buffer
	if err := writeInsightsJSON(&out, report); err != nil {
		t.Fatalf("writeInsightsJSON: %v", err)
	}
	if got := out.String(); !strings.HasSuffix(got, "\n") {
		t.Fatalf("json output does not end with newline: %q", got)
	}
	if got := out.String(); !strings.Contains(got, "  \"insights\": [\n") {
		t.Fatalf("json output is not indented:\n%s", got)
	}

	var got gputrace.InsightsReport
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("json output is invalid: %v\n%s", err, out.String())
	}
	if got.HighCount != 1 || got.TotalGPUTimeMs != 12.5 {
		t.Fatalf("json report = %+v", got)
	}
	if !got.TimingApprox || len(got.TimingSources) != 1 || got.TimingSources[0] != "synthetic" {
		t.Fatalf("json timing provenance = %q, approximate %t", got.TimingSources, got.TimingApprox)
	}
	if len(got.Insights) != 1 || got.Insights[0].ShaderName != "kernel_a" {
		t.Fatalf("json insights = %+v", got.Insights)
	}
}

func TestWriteInsightsTextPreservesSummaryBytes(t *testing.T) {
	report := testInsightsReport()

	var out bytes.Buffer
	if err := writeInsightsText(&out, report); err != nil {
		t.Fatalf("writeInsightsText: %v", err)
	}

	want := gputrace.FormatInsightsReport(report) +
		"\n=== Summary ===\n" +
		"1 HIGH-priority optimizations recommended\n"
	if got := out.String(); got != want {
		t.Fatalf("text output mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestWriteInsightsTextNoInsights(t *testing.T) {
	report := &gputrace.InsightsReport{
		Insights:       []*gputrace.PerformanceInsight{},
		TotalGPUTimeMs: 1.25,
	}

	var out bytes.Buffer
	if err := writeInsightsText(&out, report); err != nil {
		t.Fatalf("writeInsightsText: %v", err)
	}

	want := gputrace.FormatInsightsReport(report) + "No supported performance issues identified.\n"
	if got := out.String(); got != want {
		t.Fatalf("text output mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestWriteInsightsTextDoesNotPromoteLimitedAttributionToOptimization(t *testing.T) {
	report := testInsightsReport()
	report.TimingApprox = false
	report.TimingSources = []string{"streamData gpuCommandInfoData dispatch durations"}

	var out bytes.Buffer
	if err := writeInsightsText(&out, report); err != nil {
		t.Fatalf("writeInsightsText: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "1 HIGH-priority attribution hypotheses") {
		t.Fatalf("limited-attribution summary missing:\n%s", got)
	}
	if strings.Contains(got, "optimizations recommended") {
		t.Fatalf("limited attribution promoted to optimization:\n%s", got)
	}
}

func TestWriteInsightsTextApproximateDoesNotGiveCleanBill(t *testing.T) {
	report := &gputrace.InsightsReport{
		Insights:     []*gputrace.PerformanceInsight{},
		TimingApprox: true,
	}

	var out bytes.Buffer
	if err := writeInsightsText(&out, report); err != nil {
		t.Fatalf("writeInsightsText: %v", err)
	}
	got := out.String()
	if strings.Contains(got, "✓") || strings.Contains(got, "No performance issues") {
		t.Fatalf("approximate report gives a clean bill:\n%s", got)
	}
	if !strings.Contains(got, "Measured timing is required") {
		t.Fatalf("approximate report omits measurement limitation:\n%s", got)
	}
}

func testInsightsReport() *gputrace.InsightsReport {
	return &gputrace.InsightsReport{
		Insights: []*gputrace.PerformanceInsight{
			{
				Type:        gputrace.InsightBottleneck,
				Severity:    gputrace.SeverityHigh,
				ShaderName:  "kernel_a",
				Title:       "kernel_a is a bottleneck",
				Description: "kernel_a consumes most GPU time",
				Recommendations: []string{
					"profile kernel_a in detail",
				},
				Impact: "major contributor to GPU time",
			},
		},
		HighCount:      1,
		TotalGPUTimeMs: 12.5,
		TimingSources:  []string{"synthetic"},
		TimingApprox:   true,
		TopBottlenecks: []string{"kernel_a"},
	}
}

func writeInsightsMinimalTraceBundle(t *testing.T) string {
	t.Helper()

	tracePath := filepath.Join(t.TempDir(), "minimal.gputrace")
	if err := os.Mkdir(tracePath, 0o755); err != nil {
		t.Fatalf("mkdir trace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tracePath, "metadata"), []byte(insightsMinimalMetadataPlist), 0o644); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tracePath, "capture"), []byte(gputrace.MagicMTSP), 0o644); err != nil {
		t.Fatalf("write capture: %v", err)
	}
	return tracePath
}

const insightsMinimalMetadataPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>(uuid)</key>
	<string>insights-minimal-test-trace</string>
	<key>DYCaptureSession.capture_version</key>
	<integer>1</integer>
	<key>DYCaptureSession.graphics_api</key>
	<integer>1</integer>
	<key>DYCaptureSession.deviceId</key>
	<integer>0</integer>
	<key>DYCaptureSession.nativePointerSize</key>
	<integer>8</integer>
	<key>DYCaptureEngine.captured_frames_count</key>
	<integer>1</integer>
</dict>
</plist>
`
