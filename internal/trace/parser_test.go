package trace

import "testing"

func TestExtractEncodersMarksParsedLabels(t *testing.T) {
	tr := &Trace{
		EncoderLabels: []string{"encoder_a", "encoder_b"},
		KernelNames:   []string{"kernel_a"},
	}

	encoders := tr.extractEncoders()
	if len(encoders) != 2 {
		t.Fatalf("got %d encoders, want 2", len(encoders))
	}
	for i, enc := range encoders {
		if enc.Index != i {
			t.Fatalf("encoder[%d].Index = %d, want %d", i, enc.Index, i)
		}
		if enc.Source != EncoderSourceParsedLabel {
			t.Fatalf("encoder[%d].Source = %q, want %q", i, enc.Source, EncoderSourceParsedLabel)
		}
		if enc.Label != tr.EncoderLabels[i] {
			t.Fatalf("encoder[%d].Label = %q, want %q", i, enc.Label, tr.EncoderLabels[i])
		}
	}
}

func TestExtractEncodersMarksSyntheticFallback(t *testing.T) {
	tr := &Trace{
		KernelNames: []string{"kernel_a"},
	}

	encoders := tr.extractEncoders()
	if len(encoders) != 1 {
		t.Fatalf("got %d encoders, want 1", len(encoders))
	}
	if encoders[0].Label != "ComputeEncoder" {
		t.Fatalf("synthetic label = %q, want ComputeEncoder", encoders[0].Label)
	}
	if encoders[0].Source != EncoderSourceSynthetic {
		t.Fatalf("synthetic source = %q, want %q", encoders[0].Source, EncoderSourceSynthetic)
	}
}
