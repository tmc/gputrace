package trace

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// The record layouts below are the ones AnalyzeKernels walks. Building them by
// hand is the only way to reach a capture with no encoder record: every capture
// in testdata was exported from Xcode and has one, which is why an application
// written capture reported no attribution at all for as long as it did.

// commandBufferHeader starts a command buffer region.
func commandBufferHeader(timestamp uint64) []byte {
	return binary.LittleEndian.AppendUint64([]byte("CUUU"), timestamp)
}

// pipelineStateRecord is a Ct record: it binds a pipeline to an encoder.
func pipelineStateRecord(encoderAddr, pipelineAddr uint64) []byte {
	rec := []byte("Ct\x00\x00")
	rec = binary.LittleEndian.AppendUint64(rec, encoderAddr)
	return binary.LittleEndian.AppendUint64(rec, pipelineAddr)
}

// encoderRecord is a CS record as seen inside a command buffer, where it names
// an encoder rather than a shader function.
func encoderRecord(encoderAddr uint64, label string) []byte {
	rec := []byte("CS\x00\x00")
	rec = binary.LittleEndian.AppendUint64(rec, encoderAddr)
	rec = append(rec, label...)
	return append(rec, 0)
}

// dispatchRecord is one dispatch. Only the marker and the record length matter
// to attribution; the thread counts are carried through untouched.
func dispatchRecord() []byte {
	rec := make([]byte, 0x41)
	copy(rec, "ul@3")
	binary.LittleEndian.PutUint64(rec[0x11:0x19], 64) // threadsX
	binary.LittleEndian.PutUint64(rec[0x19:0x21], 1)
	binary.LittleEndian.PutUint64(rec[0x21:0x29], 1)
	binary.LittleEndian.PutUint64(rec[0x29:0x31], 32) // threadsPerGroupX
	binary.LittleEndian.PutUint64(rec[0x31:0x39], 1)
	binary.LittleEndian.PutUint64(rec[0x39:0x41], 1)
	return rec
}

// cttRecord maps a pipeline state to the function that produced it.
func cttRecord(funcAddr, pipelineAddr uint64) []byte {
	rec := make([]byte, 0x28)
	copy(rec, "Ctt\x00")
	binary.LittleEndian.PutUint64(rec[0x0c:0x14], funcAddr)
	binary.LittleEndian.PutUint64(rec[0x20:0x28], pipelineAddr)
	return rec
}

// newSyntheticTrace writes capture to a bundle on disk, because
// ParseCommandBuffers reads the capture file rather than the in-memory copy.
func newSyntheticTrace(t *testing.T, capture []byte, deviceResources []byte) *Trace {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "capture"), capture, 0o600); err != nil {
		t.Fatal(err)
	}
	return &Trace{
		Path:            dir,
		CaptureData:     capture,
		DeviceResources: map[string][]byte{"synthetic": deviceResources},
	}
}

func dispatchCounts(t *testing.T, tr *Trace) map[string]int {
	t.Helper()
	stats, err := tr.AnalyzeKernels()
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]int)
	for _, s := range stats {
		if s.DispatchCount > 0 {
			got[s.Name] = s.DispatchCount
		}
	}
	return got
}

const (
	testPipelineA = 0x1000
	testPipelineB = 0x2000
	testFuncA     = 0x9000
	testFuncB     = 0xa000
	testEncoder   = 0x5000
)

// resourcesNamingAB names both test pipelines, so a failure to attribute is
// never a failure to name.
func resourcesNamingAB() []byte {
	var d []byte
	d = append(d, csRecord(0xdead0000, "kernel_a", csTagFunction, testFuncA)...)
	d = append(d, csRecord(0xdead0000, "kernel_b", csTagFunction, testFuncB)...)
	d = append(d, cttRecord(testFuncA, testPipelineA)...)
	d = append(d, cttRecord(testFuncB, testPipelineB)...)
	return d
}

