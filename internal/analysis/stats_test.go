package analysis

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGetFileSizeExcludesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "MTLBuffer-1-0")
	if err := os.WriteFile(target, []byte("buffer"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Base(target), filepath.Join(dir, "MTLBuffer-2-0")); err != nil {
		t.Fatal(err)
	}

	if got := getFileSize(dir, filepath.Base(target)); got != 6 {
		t.Fatalf("regular file size = %d, want 6", got)
	}
	if got := getFileSize(dir, "MTLBuffer-2-0"); got != 0 {
		t.Fatalf("symlink size = %d, want 0", got)
	}
}
