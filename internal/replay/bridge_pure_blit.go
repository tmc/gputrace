//go:build darwin

package replay

import (
	"github.com/tmc/apple/metal"
	"github.com/tmc/apple/objc"
)

// CreateBlitEncoder creates a blit command encoder.
func (h *MetalCommandBufferHandle) CreateBlitEncoder() *MetalBlitEncoderHandle {
	encoderID := objc.Send[objc.ID](h.cmdBuffer.GetID(), objc.Sel("blitCommandEncoder"))
	encoder := metal.MTLBlitCommandEncoderObjectFromID(encoderID)
	return &MetalBlitEncoderHandle{encoder: encoder}
}

// MetalBlitEncoderHandle wraps a blit command encoder.
type MetalBlitEncoderHandle struct {
	encoder metal.MTLBlitCommandEncoderObject
}

// SampleCounters inserts a counter sample.
func (h *MetalBlitEncoderHandle) SampleCounters(sampleBuffer *MetalCounterSampleBufferHandle, sampleIndex int) {
	h.encoder.SampleCountersInBufferAtSampleIndexWithBarrier(sampleBuffer.buffer, uint(sampleIndex), true)
}

// EndEncoding finishes encoding commands.
func (h *MetalBlitEncoderHandle) EndEncoding() {
	h.encoder.EndEncoding()
}

// Release frees the encoder.
func (h *MetalBlitEncoderHandle) Release() {
}
