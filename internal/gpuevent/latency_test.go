package gpuevent

import (
	"strings"
	"testing"
)

// kernelWithLatency builds a kernel as it arrives from a capture, so the
// tests exercise the same validity gate the decoder applies.
func kernelWithLatency(start, queued, submitted uint64) Event {
	return Event{
		Kind:    KindKernel,
		Name:    "k",
		StartNS: start,
		EndNS:   start + 100,
		Latency: decodeLatency(queued, submitted, start),
	}
}

func TestValidLatency(t *testing.T) {
	tests := []struct {
		name  string
		event Event
		want  bool
	}{
		{"ordered", kernelWithLatency(1000, 800, 900), true},
		{"unknown queued", kernelWithLatency(1000, 0, 900), false},
		{"unknown submitted", kernelWithLatency(1000, 800, 0), false},
		{"stale queued past start", kernelWithLatency(1000, 2000, 0), false},
		{"submitted after start", kernelWithLatency(1000, 800, 1100), false},
		{"queued after submitted", kernelWithLatency(1000, 950, 900), false},
		{"not a kernel", Event{Kind: KindMemcpy, StartNS: 1000, EndNS: 1100, Latency: decodeLatency(800, 900, 1000)}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.event.ValidLatency(); got != tt.want {
				t.Errorf("ValidLatency() = %v, want %v", got, tt.want)
			}
		})
	}
}

// A capture where most kernels carry a stale queued timestamp and no
// submitted one is the observed GB10 CUDA-graph case: the raw fields imply
// a sub-second "latency" that is an artifact, so the analysis must refuse
// to report a figure rather than average the garbage in.
func TestLaunchLatencyRefusesStaleTimestamps(t *testing.T) {
	events := []Event{kernelWithLatency(2_000_000_000, 1_999_000_000, 1_999_500_000)}
	for i := 0; i < 50; i++ {
		e := kernelWithLatency(3_000_000_000+uint64(i)*1000, 1_800_000_000, 0)
		events = append(events, e)
	}
	got := LaunchLatencyAnalysis(events)
	if got.Usable {
		t.Fatalf("Usable = true with %.1f%% coverage; want refusal", got.CoveragePct)
	}
	if got.Timed != 1 || got.Rejected != 50 {
		t.Errorf("Timed=%d Rejected=%d, want 1 and 50", got.Timed, got.Rejected)
	}
	if !strings.Contains(got.Reason, "latency timestamps") {
		t.Errorf("Reason = %q, want it to explain the missing timestamps", got.Reason)
	}
}

func TestLaunchLatencyReportsCoveredCapture(t *testing.T) {
	var events []Event
	for i := 0; i < 10; i++ {
		start := 1000 + uint64(i)*1000
		events = append(events, kernelWithLatency(start, start-500, start-100))
	}
	got := LaunchLatencyAnalysis(events)
	if !got.Usable {
		t.Fatalf("Usable = false (%s); want a usable result", got.Reason)
	}
	if got.CoveragePct != 100 {
		t.Errorf("CoveragePct = %v, want 100", got.CoveragePct)
	}
	if got.QueueToSubmitNS.P50NS != 400 {
		t.Errorf("queue->submit p50 = %d, want 400", got.QueueToSubmitNS.P50NS)
	}
	if got.SubmitToStartNS.P50NS != 100 {
		t.Errorf("submit->start p50 = %d, want 100", got.SubmitToStartNS.P50NS)
	}
	if got.QueueToStartNS.MeanNS != 500 {
		t.Errorf("queue->start mean = %d, want 500", got.QueueToStartNS.MeanNS)
	}
}

// Latency must survive normalization intact: it is stored as durations
// precisely so that shifting the clock cannot corrupt it, which is what
// produced 1.16 s of phantom launch latency when absolute timestamps were
// read next to normalized start times.
func TestNormalizePreservesLatency(t *testing.T) {
	const origin = 1_787_716_468_626_309_909
	cap := Capture{Events: []Event{
		kernelWithLatency(origin+10_000, origin, origin+9_000),
		kernelWithLatency(origin+50_000, origin-1_162_307_115, 0), // stale queued, no submit
	}}
	if got := cap.Normalize(); got != origin+10_000 {
		t.Fatalf("Normalize() origin = %d, want %d", got, origin+10_000)
	}
	first := cap.Events[0]
	if !first.ValidLatency() {
		t.Fatal("first event lost its valid latency across normalization")
	}
	if got, want := first.Latency.QueueToStartNS(), uint64(10_000); got != want {
		t.Errorf("queue->start = %d, want %d", got, want)
	}
	if second := cap.Events[1]; second.ValidLatency() {
		t.Errorf("stale timestamps accepted: %+v", second.Latency)
	}
}
