package cmd

import (
	"bytes"
	"errors"
	"testing"

	"github.com/tmc/gputrace/internal/metallib"
)

func TestPrintMTLBDetailsReturnsListError(t *testing.T) {
	want := errors.New("invalid function table")
	var out bytes.Buffer
	err := printMTLBDetails(&out, metallib.Header{}, func() ([]string, error) {
		return nil, want
	}, false)
	if !errors.Is(err, want) {
		t.Fatalf("printMTLBDetails error = %v, want %v", err, want)
	}
}
