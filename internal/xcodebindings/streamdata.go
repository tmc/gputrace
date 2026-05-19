//go:build darwin

package xcodebindings

import (
	"fmt"
	"sort"
	"strings"

	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/objectivec"
)

// StreamDataSummary is the safely readable GTShaderProfilerStreamData metadata
// for a profiler streamData archive.
type StreamDataSummary struct {
	Path                   string          `json:"path"`
	ObjectID               string          `json:"object_id,omitempty"`
	GPUGeneration          uint32          `json:"gpu_generation,omitempty"`
	MetalDeviceName        string          `json:"metal_device_name,omitempty"`
	MetalPluginName        string          `json:"metal_plugin_name,omitempty"`
	EncoderInfoCount       uint64          `json:"encoder_info_count"`
	GPUCommandInfoCount    uint64          `json:"gpu_command_info_count"`
	PipelineStateInfoCount uint64          `json:"pipeline_state_info_count"`
	FunctionInfoCount      uint64          `json:"function_info_count"`
	EncoderInfoBytes       uint64          `json:"encoder_info_bytes,omitempty"`
	GPUCommandInfoBytes    uint64          `json:"gpu_command_info_bytes,omitempty"`
	PipelineStateInfoBytes uint64          `json:"pipeline_state_info_bytes,omitempty"`
	FunctionInfoBytes      uint64          `json:"function_info_bytes,omitempty"`
	APSTimelineCount       uint64          `json:"aps_timeline_count,omitempty"`
	APSCounterCount        uint64          `json:"aps_counter_count,omitempty"`
	ShaderProfilerCount    uint64          `json:"shader_profiler_count,omitempty"`
	APSTimelineSamples     []ObjectSummary `json:"aps_timeline_samples,omitempty"`
	APSCounterSamples      []ObjectSummary `json:"aps_counter_samples,omitempty"`
	APSTimelineKeys        []KeyCount      `json:"aps_timeline_keys,omitempty"`
	APSCounterKeys         []KeyCount      `json:"aps_counter_keys,omitempty"`
	APSTimelineTimeKeys    []KeyCount      `json:"aps_timeline_time_keys,omitempty"`
	SelectedValues         []ValueSummary  `json:"selected_values,omitempty"`
	ReplayerGPUTimeNs      uint64          `json:"replayer_gpu_time_ns,omitempty"`
}

// KeyCount reports how often a dictionary key appears in an Objective-C array.
type KeyCount struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

// ValueSummary reports the class and shallow metadata for a selected dictionary
// value.
type ValueSummary struct {
	Array       string          `json:"array"`
	Index       uint64          `json:"index"`
	Key         string          `json:"key"`
	Class       string          `json:"class,omitempty"`
	Description string          `json:"description,omitempty"`
	Keys        []string        `json:"keys,omitempty"`
	Bytes       uint64          `json:"bytes,omitempty"`
	Count       uint64          `json:"count,omitempty"`
	Number      uint64          `json:"number,omitempty"`
	Children    []ObjectSummary `json:"children,omitempty"`
}

// ObjectSummary describes a private Objective-C object without traversing its
// full graph.
type ObjectSummary struct {
	Index      uint64            `json:"index"`
	ClassName  string            `json:"class_name,omitempty"`
	Bytes      uint64            `json:"bytes,omitempty"`
	Count      uint64            `json:"count,omitempty"`
	Keys       []string          `json:"keys,omitempty"`
	Properties map[string]uint64 `json:"properties,omitempty"`
	Children   []ObjectSummary   `json:"children,omitempty"`
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
	apsTimeline := objc.Send[objc.ID](stream, objc.Sel("unarchivedAPSTimelineData"))
	apsCounter := objc.Send[objc.ID](stream, objc.Sel("unarchivedAPSCounterData"))
	summary.APSTimelineCount = arrayCount(apsTimeline)
	summary.APSCounterCount = arrayCount(apsCounter)
	summary.ShaderProfilerCount = arrayCount(objc.Send[objc.ID](stream, objc.Sel("unarchivedShaderProfilerData")))
	summary.APSTimelineSamples = objectSamples(apsTimeline, 5)
	summary.APSCounterSamples = objectSamples(apsCounter, 5)
	summary.APSTimelineKeys = dictionaryKeyCounts(apsTimeline)
	summary.APSCounterKeys = dictionaryKeyCounts(apsCounter)
	summary.APSTimelineTimeKeys = filterKeyCounts(summary.APSTimelineKeys, "time")
	if value, ok := dictionaryNumberInArray(apsTimeline, "ReplayerGPUTime"); ok {
		summary.ReplayerGPUTimeNs = value
	} else if value, ok := dictionaryNumberInArray(apsCounter, "ReplayerGPUTime"); ok {
		summary.ReplayerGPUTimeNs = value
	}
	for _, key := range []string{
		"ReplayerGPUTime",
		"Binaries",
		"Derived Counter Sample Data",
		"Derived Counters Info Data",
		"Encoder Time Sample Data",
		"Encoder Sample Index Data",
		"Encoder Infos",
		"ShaderProfilerData",
	} {
		summary.SelectedValues = append(summary.SelectedValues, selectedValues(apsTimeline, "aps_timeline", key)...)
		summary.SelectedValues = append(summary.SelectedValues, selectedValues(apsCounter, "aps_counter", key)...)
	}
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
	if id == 0 || !objc.RespondsToSelector(id, objc.Sel("length")) {
		return 0
	}
	return uint64(objc.Send[uint](id, objc.Sel("length")))
}

