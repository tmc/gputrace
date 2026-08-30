// Package cuptiprofile renders GPU work as pprof profiles.
//
// Two inputs produce one format. A CUPTI activity capture becomes a profile
// of measured device work: per-launch GPU time, launch counts, queue delay,
// and per-stream idle. A CUDA-graph structure dump becomes a profile of
// declared work: which kernels a graph commits, and how many commits there
// were. Either can carry the other, so "+56 kernels" and "worth X ms" are
// answerable from one file.
//
// The point of the format is the toolchain around it. `pprof -diff_base`
// is a signed multiset difference over stacks, which is exactly the
// comparison a two-implementation parity investigation keeps hand-writing,
// and `-sample_index` picks which question it answers: gpu_time says the
// extra time is inside kernels, idle_after says it is between them.
//
// # Overlap semantics
//
// gpu_time is the sum of kernel durations, not wall time. Kernels on
// different streams run concurrently, so the sum can exceed the capture's
// wall span, and it is not rescaled to fit. Sum-of-durations is the right
// answer to "which kernel costs most", which is what a profile is read for;
// a flame graph built from it is not a timeline. Wall span and occupancy
// are measured separately by gpuevent.UtilizationOf and stay out of here.
package cuptiprofile

import (
	"fmt"
	"io"
	"sort"

	"github.com/google/pprof/profile"

	"github.com/tmc/gputrace/internal/gpuevent"
)

// Sample type names and units, in the order Build emits them. The order is
// part of the contract: `pprof -diff_base` requires the two profiles to
// carry identical sample type lists, and reports a confusing mismatch
// rather than a diff when they do not.
const (
	SampleGPUTime      = "gpu_time"
	SampleLaunchCount  = "launch_count"
	SampleQueueDelay   = "queue_delay"
	SampleIdleAfter    = "idle_after"
	SampleKernelCount  = "kernel_count"
	SampleGraphCommits = "graph_commits"

	unitNanoseconds = "nanoseconds"
	unitCount       = "count"
)

// unattributedRoot is the frame given to kernels no application span
// encloses. Dropping them would make the profile disagree with the
// capture's own kernel total, which is a worse failure than an honest
// bucket named for what it is.
//
// The name carries no angle brackets on purpose [V]: pprof's report
// renders any function name enclosed in them as "<unknown>", so a frame
// named "<unattributed>" is invisible in -top, -tree, and the web UI — the
// exact outcome not dropping the kernels exists to avoid.
const unattributedRoot = "unattributed"

// StructureNode is one kernel node of a CUDA-graph dump, flattened: the
// chain of graph names from the committed root down to the graph holding
// the node, and the kernel it launches. It is what joins declared
// structure to measured cost — the kernel symbol is the join key.
type StructureNode struct {
	GraphPath []string
	Symbol    string
}

// Options controls what a built profile carries beyond kernel activity.
type Options struct {
	// Structure adds kernel_count and graph_commits sample types carrying
	// CUDA-graph structure, joined to activity on the kernel symbol.
	Structure []StructureNode
	// Commits names one committed graph each; their count is the
	// graph_commits total.
	Commits []string
}

// Stats reports what a build measured, so a caller can qualify the numbers
// it prints rather than implying coverage the capture does not have.
type Stats struct {
	Kernels        int
	SpanAttributed int // kernels an application span enclosed
	Spans          int
	QueueTimed     int // kernels carrying a usable queue/submit/start triple
	StructureNodes int
	Commits        int
	GPUTimeNS      uint64
	MedianQueueNS  uint64 // over QueueTimed kernels only
	// Completeness says whether the capture behind these numbers kept
	// every record the run produced. When it did not, every value above
	// describes a share of the run rather than the run, and the share is
	// not recoverable: the loss is uniform across kernel names and sizes,
	// so nothing in the profile itself looks wrong.
	Completeness gpuevent.Completeness
}

// SpanAttributedPct is the share of kernels that got a span stack.
func (s Stats) SpanAttributedPct() float64 {
	if s.Kernels == 0 {
		return 0
	}
	return 100 * float64(s.SpanAttributed) / float64(s.Kernels)
}

// Complete reports whether the capture behind these numbers kept every
// record the run produced. When it is false the profile is still worth
// reading for structure — which kernels ran, in what stacks — and its
// totals are not worth reading at all.
func (s Stats) Complete() bool { return s.Completeness.Complete() }

