//go:build darwin

package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeStandaloneExportFixture(t *testing.T, name, uuid string, full bool) string {
	t.Helper()
	bundle := filepath.Join(t.TempDir(), name+".gputrace")
	profilerDir := filepath.Join(bundle, name+".gputrace.gpuprofiler_raw")
	if err := os.MkdirAll(profilerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	metadata := `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict><key>(uuid)</key><string>` + uuid + `</string></dict></plist>`
	files := map[string]string{
		"metadata": metadata,
		filepath.Join(filepath.Base(profilerDir), "streamData"): "profiler",
	}
	if full {
		files["capture"] = "capture"
		files["MTLBuffer-1-0"] = "raw resource"
	}
	for path, data := range files {
		if err := os.WriteFile(filepath.Join(bundle, path), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return bundle
}

func TestFinalizeStandaloneExportRequiresBoundIdentity(t *testing.T) {
	output := writeStandaloneExportFixture(t, "output", "same", true)
	var status bytes.Buffer
	_, err := finalizeStandaloneExport(&status, "", output)
	if err == nil || !strings.Contains(err.Error(), "no AXDocument binding") {
		t.Fatalf("error = %v, want unbound identity error", err)
	}
	if strings.Contains(status.String(), "Exported to:") {
		t.Fatalf("unbound export printed success:\n%s", status.String())
	}
}

func TestFinalizeStandaloneExportRejectsAndPreservesProfilerOnly(t *testing.T) {
	input := writeStandaloneExportFixture(t, "input", "same", true)
	output := writeStandaloneExportFixture(t, "output", "same", false)
	var status bytes.Buffer
	payload, err := finalizeStandaloneExport(&status, input, output)
	if err == nil || !strings.Contains(err.Error(), "not self-contained") {
		t.Fatalf("error = %v, want self-contained rejection", err)
	}
	if payload.Class != "profiler-only" || !payload.HasProfilerStream {
		t.Fatalf("payload = %+v, want usable profiler-only", payload)
	}
	if !strings.Contains(status.String(), "profiler-only (not self-contained)") {
		t.Fatalf("status missing payload classification:\n%s", status.String())
	}
	if strings.Contains(status.String(), "Exported to:") {
		t.Fatalf("rejected export printed success:\n%s", status.String())
	}
	if _, err := os.Stat(output); err != nil {
		t.Fatalf("rejected profiler-only output was not preserved: %v", err)
	}
}

func TestFinalizeStandaloneExportAcceptsFullPayloadFields(t *testing.T) {
	input := writeStandaloneExportFixture(t, "input", "same", true)
	output := writeStandaloneExportFixture(t, "output", "same", true)
	var status bytes.Buffer
	payload, err := finalizeStandaloneExport(&status, input, output)
	if err != nil {
		t.Fatalf("finalizeStandaloneExport: %v", err)
	}
	if !strings.Contains(status.String(), "full and self-contained") {
		t.Fatalf("status missing full classification:\n%s", status.String())
	}

	action := xcodeProfileActionOutput{Action: "export", Target: input, Output: output}
	applyXcodePayload(&action, payload)
	if action.PayloadClass != "full" ||
		action.SelfContained == nil || !*action.SelfContained ||
		action.ProfilerTimingAvailable == nil || !*action.ProfilerTimingAvailable ||
		action.StructuralAnalysisAvailable == nil || !*action.StructuralAnalysisAvailable {
		t.Fatalf("payload action fields = %+v", action)
	}
	data, err := json.Marshal(action)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{
		`"payload_class":"full"`,
		`"self_contained":true`,
		`"profiler_timing_available":true`,
		`"structural_analysis_available":true`,
	} {
		if !bytes.Contains(data, []byte(field)) {
			t.Fatalf("action JSON missing %s: %s", field, data)
		}
	}
}

func TestStandaloneExportTargetRequiresUniqueDocumentBinding(t *testing.T) {
	trace := "/Users/tmc/tmp/trace.gputrace"
	window, doc, err := standaloneExportTarget([]xcodeAXWindow{
		{Element: 1, Title: "Source", Document: "/Users/tmc/project/main.swift"},
		{Element: 2, Title: "Performance", Document: trace},
	})
	if err != nil {
		t.Fatal(err)
	}
	if window != 2 || doc != trace {
		t.Fatalf("target = (%d, %q)", window, doc)
	}

	_, _, err = standaloneExportTarget([]xcodeAXWindow{
		{Element: 2, Document: trace},
		{Element: 3, Document: "/Users/tmc/tmp/other.gputrace"},
	})
	if err == nil || !strings.Contains(err.Error(), "multiple .gputrace windows") {
		t.Fatalf("ambiguous target error = %v", err)
	}
}
