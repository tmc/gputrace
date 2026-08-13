// Package environment compares versioned capture environments.
package environment

import (
	"fmt"
	"sort"
)

const SchemaV1 = "gputrace.environment/v1"

// Value is one observed environment value and its retrieval provenance.
type Value struct {
	Value        string `json:"value,omitempty"`
	Source       string `json:"source"`
	Parser       string `json:"parser"`
	Availability string `json:"availability"`
}

// Snapshot separates exact comparison gates from queried capabilities and
// informational observations.
type Snapshot struct {
	Schema       string           `json:"schema"`
	Exact        Exact            `json:"exact"`
	Capabilities map[string]Value `json:"capabilities,omitempty"`
	Information  map[string]Value `json:"information,omitempty"`
	Catalog      Catalog          `json:"capability_catalog"`
}

type Exact struct {
	Workload     Value `json:"workload"`
	Device       Value `json:"device"`
	Driver       Value `json:"driver"`
	Runtime      Value `json:"runtime"`
	CaptureMode  Value `json:"capture_mode"`
	TimingSource Value `json:"timing_source"`
}

type Catalog struct {
	Revision string `json:"revision,omitempty"`
	Digest   string `json:"digest,omitempty"`
}

// Comparison reports whether two captures form a controlled regression.
type Comparison struct {
	Label                string   `json:"label"`
	ExactMismatches      []string `json:"exact_mismatches,omitempty"`
	CapabilityMismatches []string `json:"capability_mismatches,omitempty"`
	InformationalChanges []string `json:"informational_changes,omitempty"`
}

// Compare evaluates the versioned comparison projection. Informational
// changes do not make a controlled comparison incompatible.
func Compare(left, right Snapshot, override bool) (Comparison, error) {
	if left.Schema != SchemaV1 || right.Schema != SchemaV1 {
		return Comparison{}, fmt.Errorf("compare environments: unsupported schema %q and %q", left.Schema, right.Schema)
	}
	result := Comparison{Label: "controlled"}
	compareValue := func(name string, a, b Value, destination *[]string) {
		if a.Availability != b.Availability || a.Value != b.Value {
			*destination = append(*destination, name)
		}
	}
	compareValue("workload", left.Exact.Workload, right.Exact.Workload, &result.ExactMismatches)
	compareValue("device", left.Exact.Device, right.Exact.Device, &result.ExactMismatches)
	compareValue("driver", left.Exact.Driver, right.Exact.Driver, &result.ExactMismatches)
	compareValue("runtime", left.Exact.Runtime, right.Exact.Runtime, &result.ExactMismatches)
	compareValue("capture_mode", left.Exact.CaptureMode, right.Exact.CaptureMode, &result.ExactMismatches)
	compareValue("timing_source", left.Exact.TimingSource, right.Exact.TimingSource, &result.ExactMismatches)
	if left.Catalog != right.Catalog {
		result.CapabilityMismatches = append(result.CapabilityMismatches, "capability_catalog")
	}
	for name, value := range left.Capabilities {
		other, ok := right.Capabilities[name]
		if !ok || value.Availability != other.Availability || value.Value != other.Value {
			result.CapabilityMismatches = append(result.CapabilityMismatches, name)
		}
	}
	for name := range right.Capabilities {
		if _, ok := left.Capabilities[name]; !ok {
			result.CapabilityMismatches = append(result.CapabilityMismatches, name)
		}
	}
	for name, value := range left.Information {
		other, ok := right.Information[name]
		if !ok || value.Availability != other.Availability || value.Value != other.Value {
			result.InformationalChanges = append(result.InformationalChanges, name)
		}
	}
	for name := range right.Information {
		if _, ok := left.Information[name]; !ok {
			result.InformationalChanges = append(result.InformationalChanges, name)
		}
	}
	if len(result.ExactMismatches) > 0 || len(result.CapabilityMismatches) > 0 {
		if !override {
			result.Label = "incompatible"
		} else {
			result.Label = "cross-environment, not causally attributable"
		}
	}
	sort.Strings(result.ExactMismatches)
	sort.Strings(result.CapabilityMismatches)
	sort.Strings(result.InformationalChanges)
	return result, nil
}
