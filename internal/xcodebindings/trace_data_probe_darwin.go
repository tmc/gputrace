//go:build darwin && gputrace_private_bindings

package xcodebindings

import (
	"fmt"
	"os"
	"runtime"

	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/private/xcode/gtshaderprofiler"
	"github.com/tmc/apple/x/plist"
)

// TraceDataSummary reports the small, non-iterating portion of a GTMioTraceData
// object. GTMioTraceData is the model used for binary enumeration; it is a
// different class from GTShaderProfilerStreamData, which stores the archive.
type TraceDataSummary struct {
	Path               string `json:"path"`
	ObjectID           string `json:"object_id,omitempty"`
	PipelineStateCount uint64 `json:"pipeline_state_count"`
	CostCount          uint64 `json:"cost_count"`
	StreamDataID       string `json:"stream_data_id,omitempty"`
}

// ProbeTraceData constructs GTMioTraceData through Apple's NSError-returning
// class method. It deliberately does not assume that path names a supported
// archive format; the framework's NSError is returned when it does not.
func ProbeTraceData(path string) (summary TraceDataSummary, err error) {
	summary.Path = path
	if path == "" {
		return summary, fmt.Errorf("trace data path is empty")
	}
	if className, ok := keyedArchiveRootClass(path); ok && className != "GTMioTraceData" {
		return summary, fmt.Errorf("trace data archive root is %q, want GTMioTraceData", className)
	}
	if err := loadFramework(); err != nil {
		return summary, fmt.Errorf("load GTShaderProfiler.framework: %w", err)
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	objc.AutoreleasePool(func() {
		url := foundation.NewURLFileURLWithPath(path)
		data, loadErr := gtshaderprofiler.GetGTMioTraceDataClass().TraceDataFromURLError(url)
		if loadErr != nil {
			err = fmt.Errorf("construct GTMioTraceData from %q: %w", path, loadErr)
			return
		}
		if data == nil || data.GetID() == 0 {
			err = fmt.Errorf("construct GTMioTraceData from %q returned nil", path)
			return
		}
		trace := gtshaderprofiler.GTMioTraceDataFromID(data.GetID())
		summary.ObjectID = fmt.Sprintf("0x%x", uintptr(trace.GetID()))
		summary.PipelineStateCount = trace.PipelineStateCount()
		summary.CostCount = trace.CostCount()
		if stream := trace.StreamData(); stream != nil && stream.GetID() != 0 {
			summary.StreamDataID = fmt.Sprintf("0x%x", uintptr(stream.GetID()))
		}
	})
	return summary, err
}

// keyedArchiveRootClass is a preflight for the private unarchiver. Some
// GTMioTraceData entry points throw NSInvalidUnarchiveOperationException for a
// valid archive of another class instead of returning NSError. Rejecting a
// known root class here keeps the probe fail-closed without installing a
// process-global exception handler.
func keyedArchiveRootClass(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var archive map[string]any
	if _, err := plist.Unmarshal(data, &archive); err != nil {
		return "", false
	}
	objects, ok := archive["$objects"].([]any)
	if !ok {
		return "", false
	}
	top, ok := archive["$top"].(map[string]any)
	if !ok {
		return "", false
	}
	root, ok := top["root"].(plist.UID)
	if !ok || int(root) >= len(objects) {
		return "", false
	}
	rootObject, ok := objects[int(root)].(map[string]any)
	if !ok {
		return "", false
	}
	classUID, ok := rootObject["$class"].(plist.UID)
	if !ok || int(classUID) >= len(objects) {
		return "", false
	}
	classObject, ok := objects[int(classUID)].(map[string]any)
	if !ok {
		return "", false
	}
	className, _ := classObject["$classname"].(string)
	return className, className != ""
}
