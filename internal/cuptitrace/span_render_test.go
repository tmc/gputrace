package cuptitrace

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/gputrace/internal/cupticapture"
	"github.com/tmc/gputrace/internal/gpuevent"
)

// spanBundleJSONL is a minimal bundle body exercising span rendering:
// one span with two attributed kernels (one carrying latency timestamps),
// one unattributed kernel that must stay on the flat tracks.
const spanBundleJSONL = `{"kind":"capture_meta","concurrent_kernel":true,"pid":1}
{"kind":"clock_sync","unix_ns":1000000000000,"cupti_ns":1000000000001}
{"kind":"span","name":"decode token 47","start_ns":500,"end_ns":3500,"labels":{"phase":"decode"},"eval_seq":47,"streams":[7]}
{"kind":"kernel","raw_symbol":"_Zgemv","name":"gemv<bf16>","start_ns":600,"end_ns":1600,"grid":"112x1x1","block":"32x8x1","registers":40,"correlation_id":5,"stream_id":7,"queued_ns":550,"submitted_ns":580}
{"kind":"kernel","raw_symbol":"_Zrmsnorm","name":"rms_norm","start_ns":1700,"end_ns":2000,"grid":"5x1x1","block":"128x1x1","registers":40,"correlation_id":6,"stream_id":7}
{"kind":"kernel","raw_symbol":"_Zorphan","name":"orphan_kernel","start_ns":4000,"end_ns":5000,"grid":"64x64x1","block":"16x16x1","correlation_id":9,"stream_id":7}
`

const plainBundleJSONL = `{"kind":"capture_meta","concurrent_kernel":true,"pid":2}
{"kind":"kernel","raw_symbol":"_Zsolo","name":"solo_kernel","start_ns":10,"end_ns":110,"grid":"1x1x1","block":"32x1x1","correlation_id":1,"stream_id":7}
`

const repeatedSpanNameBundleJSONL = `{"kind":"capture_meta","concurrent_kernel":true,"pid":3}
{"kind":"span","name":"token","start_ns":100,"end_ns":200,"eval_seq":1}
{"kind":"span","name":"token","start_ns":300,"end_ns":400,"eval_seq":2}
{"kind":"kernel","raw_symbol":"_Ztoken","name":"token_kernel","start_ns":120,"end_ns":180,"grid":"1x1x1","block":"32x1x1","correlation_id":1,"stream_id":7}
`

func writeBundle(t *testing.T, events string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(events), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func render(t *testing.T, dir string) (*bytes.Buffer, map[string]bool) {
	t.Helper()
	capData, err := ReadCapture(dir)
	if err != nil {
		t.Fatalf("read capture: %v", err)
	}
	for i := range capData.Events {
		if capData.Events[i].Name == "" {
			capData.Events[i].Name = capData.Events[i].RawSymbol
		}
	}
	trace, err := BuildCapture(capData, dir, Options{PerKernelTracks: true})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var buf bytes.Buffer
	if err := Write(trace, &buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	names := make(map[string]bool)
	for _, ev := range trace.Events {
		names[ev.Name] = true
	}
	_ = cupticapture.EventsFileName
	return &buf, names
}

func TestSpanRenderingGolden(t *testing.T) {
	dir := writeBundle(t, spanBundleJSONL)
	buf, _ := render(t, dir)

	got := buf.String()
	for _, want := range []string{
		"decode token 47",
		"app_span",
		"gemv<bf16>",
		"rms_norm",
		"orphan_kernel",
		"label.phase",
		"attribution",
	} {
		if !bytes.Contains([]byte(got), []byte(want)) {
			t.Errorf("rendered trace missing %q", want)
		}
	}

	// Structural expectations via the decoded model.
	capData, err := ReadCapture(dir)
	if err != nil {
		t.Fatal(err)
	}
	spans := gpuevent.AttributeSpans(capData)
	if len(spans) != 1 || len(spans[0].Kernels) != 2 {
		t.Fatalf("spans/kernels = %+v", spans)
	}
	if got := spans[0].Kernels[0].StartNS - spans[0].StartNS; got != 100 {
		t.Errorf("first kernel offset after normalize = %d, want 100", got)
	}
}

func TestPlainBundleUnchangedBySpanPath(t *testing.T) {
	dir := writeBundle(t, plainBundleJSONL)
	buf, names := render(t, dir)
	if !names["solo_kernel"] {
		t.Error("plain kernel missing from rendered trace")
	}
	for _, banned := range []string{"app_span", "Application spans", "span"} {
		if bytes.Contains(buf.Bytes(), []byte(banned)) {
			t.Errorf("span-only artifact %q leaked into a span-less capture", banned)
		}
	}
}

func TestRepeatedSpanNamesShareTrack(t *testing.T) {
	dir := writeBundle(t, repeatedSpanNameBundleJSONL)
	_, _ = render(t, dir)

	capData, err := ReadCapture(dir)
	if err != nil {
		t.Fatal(err)
	}
	trace, err := BuildCapture(capData, dir, Options{PerKernelTracks: true})
	if err != nil {
		t.Fatal(err)
	}
	var tracks int
	for _, track := range trace.Tracks {
		if track.Name == "token" {
			tracks++
		}
	}
	if tracks != 1 {
		t.Errorf("token tracks = %d, want 1", tracks)
	}
}
