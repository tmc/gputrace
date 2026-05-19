//go:build darwin

package xcodebindings

import (
	"fmt"

	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
)

// StreamDataSummary is the safely readable GTShaderProfilerStreamData metadata
// for a profiler streamData archive.
type StreamDataSummary struct {
	Path                   string `json:"path"`
	ObjectID               string `json:"object_id,omitempty"`
	GPUGeneration          uint32 `json:"gpu_generation,omitempty"`
	MetalDeviceName        string `json:"metal_device_name,omitempty"`
	MetalPluginName        string `json:"metal_plugin_name,omitempty"`
	EncoderInfoCount       uint64 `json:"encoder_info_count"`
	GPUCommandInfoCount    uint64 `json:"gpu_command_info_count"`
	PipelineStateInfoCount uint64 `json:"pipeline_state_info_count"`
	FunctionInfoCount      uint64 `json:"function_info_count"`
	EncoderInfoBytes       uint64 `json:"encoder_info_bytes,omitempty"`
	GPUCommandInfoBytes    uint64 `json:"gpu_command_info_bytes,omitempty"`
	PipelineStateInfoBytes uint64 `json:"pipeline_state_info_bytes,omitempty"`
	FunctionInfoBytes      uint64 `json:"function_info_bytes,omitempty"`
	APSTimelineCount       uint64 `json:"aps_timeline_count,omitempty"`
	APSCounterCount        uint64 `json:"aps_counter_count,omitempty"`
	ShaderProfilerCount    uint64 `json:"shader_profiler_count,omitempty"`
}

// ProbeStreamData loads a streamData archive through GTShaderProfilerStreamData
// and returns metadata that does not require walking private object graphs.
func ProbeStreamData(path string) (StreamDataSummary, error) {
	summary := StreamDataSummary{Path: path}
	if err := loadFramework(); err != nil {
		return summary, fmt.Errorf("load GTShaderProfiler.framework: %w", err)
	}
	cls := objc.GetClass("GTShaderProfilerStreamData")
	if cls == 0 {
		return summary, fmt.Errorf("GTShaderProfilerStreamData class not found")
	}
	url := foundation.NewURLFileURLWithPath(path)
	stream := objc.Send[objc.ID](objc.ID(cls), objc.Sel("dataFromArchivedDataURL:"), url)
	if stream == 0 {
		return summary, fmt.Errorf("dataFromArchivedDataURL returned nil")
	}
	summary.ObjectID = fmt.Sprintf("0x%x", uintptr(stream))
	summary.GPUGeneration = objc.Send[uint32](stream, objc.Sel("gpuGeneration"))
	summary.MetalDeviceName = stringProperty(stream, "metalDeviceName")
	summary.MetalPluginName = stringProperty(stream, "metalPluginName")
	summary.EncoderInfoCount = objc.Send[uint64](stream, objc.Sel("encoderInfoCount"))
	summary.GPUCommandInfoCount = objc.Send[uint64](stream, objc.Sel("gpuCommandInfoCount"))
	summary.PipelineStateInfoCount = objc.Send[uint64](stream, objc.Sel("pipelineStateInfoCount"))
	summary.FunctionInfoCount = objc.Send[uint64](stream, objc.Sel("functionInfoCount"))
	summary.EncoderInfoBytes = dataLength(objc.Send[objc.ID](stream, objc.Sel("encoderInfoData")))
	summary.GPUCommandInfoBytes = dataLength(objc.Send[objc.ID](stream, objc.Sel("gpuCommandInfoData")))
	summary.PipelineStateInfoBytes = dataLength(objc.Send[objc.ID](stream, objc.Sel("pipelineStateInfoData")))
	summary.FunctionInfoBytes = dataLength(objc.Send[objc.ID](stream, objc.Sel("functionInfoData")))
	summary.APSTimelineCount = arrayCount(objc.Send[objc.ID](stream, objc.Sel("unarchivedAPSTimelineData")))
	summary.APSCounterCount = arrayCount(objc.Send[objc.ID](stream, objc.Sel("unarchivedAPSCounterData")))
	summary.ShaderProfilerCount = arrayCount(objc.Send[objc.ID](stream, objc.Sel("unarchivedShaderProfilerData")))
	return summary, nil
}

func stringProperty(id objc.ID, selector string) string {
	value := objc.Send[objc.ID](id, objc.Sel(selector))
	if value == 0 {
		return ""
	}
	return objc.IDToString(value)
}

func dataLength(id objc.ID) uint64 {
	if id == 0 {
		return 0
	}
	return uint64(objc.Send[uint](id, objc.Sel("length")))
}

func arrayCount(id objc.ID) uint64 {
	if id == 0 {
		return 0
	}
	return uint64(objc.Send[uint](id, objc.Sel("count")))
}
