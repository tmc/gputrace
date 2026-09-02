// Package tracebundle inspects the contents of GPU trace bundles.
package tracebundle

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PayloadClass describes which trace evidence a bundle contains.
type PayloadClass string

const (
	PayloadFull         PayloadClass = "full"
	PayloadProfilerOnly PayloadClass = "profiler-only"
	PayloadIncomplete   PayloadClass = "incomplete"
)

// Payload describes the evidence available in a trace bundle.
type Payload struct {
	Class             PayloadClass
	HasCapture        bool
	HasRawResources   bool
	HasProfilerStream bool
}

// InspectPayload classifies the source-backed payload in path.
func InspectPayload(path string) (Payload, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return Payload{}, fmt.Errorf("read trace bundle: %w", err)
	}

	var p Payload
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			if strings.HasSuffix(name, ".gpuprofiler_raw") && nonemptyFile(filepath.Join(path, name, "streamData")) {
				p.HasProfilerStream = true
			}
			continue
		}
		switch {
		case name == "capture" || name == "unsorted-capture":
			p.HasCapture = p.HasCapture || nonemptyFile(filepath.Join(path, name))
		case isRawResource(name):
			p.HasRawResources = p.HasRawResources || nonemptyFile(filepath.Join(path, name))
		}
	}
	if strings.HasSuffix(path, ".gpuprofiler_raw") && nonemptyFile(filepath.Join(path, "streamData")) {
		p.HasProfilerStream = true
	}

	switch {
	case p.HasCapture && p.HasRawResources:
		p.Class = PayloadFull
	case p.HasProfilerStream && !p.HasCapture && !p.HasRawResources:
		p.Class = PayloadProfilerOnly
	default:
		p.Class = PayloadIncomplete
	}
	return p, nil
}

func isRawResource(name string) bool {
	for _, prefix := range []string{
		"device-resources-",
		"delta-device-resources-",
		"unused-device-resources-",
		"MTLBuffer-",
		"MTLHeap-",
	} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func nonemptyFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}
