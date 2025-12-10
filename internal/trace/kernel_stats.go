package trace

import (
	"bytes"
	"encoding/binary"
	"sort"
)

// KernelStat holds statistics for a kernel function.
type KernelStat struct {
	Name          string
	PipelineAddr  uint64
	DispatchCount int
	DebugGroups   map[string]int // Debug group -> count
	EncoderLabels map[string]int // Encoder label -> count
}

// PipelineStateEvent represents a change in pipeline state within a command buffer.
type PipelineStateEvent struct {
	Offset       int64
	PipelineAddr uint64
	EncoderAddr  uint64
}

// AnalyzeKernels aggregates statistics for all kernels in the trace.
func (t *Trace) AnalyzeKernels() (map[string]*KernelStat, error) {
	stats := make(map[string]*KernelStat)
	pipelineMap := t.BuildPipelineFunctionMap()

	// Initialize stats for all known kernels (even if 0 dispatches)
	for addr, name := range pipelineMap {
		if _, exists := stats[name]; !exists {
			stats[name] = &KernelStat{
				Name:          name,
				PipelineAddr:  addr, // Store one of them
				DispatchCount: 0,
				DebugGroups:   make(map[string]int),
				EncoderLabels: make(map[string]int),
			}
		}
	}

	// Parse command buffers to find actual usage
	cbs, err := t.ParseCommandBuffers()
	if err != nil {
		return nil, err
	}

	captureData := t.CaptureData

	for i, cb := range cbs {
		// Determine CB region
		var cbEnd int64
		if i+1 < len(cbs) {
			cbEnd = cbs[i+1].Offset
		} else {
			cbEnd = int64(len(captureData))
		}

		if cb.Offset >= int64(len(captureData)) {
			continue
		}
		if cbEnd > int64(len(captureData)) {
			cbEnd = int64(len(captureData))
		}

		cbData := captureData[cb.Offset:cbEnd]

		// 1. Find Encoders (CS records)
		var encoders []EncoderSection
		csMarker := []byte("CS\x00\x00")
		offset := 0
		for {
			pos := bytes.Index(cbData[offset:], csMarker)
			if pos == -1 {
				break
			}
			absolutePos := offset + pos

			if absolutePos+12 > len(cbData) {
				break
			}

			// Read CS address
			csAddr := binary.LittleEndian.Uint64(cbData[absolutePos+4 : absolutePos+12])

			// Read label
			labelStart := absolutePos + 12
			labelEnd := labelStart
			for labelEnd < len(cbData) && cbData[labelEnd] != 0 {
				labelEnd++
			}
			label := string(cbData[labelStart:labelEnd])

			encoders = append(encoders, EncoderSection{
				Label:       label,
				Address:     csAddr,
				StartOffset: int64(absolutePos),
			})

			offset += pos + 4
		}

		// Treat all CS records as potential encoders.
		// Previously we skipped the first one assuming it was the Command Buffer label,
		// but this breaks single-encoder command buffers.
		actualEncoders := encoders

		// Sort encoders by offset
		sort.Slice(actualEncoders, func(i, j int) bool {
			return actualEncoders[i].StartOffset < actualEncoders[j].StartOffset
		})

		// Set end offsets
		for j := range actualEncoders {
			if j < len(actualEncoders)-1 {
				actualEncoders[j].EndOffset = actualEncoders[j+1].StartOffset
			} else {
				actualEncoders[j].EndOffset = int64(len(cbData))
			}
		}

		// 2. Find all Pipeline State Changes (Ct records)
		// We need to track these sequentially.
		var pipelineEvents []PipelineStateEvent
		ctMarker := []byte("Ct\x00\x00")
		offset = 0
		for {
			pos := bytes.Index(cbData[offset:], ctMarker)
			if pos == -1 {
				break
			}
			absolutePos := offset + pos

			if absolutePos+20 <= len(cbData) {
				encoderAddr := binary.LittleEndian.Uint64(cbData[absolutePos+4 : absolutePos+12])
				pipelineAddr := binary.LittleEndian.Uint64(cbData[absolutePos+12 : absolutePos+20])

				pipelineEvents = append(pipelineEvents, PipelineStateEvent{
					Offset:       int64(absolutePos),
					PipelineAddr: pipelineAddr,
					EncoderAddr:  encoderAddr,
				})
			}
			offset += pos + 4
		}

		// 3. Find Dispatches
		dispatches, _ := t.ParseDispatchInRegion(cbData, 0)

		// 4. Correlate Dispatches with Pipeline State
		// For each dispatch, we need to know:
		// a) Which encoder is it in?
		// b) What was the LAST pipeline state set for that encoder?

		// Optimization: Sort events by offset to process linearly
		type Event struct {
			Offset int64
			Type   int // 0=EncoderStart, 1=PipelineSet, 2=Dispatch
			Index  int // Index into respective slice
		}

		var events []Event
		for idx, enc := range actualEncoders {
			events = append(events, Event{Offset: enc.StartOffset, Type: 0, Index: idx})
		}
		for idx, pse := range pipelineEvents {
			events = append(events, Event{Offset: pse.Offset, Type: 1, Index: idx})
		}
		for idx, disp := range dispatches {
			events = append(events, Event{Offset: disp.Offset, Type: 2, Index: idx})
		}

		sort.Slice(events, func(i, j int) bool {
			return events[i].Offset < events[j].Offset
		})

		// Track current state
		var currentEncoder *EncoderSection
		var currentPipelineAddr uint64

		// Map encoder address to current pipeline for that encoder (though mostly we only care about the active one)
		// Metal technically records commands into a specific encoder.
		// `Ct` records specify which encoder they apply to.
		// Dispatches are purely sequential in the command buffer stream (mostly).
		// Wait, dispatches don't have an encoder ID in them. They are just in the stream.
		// They implicitly belong to the currently active encoder (the one whose `endEncoding` hasn't happened yet).
		// In our parsing, we assume encoders are sequential blocks.

		encoderPipelines := make(map[uint64]uint64)

		for _, ev := range events {
			switch ev.Type {
			case 0: // Encoder Start
				currentEncoder = &actualEncoders[ev.Index]
				// Reset current pipeline? Or does it carry over? No, it's a new encoder.
				// However, `currentPipelineAddr` tracks the *currently active* pipeline state.
				currentPipelineAddr = 0

			case 1: // Pipeline Set
				pse := pipelineEvents[ev.Index]
				// Update the state for the specific encoder
				encoderPipelines[pse.EncoderAddr] = pse.PipelineAddr

				// If this set call is for the current encoder, update currentPipelineAddr
				if currentEncoder != nil && pse.EncoderAddr == currentEncoder.Address {
					currentPipelineAddr = pse.PipelineAddr
				}

			case 2: // Dispatch
				// Attribute to current encoder and pipeline
				if currentEncoder != nil {
					// Use the currently tracked pipeline for this encoder
					// (which should have been updated by Type 1 events)
					pipelineAddr := currentPipelineAddr

					// If no pipeline set yet, try falling back to what we found in simple parsing
					// (Wait, simple parsing logic was flawed too)

					kernelName := "unknown"
					if pipelineAddr != 0 {
						if name, ok := pipelineMap[pipelineAddr]; ok {
							kernelName = name
						}
					}

					// If still unknown, fallback to encoder label guess
					// For ICB, we trust the encoder label if it looks plausible
					if kernelName == "unknown" && currentEncoder.Label != "" {
						// Relaxed check: Accept if it has underscores OR if it matches known MLX patterns
						// Also accept Capitalized names (like "Multiply") if pipeline is missing,
						// assuming the debug group label represents the operation.
						if isActualFunctionName(currentEncoder.Label) {
							kernelName = currentEncoder.Label
						} else if len(currentEncoder.Label) > 0 {
							// For missing pipelines, we accept any label as better than "unknown"
							// This catches cases like "Multiply" where no Ct record exists.
							kernelName = currentEncoder.Label
						}
					}

					// Update stats
					if _, ok := stats[kernelName]; !ok {
						stats[kernelName] = &KernelStat{
							Name:          kernelName,
							PipelineAddr:  pipelineAddr,
							DebugGroups:   make(map[string]int),
							EncoderLabels: make(map[string]int),
						}
					}

					s := stats[kernelName]
					s.DispatchCount++
					if currentEncoder.Label != "" {
						s.EncoderLabels[currentEncoder.Label]++
					}

					if debugGroup := t.GetDebugGroupForLabel(currentEncoder.Label); debugGroup != "" {
						s.DebugGroups[debugGroup]++
					} else if debugGroup := t.GetDebugGroupForLabel(kernelName); debugGroup != "" {
                        s.DebugGroups[debugGroup]++
                    }
				} else {
					// Dispatch outside of known encoder?
					// Add to "unknown"
					if _, ok := stats["unknown"]; !ok {
						stats["unknown"] = &KernelStat{
							Name:          "unknown",
							DebugGroups:   make(map[string]int),
							EncoderLabels: make(map[string]int),
						}
					}
					stats["unknown"].DispatchCount++
				}
			}
		}
	}

    // Cleanup
    if s, ok := stats["unknown"]; ok && s.DispatchCount == 0 {
        delete(stats, "unknown")
    }

	return stats, nil
}
