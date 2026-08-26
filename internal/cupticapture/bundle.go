// Bundle is the on-disk capture format for NVIDIA workloads:
// a directory named *.gpucapture holding activity events, optional device
// samples, and provenance metadata.
package cupticapture

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// BundleFileNames are the well-known members of a capture bundle.
const (
	EventsFileName     = "events.jsonl"
	SamplesFileName    = "nvml_samples.jsonl"
	MetaFileName       = "meta.json"
	bundleSuffix       = ".gpucapture"
)

// Meta records how and when a bundle was produced.
type Meta struct {
	Schema        string   `json:"schema"`
	CreatedAt     time.Time `json:"created_at"`
	Command       []string `json:"command"`
	Dir           string   `json:"dir,omitempty"`
	GPUTRACEVersion string  `json:"gputrace_version,omitempty"`
	ShimPath      string   `json:"shim_path,omitempty"`
	Hostname      string   `json:"hostname,omitempty"`

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

// IsBundle reports whether path looks like a capture bundle directory.
func IsBundle(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return false
	}
	_, err = os.Stat(filepath.Join(path, EventsFileName))
	return err == nil
}

// ReadEventsJSONL returns the events file path for a bundle or bare JSONL.
func ResolveEvents(path string) (string, error) {
	if IsBundle(path) {
		p := filepath.Join(path, EventsFileName)
		if _, err := os.Stat(p); err != nil {
			return "", fmt.Errorf("bundle %s has no %s", path, EventsFileName)
		}
		return p, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory but not a .gpucapture bundle", path)
	}
	return path, nil
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
