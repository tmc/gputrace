package cuptiprofile

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/google/pprof/profile"

	"github.com/tmc/gputrace/internal/gpuevent"
)

// roundTrip writes and reparses a profile, so assertions run against what a
// reader actually gets rather than against the in-memory object.
func roundTrip(t *testing.T, p *profile.Profile) *profile.Profile {
	t.Helper()
	var buf bytes.Buffer
	if err := Write(p, &buf); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := profile.Parse(&buf)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return got
}

func sampleTypeNames(p *profile.Profile) []string {
	names := make([]string, len(p.SampleType))
	for i, st := range p.SampleType {
		names[i] = st.Type
	}
	return names
}

func totalOf(p *profile.Profile, sampleType string) int64 {
	index := -1
	for i, st := range p.SampleType {
		if st.Type == sampleType {
			index = i
		}
	}
	if index < 0 {
		return -1
	}
	var total int64
	for _, s := range p.Sample {
		total += s.Value[index]
	}
	return total
}

// leafNames maps each sample's leaf function name to how many samples had it.
func leafNames(p *profile.Profile) map[string]int {
	out := map[string]int{}
	for _, s := range p.Sample {
		out[s.Location[0].Line[0].Function.Name]++
	}
	return out
}

func rootNames(p *profile.Profile) map[string]int {
	out := map[string]int{}
	for _, s := range p.Sample {
		l := s.Location[len(s.Location)-1]
		out[l.Line[0].Function.Name]++
	}
	return out
}