// NodeToLaunchRatio compares declared graph work against measured launches:
// the kernel nodes a run's CUDA graphs committed, over the kernel launches
// its activity records report. It is a completeness check that needs no
// second instrument, because a capture supplies both halves.
//
// It returns 0 when either half is absent. A ratio near 1 means the two
// agree; well below 1 means launches went unrecorded. Above 1 is not a
// defect: a graph committed once and replayed many times launches more
// kernels than it declares, which is the ordinary shape of a decode loop.
func (s Stats) NodeToLaunchRatio() float64 {
	if s.StructureNodes == 0 || s.Kernels == 0 {
		return 0
	}
	return float64(s.Kernels) / float64(s.StructureNodes)
}

// Build converts a capture's kernel activity into a pprof profile with one
// sample per kernel launch.
//
// Sample values, in order: gpu_time (end-start), launch_count (1),
// queue_delay (queued to device start), idle_after (device time on this
// kernel's stream before the next activity starts). With opts.Structure,
// kernel_count and graph_commits follow.
//
// queue_delay comes from Event.Latency, which holds durations derived
// inside one clock domain at decode time. Recomputing it from a raw
// queued_ns against a normalized start is the mistake that reports ~1.16
// seconds of launch latency; there is no raw timestamp here to make it
// with.
//
// A capture with no kernel records is an error, not an empty profile: the
// common cause is a target that never flushed CUPTI, and a valid empty
// profile hides that behind a file that parses.
func Build(cap gpuevent.Capture, opts Options) (*profile.Profile, Stats, error) {
	var kernels []gpuevent.Event
	for _, e := range cap.Events {
		if e.Kind == gpuevent.KindKernel {
			kernels = append(kernels, e)
		}
	}
	if len(kernels) == 0 {
		return nil, Stats{}, emptyCaptureError(cap)
	}

	prof := newProfile(sampleTypes(len(opts.Structure) > 0 || len(opts.Commits) > 0))
	prof.DefaultSampleType = SampleGPUTime
	prof.Comments = append(prof.Comments, overlapComment)
	setCaptureWindow(prof, cap)

	b := newBuilder(prof)
	stats := Stats{
		Kernels:      len(kernels),
		Spans:        len(cap.Spans),
		Completeness: gpuevent.MeasureCompleteness(cap),
	}

	// idle_after is measured against every activity on the stream, not
	// only kernels: a stream running a copy is busy.
	idle := gpuevent.IdleAfter(cap.Events)
	idleByKernel := make([]uint64, len(kernels))
	{
		k := 0
		for i, e := range cap.Events {
			if e.Kind == gpuevent.KindKernel {
				idleByKernel[k] = idle[i]
				k++
			}
		}
	}
	stacks := gpuevent.SpanStacks(kernels, cap.Spans)

	width := len(prof.SampleType)
	var queueDelays []uint64
	for i, k := range kernels {
		values := make([]int64, width)
		values[0] = int64(k.DurationNS())
		values[1] = 1
		if k.Latency.Known {
			values[2] = int64(k.Latency.QueueToStartNS())
			stats.QueueTimed++
			queueDelays = append(queueDelays, k.Latency.QueueToStartNS())
		}
		values[3] = int64(idleByKernel[i])
		stats.GPUTimeNS += k.DurationNS()

		frames := make([]string, 0, len(stacks[i])+1)
		switch {
		case len(stacks[i]) > 0:
			stats.SpanAttributed++
			for _, s := range stacks[i] {
				frames = append(frames, s.Name)
			}
		case len(cap.Spans) > 0:
			// The capture declares spans, so a kernel outside all of
			// them is a fact about the workload worth naming. With no
			// spans at all there is nothing to be outside of, and a
			// root frame on every sample would only add a frame.
			frames = append(frames, unattributedRoot)
		}
		frames = append(frames, kernelName(k))

		prof.Sample = append(prof.Sample, &profile.Sample{
			Location: b.stack(frames),
			Value:    values,
			Label:    kernelLabels(k, stacks[i]),
		})
	}
	stats.MedianQueueNS = median(queueDelays)

	if width > 4 {
		appendStructure(prof, b, opts, width, 4)
		stats.StructureNodes = len(opts.Structure)
		stats.Commits = len(opts.Commits)
	}
	return prof, stats, nil
}

