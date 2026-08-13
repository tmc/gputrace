package profilerraw

import (
	"crypto/sha256"
	"encoding/binary"
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

func TestInventoryTimelineHeader(t *testing.T) {
	dir := t.TempDir()
	data := make([]byte, timelineHeaderSize)
	const magic = 0x0102030405060708
	binary.LittleEndian.PutUint64(data[0:8], magic)
	binary.LittleEndian.PutUint32(data[12:16], 752)
	binary.LittleEndian.PutUint64(data[32:40], 4096)
	binary.LittleEndian.PutUint64(data[80:88], 99)
	binary.LittleEndian.PutUint64(data[104:112], 12345)
	if err := os.WriteFile(filepath.Join(dir, "Timeline_f_3.raw"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Inventory(dir)
	if err != nil {
		t.Fatal(err)
	}
	header := got.Artifacts[0].TimelineHeader
	if header == nil || header.Magic != magic || header.CounterCount != 752 || header.DataOffset != 4096 || header.EntryCount != 99 || header.Timestamp != 12345 {
		t.Fatalf("timeline header = %#v", header)
	}
	data[80] = 100
	if err := os.WriteFile(filepath.Join(dir, "Timeline_f_3.raw"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	changed, err := Inventory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Artifacts[0].TimelineHeader.EntryCount == header.EntryCount || changed.SHA256 == got.SHA256 {
		t.Fatal("header mutation did not change decoded value and inventory digest")
	}
}

func TestInventoryRejectsShortTimelineHeader(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Timeline_f_0.raw"), make([]byte, timelineHeaderSize-1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Inventory(dir); err == nil {
		t.Fatal("short timeline header accepted")
	}
}

func TestInventoryDigestChangesWithContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Timeline_f_0.raw")
	data := make([]byte, timelineHeaderSize+1)
	binary.LittleEndian.PutUint64(data[0:8], 1)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := Inventory(dir)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] = 1
	if err := os.WriteFile(path, data, 0o600); err != nil {
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
