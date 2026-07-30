# Upstream requests for github.com/tmc/apple

Changes to `objc` and `objectivec` that would make private-framework work
materially safer. Each item names the failure in this repository that motivates
it, and states whether the primitive already exists upstream.

The context is GTShaderProfiler: an unpublished Objective-C surface reached
through purego, where selectors are undocumented, return types include raw C
pointers, and a wrong guess crashes the process rather than returning an error.
Everything here generalises to any private framework.

## 1. AutoreleasePool must lock the OS thread

`objc.AutoreleasePool` pushes a pool, defers the pop, and calls `fn`:

```go
func AutoreleasePool(fn func()) {
	ensureLibObjC()
	pool := objc_autoreleasePoolPush()
	defer objc_autoreleasePoolPop(pool)
	fn()
}
```

Autorelease pools are thread-affine. Nothing here pins the goroutine, so a
migration between push and pop pops the pool on a different thread than pushed
it, which crashes inside `objc_autoreleasePoolPop`. Every caller has to know to
wrap the call in `runtime.LockOSThread`, and this repository did not, until
crashes forced the fix into four separate call sites.

The lock belongs inside the helper. It is not a caller's choice: there is no
correct way to run a pool across a thread migration.

```go
func AutoreleasePool(fn func()) {
	ensureLibObjC()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pool := objc_autoreleasePoolPush()
	defer objc_autoreleasePoolPop(pool)
	fn()
}
```

Callers that already lock are unaffected; the calls nest correctly.

## 2. A type-encoding parser

`Method_getTypeEncoding` is bound and returns strings like `@28@0:8Q16S24` for
methods and `^{GTMioDrawMetadata=IIIIiIQIII}` for struct-pointer returns.
Nothing upstream parses them, so every consumer hand-reads them, and this
repository resorted to grepping a class dump to recover argument widths.

```go
type Signature struct {
	Return Type
	Args   []Type   // includes self and _cmd
}

func ParseSignature(encoding string) (Signature, error)
func ParseStruct(encoding string) (name string, fields []Type, naturalSize int, err error)
```

This is the enabling primitive for items 3 and 4; on its own it just stops
everyone writing the same fragile parser.

One property must be documented rather than papered over: **the encoding does
not describe packing.** `{GTMioDrawMetadata=IIIIiIQIII}` implies a 48-byte
record under natural alignment; the real array is packed at 44. A parser that
reports only the natural size will mislead. Report it as `naturalSize` and say
plainly that the true stride must be established by other means.

## 3. A checked send

`Send[T any](id ID, sel SEL, args ...any) T` validates nothing against the
method's actual signature. Three failure modes follow, in increasing order of
how quietly they fail:

- **Return-kind mismatch.** Asking for `objc.ID` from a property that returns
  `^{...}` yields a raw C pointer typed as an object. Messaging it later
  crashes. This is the documented hazard in our binding layer, and the guard we
  wrote against it, `RespondsToSelector`, does not detect it: a struct-pointer
  property responds to its selector perfectly well. The predicate tests
  existence; the hazard is type.
- **Return-width mismatch.** `highRegisterCount` encodes as `S`, a `uint16`,
  and was read into an `int32`.
- **Argument-width mismatch.** `durationForDraw:dataMaster:` takes
  `(uint32, uint16)`. A wrong width is silent corruption, not a crash, and is
  the hardest of the three to notice.

```go
func SendChecked[T any](id ID, sel SEL, args ...any) (T, error)
```

validating the requested `T` and the supplied argument kinds against
`ParseSignature`. A build tag or package-level switch enabling checking for
plain `Send` in development builds would be even better, since the value is
highest exactly where people are exploring.

## 4. An object-validity check

The single worst error in this work:

```go
collectionCount(objectFor(mio, "uscs"), "count")
```

`collectionCount` resolves the selector internally, so this sent `count` to the
integer count reinterpreted as a pointer. It crashed, the crash was reported as
a framework trap, and that false trap was offered to a collaborating agent as
independent evidence that no trace database existed. Both conclusions were
wrong: the collection holds 40 entries, one per GPU core.

`RespondsToSelector` cannot help, because it is itself a message send to the
bad pointer. A cheap sanity check would:

```go
func IsProbablyObject(id ID) bool
```

reject null and misaligned pointers, handle tagged pointers, read the isa and
confirm the resulting class is one the runtime has registered. It cannot be
sound in general, but it converts the common case from an unrecoverable
segfault into a `false`, which is the difference between a wrong published
conclusion and a caught mistake.

## 5. Exception-safe sends

`SendWithError` handles the `NSError**` out-parameter convention:

```go
func SendWithError[T any](id ID, sel SEL, args ...any) (T, error) {
	var err ID
	args = append(args, &err)
	ret := Send[T](id, sel, args...)
	...
}
```

It does not catch Objective-C exceptions. Private selectors throw:
`generateShaderTrackForProgramTypes` raises `dataType unrecognized`, and
`GTMioTraceDataStats -initWithTraceData:` throws
`-[GTMioTraceData databaseInternal] unrecognized selector`. An uncaught
Objective-C exception crossing into Go is fatal, so a single exploratory call
takes down the process and loses the surrounding work.

The package already has the machinery: `SetupExceptionHandler`,
`AddExceptionHandler`, `SetExceptionPreprocessor`. What is missing is a send
that uses it:

```go
func SendCatching[T any](id ID, sel SEL, args ...any) (T, *ObjCException, error)
```

The naming should keep the two concepts apart. `SendWithError` is a calling
convention; catching an exception is a safety net. Conflating them would be a
mistake.

## 6. Document the block-signature boundary

`NewBlock` and `SetBlockSignature` exist, so callback-taking selectors are
mechanically reachable. The blocker is different: a method encoding renders a
block argument as bare `@?`, with no inner signature.

That is why `enumerateDrawsForPipelineState:enumerator:`
(`v32@0:8Q16@?24`) was left unused here, and the draw-to-pipeline join was
instead recovered by reading a packed C array and validating it against
independently reported counts. Guessing a callback ABI is not a recoverable
error.

No code change is requested. The package documentation should say that `@?`
carries no inner signature and that the signature must come from the binary,
so the next person does not read the presence of `NewBlock` as permission to
guess.

## Priority

1 and 5 are correctness fixes with no design questions attached: a pool that
migrates threads is always wrong, and a fatal exception always loses more than
it should. 2 unblocks 3. 4 is cheap and prevents an entire error class.

Items 1 through 5 are runtime mechanics with no knowledge of any particular
framework in them, which is why they belong upstream rather than in each
consumer.
