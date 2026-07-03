//go:build darwin

// Package counter provides GPU performance counter parsing and mapping.
// This file provides mappings from GPUCounterGraph.plist - the authoritative
// source for counter metadata in Xcode's GPU debugger.

package counter

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// CounterDataType indicates the data type for a counter value.
type CounterDataType int

const (
	// DataTypePercentage is for percentage values (0-100)
	DataTypePercentage CounterDataType = 0
	// DataTypeBandwidth is for bandwidth values (GB/s)
	DataTypeBandwidth CounterDataType = 1
	// DataTypeCount is for count values
	DataTypeCount CounterDataType = 2
	// DataTypeBytes is for byte counts (uint64)
	DataTypeBytes CounterDataType = 3
)

// CounterMetadata holds metadata for a performance counter from GPUCounterGraph.plist.
type CounterMetadata struct {
	Name           string          `json:"name"`
	Description    string          `json:"description"`
	Unit           string          `json:"unit"`
	VendorCounters []string        `json:"vendorCounters"`
	DataType       CounterDataType `json:"datatype"`
	Visible        bool            `json:"visible"`
	BatchFiltered  bool            `json:"batchfiltered"`
	ToolsCounter   string          `json:"toolsCounter"`
}

// GPUCounterGraph represents the parsed GPUCounterGraph.plist structure.
type GPUCounterGraph struct {
	Counters       map[string]CounterMetadata `json:"counters"`
	TimelineGroups []TimelineGroup            `json:"timelineGroups"`
}

// TimelineGroup represents a group of counters in the timeline view.
type TimelineGroup struct {
	Name     string   `json:"name"`
	Counters []string `json:"counters"`
	Style    string   `json:"style,omitempty"`
	Subtitle string   `json:"subtitle,omitempty"`
}

var (
	plistData     *GPUCounterGraph
	plistLoadOnce sync.Once
	plistLoadErr  error

	// vendorToUserName maps vendor counter names to user-facing names
	vendorToUserName map[string]string
	// userToVendorNames maps user-facing names to vendor counter names
	userToVendorNames map[string][]string
)

// DefaultPlistPath returns the default path to GPUCounterGraph.plist in Xcode.
func DefaultPlistPath() string {
	return "/Applications/Xcode.app/Contents/PlugIns/GPUDebugger.ideplugin/Contents/Resources/GPUCounterGraph.plist"
}

// LoadGPUCounterGraph loads and parses GPUCounterGraph.plist from Xcode.
// Results are cached after first successful load.
func LoadGPUCounterGraph() (*GPUCounterGraph, error) {
	plistLoadOnce.Do(func() {
		plistData, plistLoadErr = loadGPUCounterGraphFromPath(DefaultPlistPath())
		if plistLoadErr == nil {
			buildVendorMappings()
		}
	})
	return plistData, plistLoadErr
}

// LoadGPUCounterGraphFromPath loads GPUCounterGraph.plist from a custom path.
func LoadGPUCounterGraphFromPath(path string) (*GPUCounterGraph, error) {
	return loadGPUCounterGraphFromPath(path)
}

func loadGPUCounterGraphFromPath(plistPath string) (*GPUCounterGraph, error) {
	// Convert plist to JSON using plutil
	tmpFile := filepath.Join(os.TempDir(), "GPUCounterGraph.json")
	defer os.Remove(tmpFile)

	// Use plutil to convert - it's available on all macOS systems
	cmd := fmt.Sprintf("plutil -convert json -o %q %q", tmpFile, plistPath)
	if err := runCommand(cmd); err != nil {
		return nil, fmt.Errorf("convert plist: %w", err)
	}

	data, err := os.ReadFile(tmpFile)
	if err != nil {
		return nil, fmt.Errorf("read converted json: %w", err)
	}

	var graph GPUCounterGraph
	if err := json.Unmarshal(data, &graph); err != nil {
		return nil, fmt.Errorf("parse json: %w", err)
	}

	return &graph, nil
}

func runCommand(cmd string) error {
	// Simple shell execution for plutil
	return runShellCommand(cmd)
}

func runShellCommand(cmd string) error {
	// Use /bin/sh -c for shell command execution
	proc := &os.ProcAttr{
		Files: []*os.File{nil, nil, nil},
	}
	p, err := os.StartProcess("/bin/sh", []string{"/bin/sh", "-c", cmd}, proc)
	if err != nil {
		return err
	}
	state, err := p.Wait()
	if err != nil {
		return err
	}
	if !state.Success() {
		return fmt.Errorf("command failed: %s", cmd)
	}
	return nil
}