func arrayCount(id objc.ID) uint64 {
	if id == 0 || !objc.RespondsToSelector(id, objc.Sel("count")) {
		return 0
	}
	return uint64(objc.Send[uint](id, objc.Sel("count")))
}

func objectSamples(array objc.ID, limit uint64) []ObjectSummary {
	count := arrayCount(array)
	if count == 0 {
		return nil
	}
	if count < limit {
		limit = count
	}
	samples := make([]ObjectSummary, 0, limit)
	for i := uint64(0); i < limit; i++ {
		id := objc.Send[objc.ID](array, objc.Sel("objectAtIndex:"), uint(i))
		if id == 0 {
			continue
		}
		samples = append(samples, summarizeObject(id, i, 1))
	}
	return samples
}

func summarizeObject(id objc.ID, index uint64, depth int) ObjectSummary {
	summary := ObjectSummary{
		Index:      index,
		ClassName:  className(id),
		Bytes:      dataLength(id),
		Count:      arrayCount(id),
		Keys:       dictionaryKeys(id, 24),
		Properties: safeNumericProperties(id),
	}
	if summary.Count == 0 {
		summary.Count = dictionaryCount(id)
	}
	if depth > 0 {
		summary.Children = childSamples(id, 4, depth-1)
	}
	return summary
}

func childSamples(id objc.ID, limit uint64, depth int) []ObjectSummary {
	if id == 0 || !objc.RespondsToSelector(id, objc.Sel("objectAtIndex:")) {
		return nil
	}
	count := arrayCount(id)
	if count == 0 {
		return nil
	}
	if count < limit {
		limit = count
	}
	children := make([]ObjectSummary, 0, limit)
	for i := uint64(0); i < limit; i++ {
		child := objc.Send[objc.ID](id, objc.Sel("objectAtIndex:"), uint(i))
		if child != 0 {
			children = append(children, summarizeObject(child, i, depth))
		}
	}
	return children
}

func className(id objc.ID) string {
	if id == 0 {
		return ""
	}
	defer func() {
		_ = recover()
	}()
	return objc.GoString(objectivec.Object_getClassName(objectivec.Object{ID: id}))
}

func safeNumericProperties(id objc.ID) map[string]uint64 {
	properties := make(map[string]uint64)
	for _, name := range []string{
		"ReplayerGPUTime",
		"replayerGPUTime",
		"EffectiveGPUTime",
		"effectiveGPUTime",
		"CommandBufferActiveTime",
		"commandBufferActiveTime",
		"GPUTime",
		"gpuTime",
	} {
		if value, ok := dictionaryNumber(id, name); ok {
			properties[name] = value
		}
	}
	for _, name := range []string{
		"version",
		"profiledState",
		"timeBaseNumerator",
		"timeBaseDenominator",
		"numPeriodicSamples",
	} {
		if objc.RespondsToSelector(id, objc.Sel(name)) {
			properties[name] = objc.Send[uint64](id, objc.Sel(name))
		}
	}
	for _, name := range []string{
		"restoreTimestamps",
		"coreCounts",
		"counterGroups",
		"perRingSampledDerivedCounters",
		"MGPUTimelineInfos",
		"derivedCounterNames",
	} {
		if objc.RespondsToSelector(id, objc.Sel(name)) {
			properties[name+".count"] = arrayCount(objc.Send[objc.ID](id, objc.Sel(name)))
		}
	}
	for _, name := range []string{
		"coalescedEncoderInfo",
	} {
		if objc.RespondsToSelector(id, objc.Sel(name)) {
			properties[name+".count"] = dictionaryCount(objc.Send[objc.ID](id, objc.Sel(name)))
		}
	}
	for _, name := range []string{
		"aggregatedGPUTimelineInfo",
		"derivedEncoderCounterInfo",
	} {
		if objc.RespondsToSelector(id, objc.Sel(name)) {
			child := objc.Send[objc.ID](id, objc.Sel(name))
			if child != 0 {
				properties[name+".present"] = 1
				for key, value := range safeNumericProperties(child) {
					properties[name+"."+key] = value
				}
			}
		}
	}
	for _, name := range []string{
		"timestamps",
		"derivedCounters",
		"encoderTimelineInfos",
		"activeCoreInfoMasksPerPeriodicSample",
		"activeShadersPerPeriodicSample",
		"numActiveShadersPerPeriodicSample",
		"metalFXTimelineInfo",
	} {
		if objc.RespondsToSelector(id, objc.Sel(name)) {
			properties[name+".bytes"] = dataLength(objc.Send[objc.ID](id, objc.Sel(name)))
		}
	}
	if len(properties) == 0 {
		return nil
	}
	return properties
}

