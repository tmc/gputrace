package replay

import (
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

func TestValidateReplayRejectsICBExecutions(t *testing.T) {
	const (
		icbAddr = 0xabcddcba
		count   = 3
	)

	re := NewReplayEngine(&Trace{
		Path:        t.TempDir(),
		CaptureData: mtspData(ciRecord(icbAddr, count)),
	})

	validation, err := re.ValidateReplay()
	if err != nil {
		t.Fatal(err)
	}
	if validation.CanReplay {
		t.Fatal("ValidateReplay reported ICB trace can replay")
	}
	if len(validation.Errors) != 1 {
		t.Fatalf("got %d errors, want 1: %#v", len(validation.Errors), validation.Errors)
	}
	for _, want := range []string{
		"1 indirect command buffer executions cannot be replayed",
		"sequence=0",
		"encoder=0",
		"icb=0xabcddcba",
		"count=3",
	} {
		if !strings.Contains(validation.Errors[0], want) {
			t.Fatalf("validation error %q does not contain %q", validation.Errors[0], want)
		}
	}
}

func TestAnalyzeReplayResolvesDispatchFromPipeline(t *testing.T) {
	const (
		functionAddr = 0x1000
		pipelineAddr = 0x2000
	)

	re := NewReplayEngine(&Trace{
		Path:        t.TempDir(),
		CaptureData: mtspData(ctDispatchRecord(pipelineAddr, 0)),
		DeviceResources: map[string][]byte{
			"0xabc": mtspData(
				csRecord(functionAddr, "vector_add"),
				cttRecord(functionAddr, pipelineAddr),
			),
		},
		FunctionToName: make(map[uint64]string),
	})

	plan, err := re.AnalyzeReplay()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(plan.Commands), 1; got != want {
		t.Fatalf("len(commands) = %d, want %d", got, want)
	}
	cmd := plan.Commands[0]
	if got, want := cmd.FunctionAddr, uint64(functionAddr); got != want {
		t.Fatalf("function address = 0x%x, want 0x%x", got, want)
	}
	if got, want := cmd.FunctionName, "vector_add"; got != want {
		t.Fatalf("function name = %q, want %q", got, want)
	}

	validation, err := re.ValidateReplay()
	if err != nil {
		t.Fatal(err)
	}
	for _, warning := range validation.Warnings {
		if strings.Contains(warning, "unresolved function names") {
			t.Fatalf("validation warning = %q, want resolved dispatch", warning)
		}
	}
}

func TestUnsupportedICBExecutionErrorWrapsSentinel(t *testing.T) {
	err := unsupportedICBExecutionError(ReplayCommand{
		Type:         "execute_icb",
		SequenceNum:  5,
		EncoderIndex: 1,
		ICBAddr:      0xfeed,
		ICBCount:     2,
	})

	if !errors.Is(err, ErrICBExecutionUnsupported) {
		t.Fatalf("error = %v, want ErrICBExecutionUnsupported", err)
	}
	for _, want := range []string{"sequence=5", "encoder=1", "icb=0xfeed", "count=2"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not contain %q", err, want)
		}
	}
}

func TestFormatReplayValidationShowsICBError(t *testing.T) {
	validation := &ReplayValidation{
		CanReplay: false,
		Errors: []string{
			"1 indirect command buffer executions cannot be replayed: first indirect command buffer execution cannot be replayed: sequence=0 encoder=0 icb=0xabcddcba count=3",
		},
	}

	output := FormatReplayValidation(validation)
	for _, want := range []string{
		"Trace CANNOT be replayed",
		"indirect command buffer executions cannot be replayed",
		"icb=0xabcddcba",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("formatted validation missing %q in:\n%s", want, output)
		}
	}
}

func ciRecord(icbAddr uint64, count uint32) []byte {
	rec := make([]byte, 52)
	binary.LittleEndian.PutUint32(rec[0x00:], uint32(len(rec)))
	copy(rec[0x24:], []byte("Ci\x00\x00"))
	binary.LittleEndian.PutUint64(rec[0x28:], icbAddr)
	binary.LittleEndian.PutUint32(rec[0x30:], count)
	return rec
}

func ctDispatchRecord(pipelineAddr, functionAddr uint64, bindings ...uint64) []byte {
	const markerOffset = 0x24

	rec := make([]byte, markerOffset+0x1c+len(bindings)*8)
	binary.LittleEndian.PutUint32(rec[0x00:], uint32(len(rec)))
	copy(rec[markerOffset:], []byte("Ct\x00\x00"))
	binary.LittleEndian.PutUint64(rec[markerOffset+0x04:], pipelineAddr)
	binary.LittleEndian.PutUint64(rec[markerOffset+0x0c:], functionAddr)
	binary.LittleEndian.PutUint32(rec[markerOffset+0x14:], uint32(len(bindings)))
	binary.LittleEndian.PutUint32(rec[markerOffset+0x18:], 8)
	for i, binding := range bindings {
		binary.LittleEndian.PutUint64(rec[markerOffset+0x1c+i*8:], binding)
	}
	return rec
}
