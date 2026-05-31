package trace

import (
	"bytes"
	"os"
	"testing"
)

func TestDumpOutputMatchesExpected(t *testing.T) {
	tracePath, expectedPath := apiCallGoldenPathsFromEnv(t)

	trace := &Trace{
		Path: tracePath,
	}

	// Read expected output
	expected, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("read %s=%q: %v", apiCallExpectedEnv, expectedPath, err)
	}

	// Generate actual output
	var buf bytes.Buffer
	err = trace.FormatAPICallList(&buf)
	if err != nil {
		t.Fatalf("FormatAPICallList failed: %v", err)
	}

	actual := buf.Bytes()

	// Compare
	if !bytes.Equal(expected, actual) {
		t.Errorf("Output does not match expected:\nExpected:\n%s\nActual:\n%s", expected, actual)
	}
}