func dictionaryCount(id objc.ID) uint64 {
	if id == 0 || !objc.RespondsToSelector(id, objc.Sel("count")) {
		return 0
	}
	return uint64(objc.Send[uint](id, objc.Sel("count")))
}

func dictionaryKeys(id objc.ID, limit uint64) []string {
	if id == 0 || !objc.RespondsToSelector(id, objc.Sel("allKeys")) {
		return nil
	}
	keys := objc.Send[objc.ID](id, objc.Sel("allKeys"))
	count := arrayCount(keys)
	if count == 0 {
		return nil
	}
	if count < limit {
		limit = count
	}
	out := make([]string, 0, limit)
	for i := uint64(0); i < limit; i++ {
		key := objc.Send[objc.ID](keys, objc.Sel("objectAtIndex:"), uint(i))
		if key == 0 {
			continue
		}
		out = append(out, objc.IDToString(key))
	}
	return out
}

func dictionaryNumber(id objc.ID, key string) (uint64, bool) {
	if id == 0 || !objc.RespondsToSelector(id, objc.Sel("objectForKey:")) {
		return 0, false
	}
	value := objc.Send[objc.ID](id, objc.Sel("objectForKey:"), objc.String(key))
	if value == 0 {
		return 0, false
	}
	if strings.Contains(className(value), "Number") {
		v := foundation.NSNumberFromID(value).UnsignedLongLongValue()
		if v != 0 {
			return v, true
		}
		d := foundation.NSNumberFromID(value).DoubleValue()
		if d < 0 {
			return 0, false
		}
		return uint64(d), true
	}
	switch {
	case objc.RespondsToSelector(value, objc.Sel("unsignedLongLongValue")):
		return objc.Send[uint64](value, objc.Sel("unsignedLongLongValue")), true
	case objc.RespondsToSelector(value, objc.Sel("unsignedLongValue")):
		return uint64(objc.Send[uint](value, objc.Sel("unsignedLongValue"))), true
	case objc.RespondsToSelector(value, objc.Sel("longLongValue")):
		v := objc.Send[int64](value, objc.Sel("longLongValue"))
		if v < 0 {
			return 0, false
		}
		return uint64(v), true
	case objc.RespondsToSelector(value, objc.Sel("integerValue")):
		v := objc.Send[int](value, objc.Sel("integerValue"))
		if v < 0 {
			return 0, false
		}
		return uint64(v), true
	case objc.RespondsToSelector(value, objc.Sel("doubleValue")):
		v := objc.Send[float64](value, objc.Sel("doubleValue"))
		if v < 0 {
			return 0, false
		}
		return uint64(v), true
	default:
		return 0, false
	}
}

func dictionaryKeyCounts(array objc.ID) []KeyCount {
	count := arrayCount(array)
	if count == 0 {
		return nil
	}
	counts := make(map[string]int)
	for i := uint64(0); i < count; i++ {
		id := objc.Send[objc.ID](array, objc.Sel("objectAtIndex:"), uint(i))
		for _, key := range dictionaryKeys(id, 256) {
			counts[key]++
		}
	}
	out := make([]KeyCount, 0, len(counts))
	for key, count := range counts {
		out = append(out, KeyCount{Key: key, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Key < out[j].Key
	})
	return out
}

func dictionaryNumberInArray(array objc.ID, key string) (uint64, bool) {
	count := arrayCount(array)
	for i := uint64(0); i < count; i++ {
		id := objc.Send[objc.ID](array, objc.Sel("objectAtIndex:"), uint(i))
		if value, ok := dictionaryNumber(id, key); ok {
			return value, true
		}
	}
	return 0, false
}

func selectedValues(array objc.ID, arrayName, key string) []ValueSummary {
	count := arrayCount(array)
	var out []ValueSummary
	for i := uint64(0); i < count; i++ {
		id := objc.Send[objc.ID](array, objc.Sel("objectAtIndex:"), uint(i))
		value := dictionaryObject(id, key)
		if value == 0 {
			continue
		}
		summary := ValueSummary{
			Array:       arrayName,
			Index:       i,
			Key:         key,
			Class:       className(value),
			Description: truncateString(objectivec.Object{ID: value}.Description(), 120),
			Keys:        dictionaryKeys(value, 24),
			Bytes:       dataLength(value),
			Count:       arrayCount(value),
			Children:    childSamples(value, 4, 2),
		}
		if number, ok := dictionaryNumber(id, key); ok {
			summary.Number = number
		}
		out = append(out, summary)
	}
	return out
}

func dictionaryObject(id objc.ID, key string) objc.ID {
	if id == 0 || !objc.RespondsToSelector(id, objc.Sel("objectForKey:")) {
		return 0
	}
	return objc.Send[objc.ID](id, objc.Sel("objectForKey:"), objc.String(key))
}

func truncateString(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

func filterKeyCounts(keys []KeyCount, needle string) []KeyCount {
	var out []KeyCount
	for _, key := range keys {
		if strings.Contains(strings.ToLower(key.Key), needle) {
			out = append(out, key)
		}
	}
	return out
}
