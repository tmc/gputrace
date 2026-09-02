package profilerraw

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Artifact describes one regular file in a profiler archive. Name is a
// basename; Inventory does not retain host paths.
type Artifact struct {
	Name           string          `json:"name"`
	Kind           string          `json:"kind"`
	Index          *int            `json:"index,omitempty"`
	Size           int64           `json:"size"`
	SHA256         string          `json:"sha256"`
	TimelineHeader *TimelineHeader `json:"timeline_header,omitempty"`
}

// TimelineHeader contains the named fields in the fixed 128-byte prefix of a
// Timeline_f raw file. Timestamp is retained in its private raw domain.
type TimelineHeader struct {
	Magic        uint64 `json:"magic"`
	CounterCount uint32 `json:"counter_count"`
	DataOffset   uint64 `json:"data_offset_bytes"`
	EntryCount   uint64 `json:"entry_count"`
	Timestamp    uint64 `json:"timestamp_raw"`
}

const timelineHeaderSize = 128

// ArtifactInventory is a deterministic content-identified profiler archive.
type ArtifactInventory struct {
	Artifacts  []Artifact `json:"artifacts"`
	TotalBytes int64      `json:"total_bytes"`
	SHA256     string     `json:"sha256"`
}

// Inventory hashes every regular file directly beneath dir. Symlinks and
// nested directories are ignored.
func Inventory(dir string) (*ArtifactInventory, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read profiler directory: %w", err)
	}
	result := &ArtifactInventory{}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("stat profiler artifact %s: %w", entry.Name(), err)
		}
		if !info.Mode().IsRegular() {
			continue
		}
		artifact, err := hashArtifact(filepath.Join(dir, entry.Name()), entry.Name(), info.Size())
		if err != nil {
			return nil, err
		}
		result.Artifacts = append(result.Artifacts, artifact)
		result.TotalBytes += artifact.Size
	}
	sort.Slice(result.Artifacts, func(i, j int) bool {
		return result.Artifacts[i].Name < result.Artifacts[j].Name
	})
	h := sha256.New()
	for _, artifact := range result.Artifacts {
		fmt.Fprintf(h, "%s\x00%s\x00%d\x00%s\n", artifact.Kind, artifact.Name, artifact.Size, artifact.SHA256)
	}
	result.SHA256 = hex.EncodeToString(h.Sum(nil))
	return result, nil
}

func hashArtifact(path, name string, size int64) (Artifact, error) {
	f, err := os.Open(path)
	if err != nil {
		return Artifact{}, fmt.Errorf("open profiler artifact %s: %w", name, err)
	}
	defer f.Close()
	kind, index := artifactIdentity(name)
	h := sha256.New()
	var header *TimelineHeader
	if kind == "timeline" {
		var data [timelineHeaderSize]byte
		if _, err := io.ReadFull(f, data[:]); err != nil {
			return Artifact{}, fmt.Errorf("read profiler artifact %s header: %w", name, err)
		}
		header = decodeTimelineHeader(data[:])
		h.Write(data[:])
	}
	if _, err := io.Copy(h, f); err != nil {
		return Artifact{}, fmt.Errorf("hash profiler artifact %s: %w", name, err)
	}
	return Artifact{
		Name: name, Kind: kind, Index: index, Size: size,
		SHA256: hex.EncodeToString(h.Sum(nil)), TimelineHeader: header,
	}, nil
}

func decodeTimelineHeader(data []byte) *TimelineHeader {
	magic := binary.LittleEndian.Uint64(data[0:8])
	return &TimelineHeader{
		Magic:        magic,
		CounterCount: binary.LittleEndian.Uint32(data[12:16]),
		DataOffset:   binary.LittleEndian.Uint64(data[32:40]),
		EntryCount:   binary.LittleEndian.Uint64(data[80:88]),
		Timestamp:    binary.LittleEndian.Uint64(data[104:112]),
	}
}

func artifactIdentity(name string) (string, *int) {
	if name == "streamData" {
		return "stream_data", nil
	}
	for _, family := range []struct {
		prefix string
		kind   string
	}{
		{"Counters_f_", "counters"},
		{"Profiling_f_", "profiling"},
		{"Timeline_f_", "timeline"},
	} {
		if strings.HasPrefix(name, family.prefix) && strings.HasSuffix(name, ".raw") {
			value := strings.TrimSuffix(strings.TrimPrefix(name, family.prefix), ".raw")
			index, err := strconv.Atoi(value)
			if err == nil && index >= 0 {
				return family.kind, &index
			}
		}
	}
	return "other", nil
}
