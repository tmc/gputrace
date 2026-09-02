package trace

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// DeviceResources describes resources found in a device-resources MTSP file.
// It is deliberately independent of Metal and can be used on any platform.
type DeviceResources struct {
	DeviceAddress           uint64
	Resources               []ResourceNode
	OptimizationSuggestions []string
}

// ResourceNode describes one resource record discovered in an MTSP stream.
type ResourceNode struct {
	Type    string
	Label   string
	Address uint64
	Size    uint64
	Offset  int
}

// ParseDeviceResources reads a device-resources-* or
// unused-device-resources-* file and extracts resource records.
func ParseDeviceResources(path string) (*DeviceResources, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read device resources: %w", err)
	}
	return ParseDeviceResourcesData(filepath.Base(path), data)
}

// ParseDeviceResourcesData parses one device-resources MTSP payload.
// name is used to identify the device address and whether the file contains
// resources marked unused by the capture.
func ParseDeviceResourcesData(name string, data []byte) (*DeviceResources, error) {
	if len(data) < len(MagicMTSP) || !bytes.Equal(data[:len(MagicMTSP)], []byte(MagicMTSP)) {
		return nil, fmt.Errorf("parse device resources: %w", ErrInvalidMagic)
	}
	if len(data) >= 16 {
		if _, err := ReadMTSPHeader(data); err != nil {
			return nil, fmt.Errorf("read device resources header: %w", err)
		}
	}

	resources := &DeviceResources{DeviceAddress: deviceAddress(name)}
	resources.Resources = append(resources.Resources, parseBufferResources(data)...)
	resources.Resources = append(resources.Resources, parseTextureResources(data)...)
	resources.Resources = append(resources.Resources, parseSchemaRecords(data)...)
	if strings.HasPrefix(name, "unused-device-resources-") {
		if len(resources.Resources) != 0 {
			resources.OptimizationSuggestions = append(resources.OptimizationSuggestions,
				"review unused resources and release them when their lifetime ends")
		}
	}
	return resources, nil
}

// parseSchemaRecords retains named resource-schema records from real MTSP
// sidecars. These records describe the resource tables present in a capture;
// their addresses are not resource allocations, so Size remains zero.
func parseSchemaRecords(data []byte) []ResourceNode {
	resources := make([]ResourceNode, 0)
	seen := make(map[string]bool)
	marker := []byte("CSuwuw")
	for search := 0; ; {
		rel := bytes.Index(data[search:], marker)
		if rel < 0 {
			break
		}
		offset := search + rel
		address, label := parseCSuwuwAt(data, offset)
		if label == "" || seen[label] {
			search = offset + len(marker)
			continue
		}
		seen[label] = true
		resources = append(resources, ResourceNode{
			Type:    "schema",
			Label:   label,
			Address: address,
			Offset:  offset,
		})
		search = offset + len(marker)
	}
	return resources
}

func deviceAddress(name string) uint64 {
	for _, prefix := range []string{"device-resources-", "unused-device-resources-"} {
		if strings.HasPrefix(name, prefix) {
			value := strings.TrimPrefix(name, prefix)
			address, err := strconv.ParseUint(strings.TrimPrefix(value, "0x"), 16, 64)
			if err == nil {
				return address
			}
		}
	}
	return 0
}

func parseBufferResources(data []byte) []ResourceNode {
	marker := []byte("CU<b>ulul")
	var resources []ResourceNode
	for search := 0; ; {
		rel := bytes.Index(data[search:], marker)
		if rel < 0 {
			break
		}
		offset := search + rel
		nameStart := offset + len(marker) + 3 + 8
		if nameStart >= len(data) {
			break
		}
		nameEnd := bytes.IndexByte(data[nameStart:], 0)
		if nameEnd <= 0 || nameEnd > 128 {
			search = offset + len(marker)
			continue
		}
		label := string(data[nameStart : nameStart+nameEnd])
		if !strings.HasPrefix(label, "MTLBuffer-") {
			search = offset + len(marker)
			continue
		}
		paddingEnd := nameStart + nameEnd + 5
		if paddingEnd+8 > len(data) {
			search = offset + len(marker)
			continue
		}
		resources = append(resources, ResourceNode{
			Type:    "buffer",
			Label:   label,
			Address: readAddress(data, offset+len(marker)+3),
			Size:    binary.LittleEndian.Uint64(data[paddingEnd : paddingEnd+8]),
			Offset:  offset,
		})
		search = paddingEnd + 8
	}
	return resources
}

func parseTextureResources(data []byte) []ResourceNode {
	marker := []byte("MTLTexture-")
	var resources []ResourceNode
	for search := 0; ; {
		rel := bytes.Index(data[search:], marker)
		if rel < 0 {
			break
		}
		offset := search + rel
		end := bytes.IndexByte(data[offset:], 0)
		if end <= len(marker) || end > 128 {
			search = offset + len(marker)
			continue
		}
		resources = append(resources, ResourceNode{
			Type:   "texture",
			Label:  string(data[offset : offset+end]),
			Offset: offset,
		})
		search = offset + end
	}
	return resources
}

func readAddress(data []byte, offset int) uint64 {
	if offset < 0 || offset+8 > len(data) {
		return 0
	}
	return binary.LittleEndian.Uint64(data[offset : offset+8])
}

// ParseNestedRecords attempts to parse the data of the current record as a sequence
// of nested MTSP records. This is used for container records like CS and Ci.
// It skips the first 16 bytes (standard MTSP header/padding for containers)
// and attempts to parse the rest.
func (t *Trace) ParseNestedRecords(rec MTSPRecord) ([]MTSPRecord, error) {
	// Standard container header size heuristic
	const containerHeaderSize = 16

	if len(rec.Data) <= containerHeaderSize {
		return nil, nil // Not enough data to be a container
	}

	// Attempt to parse the payload
	nestedData := rec.Data[containerHeaderSize:]
	nestedRecords, err := t.ParseMTSPFromData(nestedData)
	if err != nil {
		return nil, fmt.Errorf("failed to parse nested data: %w", err)
	}

	// Heuristic: If we found valid records, return them.
	// If ParseMTSPFromData returns empty or error, it wasn't a container.
	if len(nestedRecords) > 0 {
		return nestedRecords, nil
	}

	return nil, nil
}
