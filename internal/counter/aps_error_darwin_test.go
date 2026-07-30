//go:build darwin && gputrace_private_bindings

package counter

import (
	"errors"
	"strings"
	"testing"
)

func TestAPSUnavailableErrorPreservesNSError(t *testing.T) {
	err := &APSUnavailableError{
		Domain: "GPURawCounterErrorDomain",
		Code:   -1,
		Detail: "Fail to instantiate AGXGPURawCounterSourceGroup",
	}
	if !errors.Is(err, ErrAPSUnavailable) {
		t.Fatal("APSUnavailableError does not unwrap to ErrAPSUnavailable")
	}
	var unavailable *APSUnavailableError
	if !errors.As(err, &unavailable) || unavailable.Code != -1 {
		t.Fatalf("errors.As = %#v, want code -1", unavailable)
	}
	if got, want := err.Error(), "GPURawCounterErrorDomain (code -1): Fail to instantiate AGXGPURawCounterSourceGroup"; !strings.Contains(got, want) {
		t.Fatalf("Error() = %q, want substring %q", got, want)
	}
}
