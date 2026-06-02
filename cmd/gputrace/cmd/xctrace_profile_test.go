//go:build darwin

package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseXctraceTOCExtractsRunsAndSchemas(t *testing.T) {
	runs, schemas := parseXctraceTOC(filepath.Join("testdata", "xctrace_toc.xml"))
	if !reflect.DeepEqual(runs, []int{1, 2}) {
		t.Fatalf("runs = %#v, want [1 2]", runs)
	}
	wantSchemas := []string{"cpu-samples", "gpu-counter-value", "metal-gpu-intervals"}
	if !reflect.DeepEqual(schemas, wantSchemas) {
		t.Fatalf("schemas = %#v, want %#v", schemas, wantSchemas)
	}
}

func TestXctraceProfileSchemasAddsDiscoveredMetalAndGPUSchemas(t *testing.T) {
	got := xctraceProfileSchemas(xctraceProfileTOCSummary{
		Schemas: []string{
			"cpu-samples",
			"metal-application-encoders-list",
			"gpu-extra-table",
			"metal-gpu-intervals",
		},
	})
	for _, want := range []string{
		"metal-gpu-intervals",
		"metal-application-encoders-list",
		"gpu-extra-table",
	} {
		if !containsString(got, want) {
			t.Fatalf("schemas %#v missing %q", got, want)
		}
	}
	if containsString(got, "cpu-samples") {
		t.Fatalf("schemas %#v unexpectedly included cpu-samples", got)
	}
}

func TestWriteXctraceIntervalRowsJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "xctrace-interval-rows.json")
	rows := []xctraceIntervalRow{{
		StartNs:         100,
		DurationNs:      25,
		Process:         "target-app",
		Label:           "Command Buffer 0:Compute Command 1",
		CommandBufferID: 7,
		EncoderID:       9,
	}}
	if err := writeXctraceIntervalRowsJSON(path, rows); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got []xctraceIntervalRow
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, rows) {
		t.Fatalf("rows = %#v, want %#v", got, rows)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
