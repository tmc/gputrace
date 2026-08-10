//go:build darwin

package xcodebindings

import (
	"fmt"
	"runtime"
	"testing"
	"unsafe"

	"github.com/tmc/gputrace/internal/testtrace"

	"github.com/tmc/apple/objc"
)

// TestAPSCostScopeIdentifiers reads the scope identifiers out of the cost
// records instead of guessing them.
//
// TestAPSCostProcessing reports the cost model empty, but it asks for it with
// scopeIdentifier hardcoded to 0 across all 32 of its probes:
//
//	totalCostForScope:scopeIdentifier:dataMaster:   d32@0:8 S16 Q20 S28
//
// If the real identifiers are encoder ids, pipeline hashes, or anything else
// non-zero, every one of those lookups misses, and a miss is indistinguishable
// from a measured zero: GTMio substitutes 0.0 for a NULL container lookup
// (allValues -> cbz -> movi d0, #0). So "empty cost model" may be a statement
// about the query rather than about the data.
//
// GTMioMGPUTraceData also vends the records directly:
//
//	costs      ^{GTMioCostInfo={GTMioCostContext=SS(?=IIIII)(?=QIIIQ)}d[10d]d[10d]Q[10Q]QQQ}
//	costCount  Q16@0:8
//
// This test walks those records, recovers the (scope, identifier) pairs that
// actually exist, and re-queries with them. It deliberately makes no claim
// about which field is the identifier: it dumps the context bytes and tries
// every plausible reading, because guessing an offset and finding a number
// that looks reasonable is exactly how a wrong field survives in this project.
func TestAPSCostScopeIdentifiers(t *testing.T) {
	streamPath := testtrace.Path("GPUTRACE_PROCESS_STREAMDATA", testtrace.StreamData)
	if streamPath == "" {
		t.Skip("set GPUTRACE_TEST_TRACE to a .gputrace bundle, or GPUTRACE_PROCESS_STREAMDATA to a streamData archive")
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	objc.AutoreleasePool(func() {
		stream, err := loadStreamData(streamPath)
		if err != nil {
			t.Fatalf("load streamData: %v", err)
		}
		helper, err := llvmHelperPath()
		if err != nil {
			t.Fatalf("locate helper: %v", err)
		}
		processor, err := newStreamDataProcessor(stream, helper)
		if err != nil {
			t.Fatalf("new processor: %v", err)
		}
		defer objc.Send[objc.ID](processor, objc.Sel("release"))

		objc.Send[objc.ID](processor, objc.Sel("processStreamData"))
		objc.Send[objc.ID](processor, objc.Sel("processShaderProfilerStreamData"))
		objc.Send[objc.ID](processor, objc.Sel("processTimelineStreamData"))
		for _, sel := range []string{"processAPSTimelineData", "processAPSCostData"} {
			if responds(processor, sel) {
				t.Logf("%s -> %v", sel, objc.Send[bool](processor, objc.Sel(sel)))
			}
		}
		for _, sel := range []string{"waitUntilShaderProfilerFinished", "waitUntilTimelineFinished", "waitUntilFinished"} {
			if responds(processor, sel) {
				objc.Send[objc.ID](processor, objc.Sel(sel))
			}
		}

		mio := objc.Send[objc.ID](processor, objc.Sel("mioData"))
		if mio == 0 {
			t.Fatal("mioData returned nil")
		}
		count := uint64Property(mio, "costCount")
		t.Logf("costCount=%d", count)
		if count == 0 {
			t.Skip("no cost records to inspect; this test has nothing to say")
		}
		if !responds(mio, "costs") {
			t.Fatal("mioData does not respond to costs")
		}
		base := objc.Send[unsafe.Pointer](mio, objc.Sel("costs"))
		if base == nil {
			t.Fatal("costs returned nil")
		}

		// Layout. The ObjC encoding
		//   {GTMioCostContext=SS(?=IIIII)(?=QIIIQ)}
		// reads as two uint16 then two unions, which invites a 32-byte
		// context. That is wrong: the unions are discriminated views of the
		// SAME two identifier fields, so the context is
		//   0  level             uint16
		//   2  scope             uint16
		//   4  levelIdentifier   uint32
		//   8  scopeIdentifier   uint64   -> 16 bytes
		// and the record is 16 + 8 + 80 + 8 + 80 + 8 + 80 + 8 + 8 + 8 = 304.
		//
		// An earlier revision of this test used 32/320. With a 320 stride the
		// reads drift 16 bytes per record and every field after the first is
		// garbage, which presented as "all contexts are zero" -- a plausible
		// wrong answer, not an error. The sizes below match the independently
		// derived layout in fasterthanlime/gputrace-rs, which pins both with
		// static assertions.
		const (
			contextSize = 16
			recordSize  = 304
		)
		// A wrong stride yields plausible-looking garbage rather than an
		// error, so bound the read first and sanity-check the shape after.
		checkCounterBufferExtent(t, "costs", base, count, recordSize)

		type reading struct {
			level uint16
			scope uint16
			levID uint32
			scpID uint64
		}
		seen := map[reading]int{}
		var order []reading
		for i := uint64(0); i < count; i++ {
			rec := unsafe.Add(base, uintptr(i)*recordSize)
			r := reading{
				level: *(*uint16)(rec),
				scope: *(*uint16)(unsafe.Add(rec, 2)),
				levID: *(*uint32)(unsafe.Add(rec, 4)),
				scpID: *(*uint64)(unsafe.Add(rec, 8)),
			}
			if _, ok := seen[r]; !ok {
				order = append(order, r)
			}
			seen[r]++
			if i < 4 {
				t.Logf("record[%d] context bytes: % x", i, unsafe.Slice((*byte)(rec), contextSize))
			}
		}

		// The falsifier for the layout: scope is a uint16 the API sweeps as a
		// small enum. If these come back huge or all-identical-garbage, the
		// stride or offsets are wrong and nothing below can be trusted.
		t.Logf("distinct context readings: %d", len(order))
		for i, r := range order {
			if i >= 16 {
				t.Logf("... %d more", len(order)-16)
				break
			}
			t.Logf("  level=%d scope=%d levelID=%d scopeID=%d  x%d", r.level, r.scope, r.levID, r.scpID, seen[r])
		}

		// Re-query with the identifiers that actually occur. Every candidate
		// field is tried; the test asserts nothing about which one is right,
		// it reports which one produces non-zero cost.
		type candidate struct {
			name string
			get  func(reading) uint64
		}
		candidates := []candidate{
			{"scopeID", func(r reading) uint64 { return r.scpID }},
			{"levelID", func(r reading) uint64 { return uint64(r.levID) }},
			{"zero", func(reading) uint64 { return 0 }},
		}
		// Two scalar accessors take the same (scope, identifier) pair but a
		// different third argument. Only dataMaster has ever been swept.
		// programType is a distinct axis, and both pass scalars only, so
		// neither depends on the GTMioCostInfo struct binding -- which is
		// separately known to be mis-sized and must not be called yet.
		for _, third := range []string{
			"totalCostForScope:scopeIdentifier:dataMaster:",
			"totalCostForScope:scopeIdentifier:programType:",
		} {
			if !responds(mio, third) {
				t.Logf("%s: not implemented on this build", third)
				continue
			}
			sel := objc.Sel(third)
			for _, c := range candidates {
				var nonZero int
				var sample string
				for _, r := range order {
					id := c.get(r)
					for k := uint16(0); k < 8; k++ {
						v := objc.Send[float64](mio, sel, r.scope, id, k)
						if v != 0 {
							nonZero++
							if sample == "" {
								sample = fmt.Sprintf("scope=%d id=%d arg3=%d -> %g", r.scope, id, k, v)
							}
						}
					}
				}
				t.Logf("%-46s identifier=%-8s non-zero: %d of %d  %s",
					third, c.name, nonZero, len(order)*8, sample)
			}
		}
	})
}
