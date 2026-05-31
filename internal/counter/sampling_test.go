package counter

import (
	"errors"
	"strings"
	"testing"
)

func TestCounterSamplerMetalSamplingUnavailable(t *testing.T) {
	cs := NewCounterSampler(DefaultCounterSamplingConfig())

	if err := cs.CreateCounterSampleBuffers(struct{}{}, 4); !errors.Is(err, ErrMetalCounterSamplingUnavailable) {
		t.Fatalf("CreateCounterSampleBuffers error = %v, want %v", err, ErrMetalCounterSamplingUnavailable)
	}
	if len(cs.Buffers) != 0 {
		t.Fatalf("len(Buffers) = %d, want 0", len(cs.Buffers))
	}

	if err := cs.SampleCounters(struct{}{}, "encoder_start", 0, -1); !errors.Is(err, ErrMetalCounterSamplingUnavailable) {
		t.Fatalf("SampleCounters error = %v, want %v", err, ErrMetalCounterSamplingUnavailable)
	}
	if len(cs.Samples) != 0 {
		t.Fatalf("len(Samples) = %d, want 0", len(cs.Samples))
	}
	if cs.NextSampleIndex != 0 {
		t.Fatalf("NextSampleIndex = %d, want 0", cs.NextSampleIndex)
	}

	if err := cs.ResolveCounterSamples(); !errors.Is(err, ErrMetalCounterSamplingUnavailable) {
		t.Fatalf("ResolveCounterSamples error = %v, want %v", err, ErrMetalCounterSamplingUnavailable)
	}
}

func TestCreateCounterSampleBuffersRejectsUnknownCounterSet(t *testing.T) {
	cs := NewCounterSampler(&CounterSamplingConfig{
		EnabledCounterSets: []string{"timestamp", "not_a_counter_set"},
	})

	err := cs.CreateCounterSampleBuffers(nil, 2)
	if err == nil {
		t.Fatal("CreateCounterSampleBuffers error = nil, want error")
	}
	if errors.Is(err, ErrMetalCounterSamplingUnavailable) {
		t.Fatalf("CreateCounterSampleBuffers error = %v, want unknown counter set", err)
	}
	if !strings.Contains(err.Error(), "unknown counter set: not_a_counter_set") {
		t.Fatalf("CreateCounterSampleBuffers error = %v, want unknown counter set", err)
	}
	if len(cs.Buffers) != 0 {
		t.Fatalf("len(Buffers) = %d, want 0", len(cs.Buffers))
	}
}