// TestAttributionWithoutEncoders pins the case that matters for an application
// written capture: the command buffer holds dispatches and pipeline state but
// no encoder record at all.
func TestAttributionWithoutEncoders(t *testing.T) {
	var c []byte
	c = append(c, commandBufferHeader(1)...)
	c = append(c, pipelineStateRecord(testEncoder, testPipelineA)...)
	c = append(c, dispatchRecord()...)
	c = append(c, dispatchRecord()...)
	c = append(c, pipelineStateRecord(testEncoder, testPipelineB)...)
	c = append(c, dispatchRecord()...)

	got := dispatchCounts(t, newSyntheticTrace(t, c, resourcesNamingAB()))
	want := map[string]int{"kernel_a": 2, "kernel_b": 1}

	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for name, n := range want {
		if got[name] != n {
			t.Errorf("%s = %d, want %d (all: %v)", name, got[name], n, got)
		}
	}
}

// TestAttributionWithoutEncodersSplitsCounts is the property the cross-check
// against profiler streamData confirmed empirically: the most recent pipeline
// state wins, so counts are not pooled onto whichever pipeline was bound first.
func TestAttributionWithoutEncodersSplitsCounts(t *testing.T) {
	var c []byte
	c = append(c, commandBufferHeader(1)...)
	for range 3 {
		c = append(c, pipelineStateRecord(testEncoder, testPipelineA)...)
		c = append(c, dispatchRecord()...)
		c = append(c, pipelineStateRecord(testEncoder, testPipelineB)...)
		c = append(c, dispatchRecord()...)
		c = append(c, dispatchRecord()...)
	}

	got := dispatchCounts(t, newSyntheticTrace(t, c, resourcesNamingAB()))
	if got["kernel_a"] != 3 || got["kernel_b"] != 6 {
		t.Errorf("got %v, want kernel_a=3 kernel_b=6", got)
	}
}

// TestAttributionBeforeAnyPipelineState pins that a dispatch with no pipeline
// bound is still counted, as unknown, rather than dropped.
func TestAttributionBeforeAnyPipelineState(t *testing.T) {
	var c []byte
	c = append(c, commandBufferHeader(1)...)
	c = append(c, dispatchRecord()...)
	c = append(c, pipelineStateRecord(testEncoder, testPipelineA)...)
	c = append(c, dispatchRecord()...)

	got := dispatchCounts(t, newSyntheticTrace(t, c, resourcesNamingAB()))
	if got["unknown"] != 1 || got["kernel_a"] != 1 {
		t.Errorf("got %v, want unknown=1 kernel_a=1", got)
	}
}

// TestAttributionWithEncoderUnchanged guards the Xcode-shaped path: where a
// command buffer does carry an encoder, a dispatch that precedes it is still
// unknown. Without this the encoder-less fallback could quietly widen to
// captures it was not meant for.
func TestAttributionWithEncoderUnchanged(t *testing.T) {
	var c []byte
	c = append(c, commandBufferHeader(1)...)
	c = append(c, dispatchRecord()...) // before the encoder starts
	c = append(c, encoderRecord(testEncoder, "Encoder_1")...)
	c = append(c, pipelineStateRecord(testEncoder, testPipelineA)...)
	c = append(c, dispatchRecord()...)

	got := dispatchCounts(t, newSyntheticTrace(t, c, resourcesNamingAB()))
	if got["unknown"] != 1 {
		t.Errorf("dispatch before the encoder should be unknown, got %v", got)
	}
	if got["kernel_a"] != 1 {
		t.Errorf("dispatch inside the encoder should be named, got %v", got)
	}
}

func TestParseAttributedDispatchesRequiresNamedPipeline(t *testing.T) {
	var capture []byte
	capture = append(capture, commandBufferHeader(1)...)
	capture = append(capture, pipelineStateRecord(testEncoder, testPipelineA)...)
	capture = append(capture, dispatchRecord()...)

	trace := newSyntheticTrace(t, capture, nil)
	dispatches, err := trace.ParseAttributedDispatches()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(dispatches), 1; got != want {
		t.Fatalf("dispatches = %d, want %d", got, want)
	}
	got := dispatches[0]
	if got.PipelineAddr != testPipelineA || got.FunctionName != "" || got.AttributionBasis != "unavailable" {
		t.Fatalf("dispatch = %+v", got)
	}
	if got.SIMDGroups() != 2 {
		t.Fatalf("SIMD groups = %d, want 2", got.SIMDGroups())
	}
}
