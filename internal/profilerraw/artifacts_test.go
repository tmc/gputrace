package profilerraw

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestInventory(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"streamData":        "stream",
		"Counters_f_2.raw":  "counter",
		"Profiling_f_1.raw": "profile",
		"notes.txt":         "other",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(dir, "streamData"), filepath.Join(dir, "linked")); err != nil {
		t.Fatal(err)
	}

	got, err := Inventory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Artifacts) != len(files) || got.TotalBytes != int64(len("stream")+len("counter")+len("profile")+len("other")) || got.SHA256 == "" {
		t.Fatalf("inventory = %#v", got)
	}
	byName := make(map[string]Artifact)
	for _, artifact := range got.Artifacts {
		byName[artifact.Name] = artifact
	}
	if artifact := byName["Counters_f_2.raw"]; artifact.Kind != "counters" || artifact.Index == nil || *artifact.Index != 2 {
		t.Fatalf("counter artifact = %#v", artifact)
	}
	wantHash := sha256.Sum256([]byte("stream"))
	if artifact := byName["streamData"]; artifact.Kind != "stream_data" || artifact.Index != nil || artifact.SHA256 != hex.EncodeToString(wantHash[:]) {
		t.Fatalf("streamData artifact = %#v", artifact)
	}
	if byName["notes.txt"].Kind != "other" {
		t.Fatalf("other artifact = %#v", byName["notes.txt"])
	}
}

func TestInventoryDigestChangesWithContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Timeline_f_0.raw")
	if err := os.WriteFile(path, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := Inventory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("after!"), 0o600); err != nil {
		t.Fatal(err)
	}
	after, err := Inventory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if before.SHA256 == after.SHA256 || before.Artifacts[0].SHA256 == after.Artifacts[0].SHA256 {
		t.Fatal("content mutation did not change artifact and inventory digests")
	}
}