func buildVendorMappings() {
	if plistData == nil {
		return
	}

	vendorToUserName = make(map[string]string)
	userToVendorNames = make(map[string][]string)

	for userName, meta := range plistData.Counters {
		userToVendorNames[userName] = meta.VendorCounters
		for _, vendor := range meta.VendorCounters {
			// First vendor counter wins if there are duplicates
			if _, exists := vendorToUserName[vendor]; !exists {
				vendorToUserName[vendor] = userName
			}
		}
	}
}

// CounterMetadataForName returns metadata for a counter by its user-facing name.
func CounterMetadataForName(name string) (*CounterMetadata, bool) {
	graph, err := LoadGPUCounterGraph()
	if err != nil || graph == nil {
		return nil, false
	}
	meta, ok := graph.Counters[name]
	if !ok {
		return nil, false
	}
	return &meta, true
}

// VendorCounterNames returns the vendor counter names for a user-facing name.
func VendorCounterNames(userName string) []string {
	if _, err := LoadGPUCounterGraph(); err != nil {
		return nil
	}
	return userToVendorNames[userName]
}

// UserNameForVendor returns the user-facing name for a vendor counter name.
func UserNameForVendor(vendorName string) (string, bool) {
	if _, err := LoadGPUCounterGraph(); err != nil {
		return "", false
	}
	name, ok := vendorToUserName[vendorName]
	return name, ok
}

// DataTypeForCounter returns the data type for a counter by name.
// Returns DataTypePercentage as default if not found or no datatype specified.
func DataTypeForCounter(name string) CounterDataType {
	meta, ok := CounterMetadataForName(name)
	if !ok {
		return DataTypePercentage
	}
	return meta.DataType
}

// IsCounterVisible returns whether a counter should be visible in the UI.
func IsCounterVisible(name string) bool {
	meta, ok := CounterMetadataForName(name)
	if !ok {
		return true // Default to visible
	}
	return meta.Visible
}

// CounterUnit returns the unit string for a counter.
func CounterUnit(name string) string {
	meta, ok := CounterMetadataForName(name)
	if !ok {
		return ""
	}
	return meta.Unit
}

// CounterDescription returns the description for a counter.
func CounterDescription(name string) string {
	meta, ok := CounterMetadataForName(name)
	if !ok {
		return ""
	}
	return meta.Description
}

// ListAllCounters returns all counter names from GPUCounterGraph.plist.
func ListAllCounters() []string {
	graph, err := LoadGPUCounterGraph()
	if err != nil || graph == nil {
		return nil
	}
	names := make([]string, 0, len(graph.Counters))
	for name := range graph.Counters {
		names = append(names, name)
	}
	return names
}

// ListByteCounters returns all counters with DataTypeBytes.
func ListByteCounters() []string {
	graph, err := LoadGPUCounterGraph()
	if err != nil || graph == nil {
		return nil
	}
	var names []string
	for name, meta := range graph.Counters {
		if meta.DataType == DataTypeBytes {
			names = append(names, name)
		}
	}
	return names
}

// ListBandwidthCounters returns all counters with DataTypeBandwidth.
func ListBandwidthCounters() []string {
	graph, err := LoadGPUCounterGraph()
	if err != nil || graph == nil {
		return nil
	}
	var names []string
	for name, meta := range graph.Counters {
		if meta.DataType == DataTypeBandwidth {
			names = append(names, name)
		}
	}
	return names
}

// CounterFileMapping provides the mapping between file indices and counter metadata.
// This combines the file_mapping.go index information with GPUCounterGraph.plist metadata.
type CounterFileMapping struct {
	FileIndex      int
	UserName       string
	VendorCounters []string
	DataType       CounterDataType
	Unit           string
}

// CounterFileMappings returns mappings for all known counter files (4-39).
// Combines file_mapping.go indices with GPUCounterGraph.plist metadata.
func CounterFileMappings() []CounterFileMapping {
	mappings := make([]CounterFileMapping, 0, len(CounterFileToName))

	for fileIdx, userName := range CounterFileToName {
		mapping := CounterFileMapping{
			FileIndex: fileIdx,
			UserName:  userName,
			DataType:  DataTypeForCounter(userName),
			Unit:      CounterUnit(userName),
		}
		mapping.VendorCounters = VendorCounterNames(userName)
		mappings = append(mappings, mapping)
	}

	return mappings
}

// CounterFileMappingByIndex returns the mapping for a specific file index.
func CounterFileMappingByIndex(fileIndex int) (*CounterFileMapping, bool) {
	userName, ok := CounterFileToName[fileIndex]
	if !ok {
		return nil, false
	}

	mapping := &CounterFileMapping{
		FileIndex:      fileIndex,
		UserName:       userName,
		VendorCounters: VendorCounterNames(userName),
		DataType:       DataTypeForCounter(userName),
		Unit:           CounterUnit(userName),
	}
	return mapping, true
}
