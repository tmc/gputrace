package cmd

import (
	"strings"
	"testing"
)

func TestParsePerformanceMemoryMetrics(t *testing.T) {
	metrics := parsePerformanceMemoryMetrics([]string{
		"Total Memory Allocated: 4.25 MB",
		"Peak Memory Usage 8 KiB",
		"Device Memory Bandwidth: 1.5 GB/s",
	})

	byName := map[string]PerformanceMemoryMetric{}
	for _, metric := range metrics {
		byName[metric.Name] = metric
	}

	if got := byName["total_memory_allocated"].Bytes; got != 4250000 {
		t.Fatalf("total_memory_allocated bytes = %d, want 4250000", got)
	}
	if got := byName["peak_memory_usage"].Bytes; got != 8192 {
		t.Fatalf("peak_memory_usage bytes = %d, want 8192", got)
	}
	if _, ok := byName["device_memory_bandwidth"]; ok {
		t.Fatalf("bandwidth metric should not be reported as memory usage")
	}
}

func TestParsePerformanceMemoryMetricsAdjacentText(t *testing.T) {
	metrics := parsePerformanceMemoryMetrics([]string{
		"Texture Memory",
		"1.5 GiB",
		"Unrelated",
		"Resident Size",
		"512 bytes",
	})

	byName := map[string]PerformanceMemoryMetric{}
	for _, metric := range metrics {
		byName[metric.Name] = metric
	}

	if got := byName["texture_memory"].Bytes; got != 1610612736 {
		t.Fatalf("texture_memory bytes = %d, want 1610612736", got)
	}
	if got := byName["resident_size"].Bytes; got != 512 {
		t.Fatalf("resident_size bytes = %d, want 512", got)
	}
}

func TestPerformanceMemoryInfoFailClosed(t *testing.T) {
	info := performanceMemoryInfoFromTexts([]string{
		"Performance",
		"No Memory statistics are currently visible",
	}, "test")

	if info.Available {
		t.Fatalf("Available = true, want false")
	}
	if info.Status != "not_available" {
		t.Fatalf("Status = %q, want not_available", info.Status)
	}
	if len(info.Metrics) != 0 {
		t.Fatalf("Metrics length = %d, want 0", len(info.Metrics))
	}
	if !strings.Contains(info.Message, "no memory metrics") {
		t.Fatalf("Message = %q, want no memory metrics explanation", info.Message)
	}
}

func TestPerformanceSummaryFromTexts(t *testing.T) {
	summary, err := performanceSummaryFromTexts([]string{
		"Overview",
		"GPU Time",
		"18.5 ms",
		"ALU Utilization: 72.4 %",
		"GPU Read Bandwidth",
		"1.25 GB/s",
		"Timeline",
	}, "test")
	if err != nil {
		t.Fatalf("performanceSummaryFromTexts returned error: %v", err)
	}
	if !summary.Available {
		t.Fatalf("Available = false, want true")
	}
	if summary.Status != "ready" {
		t.Fatalf("Status = %q, want ready", summary.Status)
	}

	byName := map[string]PerformanceMetric{}
	for _, metric := range summary.Metrics {
		byName[metric.Name] = metric
	}

	if got := byName["GPU Time"]; got.Value != 18.5 || got.Unit != "ms" || got.DisplayValue != "18.5 ms" {
		t.Fatalf("GPU Time = %+v, want 18.5 ms", got)
	}
	if got := byName["ALU Utilization"]; got.Value != 72.4 || got.Unit != "%" || got.DisplayValue != "72.4 %" {
		t.Fatalf("ALU Utilization = %+v, want 72.4 %%", got)
	}
	if got := byName["GPU Read Bandwidth"]; got.Value != 1.25 || got.Unit != "GB/s" || got.DisplayValue != "1.25 GB/s" {
		t.Fatalf("GPU Read Bandwidth = %+v, want 1.25 GB/s", got)
	}
}

func TestPerformanceSummaryFromTextsFailClosed(t *testing.T) {
	summary, err := performanceSummaryFromTexts([]string{
		"Performance",
		"Overview",
		"Timeline",
		"No summary statistics are currently visible",
	}, "test")
	if err == nil {
		t.Fatalf("performanceSummaryFromTexts returned nil error, want fail-closed error")
	}
	if summary.Available {
		t.Fatalf("Available = true, want false")
	}
	if summary.Status != "not_available" {
		t.Fatalf("Status = %q, want not_available", summary.Status)
	}
	summaryErr, ok := err.(performanceSummaryError)
	if !ok {
		t.Fatalf("error type = %T, want performanceSummaryError", err)
	}
	if summaryErr.Code != "PERFORMANCE_SUMMARY_METRICS_NOT_FOUND" {
		t.Fatalf("Code = %q, want PERFORMANCE_SUMMARY_METRICS_NOT_FOUND", summaryErr.Code)
	}
	if !strings.Contains(summaryErr.Message, "no label/value metric pairs") {
		t.Fatalf("Message = %q, want label/value explanation", summaryErr.Message)
	}
}
