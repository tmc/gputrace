package gpuevent

import (
	"strings"
	"testing"
)

const sampleJSONL = `{"kind":"kernel","name":"void mlx::qmv_kernel<8>","raw_symbol":"_ZN3mlx10qmv","start_ns":1000,"end_ns":25000,"grid":"112x1x1","block":"32x8x1","registers":40}
{"kind":"memcpy","start_ns":26000,"end_ns":30000,"bytes":9216}
{"kind":"memset","start_ns":31000,"end_ns":32000,"bytes":4096}
{"timestamp_ns":500,"power_mw":7103,"gpu_util_pct":96,"mem_util_pct":12,"temp_c":44,"mem_used_bytes":2147483648}
{"kind":"kernel","raw_symbol":"_Ztruncated`

// last line is a partial record from a tracer killed mid-write.

func TestDecodeJSONL(t *testing.T) {
	cap, err := DecodeJSONL(strings.NewReader(sampleJSONL))
	if err != nil {
		t.Fatalf("DecodeJSONL: %v", err)
	}
	if got := len(cap.Events); got != 3 {
		t.Fatalf("events = %d, want 3", got)
	}
	if got := len(cap.Samples); got != 1 {
		t.Fatalf("samples = %d, want 1", got)
	}
	k := cap.Events[0]
	if k.Kind != KindKernel || k.Name == "" || k.RawSymbol != "_ZN3mlx10qmv" {
		t.Errorf("kernel decode = %+v", k)
	}
	if k.DurationNS() != 24000 {
		t.Errorf("DurationNS = %d, want 24000", k.DurationNS())
	}
	m := cap.Events[1]
	if m.Kind != KindMemcpy || m.Bytes != 9216 {
		t.Errorf("memcpy decode = %+v", m)
	}
	s := cap.Samples[0]
	if s.PowerMW != 7103 || s.GPUUtilPct != 96 {
		t.Errorf("sample decode = %+v", s)
	}
}

func TestDecodeJSONLToleratesGarbageLines(t *testing.T) {
	r := strings.NewReader("not json\n{\"kind\":\"kernel\",\"raw_symbol\":\"x\",\"start_ns\":1,\"end_ns\":2}\n\n")
	cap, err := DecodeJSONL(r)
	if err != nil {
		t.Fatalf("DecodeJSONL: %v", err)
	}
	if len(cap.Events) != 1 {
		t.Fatalf("events = %d, want 1 (garbage and blank dropped)", len(cap.Events))
	}
}

func TestNormalize(t *testing.T) {
	cap, _ := DecodeJSONL(strings.NewReader(sampleJSONL))
	origin := cap.Normalize()
	if origin != 500 {
		t.Fatalf("origin = %d, want 500 (earliest is the sample)", origin)
	}
	if cap.Events[0].StartNS != 500 {
		t.Errorf("event[0].StartNS = %d, want 500", cap.Events[0].StartNS)
	}
	if cap.Samples[0].TimestampNS != 0 {
		t.Errorf("sample.TimestampNS = %d, want 0", cap.Samples[0].TimestampNS)
	}
	// Normalizing twice must be idempotent.
	again := cap.Normalize()
	if again != 0 {
		t.Errorf("second Normalize origin = %d, want 0", again)
	}
}

func TestNormalizeEmpty(t *testing.T) {
	var cap Capture
	if origin := cap.Normalize(); origin != 0 {
		t.Errorf("empty Normalize origin = %d, want 0", origin)
	}
}

func TestEventString(t *testing.T) {
	e := Event{Kind: KindKernel, Name: "k", Grid: "2x1x1", Block: "32x1x1"}
	if s := e.String(); !strings.Contains(s, "kernel k [2x1x1..32x1x1]") {
		t.Errorf("String = %q", s)
	}
	dash := Event{Kind: KindMemcpy}
	if s := dash.String(); !strings.Contains(s, "-") {
		t.Errorf("missing geometry dash in %q", s)
	}
}

func TestDecodeAPIAndMeta(t *testing.T) {
	r := strings.NewReader(`{"kind":"capture_meta","concurrent_kernel":true,"pid":123}
{"kind":"clock_sync","unix_ns":100,"cupti_ns":90}
{"kind":"api","api":"runtime","name":"cudaLaunchKernel","cbid":211,"start_ns":200,"end_ns":300,"thread_id":7,"correlation_id":5}
{"kind":"kernel","name":"k","start_ns":350,"end_ns":1350,"grid":"1x1x1","block":"32x1x1","correlation_id":5}
`)
	cap, err := DecodeJSONL(r)
	if err != nil {
		t.Fatal(err)
	}
	if cap.Meta == nil || !cap.Meta.ConcurrentKernel || cap.Meta.PID != 123 {
		t.Errorf("meta = %+v", cap.Meta)
	}
	if cap.ClockSync == nil || cap.ClockSync.CuptiNS != 90 {
		t.Errorf("clock sync = %+v", cap.ClockSync)
	}
	if len(cap.APIs) != 1 || cap.APIs[0].Name != "cudaLaunchKernel" {
		t.Fatalf("apis = %+v", cap.APIs)
	}
	oh := LaunchOverheadAnalysis(cap)
	if oh.Joins != 1 {
		t.Fatalf("joins = %d, want 1", oh.Joins)
	}
	if oh.MeanHostCostNS != 100 {
		t.Errorf("host cost = %d, want 100", oh.MeanHostCostNS)
	}
	if oh.TotalGPUNS != 1000 {
		t.Errorf("gpu total = %d, want 1000", oh.TotalGPUNS)
	}
}
