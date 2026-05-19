//go:build darwin

// Package xcodebindings probes the private Xcode GTShaderProfiler runtime
// surface without constructing profiler objects.
package xcodebindings

import (
	"os"

	"github.com/ebitengine/purego"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/objectivec"
)

const frameworkPath = "/Applications/Xcode.app/Contents/PlugIns/GPUDebugger.ideplugin/Contents/Frameworks/GTShaderProfiler.framework/Versions/A/GTShaderProfiler"

// Report describes the GTShaderProfiler Objective-C surface gputrace needs for
// Xcode parity.
type Report struct {
	FrameworkPath string         `json:"framework_path"`
	Framework     bool           `json:"framework"`
	Classes       []Class        `json:"classes"`
	Gaps          []Gap          `json:"gaps"`
	Notes         []string       `json:"notes,omitempty"`
	Summary       map[string]int `json:"summary"`
}

// Class describes one Objective-C class and the selectors gputrace cares about.
type Class struct {
	Name      string     `json:"name"`
	Present   bool       `json:"present"`
	Selectors []Selector `json:"selectors"`
}

// Selector describes one class or instance selector.
type Selector struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Present bool   `json:"present"`
}

// Gap maps a missing gputrace metric to the private Xcode surface that can
// supply it.
type Gap struct {
	Metric    string `json:"metric"`
	Binding   string `json:"binding"`
	Status    string `json:"status"`
	Next      string `json:"next"`
	Signature string `json:"signature_risk,omitempty"`
}

