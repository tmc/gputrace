package gpuevent

import (
	"reflect"
	"strings"
	"testing"
)

func streamKernel(start, end uint64, stream uint32) Event {
	return Event{Kind: KindKernel, Name: "k", StartNS: start, EndNS: end, StreamID: stream}
}

func stackNames(spans []Span) []string {
	names := make([]string, len(spans))
	for i, s := range spans {
		names[i] = s.Name
	}
	return names
}

func TestSpanStacksOrdersOutermostFirst(t *testing.T) {
	spans := []Span{
		{Name: "attention", StartNS: 40, EndNS: 60},
		{Name: "generate", StartNS: 0, EndNS: 1000},
		{Name: "layer_17", StartNS: 30, EndNS: 90},
		{Name: "decode_step", StartNS: 10, EndNS: 100},
	}
	events := []Event{streamKernel(45, 50, 7)}

	got := SpanStacks(events, spans)
	want := []string{"generate", "decode_step", "layer_17", "attention"}
	if !reflect.DeepEqual(stackNames(got[0]), want) {
		t.Errorf("stack = %v, want %v", stackNames(got[0]), want)
	}
}

func TestSpanStacksIndexesInStepWithEvents(t *testing.T) {
	// The result must line up with the input slice however the events are
	// ordered, because callers index it by position.
	spans := []Span{{Name: "outer", StartNS: 0, EndNS: 100}}
	events := []Event{
		streamKernel(80, 90, 1),
		streamKernel(200, 210, 1), // after the span
		streamKernel(10, 20, 1),
	}
	got := SpanStacks(events, spans)
	if len(got[0]) != 1 || got[0][0].Name != "outer" {
		t.Errorf("events[0] stack = %v, want [outer]", stackNames(got[0]))
	}
	if len(got[1]) != 0 {
		t.Errorf("events[1] stack = %v, want none", stackNames(got[1]))
	}
	if len(got[2]) != 1 || got[2][0].Name != "outer" {
		t.Errorf("events[2] stack = %v, want [outer]", stackNames(got[2]))
	}
}

func TestSpanStacksRequiresFullContainment(t *testing.T) {
	spans := []Span{{Name: "partial", StartNS: 0, EndNS: 50}}
	// The kernel starts inside the span but ends after it. A span that does
	// not cover the whole kernel did not contain its work.
	got := SpanStacks([]Event{streamKernel(40, 80, 1)}, spans)
	if len(got[0]) != 0 {
		t.Errorf("stack = %v, want none for a kernel outliving the span", stackNames(got[0]))
	}
}

func TestSpanStacksHonorsDeclaredStreams(t *testing.T) {
	spans := []Span{
		{Name: "stream3only", StartNS: 0, EndNS: 100, Streams: []int64{3}},
		{Name: "anystream", StartNS: 0, EndNS: 100},
	}
	got := SpanStacks([]Event{streamKernel(10, 20, 3), streamKernel(10, 20, 9)}, spans)
	if want := []string{"stream3only", "anystream"}; !reflect.DeepEqual(stackNames(got[0]), want) {
		t.Errorf("stream 3 stack = %v, want %v", stackNames(got[0]), want)
	}
	if want := []string{"anystream"}; !reflect.DeepEqual(stackNames(got[1]), want) {
		t.Errorf("stream 9 stack = %v, want %v", stackNames(got[1]), want)
	}
}

func TestSpanStacksHandlesNonNestedSpans(t *testing.T) {
	// Two producers can write spans that overlap without nesting. A stack
	// walk that assumes a well-formed nesting drops one of them; both
	// contain the kernel, so both belong on the stack.
	spans := []Span{
		{Name: "a", StartNS: 0, EndNS: 60},
		{Name: "b", StartNS: 10, EndNS: 100},
	}
	got := SpanStacks([]Event{streamKernel(20, 30, 1)}, spans)
	if want := []string{"a", "b"}; !reflect.DeepEqual(stackNames(got[0]), want) {
		t.Errorf("stack = %v, want %v", stackNames(got[0]), want)
	}
}

func TestSpanStacksWithNoSpans(t *testing.T) {
	got := SpanStacks([]Event{streamKernel(1, 2, 1)}, nil)
	if len(got) != 1 || got[0] != nil {
		t.Errorf("stacks = %v, want one nil chain", got)
	}
}

