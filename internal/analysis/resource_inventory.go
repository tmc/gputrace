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
	entries, err := os.ReadDir(t.Path)
	if err != nil {
		return resourceBufferInventory{}
	}
	sizes := make(map[string]uint64)
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "MTLBuffer-") || !strings.HasSuffix(name, "-0") {
			continue
		}
		info, err := os.Lstat(filepath.Join(t.Path, name))
		if err != nil || info.Mode()&os.ModeSymlink != 0 || info.IsDir() {
			continue
		}
		sizes[name] = uint64(info.Size())
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "device-resources-") && !strings.HasPrefix(name, "delta-device-resources-") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(t.Path, name))
		if err != nil {
			continue
		}
		addResourceBufferSizes(sizes, data)
	}
	var inv resourceBufferInventory
	inv.Buffers = len(sizes)
	for _, size := range sizes {
		inv.Bytes += size
	}
	return inv
}

func addResourceBufferSizes(sizes map[string]uint64, data []byte) {
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
		if _, ok := sizes[name]; !ok {
			if size, ok := resourceBufferRecordSize(data, start+end); ok {
				sizes[name] = size
			}
		}
		offset = start + end + 1
	}
}

func resourceBufferRecordSize(data []byte, nameEnd int) (uint64, bool) {
	if nameEnd+9 > len(data) {
		return 0, false
	}
	size := binary.LittleEndian.Uint64(data[nameEnd+1 : nameEnd+9])
	if size == 0 || size > 1<<30 {
		return 0, false
	}
	return size, true
}
