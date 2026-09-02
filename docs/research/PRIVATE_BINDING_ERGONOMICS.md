# Private binding ergonomics

What should change in `internal/xcodebindings` to make work against
GTShaderProfiler safer and faster. Everything here is drawn from failures in the
session that built the shader trace model, the cost timeline, and per-kernel GPU
time; each item names the failure it would have prevented.

The items are ordered by damage caused, not by how dangerous they sound. The
crash hazards are the ones the code already documents and mostly avoids through
discipline. The wrong answers came from somewhere else.

## 1. Nothing distinguishes "the call ran" from "we got data"

This produced every wrong conclusion. Four instances:

- `processAPSTimelineData` and `processAPSCostData` return `true` and populate
  nothing. In this framework a `BOOL` means the call ran, not that it ingested
  anything. Three separate selectors behave this way.
- `TimelineSummary.Ready` was computed as `DrawCount != 0 &&
  PipelineStateCount != 0`, so it reported ready while all 574 draw durations
  were zero.
- The regression covering those durations asserted
  `len(DrawDurationsDataMaster2) != 3`. The implementation appends three entries
  unconditionally, so three zeros satisfied it.
- MCA registers read through `mcaBinaryForBinaryKey:` return populated objects
  with plausible values that are attributed at random: one pipeline reported 98,
  60, and 66 across three runs of one capture.

The binding layer has no vocabulary for this. A selector returning `uint64`
hands back a `uint64`, and zero-because-absent is indistinguishable from
zero-because-measured.

The mechanism is small:

```go
// Measured is a value the framework actually produced. A selector that returns
// zero because nothing was ingested has not measured anything.
type Measured[T comparable] struct {
	V  T
	OK bool
}
```

The convention around it matters more than the type: **readiness is never
derived from a structural count.** A summary is ready when it holds a value
worth publishing, not when it holds a shape. Had `Ready` required a non-zero
duration, the missing `GPUTRACE_MIO_SETUP_DATA_PATH` dependency would have
surfaced when the opt-in was written rather than after it shipped.

## 2. Every probe rebuilds the whole model

This was the largest cost in wall-clock time.

`ProcessStreamData` spawns the GTLLVMHelper child process and disassembles every
shader in the capture: about 45 seconds at minimum, and minutes once
`_setupDataPath` pulls in the ~4 GB of sibling raw files. The package exposes
exactly one shape, path in and finished struct out, so there is no way to build
the model once and ask it several questions. Six or seven probes in one session
each paid the full cost again.

`WithStreamData` already establishes the right pattern for the archive. The
processed model needs the same:

```go
func WithProcessedModel(ctx context.Context, path string, opts Options, fn func(*Model) error) error
```

The context is not decoration. There is currently no way to cancel a three
minute call or observe that it is progressing, which makes every experiment a
blind wait.

## 3. respondsToSelector is the wrong guard, and the right one is already bound

`objectFor` carries this comment:

> Several neighbouring properties on this model return raw C pointers instead,
> and messaging one of those crashes, so every read goes through the guard.

The guard is `objc.RespondsToSelector`. A struct-pointer property responds to
its selector perfectly well. The predicate tests existence; the hazard is type,
so the guard does not guard against the thing its comment describes.

The fix requires no new FFI work. `Class_getInstanceMethod`,
`Method_getTypeEncoding`, and `Method_copyReturnType` are already bound in
`github.com/tmc/apple/objectivec`. This repository calls them in one place, and
it is a test. Meanwhile `process_streamdata_darwin.go` issues 64 raw
`objc.Send[...]` calls behind 44 `responds()` guards, none of which inspects a
type.

A checked send should refuse `^{...}`, `^v`, and `*` returns when the caller
asked for an object, and should also catch width mismatches, which are quieter
and therefore worse. `highRegisterCount` is `S`, a `uint16`, read here into an
`int32`. `durationForDraw:dataMaster:` takes `(uint32, uint16)`. A wrong
argument width yields silent corruption rather than a crash.

Note what this does **not** solve. `GTMioDrawMetadata` encodes as
`^{GTMioDrawMetadata=IIIIiIQIII}`, whose natural alignment implies a 48-byte
record; the array is packed at 44. The encoding gives field order and types and
says nothing about packing, so a type-aware reader would have derived 48 and
been just as wrong. The lesson for C arrays is not typing but independent
cross-checking: the layout is trustworthy because bucketing 574 draws by the
candidate field reproduces all eighteen counts `numDrawsForPipelineState:`
reports, and a wrong offset yields 205 to 213 distinct values instead of 18.

## 4. Two helpers that look alike, and one takes a selector

The worst single error of the session was this call:

```go
collectionCount(objectFor(mio, "uscs"), "count")
```

`collectionCount` resolves the selector itself, so this sent `count` to the
integer count reinterpreted as a pointer. Non-empty collections crashed; empty
ones silently returned zero. The crash was reported as a framework trap and
offered as independent confirmation that no trace database was present. Both
conclusions were wrong: `mio.uscs` holds 40 entries, one per GPU core.

No type discipline catches this, because both spellings compile and both are
plausible. It is API shape. Helpers that take a selector and helpers that take
an already-resolved object should not be confusable:

```go
func countOf(collection objc.ID) uint64                 // resolved object
func collectionCountFor(id objc.ID, selector string) uint64 // resolves internally
```

## 5. The two-run rule lives only in prose

The rule that a value which does not reproduce across two runs is not a finding
was applied by hand throughout and violated twice: once when MCA registers were
reported as resolved, and once when a first duration sweep was described as
complete before the run finished. It should be executable:

```go
func RequireReproducible[T comparable](t *testing.T, name string, fn func() T) T
```

Values known not to survive a second run, and therefore never to be wired into
anything, currently include `firstBinaryIndexForCliqueAtIndex:`, the
`mcaBinaryForBinaryKey:` attribution, and top binary track sample ordering.

## What belongs in tmc/apple

Only item 3, along with four further upstream gaps this work exposed: an
autorelease pool that does not pin its thread, the absence of a type-encoding
parser, no exception-catching send, and no object-validity check. These belong
on the `github.com/tmc/apple` issue tracker, not in this repository.

Items 1, 2, 4, and 5 encode facts about GTShaderProfiler and its capture data.
They stay here.

## If only one thing changes

Item 1. The type guard prevents crashes that discipline has so far avoided.
The vacuity convention prevents wrong answers that were published and then
retracted twice.
