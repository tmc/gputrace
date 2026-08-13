package trace

import (
	"bytes"
	"encoding/binary"
	"sort"
)

// AttributedDispatch is a dispatch command and the pipeline state in effect
// when it was recorded. Pipeline attribution is unavailable when the capture
// has no preceding, named pipeline-state record.
type AttributedDispatch struct {
	Index            int
	CommandBuffer    int
	CaptureOffset    int64
	PipelineAddr     uint64
	FunctionName     string
	AttributionBasis string
	DispatchThreads
}

// ParseAttributedDispatches returns dispatch commands in capture order.
// It joins a dispatch only to the most recent pipeline-state command in the
// same command buffer; it does not infer encoder identity from CS labels.
func (t *Trace) ParseAttributedDispatches() ([]AttributedDispatch, error) {
	commandBuffers, err := t.ParseCommandBuffers()
	if err != nil {
		return nil, err
	}
	pipelineNames := t.BuildPipelineFunctionMap()
	var out []AttributedDispatch
	for i, commandBuffer := range commandBuffers {
		end := int64(len(t.CaptureData))
		if i+1 < len(commandBuffers) {
			end = commandBuffers[i+1].Offset
		}
		if commandBuffer.Offset < 0 || commandBuffer.Offset >= end || end > int64(len(t.CaptureData)) {
			continue
		}
		data := t.CaptureData[commandBuffer.Offset:end]
		type event struct {
			offset   int64
			pipeline uint64
			dispatch *DispatchThreads
		}
		var events []event
		for offset := 0; ; {
			pos := bytes.Index(data[offset:], []byte("Ct\x00\x00"))
			if pos < 0 {
				break
			}
			pos += offset
			if pos+20 <= len(data) {
				events = append(events, event{
					offset:   int64(pos),
					pipeline: binary.LittleEndian.Uint64(data[pos+12 : pos+20]),
				})
			}
			offset = pos + 4
		}
		dispatches := t.ParseDispatchInRegion(data, 0)
		for j := range dispatches {
			events = append(events, event{offset: dispatches[j].Offset, dispatch: &dispatches[j]})
		}
		sort.SliceStable(events, func(i, j int) bool { return events[i].offset < events[j].offset })
		var pipeline uint64
		for _, event := range events {
			if event.dispatch == nil {
				pipeline = event.pipeline
				continue
			}
			dispatch := AttributedDispatch{
				Index:           len(out),
				CommandBuffer:   i,
				CaptureOffset:   commandBuffer.Offset + event.dispatch.Offset,
				PipelineAddr:    pipeline,
				DispatchThreads: *event.dispatch,
			}
			if name := pipelineNames[pipeline]; name != "" {
				dispatch.FunctionName = name
				dispatch.AttributionBasis = "preceding pipeline state"
			} else {
				dispatch.AttributionBasis = "unavailable"
			}
			out = append(out, dispatch)
		}
	}
	return out, nil
}
