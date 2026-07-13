package analysis

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"

	"github.com/tmc/gputrace/internal/trace"
)

type resourceBufferInventory struct {
	Buffers int
	Bytes   uint64
}

func extractResourceBufferInventory(t *trace.Trace) resourceBufferInventory {
	if t == nil || t.Path == "" {
		return resourceBufferInventory{}
	}
	sizes, _ := ResourceBufferInventory(t.Path)
	var inv resourceBufferInventory
	inv.Buffers = len(sizes)
	for _, size := range sizes {
		inv.Bytes += size
	}
	return inv
}

// ResourceBufferInventory returns buffer sizes and their first offsets in
// device-resource sidecars. Final buffer files take precedence over sidecar
// sizes when both are present.
func ResourceBufferInventory(tracePath string) (sizes map[string]uint64, offsets map[string]int64) {
	sizes = make(map[string]uint64)
	offsets = make(map[string]int64)
	entries, err := os.ReadDir(tracePath)
	if err != nil {
		return sizes, offsets
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "device-resources-") && !strings.HasPrefix(name, "delta-device-resources-") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(tracePath, name))
		if err != nil {
			continue
		}
		addResourceBufferSizes(sizes, offsets, data)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "MTLBuffer-") || !strings.HasSuffix(name, "-0") {
			continue
		}
		info, err := os.Lstat(filepath.Join(tracePath, name))
		if err != nil || info.Mode()&os.ModeSymlink != 0 || info.IsDir() {
			continue
		}
		sizes[name] = uint64(info.Size())
	}
	return sizes, offsets
}

func addResourceBufferSizes(sizes map[string]uint64, offsets map[string]int64, data []byte) {
	offset := 0
	for {
		pos := bytes.Index(data[offset:], []byte("MTLBuffer-"))
		if pos == -1 {
			return
		}
		start := offset + pos
		end := bytes.IndexByte(data[start:], 0)
		if end == -1 || end > 100 {
			offset = start + len("MTLBuffer-")
			continue
		}
		name := string(data[start : start+end])
		if size, ok := ResourceBufferRecordSize(data, start+end); ok {
			if _, ok := sizes[name]; !ok {
				sizes[name] = size
			}
			if _, ok := offsets[name]; !ok {
				offsets[name] = int64(start)
			}
		}
		offset = start + end + 1
	}
}

// ResourceBufferRecordSize returns the size stored after a NUL-terminated
// buffer name in a device-resource record.
func ResourceBufferRecordSize(data []byte, nameEnd int) (uint64, bool) {
	if nameEnd+9 > len(data) {
		return 0, false
	}
	size := binary.LittleEndian.Uint64(data[nameEnd+1 : nameEnd+9])
	if size == 0 || size > 1<<30 {
		return 0, false
	}
	return size, true
}