func TestIdleAfterIsPerStream(t *testing.T) {
	// Two streams interleave. A global gap computation would report zero
	// idle on both, because the other stream is always running.
	events := []Event{
		{Kind: KindKernel, Name: "s1a", StartNS: 0, EndNS: 10, StreamID: 1},
		{Kind: KindKernel, Name: "s2a", StartNS: 5, EndNS: 25, StreamID: 2},
		{Kind: KindKernel, Name: "s1b", StartNS: 30, EndNS: 40, StreamID: 1},
		{Kind: KindKernel, Name: "s2b", StartNS: 100, EndNS: 110, StreamID: 2},
	}
	got := IdleAfter(events)
	want := []uint64{20, 75, 0, 0}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("IdleAfter = %v, want %v", got, want)
	}
}

func TestIdleAfterClosesOnAnyActivity(t *testing.T) {
	// A stream running a copy is busy. Counting that interval as idle
	// would inflate the number this exists to measure.
	events := []Event{
		{Kind: KindKernel, StartNS: 0, EndNS: 10, StreamID: 1},
		{Kind: KindMemcpy, StartNS: 12, EndNS: 40, StreamID: 1},
		{Kind: KindKernel, StartNS: 50, EndNS: 60, StreamID: 1},
	}
	got := IdleAfter(events)
	if got[0] != 2 {
		t.Errorf("idle after the first kernel = %d, want 2 (the copy closes the gap)", got[0])
	}
	if got[1] != 10 {
		t.Errorf("idle after the copy = %d, want 10", got[1])
	}
}

func TestIdleAfterChargesTheActivityThatEndedLast(t *testing.T) {
	// A long kernel is still running when a short later-starting one both
	// begins and ends. The stream goes idle after the long one, so the gap
	// is charged there, not to whichever record started last.
	events := []Event{
		{Kind: KindKernel, Name: "long", StartNS: 0, EndNS: 100, StreamID: 1},
		{Kind: KindKernel, Name: "short", StartNS: 10, EndNS: 20, StreamID: 1},
		{Kind: KindKernel, Name: "next", StartNS: 150, EndNS: 160, StreamID: 1},
	}
	got := IdleAfter(events)
	if got[0] != 50 {
		t.Errorf("idle after the long kernel = %d, want 50", got[0])
	}
	if got[1] != 0 {
		t.Errorf("idle after the enclosed short kernel = %d, want 0", got[1])
	}
}

func TestIdleAfterLastActivityIsZero(t *testing.T) {
	// Nothing measured follows the last activity, so the capture cannot
	// say whether the stream idled or the capture simply ended.
	events := []Event{{Kind: KindKernel, StartNS: 0, EndNS: 10, StreamID: 1}}
	if got := IdleAfter(events); got[0] != 0 {
		t.Errorf("idle after the only activity = %d, want 0", got[0])
	}
}

func TestSpanStacksAfterClockTranslation(t *testing.T) {
	// End to end through DecodeJSONL: a unix-stamped span and a
	// CUPTI-stamped kernel only meet after the clock_sync record is
	// applied. Without it the span encloses nothing.
	const withSync = `{"kind":"clock_sync","unix_ns":1000000,"cupti_ns":5000000}
{"kind":"span","name":"decode","start_ns":1000100,"end_ns":1000900,"clock":"unix"}
{"kind":"kernel","raw_symbol":"_Z1kv","start_ns":5000400,"end_ns":5000500,"stream_id":7}
`
	cap, err := DecodeJSONL(strings.NewReader(withSync))
	if err != nil {
		t.Fatalf("DecodeJSONL: %v", err)
	}
	got := SpanStacks(cap.Events, cap.Spans)
	if len(got[0]) != 1 || got[0][0].Name != "decode" {
		t.Fatalf("stack = %v, want [decode]; the clock_sync translation did not apply", stackNames(got[0]))
	}

	const withoutSync = `{"kind":"span","name":"decode","start_ns":1000100,"end_ns":1000900,"clock":"unix"}
{"kind":"kernel","raw_symbol":"_Z1kv","start_ns":5000400,"end_ns":5000500,"stream_id":7}
`
	cap, err = DecodeJSONL(strings.NewReader(withoutSync))
	if err != nil {
		t.Fatalf("DecodeJSONL: %v", err)
	}
	if got := SpanStacks(cap.Events, cap.Spans); len(got[0]) != 0 {
		t.Errorf("stack = %v, want none: an untranslated span must attribute nothing rather than the wrong kernels", stackNames(got[0]))
	}
}
