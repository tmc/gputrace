package parity

import (
	"fmt"
	"os"
	"sort"

	"github.com/tmc/apple/x/plist"
)

// CounterGraphPaths are the copies of GPUCounterGraph.plist shipped with Xcode.
// The file maps each counter's UI name to its unit and to the vendor counter
// names it is computed from, and it is the same file in every location.
var CounterGraphPaths = []string{
	"/Applications/Xcode.app/Contents/PlugIns/GPUDebugger.ideplugin/Contents/Frameworks/GTShaderProfiler.framework/Versions/A/Resources/GPUCounterGraph.plist",
	"/Applications/Xcode.app/Contents/PlugIns/GPUDebugger.ideplugin/Contents/Resources/GPUCounterGraph.plist",
	"/Applications/Xcode.app/Contents/Applications/Instruments.app/Contents/PlugIns/GPUPlugin.xrplugin/Contents/Resources/GPUCounterGraph.plist",
}

// Catalog is Xcode's own counter dictionary, read from GPUCounterGraph.plist.
//
// It is the authority on what a Counters column means. In particular the unit
// is often not what the rendered value suggests: "Compute SIMD Groups Inflight
// per Core" has unit "SIMD Groups", a count, even though Xcode prints it with a
// percent sign. A harness that assumes percent for anything ending in "%" will
// report a unit disagreement as a value disagreement.
type Catalog struct {
	Path     string
	Counters map[string]CatalogEntry
}

// CatalogEntry is one counter's entry in GPUCounterGraph.plist.
type CatalogEntry struct {
	Name           string
	Unit           string
	VendorCounters []string
	Visible        bool
}

// LoadCatalog reads the first GPUCounterGraph.plist that exists among paths.
// It returns a nil Catalog and no error when none is installed: the catalog is
// enrichment, and the comparison stands without it.
func LoadCatalog(paths []string) (*Catalog, error) {
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var root map[string]any
		if _, err := plist.Unmarshal(data, &root); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		counters, ok := root["counters"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s: no counters dictionary", p)
		}
		c := &Catalog{Path: p, Counters: make(map[string]CatalogEntry, len(counters))}
		for name, v := range counters {
			m, ok := v.(map[string]any)
			if !ok {
				continue
			}
			e := CatalogEntry{Name: name}
			e.Unit, _ = m["unit"].(string)
			e.Visible, _ = m["visible"].(bool)
			if vc, ok := m["vendorCounters"].([]any); ok {
				for _, x := range vc {
					if s, ok := x.(string); ok {
						e.VendorCounters = append(e.VendorCounters, s)
					}
				}
				sort.Strings(e.VendorCounters)
			}
			c.Counters[name] = e
		}
		return c, nil
	}
	return nil, nil
}

// Lookup returns the catalog entry for a column name.
func (c *Catalog) Lookup(name string) (CatalogEntry, bool) {
	if c == nil {
		return CatalogEntry{}, false
	}
	e, ok := c.Counters[name]
	return e, ok
}
