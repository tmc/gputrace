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
