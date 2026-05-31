package cmd

import (
	"encoding/csv"
	"encoding/json"
	"strings"
	"testing"

	"github.com/tmc/gputrace"
)

func TestWriteShadersNoCostHonorsJSONFormat(t *testing.T) {
	report := testShaderMetricsReport()
	oldFormat := shadersFormat
	shadersFormat = "json"
	t.Cleanup(func() { shadersFormat = oldFormat })

	out, err := captureStdout(t, func() error {
		return writeShadersNoCost(report, "trace.gputrace")
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
	oldFormat := shadersFormat
	shadersFormat = "csv"
	t.Cleanup(func() { shadersFormat = oldFormat })

	out, err := captureStdout(t, func() error {
		return writeShadersNoCost(report, "trace.gputrace")
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
