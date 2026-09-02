package shader

import (
	"bytes"
	"encoding/csv"
	"strings"
	"testing"

	"github.com/tmc/gputrace/internal/counter"
)

func TestFormatShadersXcodeStyleCompilerStatColumns(t *testing.T) {
	tests := []struct {
		name            string
		metrics         ShaderMetrics
		wantTempRegs    string
		wantSpilled     string
		wantDevLoad     string
		wantDevStore    string
		wantAbsenceNote bool
	}{
		{
			name: "counts present",
			metrics: ShaderMetrics{
				AllocatedRegisters: 30,
				SpilledBytes:       0,
				DeviceLoadCount:    30,
				DeviceStoreCount:   1,
				HasPipelineStats:   true,
			},
			wantTempRegs: "30",
			wantSpilled:  "0",
			wantDevLoad:  "30",
			wantDevStore: "1",
		},
		{
			// A kernel that touches no device memory really does report
			// zero, so the zero must survive to the table.
			name: "zero counts are printed, not hidden",
			metrics: ShaderMetrics{
				AllocatedRegisters: 2,
				HasPipelineStats:   true,
			},
			wantTempRegs: "2",
			wantSpilled:  "0",
			wantDevLoad:  "0",
			wantDevStore: "0",
		},
		{
			name:            "statistics absent",
			metrics:         ShaderMetrics{},
			wantTempRegs:    "?",
			wantSpilled:     "?",
			wantDevLoad:     "?",
			wantDevStore:    "?",
			wantAbsenceNote: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := tt.metrics
			m.Name = "kernel"
			m.Address = 0xabc
			m.TotalThreadgroups = 64
			report := &ShaderMetricsReport{Shaders: []*ShaderMetrics{&m}}

			var buf bytes.Buffer
			if err := FormatShadersXcodeStyle(&buf, report, nil, false); err != nil {
				t.Fatal(err)
			}

			fields := xcodeStyleDataFields(t, buf.String())
			for _, check := range []struct {
				label string
				index int
				want  string
			}{
				{"temp regs", xcodeStyleTempRegs, tt.wantTempRegs},
				{"spilled", xcodeStyleSpilled, tt.wantSpilled},
				{"device load", xcodeStyleDevLoad, tt.wantDevLoad},
				{"device store", xcodeStyleDevStore, tt.wantDevStore},
			} {
				if got := fields[check.index]; got != check.want {
					t.Errorf("%s = %q, want %q in:\n%s", check.label, got, check.want, buf.String())
				}
			}

			gotNote := strings.Contains(buf.String(), "no compiler statistics")
			if gotNote != tt.wantAbsenceNote {
				t.Errorf("absence note present = %v, want %v in:\n%s", gotNote, tt.wantAbsenceNote, buf.String())
			}
		})
	}
}

// csvCell returns the single data row's value for the named column.
func csvCell(rows [][]string, column string) (string, bool) {
	for i, name := range rows[0] {
		if name == column {
			return rows[1][i], true
		}
	}
	return "", false
}

// An empty compile cell means the trace recorded nothing. Zero milliseconds
// and a recorded cache miss are different findings and must not collapse into
// the same cell as "absent".
func TestExportShaderMetricsCSVCompileCells(t *testing.T) {
	cached := false
	tests := []struct {
		name       string
		metrics    ShaderMetrics
		wantTime   string
		wantCached string
	}{
		{name: "absent", metrics: ShaderMetrics{}, wantTime: "", wantCached: ""},
		{
			name:       "time without cache record",
			metrics:    ShaderMetrics{CompilationTimeMs: 8.262},
			wantTime:   "8.262",
			wantCached: "",
		},
		{
			name:       "recorded cache miss is not absence",
			metrics:    ShaderMetrics{CompilationTimeMs: 1.5, FunctionWasCached: &cached},
			wantTime:   "1.500",
			wantCached: "false",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := tt.metrics
			m.Name = "kernel"
			var buf bytes.Buffer
			if err := ExportShaderMetricsCSV(&buf, &ShaderMetricsReport{Shaders: []*ShaderMetrics{&m}}); err != nil {
				t.Fatal(err)
			}
			rows, err := csv.NewReader(&buf).ReadAll()
			if err != nil {
				t.Fatal(err)
			}
			if got, _ := csvCell(rows, "Compilation Time (ms)"); got != tt.wantTime {
				t.Errorf("Compilation Time (ms) = %q, want %q", got, tt.wantTime)
			}
			if got, _ := csvCell(rows, "Function Was Cached"); got != tt.wantCached {
				t.Errorf("Function Was Cached = %q, want %q", got, tt.wantCached)
			}
		})
	}
}

func TestExportShaderMetricsCSVCompilerStatCells(t *testing.T) {
	tests := []struct {
		name    string
		metrics ShaderMetrics
		want    []string // temp regs, spilled, device load, device store
	}{
		{
			name: "present",
			metrics: ShaderMetrics{
				AllocatedRegisters: 30,
				SpilledBytes:       4,
				DeviceLoadCount:    30,
				DeviceStoreCount:   0,
				HasPipelineStats:   true,
			},
			want: []string{"30", "4", "30", "0"},
		},
		{
			name:    "absent leaves cells empty",
			metrics: ShaderMetrics{},
			want:    []string{"", "", "", ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := tt.metrics
			m.Name = "kernel"
			report := &ShaderMetricsReport{Shaders: []*ShaderMetrics{&m}}

			var buf bytes.Buffer
			if err := ExportShaderMetricsCSV(&buf, report); err != nil {
				t.Fatal(err)
			}
			rows, err := csv.NewReader(&buf).ReadAll()
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 2 {
				t.Fatalf("got %d CSV rows, want 2", len(rows))
			}
			// Located by header rather than by offset from the end: the
			// columns these assert are a named set, not the last four,
			// and a test that says "last four" fails whenever an
			// unrelated column is appended.
			columns := []string{"Temporary Registers", "Spilled Bytes", "Device Load Count", "Device Store Count"}
			for i, name := range columns {
				got, ok := csvCell(rows, name)
				if !ok {
					t.Fatalf("no %q column in header %q", name, rows[0])
				}
				if got != tt.want[i] {
					t.Errorf("column %q = %q, want %q", name, got, tt.want[i])
				}
			}
		})
	}
}

func TestApplyPipelineStatsToMetricsCarriesDeviceCounts(t *testing.T) {
	var m ShaderMetrics
	applyPipelineStatsToMetrics(&m, &counter.PipelineStats{
		TemporaryRegisterCount: 30,
		DeviceLoadCount:        30,
		DeviceStoreCount:       1,
	})
	if !m.HasPipelineStats {
		t.Error("HasPipelineStats = false, want true")
	}
	if m.DeviceLoadCount != 30 || m.DeviceStoreCount != 1 {
		t.Errorf("device counts = (%d, %d), want (30, 1)", m.DeviceLoadCount, m.DeviceStoreCount)
	}
}