// Probe checks class and selector availability. It opens GTShaderProfiler with
// RTLD_GLOBAL so Objective-C classes become visible, but does not instantiate
// any GTShaderProfiler class.
func Probe() Report {
	report := Report{
		FrameworkPath: frameworkPath,
		Framework:     fileExists(frameworkPath),
		Summary: map[string]int{
			"classes_present":   0,
			"classes_missing":   0,
			"selectors_present": 0,
			"selectors_missing": 0,
		},
	}
	if report.Framework {
		if _, err := purego.Dlopen(frameworkPath, purego.RTLD_LAZY|purego.RTLD_GLOBAL); err != nil {
			report.Framework = false
			report.Notes = append(report.Notes, "failed to load GTShaderProfiler.framework: "+err.Error())
		}
	}

	specs := []struct {
		name      string
		selectors []Selector
	}{
		{
			name: "GTShaderProfilerStreamData",
			selectors: []Selector{
				{Name: "dataFromArchivedDataURL:", Kind: "class"},
				{Name: "savedStreamDataFromCaptureArchive:", Kind: "class"},
				{Name: "steamDataFromData:", Kind: "class"},
				{Name: "streamDataClasses", Kind: "class"},
				{Name: "archivedAPSCounterData", Kind: "instance"},
				{Name: "archivedAPSTimelineData", Kind: "instance"},
				{Name: "encoderInfoData", Kind: "instance"},
				{Name: "gpuCommandInfoData", Kind: "instance"},
				{Name: "pipelinePerformanceStatistics", Kind: "instance"},
				{Name: "pipelineStateInfoData", Kind: "instance"},
				{Name: "unarchivedAPSCounterData", Kind: "instance"},
				{Name: "unarchivedAPSTimelineData", Kind: "instance"},
			},
		},
		{
			name: "XRGPUAPSDataProcessor",
			selectors: []Selector{
				{Name: "initWithGPUGeneration:variant:rev:config:options:", Kind: "instance"},
				{Name: "parseData", Kind: "instance"},
				{Name: "loadCounters:", Kind: "instance"},
				{Name: "loadAPSCounters:counterSet:", Kind: "instance"},
				{Name: "loadRDECounters:", Kind: "instance"},
				{Name: "loadShaders", Kind: "instance"},
				{Name: "apsDerivedCounters", Kind: "instance"},
				{Name: "apsRawCounterNames", Kind: "instance"},
				{Name: "counterTypeFromGroupName:counterName:", Kind: "instance"},
				{Name: "getAPSDerivedCounterData:timestamps:sampleCount:counterIndex:count:", Kind: "instance"},
				{Name: "getAPSRawCounterData:timestamps:sampleCount:counterIndex:count:", Kind: "instance"},
				{Name: "getRDEDerivedCounterAtSourceIndex:buffer:timestamps:sampleCount:counterIndex:count:", Kind: "instance"},
				{Name: "getRDERawCounterAtSourceIndex:buffer:timestamps:sampleCount:counterIndex:count:", Kind: "instance"},
			},
		},
		{
			name: "GTMioCounterData",
			selectors: []Selector{
				{Name: "name", Kind: "instance"},
				{Name: "counterIndex", Kind: "instance"},
				{Name: "sampleCount", Kind: "instance"},
				{Name: "sampleInterval", Kind: "instance"},
				{Name: "scope", Kind: "instance"},
				{Name: "scopeIndex", Kind: "instance"},
				{Name: "timestamps", Kind: "instance"},
				{Name: "valueType", Kind: "instance"},
				{Name: "values", Kind: "instance"},
			},
		},
		{
			name: "GTMioShaderBinaryData",
			selectors: []Selector{
				{Name: "cost", Kind: "instance"},
				{Name: "duration", Kind: "instance"},
				{Name: "instructionInfoCount", Kind: "instance"},
				{Name: "isaForInstructionAtIndex:", Kind: "instance"},
				{Name: "liveRegisterForInstructionAtIndex:", Kind: "instance"},
				{Name: "instructionCosts", Kind: "instance"},
				{Name: "enumerateInstructionsForBinaryRange:enumerator:", Kind: "instance"},
				{Name: "enumeratePipelineStateCosts:", Kind: "instance"},
			},
		},
	}
	for _, spec := range specs {
		cls := objc.GetClass(spec.name)
		bc := Class{Name: spec.name, Present: cls != 0}
		if bc.Present {
			report.Summary["classes_present"]++
		} else {
			report.Summary["classes_missing"]++
		}
		for _, sel := range spec.selectors {
			sel.Present = selectorPresent(cls, sel.Kind, sel.Name)
			if sel.Present {
				report.Summary["selectors_present"]++
			} else {
				report.Summary["selectors_missing"]++
			}
			bc.Selectors = append(bc.Selectors, sel)
		}
		report.Classes = append(report.Classes, bc)
	}
	report.Gaps = []Gap{
		{
			Metric:  "high_register",
			Binding: "GTMioShaderBinaryData.liveRegisterForInstructionAtIndex:",
			Status:  gapStatus(report, "GTMioShaderBinaryData", "liveRegisterForInstructionAtIndex:"),
			Next:    "map streamData pipeline or shader binary records to kernel events, then compute max live register per kernel",
		},
		{
			Metric:    "occupancy_pct",
			Binding:   "XRGPUAPSDataProcessor derived counters",
			Status:    gapStatus(report, "XRGPUAPSDataProcessor", "getAPSDerivedCounterData:timestamps:sampleCount:counterIndex:count:"),
			Next:      "wrap derived counter buffers with typed storage and attach values to encoder or dispatch samples",
			Signature: "counter buffer methods need caller-owned numeric buffers and count validation",
		},
		{
			Metric:    "alu_utilization_pct",
			Binding:   "XRGPUAPSDataProcessor derived counters",
			Status:    gapStatus(report, "XRGPUAPSDataProcessor", "getAPSDerivedCounterData:timestamps:sampleCount:counterIndex:count:"),
			Next:      "resolve the Xcode counter type for ALU utilization and feed it through timeline and pprof exporters",
			Signature: "counter buffer methods need caller-owned numeric buffers and count validation",
		},
		{
			Metric:    "counter_values",
			Binding:   "GTMioCounterData.values",
			Status:    gapStatus(report, "GTMioCounterData", "values"),
			Next:      "replace generated []objc.ID use with a typed numeric slice wrapper based on sampleCount and valueType",
			Signature: "generated Values method is not safe for numeric counter storage",
		},
	}
	if !report.Framework {
		report.Notes = append(report.Notes, "GTShaderProfiler.framework was not found at the expected Xcode path")
	}
	return report
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func selectorPresent(cls objc.Class, kind, name string) (present bool) {
	if cls == 0 {
		return false
	}
	defer func() {
		if recover() != nil {
			present = false
		}
	}()
	sel := objectivec.SEL(objc.Sel(name))
	switch kind {
	case "class":
		return objectivec.Class_getClassMethod(cls, sel) != 0
	default:
		return objectivec.Class_getInstanceMethod(cls, sel) != 0
	}
}

func gapStatus(report Report, className, selector string) string {
	for _, class := range report.Classes {
		if class.Name != className || !class.Present {
			continue
		}
		for _, sel := range class.Selectors {
			if sel.Name == selector && sel.Present {
				return "binding present; adapter missing"
			}
		}
		return "selector missing"
	}
	return "class missing"
}
