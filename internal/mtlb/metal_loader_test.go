//go:build darwin

package mtlb

import (
	"os"
	"testing"
)

const mtlbTestFileEnv = "GPUTRACE_MTLB_TEST_FILE"

func loadMTLBTestData(tb testing.TB) []byte {
	tb.Helper()

	mtlbPath := os.Getenv(mtlbTestFileEnv)
	if mtlbPath == "" {
		tb.Skipf("set %s to run MTLB Metal fixture tests", mtlbTestFileEnv)
	}

	info, err := os.Stat(mtlbPath)
	if os.IsNotExist(err) {
		tb.Skipf("MTLB test file not found: %s", mtlbPath)
	}
	if err != nil {
		tb.Fatalf("failed to stat MTLB test file %s: %v", mtlbPath, err)
	}
	if info.IsDir() {
		tb.Fatalf("MTLB test file path is a directory: %s", mtlbPath)
	}

	data, err := os.ReadFile(mtlbPath)
	if err != nil {
		tb.Fatalf("failed to read MTLB test file %s: %v", mtlbPath, err)
	}
	return data
}

func TestMetalLoader(t *testing.T) {
	data := loadMTLBTestData(t)

	lib, err := LoadMTLBWithMetal(data)
	if err != nil {
		t.Fatalf("LoadMTLBWithMetal failed: %v", err)
	}

	count := lib.FunctionCount()
	t.Logf("Loaded library with %d functions", count)

	if count == 0 {
		t.Error("Expected at least some functions")
	}

	// Get first 5 function names
	names := lib.FunctionNames()
	t.Logf("First 5 functions:")
	for i, name := range names {
		if i >= 5 {
			break
		}
		t.Logf("  %d: %s", i, name)
	}
}

func BenchmarkParserListFunctions(b *testing.B) {
	data := loadMTLBTestData(b)

	mtlb, err := ParseMTLB(data)
	if err != nil {
		b.Fatalf("ParseMTLB failed: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = mtlb.ListFunctions()
	}
}

func BenchmarkMetalLoaderFunctionNames(b *testing.B) {
	data := loadMTLBTestData(b)

	lib, err := LoadMTLBWithMetal(data)
	if err != nil {
		b.Fatalf("LoadMTLBWithMetal failed: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = lib.FunctionNames()
	}
}

func BenchmarkMetalLoaderLoad(b *testing.B) {
	data := loadMTLBTestData(b)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = LoadMTLBWithMetal(data)
	}
}
