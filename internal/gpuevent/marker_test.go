package gpuevent

import (
	"strings"
	"testing"
)

func TestPairMarkers(t *testing.T) {
	markers := []Marker{
		{Phase: "start", Name: "decode", Domain: "mlx", MarkerID: 1, TimestampNS: 100},
		{Phase: "start", Name: "layer", MarkerID: 2, TimestampNS: 150},
		{Phase: "end", MarkerID: 2, TimestampNS: 180},
		{Phase: "end", MarkerID: 1, TimestampNS: 300},
		{Phase: "start", Name: "truncated", MarkerID: 3, TimestampNS: 400}, // no end
		{Phase: "end", MarkerID: 9, TimestampNS: 500},                      // no start
	}
	spans, unpaired := pairMarkers(markers)
	if len(spans) != 2 {
		t.Fatalf("spans = %d, want 2", len(spans))
	}
	if unpaired != 2 {
		t.Errorf("unpaired = %d, want 2 (one truncated start, one orphan end)", unpaired)
	}
	if spans[0].Name != "decode" || spans[0].StartNS != 100 || spans[0].EndNS != 300 {
		t.Errorf("outer span = %+v, want decode [100,300]", spans[0])
	}
	if spans[0].Source != SourceNVTX {
		t.Errorf("Source = %q, want %q", spans[0].Source, SourceNVTX)
	}
	if spans[0].Labels["domain"] != "mlx" {
		t.Errorf("domain label = %q, want mlx", spans[0].Labels["domain"])
	}
	if spans[1].Name != "layer" {
		t.Errorf("spans out of start order: %+v", spans)
	}
}

func TestDecodeJSONLPairsNVTXMarkers(t *testing.T) {
	const records = `{"kind":"marker","phase":"start","name":"decode","marker_id":1,"timestamp_ns":100}
{"kind":"kernel","raw_symbol":"k","start_ns":120,"end_ns":140,"stream_id":1}
{"kind":"marker","phase":"end","marker_id":1,"timestamp_ns":300}
`
	cap, err := DecodeJSONL(strings.NewReader(records))
	if err != nil {
		t.Fatal(err)
	}
	if len(cap.Spans) != 1 || cap.Spans[0].Name != "decode" {
		t.Fatalf("Spans = %+v, want one NVTX span named decode", cap.Spans)
	}
	if cap.UnpairedMarkers != 0 {
		t.Errorf("UnpairedMarkers = %d, want 0", cap.UnpairedMarkers)
	}
	// The marker record must not also land in Events.
	if len(cap.Events) != 1 {
		t.Errorf("Events = %d, want only the kernel", len(cap.Events))
	}
	spans := AttributeSpans(cap)
	if len(spans) != 1 || len(spans[0].Kernels) != 1 {
		t.Errorf("NVTX span did not attract the kernel it contains: %+v", spans)
	}
}
