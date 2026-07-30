//go:build darwin

package xcodebindings

import (
	"os"
	"runtime"
	"testing"

	"github.com/tmc/apple/objc"
)

// TestAPSCostProcessing checks whether the cost model populates once the APS
// passes are run.
//
// The pipeline in process_streamdata_darwin.go calls three of the processor's
// process selectors. There are three more, and two of them return a BOOL:
//
//	processAPSTimelineData                   c16@0:8
//	processAPSCostData                       c16@0:8
//	processBatchIdFilteredCounterStreamData  v16@0:8  (waitUntilBatchIDCounterFinished)
//
// The cost model has been reported empty throughout this work, and the reason
// given was that the capture carries no counter data. That premise is wrong on
// both counts: the archive exposes unarchivedAPSCounterData and
// unarchivedAPSTimelineData, and the .gpuprofiler_raw directory holds 120 raw
// files. The likelier explanation is that the pass which ingests them was
// never run.
//
// The signals are deliberately scalar, so no C struct has to be read to know
// the answer: kickDurationForEncoder: and totalCostForScope: were measured zero
// for every encoder and every scope/dataMaster pair before these passes.
func TestAPSCostProcessing(t *testing.T) {
	streamPath := os.Getenv("GPUTRACE_PROCESS_STREAMDATA")
	if streamPath == "" {
		t.Skip("set GPUTRACE_PROCESS_STREAMDATA to a profiler streamData archive")
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

		// What the archive itself carries, before any processing.
		for _, sel := range []string{"unarchivedAPSCounterData", "unarchivedAPSTimelineData"} {
			if responds(stream, sel) {
				t.Logf("streamData.%s count=%d", sel, collectionCount(stream, sel))
			}
		}

		processor, err := newStreamDataProcessor(stream, helper)
		if err != nil {
			t.Fatalf("new processor: %v", err)
		}
		defer objc.Send[objc.ID](processor, objc.Sel("release"))

		objc.Send[objc.ID](processor, objc.Sel("processStreamData"))
		objc.Send[objc.ID](processor, objc.Sel("processShaderProfilerStreamData"))
		objc.Send[objc.ID](processor, objc.Sel("processTimelineStreamData"))

		// The two BOOL passes report whether they accepted the data.
		for _, sel := range []string{"processAPSTimelineData", "processAPSCostData"} {
			if !responds(processor, sel) {
				t.Errorf("processor does not respond to %s", sel)
				continue
			}
			t.Logf("%s -> %v", sel, objc.Send[bool](processor, objc.Sel(sel)))
		}
		if responds(processor, "processBatchIdFilteredCounterStreamData") {
			objc.Send[objc.ID](processor, objc.Sel("processBatchIdFilteredCounterStreamData"))
		}

		for _, sel := range []string{
			"waitUntilShaderProfilerFinished",
			"waitUntilTimelineFinished",
			"waitUntilBatchIDCounterFinished",
			"waitUntilFinished",
		} {
			if responds(processor, sel) {
				objc.Send[objc.ID](processor, objc.Sel(sel))
			}
		}

		mio := objc.Send[objc.ID](processor, objc.Sel("mioData"))
		if mio == 0 {
			t.Fatal("mioData returned nil")
		}
		t.Logf("draws=%d encoders=%d costs=%d",
			uint64Property(mio, "drawCount"), uint64Property(mio, "encoderCount"),
			uint64Property(mio, "costCount"))

		// Scalar cost signals. Every one of these was zero before.
		var nonZeroKicks int
		encoders := uint64Property(mio, "encoderCount")
		for i := uint64(0); i < encoders; i++ {
			if d := objc.Send[uint64](mio, objc.Sel("kickDurationForEncoder:"), uint32(i)); d != 0 {
				nonZeroKicks++
				t.Logf("kickDurationForEncoder:%d = %d", i, d)
			}
		}
		t.Logf("non-zero kick durations: %d of %d", nonZeroKicks, encoders)

		var nonZeroScopes int
		if responds(mio, "totalCostForScope:scopeIdentifier:dataMaster:") {
			for scope := uint16(0); scope < 8; scope++ {
				for dm := uint16(0); dm < 4; dm++ {
					v := objc.Send[float64](mio, objc.Sel("totalCostForScope:scopeIdentifier:dataMaster:"),
						scope, uint64(0), dm)
					if v != 0 {
						nonZeroScopes++
						t.Logf("totalCostForScope:%d dataMaster:%d = %v", scope, dm, v)
					}
				}
			}
		}
		t.Logf("non-zero scope costs: %d of 32", nonZeroScopes)

		result := shaderProfilerResult(processor)
		if result != 0 {
			t.Logf("derivedCountersData count=%d", collectionCount(result, "derivedCountersData"))
		}

		if nonZeroKicks == 0 && nonZeroScopes == 0 {
			t.Log("cost model still empty after the APS passes")
		}
	})
}
