package counter

import (
	"bytes"
	"os"
	"sort"
	"testing"

	"github.com/tmc/apple/x/plist"
)

// TestDumpStoreKeys lists every key Xcode archives in a capture bundle's
// pipeline-statistics sections, including nested dictionaries.
//
// assignPipelineStatFields reads a fixed list of top-level keys, and
// storeFunctionName reaches one level into "Compile Performance". Anything
// Xcode archives outside those is invisible to the parser, so this enumerates
// the whole tree. The immediate question is whether a live- or high-register
// value is archived per function: if it is, the long-standing high_register
// parity gap closes from data already in the bundle, with no private API.
func TestDumpStoreKeys(t *testing.T) {
	if os.Getenv("GPUTRACE_DUMP_STORE_KEYS") == "" {
		t.Skip("set GPUTRACE_DUMP_STORE_KEYS to list archived store keys")
	}
	// The fixture whose kernel was written to maximise register pressure is the
	// one most likely to carry a register field if any does.
	tr := openFixture(t, "low-occupancy-high-registers")
	sections, err := tr.DecompressStoreSections(0)
	if err != nil {
		t.Fatalf("decompress store0: %v", err)
	}

	seen := map[string]string{}
	for _, section := range sections {
		if !bytes.HasPrefix(section, []byte("bplist00")) {
			continue
		}
		var archive map[string]interface{}
		if _, err := plist.Unmarshal(section, &archive); err != nil {
			continue
		}
		objects, ok := archive["$objects"].([]interface{})
		if !ok {
			continue
		}
		top, _ := archive["$top"].(map[string]interface{})
		rootUID, ok := top["root"].(plist.UID)
		if !ok || int(rootUID) >= len(objects) {
			continue
		}
		root, ok := objects[int(rootUID)].(map[string]interface{})
		if !ok {
			continue
		}
		walk(objects, resolveKeyedDictionary(objects, root), "", seen, 0)
	}

	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	t.Logf("%d distinct archived keys", len(keys))
	for _, k := range keys {
		t.Logf("  %s = %s", k, seen[k])
	}
}

// walk records every key in a resolved keyed dictionary, descending into nested
// dictionaries so keys Xcode nests are not missed.
func walk(objects []interface{}, m map[string]interface{}, prefix string, seen map[string]string, depth int) {
	if depth > 4 {
		return
	}
	for k, v := range m {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		switch value := v.(type) {
		case map[string]interface{}:
			seen[path] = "{dict}"
			walk(objects, resolveKeyedDictionary(objects, value), path, seen, depth+1)
		case string:
			seen[path] = "string:" + truncate(value)
		default:
			seen[path] = describe(v)
		}
	}
}

func truncate(s string) string {
	if len(s) > 40 {
		return s[:40] + "…"
	}
	return s
}

func describe(v interface{}) string {
	switch value := v.(type) {
	case uint64:
		return "uint"
	case int64:
		return "int"
	case float64:
		return "float"
	case bool:
		return "bool"
	case []interface{}:
		return "array"
	case plist.UID:
		_ = value
		return "uid"
	}
	return "other"
}