func TestBuildSampleTypeOrderIsTheDiffContract(t *testing.T) {
	// pprof -diff_base requires both profiles to carry identical sample
	// type lists and reports a confusing mismatch rather than a diff when
	// they do not, so the order is pinned here rather than left to the
	// order the fields happen to be written in.
	p, _, err := Build(oneKernelCapture(), Options{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got := sampleTypeNames(roundTrip(t, p))
	want := []string{SampleGPUTime, SampleLaunchCount, SampleQueueDelay, SampleIdleAfter}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sample types = %v, want %v", got, want)
	}
	if p.DefaultSampleType != SampleGPUTime {
		t.Errorf("default sample type = %q, want %q", p.DefaultSampleType, SampleGPUTime)
	}
}

func oneKernelCapture() gpuevent.Capture {
	return gpuevent.Capture{Events: []gpuevent.Event{{
		Kind:      gpuevent.KindKernel,
		Name:      "saxpy",
		RawSymbol: "_Z5saxpyifPfS_",
		StartNS:   1000,
		EndNS:     1500,
		StreamID:  7,
	}}}
}

func TestBuildValuesComeFromTheRecords(t *testing.T) {
	cap := gpuevent.Capture{Events: []gpuevent.Event{
		{
			Kind: gpuevent.KindKernel, Name: "a", StartNS: 1000, EndNS: 1500, StreamID: 1,
			Latency: gpuevent.Latency{Known: true, QueueToSubmitNS: 30, SubmitToStartNS: 70},
		},
		{Kind: gpuevent.KindKernel, Name: "b", StartNS: 2000, EndNS: 2100, StreamID: 1},
	}}
	p, stats, err := Build(cap, Options{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got := roundTrip(t, p)

	if want := int64(600); totalOf(got, SampleGPUTime) != want {
		t.Errorf("gpu_time total = %d, want %d", totalOf(got, SampleGPUTime), want)
	}
	if want := int64(2); totalOf(got, SampleLaunchCount) != want {
		t.Errorf("launch_count total = %d, want %d", totalOf(got, SampleLaunchCount), want)
	}
	// queue_delay is the full queue-to-start latency of the one kernel that
	// carries a usable triple; the untimed one contributes nothing rather
	// than a guess.
	if want := int64(100); totalOf(got, SampleQueueDelay) != want {
		t.Errorf("queue_delay total = %d, want %d", totalOf(got, SampleQueueDelay), want)
	}
	// Kernel a ends at 1500, b starts at 2000, same stream.
	if want := int64(500); totalOf(got, SampleIdleAfter) != want {
		t.Errorf("idle_after total = %d, want %d", totalOf(got, SampleIdleAfter), want)
	}
	if stats.QueueTimed != 1 {
		t.Errorf("QueueTimed = %d, want 1", stats.QueueTimed)
	}
}

func TestBuildQueueDelayNeverUsesRawQueuedTimestamps(t *testing.T) {
	// The pathology this guards: reading queued_ns as an absolute stamp
	// against a start in another clock domain reports ~1.16 seconds of
	// launch latency. Latency carries durations derived inside one domain
	// at decode time, so the raw stamps are not reachable from here — and
	// a record whose stamps are inconsistent is rejected outright rather
	// than yielding a number.
	const records = `{"kind":"kernel","raw_symbol":"_Z1kv","start_ns":5000400,"end_ns":5000500,"queued_ns":1000,"submitted_ns":0}
{"kind":"kernel","raw_symbol":"_Z1kv","start_ns":6000400,"end_ns":6000500,"queued_ns":6000000,"submitted_ns":6000300}
`
	cap, err := gpuevent.DecodeJSONL(strings.NewReader(records))
	if err != nil {
		t.Fatalf("DecodeJSONL: %v", err)
	}
	p, stats, err := Build(cap, Options{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if stats.QueueTimed != 1 {
		t.Fatalf("QueueTimed = %d, want 1: the record with a zero submitted stamp must be rejected", stats.QueueTimed)
	}
	// 6000400 - 6000000 = 400ns, not the ~5s a raw cross-domain read gives.
	if got, want := totalOf(roundTrip(t, p), SampleQueueDelay), int64(400); got != want {
		t.Errorf("queue_delay total = %d, want %d", got, want)
	}
	if stats.MedianQueueNS != 400 {
		t.Errorf("MedianQueueNS = %d, want 400", stats.MedianQueueNS)
	}
}

func TestBuildStacksAtT1WhenTheCaptureHasNoSpans(t *testing.T) {
	p, stats, err := Build(oneKernelCapture(), Options{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got := roundTrip(t, p)
	for _, s := range got.Sample {
		if len(s.Location) != 1 {
			t.Fatalf("stack depth = %d, want 1: with no spans there is nothing to be outside of", len(s.Location))
		}
	}
	if want := map[string]int{"saxpy": 1}; !reflect.DeepEqual(leafNames(got), want) {
		t.Errorf("leaves = %v, want %v", leafNames(got), want)
	}
	if stats.SpanAttributed != 0 {
		t.Errorf("SpanAttributed = %d, want 0", stats.SpanAttributed)
	}
}

func TestBuildStacksAtT2AndKeepsUnattributedKernels(t *testing.T) {
	cap := gpuevent.Capture{
		Events: []gpuevent.Event{
			{Kind: gpuevent.KindKernel, Name: "inner_kernel", StartNS: 120, EndNS: 130, StreamID: 1},
			{Kind: gpuevent.KindKernel, Name: "loose_kernel", StartNS: 900, EndNS: 910, StreamID: 1},
		},
		Spans: []gpuevent.Span{
			{Name: "generate", StartNS: 0, EndNS: 500},
			{Name: "decode_step", StartNS: 100, EndNS: 200, EvalSeq: 42, Labels: map[string]string{"phase": "decode"}},
		},
	}
	p, stats, err := Build(cap, Options{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got := roundTrip(t, p)

	if stats.SpanAttributed != 1 {
		t.Errorf("SpanAttributed = %d, want 1", stats.SpanAttributed)
	}
	var attributed, unattributed *profile.Sample
	for _, s := range got.Sample {
		switch s.Location[0].Line[0].Function.Name {
		case "inner_kernel":
			attributed = s
		case "loose_kernel":
			unattributed = s
		}
	}
	if attributed == nil || unattributed == nil {
		t.Fatalf("expected both kernels in the profile, got leaves %v", leafNames(got))
	}
	// pprof orders locations leaf first, so the rendered stack reads
	// generate -> decode_step -> inner_kernel.
	wantStack := []string{"inner_kernel", "decode_step", "generate"}
	var gotStack []string
	for _, l := range attributed.Location {
		gotStack = append(gotStack, l.Line[0].Function.Name)
	}
	if !reflect.DeepEqual(gotStack, wantStack) {
		t.Errorf("stack = %v, want %v", gotStack, wantStack)
	}
	if got, want := unattributed.Location[len(unattributed.Location)-1].Line[0].Function.Name, unattributedRoot; got != want {
		t.Errorf("unattributed root = %q, want %q", got, want)
	}
	// The root must not be wrapped in angle brackets: pprof renders any
	// such name as "<unknown>", hiding the very kernels this keeps.
	if strings.HasPrefix(unattributedRoot, "<") {
		t.Errorf("unattributed root %q is bracketed and will render as <unknown> in pprof", unattributedRoot)
	}
	if got := attributed.Label["eval_seq"]; len(got) != 1 || got[0] != "42" {
		t.Errorf("eval_seq label = %v, want [42]", got)
	}
	if got := attributed.Label["span.phase"]; len(got) != 1 || got[0] != "decode" {
		t.Errorf("span.phase label = %v, want [decode]", got)
	}
}

func TestBuildRejectsCaptureWithNoKernels(t *testing.T) {
	// A capture that traced spans but flushed no kernel records is the
	// exact silent-empty failure this must not paper over.
	cap := gpuevent.Capture{Spans: []gpuevent.Span{{Name: "prefill", StartNS: 1, EndNS: 2}}}
	_, _, err := Build(cap, Options{})
	if err == nil {
		t.Fatal("Build accepted a capture with no kernel records")
	}
	for _, want := range []string{"no kernel records", "flush", "1 spans"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestBuildRejectsCaptureWithOnlyTransfers(t *testing.T) {
	// Tracing clearly ran, so blaming a missing flush would misdirect.
	cap := gpuevent.Capture{Events: []gpuevent.Event{
		{Kind: gpuevent.KindMemcpy, StartNS: 1, EndNS: 2},
	}}
	_, _, err := Build(cap, Options{})
	if err == nil {
		t.Fatal("Build accepted a capture with no kernel records")
	}
	if !strings.Contains(err.Error(), "no kernel launched") {
		t.Errorf("error %q should say tracing ran but no kernel launched", err)
	}
}

func TestBuildJoinsStructureOnKernelName(t *testing.T) {
	cap := oneKernelCapture()
	opts := Options{
		Structure: []StructureNode{
			{GraphPath: []string{"graph_1"}, Symbol: "saxpy"},
			{GraphPath: []string{"graph_1"}, Symbol: "saxpy"},
			{GraphPath: []string{"graph_1", "graph_2"}, Symbol: "other"},
		},
		Commits: []string{"graph_1"},
	}
	p, stats, err := Build(cap, opts)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got := roundTrip(t, p)

	want := []string{SampleGPUTime, SampleLaunchCount, SampleQueueDelay, SampleIdleAfter, SampleKernelCount, SampleGraphCommits}
	if !reflect.DeepEqual(sampleTypeNames(got), want) {
		t.Fatalf("sample types = %v, want %v", sampleTypeNames(got), want)
	}
	if n := totalOf(got, SampleKernelCount); n != 3 {
		t.Errorf("kernel_count total = %d, want 3", n)
	}
	if n := totalOf(got, SampleGraphCommits); n != 1 {
		t.Errorf("graph_commits total = %d, want 1", n)
	}
	// The join: one pprof function named saxpy must carry both the
	// measured time and the declared count. Two functions would show the
	// numbers side by side without ever relating them.
	var saxpy *profile.Function
	for _, f := range got.Function {
		if f.Name == "saxpy" {
			if saxpy != nil {
				t.Fatal("two distinct functions named saxpy; the join key is not unique")
			}
			saxpy = f
		}
	}
	if saxpy == nil {
		t.Fatal("no saxpy function in the joined profile")
	}
	var time, count int64
	for _, s := range got.Sample {
		if s.Location[0].Line[0].Function.Name != "saxpy" {
			continue
		}
		time += s.Value[0]
		count += s.Value[4]
	}
	if time == 0 || count != 2 {
		t.Errorf("saxpy joined values: gpu_time=%d kernel_count=%d, want nonzero time and 2 nodes", time, count)
	}
	if stats.StructureNodes != 3 || stats.Commits != 1 {
		t.Errorf("stats structure = %d nodes / %d commits, want 3 / 1", stats.StructureNodes, stats.Commits)
	}
}

func TestBuildStructureStandsAlone(t *testing.T) {
	nodes := []StructureNode{
		{GraphPath: []string{"graph_1"}, Symbol: "a"},
		{GraphPath: []string{"graph_1", "graph_2"}, Symbol: "b"},
	}
	p, _, err := BuildStructure(nodes, []string{"graph_1"})
	if err != nil {
		t.Fatalf("BuildStructure: %v", err)
	}
	got := roundTrip(t, p)
	if want := []string{SampleKernelCount, SampleGraphCommits}; !reflect.DeepEqual(sampleTypeNames(got), want) {
		t.Errorf("sample types = %v, want %v", sampleTypeNames(got), want)
	}
	if n := totalOf(got, SampleKernelCount); n != 2 {
		t.Errorf("kernel_count total = %d, want 2", n)
	}
	if n := totalOf(got, SampleGraphCommits); n != 1 {
		t.Errorf("graph_commits total = %d, want 1", n)
	}
	if roots := rootNames(got); roots["graph_1"] != 3 {
		t.Errorf("roots = %v, want every sample under graph_1", roots)
	}
}

func TestBuildStructureRejectsEmptyDump(t *testing.T) {
	if _, _, err := BuildStructure(nil, nil); err == nil {
		t.Fatal("BuildStructure accepted a dump with no kernel nodes")
	}
}

func TestSetSystemNamesKeepsTheMangledSymbol(t *testing.T) {
	p, _, err := Build(oneKernelCapture(), Options{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	SetSystemNames(p, map[string]string{"saxpy": "_Z5saxpyifPfS_"})
	got := roundTrip(t, p)
	for _, f := range got.Function {
		if f.Name != "saxpy" {
			continue
		}
		if f.SystemName != "_Z5saxpyifPfS_" {
			t.Errorf("SystemName = %q, want the mangled symbol", f.SystemName)
		}
		return
	}
	t.Fatal("no saxpy function in the profile")
}

func TestBuildStampsTheCaptureWindowInWallClock(t *testing.T) {
	// pprof reads TimeNanos as wall clock, so the CUPTI-domain start is
	// translated through clock_sync. Reporting the raw CUPTI stamp would
	// date the profile by however far the two domains have drifted.
	cap := oneKernelCapture()
	cap.ClockSync = &gpuevent.ClockSync{UnixNS: 9000, CuptiNS: 1000}
	p, _, err := Build(cap, Options{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got, want := p.TimeNanos, int64(9000); got != want {
		t.Errorf("TimeNanos = %d, want %d", got, want)
	}
	if got, want := p.DurationNanos, int64(500); got != want {
		t.Errorf("DurationNanos = %d, want %d", got, want)
	}

	// Without a clock_sync record the domain is unknown, so the window is
	// left unset rather than stamped in the wrong one.
	p, _, err = Build(oneKernelCapture(), Options{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if p.TimeNanos != 0 {
		t.Errorf("TimeNanos = %d, want 0 without a clock_sync record", p.TimeNanos)
	}
}

func TestBuildCarriesTheOverlapCaveat(t *testing.T) {
	// A reader who never saw the CLI help still has to learn the profile
	// is not a timeline.
	p, _, err := Build(oneKernelCapture(), Options{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got := roundTrip(t, p)
	if len(got.Comments) == 0 || !strings.Contains(got.Comments[0], "not wall time") {
		t.Errorf("comments = %v, want the sum-of-durations caveat", got.Comments)
	}
}

func TestBuilderAcceptsArbitrarilyDeepStacks(t *testing.T) {
	// Go call-stack attribution (spec 2) prepends frames to the same
	// chain. The construction must not assume a fixed depth.
	p := newProfile(sampleTypes(false))
	b := newBuilder(p)
	frames := []string{"main.main", "lm.Generate", "lm.decode", "mlx.Eval", "saxpy"}
	locs := b.stack(frames)
	if len(locs) != len(frames) {
		t.Fatalf("stack depth = %d, want %d", len(locs), len(frames))
	}
	if got := locs[0].Line[0].Function.Name; got != "saxpy" {
		t.Errorf("leaf = %q, want saxpy (pprof orders leaf first)", got)
	}
	// A repeated frame must intern to one function, so a recursive Go
	// stack does not multiply the function table.
	again := b.stack([]string{"main.main", "saxpy"})
	if again[0] != locs[0] {
		t.Error("the same frame produced two locations")
	}
}
