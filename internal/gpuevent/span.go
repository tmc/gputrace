package gpuevent

import (
	"encoding/json"
	"fmt"
	"sort"
)

// Attribution values describe how a kernel was joined to its span. The
// honest-provenance rule applies: every attributed kernel carries the
// method, so downstream claims are as strong as the evidence.
const (
	// AttributionTemporal joins a kernel to the tightest containing span
	// on the same stream (any stream when the span declares none).
	AttributionTemporal = "temporal"
)

// Span is an application-declared interval bracketing one eval or phase.
// Producers: mlx-go's EvalWithLabel/EvalCtx path, or anything appending
// to the GPUTRACE_APP_EVENTS sidecar. Timestamps share the capture's
// clock domain (see ClockSync).
type Span struct {
	Name    string            `json:"name"`
	StartNS uint64            `json:"start_ns"`
	EndNS   uint64            `json:"end_ns"`
	Labels  map[string]string `json:"labels,omitempty"`
	EvalSeq uint64            `json:"eval_seq,omitempty"`
	Streams []int64           `json:"streams,omitempty"`
}

// AttributedKernel is one kernel joined to a span.
type AttributedKernel struct {
	Event
	Attribution string `json:"attribution"`
}

// AttributedSpan pairs a span with the kernels temporally joined to it,
// ordered by kernel start time.
type AttributedSpan struct {
	Span
	Kernels []AttributedKernel `json:"kernels,omitempty"`
}

// decodeSpan parses a span record.
func decodeSpan(data []byte) (Span, error) {
	var s Span
	if err := json.Unmarshal(data, &s); err != nil {
		return Span{}, err
	}
	if s.Name == "" || s.EndNS == 0 {
		return Span{}, fmt.Errorf("span record missing name or end timestamp")
	}
	return s, nil
}

// AttributeSpans joins kernels to spans and returns spans ordered by
// start time. Join rule: a kernel belongs to the tightest containing
// span — smallest interval whose [StartNS,EndNS] contains the kernel's
// [StartNS,EndNS] — among spans that declare no streams or declare the
// kernel's stream. Kernels matching no span stay only in Capture.Events;
// attribution never removes events.
func AttributeSpans(cap Capture) []AttributedSpan {
	spans := make([]AttributedSpan, len(cap.Spans))
	for i, s := range cap.Spans {
		spans[i] = AttributedSpan{Span: s}
	}
	// Tightest = smallest duration; ties broken by later start (innermost).
	sort.Slice(spans, func(i, j int) bool {
		di := spans[i].EndNS - spans[i].StartNS
		dj := spans[j].EndNS - spans[j].StartNS
		if di != dj {
			return di < dj
		}
		return spans[i].StartNS > spans[j].StartNS
	})
	for _, e := range cap.Events {
		if e.Kind != KindKernel {
			continue
		}
		for si := range spans {
			s := &spans[si]
			if e.StartNS < s.StartNS || e.EndNS > s.EndNS {
				continue // not contained
			}
			if !s.declaresStream(e.StreamID) {
				continue
			}
			s.Kernels = append(s.Kernels, AttributedKernel{Event: e, Attribution: AttributionTemporal})
			break // tightest match wins; do not double-attribute
		}
	}
	for i := range spans {
		k := spans[i].Kernels
		sort.Slice(k, func(a, b int) bool { return k[a].StartNS < k[b].StartNS })
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].StartNS < spans[j].StartNS })
	return spans
}

// declaresStream reports whether the span covers the given stream. A span
// with no declared streams covers every stream (legacy producers).
func (s *AttributedSpan) declaresStream(streamID uint32) bool {
	if len(s.Streams) == 0 {
		return true
	}
	for _, st := range s.Streams {
		if st >= 0 && uint32(st) == streamID {
			return true
		}
	}
	return false
}

// Decompose derives luminal-style per-span host/device phases from real
// timestamps. Every field names its source records so provenance is
// checkable rather than asserted:
//
//	SetupNS [D]: first launch-API record start − span start. Lower
//	  confidence when no API records exist; falls back to first kernel
//	  device start − span start with Confidence "device-fallback".
//	LaunchLatencyNS [D]: first kernel device start − its API record end
//	  (submission-to-device latency). Zero-meaningful only with API records.
//	GPUTimeNS [V]: sum of attributed kernel durations (measured).
//	TailNS [D]: span end − last kernel device end.
//
// A span with no attributed kernels reports all phases zero and
// Confidence "no-kernels".
type SpanDecomposition struct {
	SpanName        string `json:"span_name"`
	EvalSeq         uint64 `json:"eval_seq,omitempty"`
	SetupNS         uint64 `json:"setup_ns"`
	SetupSource     string `json:"setup_source"` // "api" | "device-fallback"
	LaunchLatencyNS int64  `json:"launch_latency_ns"`
	HasLaunchAPI    bool   `json:"has_launch_api"`
	GPUTimeNS       uint64 `json:"gpu_time_ns"`
	TailNS          uint64 `json:"tail_ns"`
	KernelCount     int    `json:"kernel_count"`
	Confidence      string `json:"confidence"`
}

// Decompose computes the per-span breakdown for one attributed span. apiByCorrelation
// indexes runtime/driver API records by correlation ID; pass nil when the capture
// has none.
func (s *AttributedSpan) Decompose(apiByCorrelation map[uint64]APIEvent) SpanDecomposition {
	out := SpanDecomposition{
		SpanName:    s.Name,
		EvalSeq:     s.EvalSeq,
		Confidence:  "no-kernels",
		KernelCount: len(s.Kernels),
	}
	if len(s.Kernels) == 0 {
		return out
	}
	first := s.Kernels[0]
	last := s.Kernels[len(s.Kernels)-1]

	out.Confidence = "derived"
	if api, ok := apiByCorrelation[first.CorrelationID]; ok && api.StartNS >= s.StartNS && api.StartNS <= first.StartNS {
		out.SetupNS = api.StartNS - s.StartNS
		out.SetupSource = "api"
		out.HasLaunchAPI = true
		out.LaunchLatencyNS = int64(first.StartNS - api.EndNS)
	} else {
		out.SetupNS = first.StartNS - s.StartNS
		out.SetupSource = "device-fallback"
	}
	for _, k := range s.Kernels {
		out.GPUTimeNS += k.DurationNS()
	}
	if s.EndNS > last.EndNS {
		out.TailNS = s.EndNS - last.EndNS
	}
	return out
}

// Decompositions computes the breakdown for every span with kernels.
func Decompositions(spans []AttributedSpan, apis []APIEvent) []SpanDecomposition {
	apiByCorrelation := make(map[uint64]APIEvent, len(apis))
	for _, a := range apis {
		apiByCorrelation[a.CorrelationID] = a
	}
	out := make([]SpanDecomposition, 0, len(spans))
	for i := range spans {
		out = append(out, spans[i].Decompose(apiByCorrelation))
	}
	return out
}