// BuildStructure renders a CUDA-graph dump on its own: what the graphs
// commit, with no timing attached.
//
// It is a genuine same-instrument comparison. Both implementations' libmlx
// writes these dumps from the same code, so a diff of two structure
// profiles needs no cross-stack calibration, costs no GPU time, and has no
// run-to-run variance to average away — one dump per side is the whole
// measurement.
func BuildStructure(nodes []StructureNode, commits []string) (*profile.Profile, Stats, error) {
	if len(nodes) == 0 {
		return nil, Stats{}, fmt.Errorf("cuptiprofile: graph dump has no kernel nodes")
	}
	prof := newProfile([]*profile.ValueType{
		{Type: SampleKernelCount, Unit: unitCount},
		{Type: SampleGraphCommits, Unit: unitCount},
	})
	prof.DefaultSampleType = SampleKernelCount
	b := newBuilder(prof)
	appendStructure(prof, b, Options{Structure: nodes, Commits: commits}, 2, 0)
	return prof, Stats{StructureNodes: len(nodes), Commits: len(commits)}, nil
}

// appendStructure adds the graph-structure samples, writing kernel_count
// and graph_commits at offset within a value vector of the given width.
// Commits get their own sample rather than riding a kernel node's, so the
// graph_commits total is the commit count however the kernels fall.
func appendStructure(prof *profile.Profile, b *builder, opts Options, width, offset int) {
	for _, n := range opts.Structure {
		values := make([]int64, width)
		values[offset] = 1
		frames := append(append([]string{}, n.GraphPath...), n.Symbol)
		prof.Sample = append(prof.Sample, &profile.Sample{
			Location: b.stack(frames),
			Value:    values,
			Label:    map[string][]string{"kind": {"graph_node"}},
		})
	}
	for _, c := range opts.Commits {
		values := make([]int64, width)
		values[offset+1] = 1
		prof.Sample = append(prof.Sample, &profile.Sample{
			Location: b.stack([]string{c}),
			Value:    values,
			Label:    map[string][]string{"kind": {"graph_commit"}},
		})
	}
}

// overlapComment travels with the profile so a reader who never saw the
// CLI help still learns the profile is not a timeline.
const overlapComment = "gpu_time is the sum of kernel durations, not wall time: kernels on " +
	"different streams overlap, so the total can exceed the capture's wall span. " +
	"It is not rescaled. Read this as cost per kernel, not as a timeline."

func sampleTypes(withStructure bool) []*profile.ValueType {
	types := []*profile.ValueType{
		{Type: SampleGPUTime, Unit: unitNanoseconds},
		{Type: SampleLaunchCount, Unit: unitCount},
		{Type: SampleQueueDelay, Unit: unitNanoseconds},
		{Type: SampleIdleAfter, Unit: unitNanoseconds},
	}
	if withStructure {
		types = append(types,
			&profile.ValueType{Type: SampleKernelCount, Unit: unitCount},
			&profile.ValueType{Type: SampleGraphCommits, Unit: unitCount},
		)
	}
	return types
}

func newProfile(types []*profile.ValueType) *profile.Profile {
	return &profile.Profile{
		SampleType: types,
		PeriodType: &profile.ValueType{Type: types[0].Type, Unit: types[0].Unit},
		Period:     1,
	}
}

// setCaptureWindow stamps the profile with when the capture ran. pprof
// reads TimeNanos as wall clock, so the CUPTI-domain timestamps are
// translated through the capture's clock_sync record; without one the
// window is left unset rather than reported in the wrong domain.
func setCaptureWindow(prof *profile.Profile, cap gpuevent.Capture) {
	var first, last uint64
	first = ^uint64(0)
	for _, e := range cap.Events {
		if e.StartNS < first {
			first = e.StartNS
		}
		if e.EndNS > last {
			last = e.EndNS
		}
	}
	if first == ^uint64(0) || last <= first {
		return
	}
	prof.DurationNanos = int64(last - first)
	if cap.ClockSync == nil {
		return
	}
	delta := int64(cap.ClockSync.UnixNS) - int64(cap.ClockSync.CuptiNS)
	prof.TimeNanos = int64(first) + delta
}

