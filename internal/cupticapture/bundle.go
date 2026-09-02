// Bundle is the on-disk capture format for NVIDIA workloads:
// a directory named *.gpucapture holding activity events, optional device
// samples, and provenance metadata.
package cupticapture

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// BundleFileNames are the well-known members of a capture bundle.
const (
	EventsFileName  = "events.jsonl"
	SamplesFileName = "nvml_samples.jsonl"
	MetaFileName    = "meta.json"
	bundleSuffix    = ".gpucapture"
)

// Meta records how and when a bundle was produced.
type Meta struct {
	Schema          string    `json:"schema"`
	CreatedAt       time.Time `json:"created_at"`
	Command         []string  `json:"command"`
	Dir             string    `json:"dir,omitempty"`
	GPUTRACEVersion string    `json:"gputrace_version,omitempty"`
	ShimPath        string    `json:"shim_path,omitempty"`
	Hostname        string    `json:"hostname,omitempty"`

	// Hardware provenance: lets optimize compare refuse to diff runs from
	// different machines, drivers, or GPUs — a comparison across those is
	// noise, not evidence.
	GPUName       string `json:"gpu_name,omitempty"`
	GPUUUID       string `json:"gpu_uuid,omitempty"`
	DriverVersion string `json:"driver_version,omitempty"`
}

// CreateBundle makes an empty bundle directory with meta.json written.
// The events file is created by the shim itself when the target runs.
func CreateBundle(dir string, meta Meta) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if filepath.Ext(dir) != ".gpucapture" {
		// Not fatal, but keep the convention visible in errors elsewhere.
		_ = bundleSuffix
	}
	meta.Schema = "gputrace.capture/v1"
	if meta.CreatedAt.IsZero() {
		meta.CreatedAt = time.Now()
	}
	if meta.Hostname == "" {
		h, _ := os.Hostname()
		meta.Hostname = h
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, MetaFileName), append(data, '\n'), 0o644)
}

// ReadMeta reads a bundle's provenance. It is what lets a later tool
// re-run the very command a capture recorded rather than an approximation
// of it.
func ReadMeta(dir string) (Meta, error) {
	var meta Meta
	data, err := os.ReadFile(filepath.Join(dir, MetaFileName))
	if err != nil {
		return meta, err
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return meta, fmt.Errorf("read %s: %w", filepath.Join(dir, MetaFileName), err)
	}
	return meta, nil
}

// IsBundle reports whether path looks like a capture bundle directory.
// A directory containing any events shard (events.jsonl or
// events.<pid>.jsonl) or a meta.json qualifies.
func IsBundle(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return false
	}
	if _, err := os.Stat(filepath.Join(path, MetaFileName)); err == nil {
		return true
	}
	matches, _ := filepath.Glob(filepath.Join(path, "events*"))
	return len(matches) > 0
}

// ResolveEvents returns the event file paths for a bundle or bare JSONL.
// A bundle may hold multiple per-PID shards (events.jsonl,
// events.<pid>.jsonl) from multi-process targets; callers read all of them
// and merge on timestamp.
func ResolveEvents(path string) ([]string, error) {
	if IsBundle(path) {
		matches, err := filepath.Glob(filepath.Join(path, "events*.jsonl"))
		if err != nil || len(matches) == 0 {
			return nil, fmt.Errorf("bundle %s has no events files", path)
		}
		sort.Strings(matches)
		return matches, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%s is a directory but not a .gpucapture bundle", path)
	}
	return []string{path}, nil
}

// OpenEvents opens every event file for a capture input and returns a
// reader over their concatenated contents. Per-PID shards are read in
// filename order; records carry absolute timestamps so consumers merge by
// sorting rather than relying on file order.
func OpenEvents(path string) (io.Reader, func(), error) {
	paths, err := ResolveEvents(path)
	if err != nil {
		return nil, nil, err
	}
	files := make([]*os.File, 0, len(paths))
	closers := func() {
		for _, f := range files {
			f.Close()
		}
	}
	var readers []io.Reader
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			closers()
			return nil, nil, err
		}
		files = append(files, f)
		readers = append(readers, f)
	}
	return io.MultiReader(readers...), closers, nil
}

// ResolveSamples returns the samples file path if the input is a bundle,
// or the explicit samples argument if given. Empty when neither applies.
func ResolveSamples(input, explicitSamples string) string {
	if explicitSamples != "" {
		return explicitSamples
	}
	if IsBundle(input) {
		p := filepath.Join(input, SamplesFileName)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}
