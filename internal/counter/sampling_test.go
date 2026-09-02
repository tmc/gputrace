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

type sampleBackend struct {
	created  map[string]int
	samples  []int
	resolved map[string][]byte
}

func (b *sampleBackend) CreateSampleBuffer(name string, count int) (any, error) {
	if b.created == nil {
		b.created = make(map[string]int)
	}
	b.created[name] = count
	return name, nil
}

func (b *sampleBackend) SampleCounters(_, _ any, index int) error {
	b.samples = append(b.samples, index)
	return nil
}

func (b *sampleBackend) ResolveCounterSamples(_, buffer any, _, _ int) ([]byte, error) {
	name := buffer.(string)
	return b.resolved[name], nil
}

func TestCounterSamplerBackendRetainsRawData(t *testing.T) {
	backend := &sampleBackend{resolved: map[string][]byte{
		"timestamp": {1, 2, 3},
	}}
	cs := NewCounterSamplerWithBackend(&CounterSamplingConfig{
		EnabledCounterSets: []string{"timestamp"},
	}, backend)
	if err := cs.CreateCounterSampleBuffers(struct{}{}, 2); err != nil {
		t.Fatal(err)
	}
	if err := cs.SampleCounters(struct{}{}, "encoder_start", 0, -1); err != nil {
		t.Fatal(err)
	}
	if err := cs.SampleCounters(struct{}{}, "encoder_end", 0, -1); err != nil {
		t.Fatal(err)
	}
	if err := cs.ResolveCounterSamplesWithCommandBuffer(struct{}{}); err != nil {
		t.Fatal(err)
	}
	if got, want := len(backend.samples), 2; got != want {
		t.Fatalf("sample calls = %d, want %d", got, want)
	}
	if got := string(cs.RawData["timestamp"]); got != string([]byte{1, 2, 3}) {
		t.Fatalf("raw data = %q, want raw bytes", got)
	}
	if got, want := cs.NextSampleIndex, 2; got != want {
		t.Fatalf("NextSampleIndex = %d, want %d", got, want)
	}
}
