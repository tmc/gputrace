package cmd

import (
	"encoding/csv"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/gputrace"
)

func TestValidateShadersFormatAcceptsKnownValues(t *testing.T) {
	for _, format := range []string{"text", "csv", "json"} {
		t.Run(format, func(t *testing.T) {
			if err := validateShadersFormat(format); err != nil {
				t.Fatalf("validateShadersFormat(%q): %v", format, err)
			}
		})
	}
}

func TestValidateShadersFormatRejectsUnknownValues(t *testing.T) {
	tests := []struct {
		name   string
		format string
		want   string
	}{
		{
			name:   "empty",
			format: "",
			want:   `invalid shaders format "" (must be text, csv, or json)`,
		},
		{
			name:   "xml",
			format: "xml",
			want:   `invalid shaders format "xml" (must be text, csv, or json)`,
		},
		{
			name:   "uppercase",
			format: "JSON",
			want:   `invalid shaders format "JSON" (must be text, csv, or json)`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateShadersFormat(tt.format)
			if err == nil {
				t.Fatal("validateShadersFormat succeeded, want error")
			}
			if err.Error() != tt.want {
				t.Fatalf("error = %q, want %q", err.Error(), tt.want)
			}
		})
	}
}

func TestRunShadersValidatesFormatBeforeTraceIO(t *testing.T) {
	missingTrace := filepath.Join(t.TempDir(), "missing.gputrace")
	err := runShaders(nil, []string{missingTrace}, &shadersOptions{
		format: "xml",
	})
	if err == nil {
		t.Fatal("runShaders succeeded, want error")
	}
	want := `invalid shaders format "xml" (must be text, csv, or json)`
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}

func TestWriteShadersNoCostHonorsJSONFormat(t *testing.T) {
	report := testShaderMetricsReport()

	out, err := captureStdout(t, func() error {
		return writeShadersNoCost(report, "trace.gputrace", &shadersOptions{
			format: "json",
		})
	})
	if err != nil {
		t.Fatalf("writeShadersNoCost: %v", err)
	}
	if strings.Contains(out, "Cost      Name") {
		t.Fatalf("json stdout contains text table:\n%s", out)
	}

	var got gputrace.ShaderMetricsReport
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("unmarshal json: %v\n%s", err, out)
	}
	if len(got.Shaders) != 1 || got.Shaders[0].Name != report.Shaders[0].Name {
		t.Fatalf("json shaders = %#v, want %q", got.Shaders, report.Shaders[0].Name)
	}
}

func TestWriteShadersNoCostHonorsCSVFormat(t *testing.T) {
	report := testShaderMetricsReport()

	out, err := captureStdout(t, func() error {
		return writeShadersNoCost(report, "trace.gputrace", &shadersOptions{
			format: "csv",
		})
	})
	if err != nil {
		t.Fatalf("writeShadersNoCost: %v", err)
	}
	if strings.Contains(out, "Cost      Name") {
		t.Fatalf("csv stdout contains text table:\n%s", out)
	}

	records, err := csv.NewReader(strings.NewReader(out)).ReadAll()
	if err != nil {
		t.Fatalf("read csv: %v\n%s", err, out)
	}
	if len(records) != 2 {
		t.Fatalf("csv record count = %d, want 2\n%s", len(records), out)
	}
	if records[1][0] != report.Shaders[0].Name {
		t.Fatalf("csv shader name = %q, want %q", records[1][0], report.Shaders[0].Name)
	}
}

func testShaderMetricsReport() *gputrace.ShaderMetricsReport {
	return &gputrace.ShaderMetricsReport{
		Shaders: []*gputrace.ShaderMetrics{
			{Name: "shader \"one\",two"},
		},
	}
}
