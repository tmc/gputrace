package profilerraw

import (
	"crypto/sha256"
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
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Index  *int   `json:"index,omitempty"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

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
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return Artifact{}, fmt.Errorf("hash profiler artifact %s: %w", name, err)
	}
	kind, index := artifactIdentity(name)
	return Artifact{
		Name: name, Kind: kind, Index: index, Size: size,
		SHA256: hex.EncodeToString(h.Sum(nil)),
	}, nil
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
