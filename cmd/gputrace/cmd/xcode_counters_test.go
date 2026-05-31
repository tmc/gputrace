package cmd

import (
	"encoding/json"
	"testing"

	"github.com/tmc/gputrace"
)

func TestPrintXcodeJSONEscapesStrings(t *testing.T) {
	data := &gputrace.XcodeCounterData{
		Metrics: []string{`Metric "quoted"`},
		Encoders: []gputrace.XcodeEncoderCounters{
			{
				Index:              1,
				FunctionIndex:      2,
				CommandBufferLabel: `command "buffer" \ one`,
				EncoderLabel:       "encoder\nlabel",
				Counters: map[string]float64{
					`Metric "quoted"`: 12.5,
				},
			},
		},
	}

	out, err := captureStdout(t, func() error {
		return printXcodeJSON(data)
	})
	if err != nil {
		t.Fatalf("printXcodeJSON: %v", err)
	}

	var got xcodeCountersJSONOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("JSON output did not decode: %v\n%s", err, out)
	}
	if got.Encoders != 1 || got.Metrics != 1 || len(got.Data) != 1 {
		t.Fatalf("summary = encoders:%d metrics:%d data:%d, want 1,1,1", got.Encoders, got.Metrics, len(got.Data))
	}
	enc := got.Data[0]
	if enc.CommandBuffer != data.Encoders[0].CommandBufferLabel {
		t.Fatalf("command_buffer = %q, want %q", enc.CommandBuffer, data.Encoders[0].CommandBufferLabel)
	}
	if enc.EncoderLabel != data.Encoders[0].EncoderLabel {
		t.Fatalf("encoder_label = %q, want %q", enc.EncoderLabel, data.Encoders[0].EncoderLabel)
	}
	if enc.CounterMetrics[`Metric "quoted"`] != 12.5 {
		t.Fatalf("counter = %v, want 12.5", enc.CounterMetrics[`Metric "quoted"`])
	}
}
