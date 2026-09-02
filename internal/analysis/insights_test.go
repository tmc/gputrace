package analysis

import (
	"strings"
	"testing"
)

func TestDetectBottlenecksRejectsApproximateTiming(t *testing.T) {
	report := &InsightsReport{Insights: make([]*PerformanceInsight, 0)}
	detectBottlenecks(&ShaderMetrics{
		Name:            "steel_gemm",
		PercentOfTotal:  40,
		TotalDurationNs: 5_000_000,
		TimingSource:    "synthetic kernel-name estimate",
		TimingApprox:    true,
	}, report)

	if len(report.Insights) != 0 {
		t.Fatalf("approximate timing produced %d bottleneck insights, want 0", len(report.Insights))
	}
}

func TestInsightTimingSources(t *testing.T) {
	sources, approximate := insightTimingSources([]*ShaderMetrics{
		{TimingSource: "streamData", TimingApprox: false},
		{TimingSource: "synthetic", TimingApprox: true},
		{TimingSource: "streamData", TimingApprox: false},
	})

	if got, want := strings.Join(sources, ","), "streamData,synthetic"; got != want {
		t.Fatalf("timing sources = %q, want %q", got, want)
	}
	if !approximate {
		t.Fatal("timing approximate = false, want true")
	}
}

func TestFormatInsightsReportDisplaysTimingProvenance(t *testing.T) {
	report := &InsightsReport{
		Insights:       make([]*PerformanceInsight, 0),
		TotalGPUTimeMs: 5,
		TimingSources:  []string{"synthetic kernel-name estimate"},
		TimingApprox:   true,
	}

	got := FormatInsightsReport(report)
	want := "Timing Source: synthetic kernel-name estimate (approximate)"
	if !strings.Contains(got, want) {
		t.Fatalf("report does not contain %q:\n%s", want, got)
	}
}

func TestDetectAntiPatternsDoesNotClaimDivergence(t *testing.T) {
	report := &InsightsReport{Insights: make([]*PerformanceInsight, 0)}
	detectAntiPatterns(nil, &ShaderMetrics{
		Name:            "kernel",
		InvocationCount: 2,
		MinDurationNs:   1_000,
		MaxDurationNs:   10_000,
		AvgDurationNs:   5_500,
		TimingSource:    "streamData gpuCommandInfoData dispatch durations",
	}, report)

	if len(report.Insights) != 1 {
		t.Fatalf("insights = %d, want 1", len(report.Insights))
	}
	got := report.Insights[0]
	if strings.Contains(got.Impact, "SIMD") || strings.Contains(got.Description, "suggests divergent") {
		t.Fatalf("variability insight makes unsupported causal claim: %+v", got)
	}
	if !strings.Contains(got.Description, "does not by itself establish") {
		t.Fatalf("variability insight omits attribution limitation: %q", got.Description)
	}
}