// kernelLabels carries the evidence a sample was derived from, so
// `pprof -tagfocus` can slice by launch geometry, stream, or the decode
// step a kernel ran in without re-reading the capture.
func kernelLabels(k gpuevent.Event, stack []gpuevent.Span) map[string][]string {
	labels := map[string][]string{"kind": {"cuda_kernel"}}
	if k.Grid != "" {
		labels["grid"] = []string{k.Grid}
	}
	if k.Block != "" {
		labels["block"] = []string{k.Block}
	}
	labels["stream"] = []string{fmt.Sprintf("%d", k.StreamID)}
	if len(stack) > 0 {
		inner := stack[len(stack)-1]
		if inner.EvalSeq != 0 {
			labels["eval_seq"] = []string{fmt.Sprintf("%d", inner.EvalSeq)}
		}
		for name, value := range inner.Labels {
			labels["span."+name] = []string{value}
		}
	}
	return labels
}

func kernelName(k gpuevent.Event) string {
	if k.Name != "" {
		return k.Name
	}
	if k.RawSymbol != "" {
		return k.RawSymbol
	}
	return string(k.Kind)
}

// emptyCaptureError explains a capture with no kernel records rather than
// emitting a profile that parses and says nothing. The usual cause is a Go
// target: CUPTI flushes its activity buffers at interposed sync points,
// Go's runtime never crosses one and exits through exit_group, so the
// shim's ELF destructor never runs and every buffered record is lost. Such
// a capture is silently empty, not wrong, which is why the tool has to say
// so here.
func emptyCaptureError(cap gpuevent.Capture) error {
	detail := fmt.Sprintf("%d api records, %d spans, %d device samples",
		len(cap.APIs), len(cap.Spans), len(cap.Samples))
	hint := "the target likely exited without flushing CUPTI's activity buffers; " +
		"a Go target needs an in-process flush before exit"
	if len(cap.Events) > 0 {
		hint = fmt.Sprintf("the capture holds %d non-kernel activity records, so tracing ran but no kernel launched", len(cap.Events))
	}
	return fmt.Errorf("cuptiprofile: capture has no kernel records (%s): %s", detail, hint)
}

// builder interns functions and locations by frame name so one kernel
// symbol is one pprof function wherever it appears — which is what lets a
// structure sample and an activity sample for the same kernel join.
//
// It takes a frame list of any depth and assumes nothing about what the
// frames are, so the Go call stacks that PERFETTO-ATTRIBUTION-SPEC.md adds
// drop in by prepending frames to the same list. Nothing here needs to
// change for that.
type builder struct {
	prof *profile.Profile
	locs map[string]*profile.Location
	next uint64
}

func newBuilder(prof *profile.Profile) *builder {
	return &builder{prof: prof, locs: map[string]*profile.Location{}, next: 1}
}

func (b *builder) location(name string) *profile.Location {
	if l, ok := b.locs[name]; ok {
		return l
	}
	f := &profile.Function{ID: b.next, Name: name, SystemName: name}
	b.next++
	b.prof.Function = append(b.prof.Function, f)
	l := &profile.Location{ID: b.next, Line: []profile.Line{{Function: f}}}
	b.next++
	b.prof.Location = append(b.prof.Location, l)
	b.locs[name] = l
	return l
}

// stack converts frames given outermost-first into pprof's leaf-first
// location order.
func (b *builder) stack(frames []string) []*profile.Location {
	locs := make([]*profile.Location, 0, len(frames))
	for i := len(frames) - 1; i >= 0; i-- {
		locs = append(locs, b.location(frames[i]))
	}
	return locs
}

// SetSystemNames records each function's mangled symbol alongside the
// demangled name pprof displays, so the profile still carries the exact
// symbol the capture reported. mangled maps demangled name to symbol.
func SetSystemNames(prof *profile.Profile, mangled map[string]string) {
	for _, f := range prof.Function {
		if sym, ok := mangled[f.Name]; ok && sym != "" {
			f.SystemName = sym
		}
	}
}

// Write serializes the profile as gzipped profile.proto.
func Write(p *profile.Profile, w io.Writer) error {
	return p.Write(w)
}

func median(v []uint64) uint64 {
	if len(v) == 0 {
		return 0
	}
	sorted := append([]uint64(nil), v...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted[len(sorted)/2]
}
