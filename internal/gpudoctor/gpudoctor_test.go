package gpudoctor

import "testing"

func TestNsysVerdict(t *testing.T) {
	tests := []struct {
		version string
		want    Status
	}{
		// 2026.1 defaults -t cuda to hardware event tracing, whose GB10
		// support probe succeeds and then drops every kernel record.
		{"2026.1.1", StatusWarn},
		// 2024.5 bundles a CUPTI far older than current drivers.
		{"2024.5.1", StatusFail},
		{"2025.3.2", StatusOK},
		{"", StatusSkip},
	}
	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			got, detail, _ := nsysVerdict(tt.version)
			if got != tt.want {
				t.Errorf("nsysVerdict(%q) = %v, want %v", tt.version, got, tt.want)
			}
			if detail == "" {
				t.Error("verdict carries no explanation")
			}
		})
	}
}

func TestMajorOf(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{{"13.0", 13}, {"12.6", 12}, {"", 0}, {"nonsense", 0}}
	for _, tt := range tests {
		if got := majorOf(tt.in); got != tt.want {
			t.Errorf("majorOf(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestReportWorst(t *testing.T) {
	tests := []struct {
		name   string
		checks []Check
		want   Status
	}{
		{"empty", nil, StatusOK},
		{"warn beats skip", []Check{{Status: StatusSkip}, {Status: StatusWarn}}, StatusWarn},
		{"fail beats warn", []Check{{Status: StatusWarn}, {Status: StatusFail}, {Status: StatusOK}}, StatusFail},
		{"all ok", []Check{{Status: StatusOK}, {Status: StatusOK}}, StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rep := &Report{Checks: tt.checks}
			if got := rep.Worst(); got != tt.want {
				t.Errorf("Worst() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Run must produce a diagnosis on any host, including one with no GPU:
// an undiagnosable environment is itself the diagnosis.
func TestRunAlwaysReports(t *testing.T) {
	rep := Run(Options{})
	if len(rep.Checks) == 0 {
		t.Fatal("Run() produced no checks")
	}
	for _, c := range rep.Checks {
		if c.Name == "" || c.Status == "" || c.Detail == "" {
			t.Errorf("incomplete check: %+v", c)
		}
	}
}
