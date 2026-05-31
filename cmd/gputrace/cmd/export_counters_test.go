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
			name: "all parsed",
			summary: exportCounterSourceSummary{
				totalRows:             2,
				parsedCounterRows:     2,
				syntheticFallbackRows: 0,
				perfCountersPresent:   true,
			},
			want: []string{
				"parsed counter data (2 rows)",
			},
			avoid: []string{
				"synthetic fallback",
			},
		},
		{
			name: "all synthetic without perf counters",
			summary: exportCounterSourceSummary{
				totalRows:             2,
				parsedCounterRows:     0,
				syntheticFallbackRows: 2,
				perfCountersPresent:   false,
			},
			want: []string{
				"synthetic fallback (2 rows)",
				"no parsed .gpuprofiler_raw counter data found",
			},
			avoid: []string{
				"parsed counter data (",
			},
		},
		{
			name: "mixed parsed and synthetic",
			summary: exportCounterSourceSummary{
				totalRows:             3,
				parsedCounterRows:     1,
				syntheticFallbackRows: 2,
				perfCountersPresent:   true,
			},
			want: []string{
				"parsed counter data (1 row)",
				"synthetic fallback (2 rows)",
			},
		},
		{
			name: "synthetic despite perf counters",
			summary: exportCounterSourceSummary{
				totalRows:             1,
				parsedCounterRows:     0,
				syntheticFallbackRows: 1,
				perfCountersPresent:   true,
			},
			want: []string{
				"synthetic fallback (1 row)",
				"performance counter files were present but no parsed row metrics were available",
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

func TestExportCountersHelpDistinguishesSyntheticFallback(t *testing.T) {
	help := exportCountersCmd.Long
	for _, want := range []string{
		"parsed counter rows",
		"SYNTHETIC FALLBACK",
		"reports the row source counts on stderr",
	} {
		if !strings.Contains(help, want) {
			t.Fatalf("export-counters help does not contain %q", want)
		}
	}
}
