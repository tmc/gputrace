package gpuevent

import (
	"reflect"
	"strings"
	"testing"
)

const spanJSONL = `{"kind":"capture_meta","concurrent_kernel":true,"pid":42}
{"kind":"clock_sync","unix_ns":1000,"cupti_ns":900}
{"kind":"span","name":"decode token 47","start_ns":500,"end_ns":2500,"labels":{"phase":"decode","token":"47"},"eval_seq":47,"streams":[3,7]}
{"kind":"kernel","name":"hot","start_ns":600,"end_ns":1600,"grid":"64x64x1","block":"16x16x1","correlation_id":5,"stream_id":3}
{"kind":"kernel","name":"cold","start_ns":1700,"end_ns":1900,"grid":"8x1x1","block":"32x1x1","correlation_id":6,"stream_id":7}
{"kind":"api","api":"runtime","name":"cudaLaunchKernel","cbid":211,"start_ns":550,"end_ns":590,"thread_id":7,"correlation_id":5}
`

func TestDecodeSpanRecords(t *testing.T) {
	cap, err := DecodeJSONL(strings.NewReader(spanJSONL))
	if err != nil {
		t.Fatal(err)
	}
	if len(cap.Spans) != 1 {
		t.Fatalf("spans = %d, want 1", len(cap.Spans))
	}
	s := cap.Spans[0]
	if s.Name != "decode token 47" {
		t.Errorf("name = %q", s.Name)
	}
	if s.StartNS != 500 || s.EndNS != 2500 {
		t.Errorf("interval = [%d,%d]", s.StartNS, s.EndNS)
	}
	want := map[string]string{"phase": "decode", "token": "47"}
	if !reflect.DeepEqual(s.Labels, want) {
		t.Errorf("labels = %v, want %v", s.Labels, want)
	}
	if s.EvalSeq != 47 {
		t.Errorf("eval_seq = %d", s.EvalSeq)
	}
	if !reflect.DeepEqual(s.Streams, []int64{3, 7}) {
		t.Errorf("streams = %v", s.Streams)
	}
}

func TestAttributeKernelsTemporal(t *testing.T) {
	cap, _ := DecodeJSONL(strings.NewReader(spanJSONL))
	spans := AttributeSpans(cap)
	if len(spans) != 1 {
		t.Fatalf("attributed spans = %d, want 1", len(spans))
	}
	s := spans[0]
	if len(s.Kernels) != 2 {
		t.Fatalf("kernels attributed = %d, want 2", len(s.Kernels))
	}
	// Ordered by start time within the span.
	if s.Kernels[0].Name != "hot" || s.Kernels[1].Name != "cold" {
		t.Errorf("order = [%s, %s]", s.Kernels[0].Name, s.Kernels[1].Name)
	}
	for _, k := range s.Kernels {
		if k.Attribution != "temporal" {
			t.Errorf("attribution = %q, want temporal", k.Attribution)
		}
	}
}

func TestAttributeSpansRespectsStream(t *testing.T) {
	// A kernel on stream 9 that falls inside the span's interval but is not
	// listed in the span's streams must not be attributed.
	r := strings.NewReader(`{"kind":"span","name":"s","start_ns":0,"end_ns":2000,"streams":[3]}
{"kind":"kernel","name":"in-window-wrong-stream","start_ns":100,"end_ns":200,"correlation_id":1,"stream_id":9}
`)
	cap, _ := DecodeJSONL(r)
	spans := AttributeSpans(cap)
	if len(spans) != 1 {
		t.Fatalf("spans = %d, want 1", len(spans))
	}
	if got := len(spans[0].Kernels); got != 0 {
		t.Errorf("wrong-stream kernel attributed: %d kernels on span %+v", got, spans[0])
	}
}

func TestAttributeUnattributedKernelsRemainInEvents(t *testing.T) {
	// Kernels outside every span keep appearing in Capture.Events; the
	// Perfetto flat tracks depend on that.
	cap, _ := DecodeJSONL(strings.NewReader(spanJSONL))
	n := 0
	for _, e := range cap.Events {
		if e.Kind == KindKernel {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("events kernels = %d, want 2 (spans do not remove events)", n)
	}
}

func TestSpanWithoutStreamsAttributedTemporally(t *testing.T) {
	// A span with no streams field (older producers) attributes any kernel
	// inside its interval regardless of stream.
	r := strings.NewReader(`{"kind":"span","name":"legacy","start_ns":0,"end_ns":3000}
{"kind":"kernel","name":"any","start_ns":100,"end_ns":200,"correlation_id":2,"stream_id":11}
`)
	cap, _ := DecodeJSONL(r)
	spans := AttributeSpans(cap)
	if len(spans) != 1 || len(spans[0].Kernels) != 1 {
		t.Fatalf("want 1 span with 1 kernel; got %+v", spans)
	}
}

func TestOverlappingSpansFirstMatchWins(t *testing.T) {
	r := strings.NewReader(`{"kind":"span","name":"outer","start_ns":0,"end_ns":4000,"streams":[3]}
{"kind":"span","name":"inner","start_ns":1000,"end_ns":2000,"streams":[3]}
{"kind":"kernel","name":"k","start_ns":1200,"end_ns":1500,"correlation_id":3,"stream_id":3}
`)
	cap, _ := DecodeJSONL(r)
	spans := AttributeSpans(cap)
	byName := map[string]AttributedSpan{}
	for _, s := range spans {
		byName[s.Name] = s
	}
	if got := len(byName["inner"].Kernels); got != 1 {
		t.Errorf("inner kernels = %d, want 1 (tightest containing span wins)", got)
	}
	if got := len(byName["outer"].Kernels); got != 0 {
		t.Errorf("outer kernels = %d, want 0", got)
	}
}
