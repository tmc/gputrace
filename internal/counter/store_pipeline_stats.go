// This file parses the store sections of a .gputrace capture bundle to
// extract shader compilation statistics. Unlike streamData.go, which reads
// .gpuprofiler_raw, these statistics are archived in capture-only bundles.

package counter

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/tmc/apple/x/plist"
	"github.com/tmc/gputrace/internal/trace"
)

// StoreStats holds the shader data archived in a capture bundle's store file.
type StoreStats struct {
	Pipelines []PipelineStats `json:"pipelines"`        // Per-function compilation statistics
	Source    string          `json:"source,omitempty"` // Metal shader source, when archived
}

// pipelineStatsKey identifies a store section holding compilation statistics.
// Xcode archives these statistics one function per section.
const pipelineStatsKey = "Temporary register count"

// PipelineForLabel returns the statistics archived for an encoder or kernel
// label, or nil when the label names no compiled function. A label is either
// the function name itself or an encoder-numbered form such as
// "Encoder_1_simple_add", so the longest matching function name wins.
func (s *StoreStats) PipelineForLabel(label string) *PipelineStats {
	if s == nil {
		return nil
	}
	var match *PipelineStats
	for i := range s.Pipelines {
		name := s.Pipelines[i].FunctionName
		if name == "" {
			continue
		}
		if label != name && !strings.HasSuffix(label, "_"+name) {
			continue
		}
		if match == nil || len(name) > len(match.FunctionName) {
			match = &s.Pipelines[i]
		}
	}
	return match
}

// ExtractStoreStats reads shader compilation statistics from a trace's store
// file. Statistics are keyed by function name rather than pipeline address,
// because capture bundles archive them per compiled function.
//
// It reports an error only when the store cannot be read; a store without
// statistics yields an empty result.
func ExtractStoreStats(t *trace.Trace, storeNum int) (*StoreStats, error) {
	sections, err := t.DecompressStoreSections(storeNum)
	if err != nil {
		return nil, fmt.Errorf("decompress store%d: %w", storeNum, err)
	}

	stats := &StoreStats{}
	for _, section := range sections {
		if source, ok := metalSource(section); ok {
			stats.Source = source
			continue
		}
		if !bytes.HasPrefix(section, []byte("bplist00")) {
			continue
		}
		if ps, ok := parseStorePipelineStats(section); ok {
			stats.Pipelines = append(stats.Pipelines, ps)
		}
	}
	return stats, nil
}

// metalSource reports whether a store section is Metal shader source.
func metalSource(section []byte) (string, bool) {
	if !bytes.Contains(section, []byte("using namespace metal")) {
		return "", false
	}
	if bytes.HasPrefix(section, []byte("bplist00")) {
		return "", false
	}
	return string(section), true
}

// parseStorePipelineStats decodes one archived statistics dictionary. The
// root object is the statistics dictionary itself, so unlike streamData there
// is no enclosing pipeline-ID map.
func parseStorePipelineStats(data []byte) (PipelineStats, bool) {
	var archive map[string]interface{}
	if _, err := plist.Unmarshal(data, &archive); err != nil {
		return PipelineStats{}, false
	}

	objects, ok := archive["$objects"].([]interface{})
	if !ok {
		return PipelineStats{}, false
	}
	top, ok := archive["$top"].(map[string]interface{})
	if !ok {
		return PipelineStats{}, false
	}
	rootUID, ok := top["root"].(plist.UID)
	if !ok || int(rootUID) >= len(objects) {
		return PipelineStats{}, false
	}
	root, ok := objects[int(rootUID)].(map[string]interface{})
	if !ok {
		return PipelineStats{}, false
	}

	keyMap := resolveKeyedDictionary(objects, root)
	if _, ok := keyMap[pipelineStatsKey]; !ok {
		return PipelineStats{}, false
	}

	var ps PipelineStats
	assignPipelineStatFields(&ps, keyMap)
	ps.FunctionName = storeFunctionName(objects, keyMap)
	return ps, true
}

// storeFunctionName reads the compiled function name from the nested
// "Compile Performance" dictionary.
func storeFunctionName(objects []interface{}, keyMap map[string]interface{}) string {
	compile, ok := keyMap["Compile Performance"].(map[string]interface{})
	if !ok {
		return ""
	}
	name, _ := resolveKeyedDictionary(objects, compile)["Function Name"].(string)
	return name
}
