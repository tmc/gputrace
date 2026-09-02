//go:build darwin && metal

package replay

import (
	"fmt"

	"github.com/tmc/gputrace/internal/counter"
)

// MetalCounterBackend connects the platform-independent counter sampler to
// public MTLCounterSampleBuffer operations.
type MetalCounterBackend struct {
	bridge        *MetalBridge
	stageBoundary bool
}

// NewMetalCounterBackend creates a counter backend for bridge.
func NewMetalCounterBackend(bridge *MetalBridge) (*MetalCounterBackend, error) {
	if bridge == nil {
		return nil, fmt.Errorf("nil Metal bridge")
	}
	return &MetalCounterBackend{
		bridge:        bridge,
		stageBoundary: !bridge.SupportsExplicitCounterSampling() && bridge.SupportsStageBoundaryCounterSampling(),
	}, nil
}

var _ counter.CounterSampleBackend = (*MetalCounterBackend)(nil)

func (b *MetalCounterBackend) CreateSampleBuffer(counterSet string, sampleCount int) (any, error) {
	if b == nil || b.bridge == nil {
		return nil, fmt.Errorf("nil Metal counter backend")
	}
	sets, err := b.bridge.QueryCounterSets()
	if err != nil {
		return nil, err
	}
	for _, set := range sets {
		if set.Name() == counterSet {
			return b.bridge.CreateCounterSampleBuffer(set, sampleCount)
		}
	}
	return nil, fmt.Errorf("counter set %q is not supported", counterSet)
}

func (b *MetalCounterBackend) SampleCounters(encoder, sampleBuffer any, sampleIndex int) error {
	if b.stageBoundary {
		return nil
	}
	enc, ok := encoder.(*MetalComputeEncoderHandle)
	if !ok || enc == nil {
		return fmt.Errorf("counter sample encoder has type %T, want *MetalComputeEncoderHandle", encoder)
	}
	buffer, ok := sampleBuffer.(*MetalCounterSampleBufferHandle)
	if !ok || buffer == nil {
		return fmt.Errorf("counter sample buffer has type %T, want *MetalCounterSampleBufferHandle", sampleBuffer)
	}
	enc.SampleCounters(buffer, sampleIndex)
	return nil
}

func (b *MetalCounterBackend) CreateComputeEncoder(commandBuffer *MetalCommandBufferHandle, sampleBuffer any, startIndex, endIndex int) (*MetalComputeEncoderHandle, error) {
	if b == nil || commandBuffer == nil {
		return nil, fmt.Errorf("nil Metal command buffer")
	}
	if !b.stageBoundary {
		return commandBuffer.CreateComputeEncoder(), nil
	}
	buffer, ok := sampleBuffer.(*MetalCounterSampleBufferHandle)
	if !ok || buffer == nil {
		return nil, fmt.Errorf("counter sample buffer has type %T, want *MetalCounterSampleBufferHandle", sampleBuffer)
	}
	return commandBuffer.createComputeEncoderWithStageSamplingAt(buffer, startIndex, endIndex)
}

func (b *MetalCounterBackend) StageBoundarySampling() bool { return b != nil && b.stageBoundary }

func (b *MetalCounterBackend) ResolveCounterSamples(commandBuffer, sampleBuffer any, startIndex, count int) ([]byte, error) {
	cmd, ok := commandBuffer.(*MetalCommandBufferHandle)
	if !ok || cmd == nil {
		return nil, fmt.Errorf("counter command buffer has type %T, want *MetalCommandBufferHandle", commandBuffer)
	}
	buffer, ok := sampleBuffer.(*MetalCounterSampleBufferHandle)
	if !ok || buffer == nil {
		return nil, fmt.Errorf("counter sample buffer has type %T, want *MetalCounterSampleBufferHandle", sampleBuffer)
	}
	return cmd.ResolveCounterSamples(buffer, startIndex, count)
}

func (b *MetalCounterBackend) ReleaseSampleBuffer(sampleBuffer any) {
	if buffer, ok := sampleBuffer.(*MetalCounterSampleBufferHandle); ok && buffer != nil {
		buffer.Release()
	}
}
