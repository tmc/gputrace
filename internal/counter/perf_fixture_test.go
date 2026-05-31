package counter

import (
	"os"
	"testing"

	"github.com/tmc/gputrace/internal/trace"
)

const requirePerfFixturesEnv = "GPUTRACE_REQUIRE_PERF_FIXTURES"

func requirePerfFixturePath(t *testing.T, path string) {
	t.Helper()

	if _, err := os.Stat(path); err != nil {
		skipOrFatalPerfFixture(t, path, err)
	}
}

func openPerfTraceOrSkip(t *testing.T, path string) *trace.Trace {
	t.Helper()

	tr, err := trace.Open(path)
	if err != nil {
		skipOrFatalPerfFixture(t, path, err)
	}
	return tr
}

func skipOrFatalPerfFixture(t *testing.T, path string, err error) {
	t.Helper()

	if os.Getenv(requirePerfFixturesEnv) != "" {
		t.Fatalf("required perf fixture unavailable: %s: %v", path, err)
	}
	t.Skipf("perf fixture not available: %s: %v", path, err)
}
