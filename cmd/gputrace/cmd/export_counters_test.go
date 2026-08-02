package cmd

import (
	"strings"
	"testing"
)

func TestFormatExportCounterSourceNotice(t *testing.T) {
	tests := []struct {
		name    string
		summary exportCounterSourceSummary
		want    []string
		avoid   []string
	}{
		{
			name: "metadata only without perf counters",
			summary: exportCounterSourceSummary{
				totalRows:        2,
				metadataOnlyRows: 2,
			},
			want: []string{
				"metadata only (2 rows)",
				"no parsed .gpuprofiler_raw counter data found",
			},
			avoid: []string{
				"parsed counter data",
			},
		},
		{
			name: "metadata only with perf counters",
			summary: exportCounterSourceSummary{
				totalRows:           3,
				metadataOnlyRows:    3,
				perfCountersPresent: true,
			},
			want: []string{
				"metadata only (3 rows)",
				"pipeline-scoped and lack an encoder join",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatExportCounterSourceNotice(tt.summary)
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Fatalf("notice %q does not contain %q", got, want)
				}
			}
			for _, avoid := range tt.avoid {
				if strings.Contains(got, avoid) {
					t.Fatalf("notice %q unexpectedly contains %q", got, avoid)
				}
			}
		})
	}
}

func TestExportCountersHelpWithholdsUnjoinedCounters(t *testing.T) {
	help := exportCountersCmd.Long
	for _, want := range []string{
		"pipeline-scoped, not encoder-scoped",
		"withheld until a stable join exists",
		"state on stderr",
	} {
		if !strings.Contains(help, want) {
			t.Fatalf("export-counters help does not contain %q", want)
		}
	}
	for _, misleading := range []string{"matching the exact format", "matching Xcode's export format exactly"} {
		if strings.Contains(help, misleading) {
			t.Fatalf("export-counters help makes exactness claim %q", misleading)
		}
	}
}
