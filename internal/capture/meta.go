package capture

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// MetaFileName is the provenance sidecar written into a captured bundle.
// Xcode's own bundle members are untouched; this file only adds host
// identity so cross-session and cross-machine comparisons can be labeled.
const MetaFileName = "gputrace-meta.json"

// Meta records how, when, and on which host a Metal bundle was captured.
type Meta struct {
	Schema    string    `json:"schema"`
	CreatedAt time.Time `json:"created_at"`
	Command   []string  `json:"command"`
	Hostname  string    `json:"hostname,omitempty"`
	OS        string    `json:"os,omitempty"`
	Chip      string    `json:"chip,omitempty"`
	RunID     string    `json:"run_id,omitempty"`
}

func writeMeta(bundle string, argv []string, runID string) error {
	meta := Meta{
		Schema:    "gputrace.capture/v1",
		CreatedAt: time.Now(),
		Command:   argv,
		RunID:     runID,
	}
	meta.Hostname, _ = os.Hostname()
	meta.OS = commandOutput("uname", "-sr")
	meta.Chip = commandOutput("sysctl", "-n", "machdep.cpu.brand_string")
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("capture: encode bundle meta: %w", err)
	}
	path := filepath.Join(bundle, MetaFileName)
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("capture: write bundle meta: %w", err)
	}
	return nil
}

// ReadMeta reads the provenance sidecar from a Metal bundle. Bundles
// captured before the sidecar existed, or exported by Xcode, do not have
// one; callers must treat absence as "provenance not recorded", not as an
// error state of the bundle.
func ReadMeta(bundle string) (Meta, error) {
	var meta Meta
	data, err := os.ReadFile(filepath.Join(bundle, MetaFileName))
	if err != nil {
		return meta, err
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return meta, fmt.Errorf("read %s: %w", filepath.Join(bundle, MetaFileName), err)
	}
	return meta, nil
}

func commandOutput(name string, args ...string) string {
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
