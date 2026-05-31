package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tmc/gputrace"
)

func TestXcodeCountersValidateOptions(t *testing.T) {
	for _, format := range []string{"summary", "detailed", "metrics", "json"} {
		t.Run(format, func(t *testing.T) {
			if err := validateXcodeCountersOptions(format, 0); err != nil {
				t.Fatalf("validateXcodeCountersOptions(%q, 0): %v", format, err)
			}
		})
	}

	tests := []struct {
		name    string
		format  string
		top     int
		wantErr string
	}{
		{
			name:    "invalid format",
			format:  "yaml",
			wantErr: "unknown format: yaml",
		},
		{
			name:    "negative top",
			format:  "summary",
			top:     -1,
			wantErr: "--top must be >= 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateXcodeCountersOptions(tt.format, tt.top)
			if err == nil {
				t.Fatalf("validateXcodeCountersOptions(%q, %d) succeeded, want %q", tt.format, tt.top, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q does not contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestXcodeCountersRejectsInvalidOptionsBeforeTraceIO(t *testing.T) {
	tests := []struct {
		name    string
		format  string
		top     int
		wantErr string
	}{
		{
			name:    "invalid format",
			format:  "yaml",
			wantErr: "unknown format: yaml",
		},
		{
			name:    "negative top",
			format:  "summary",
			top:     -1,
			wantErr: "--top must be >= 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldFormat := xcodeCountersFormat
			oldTop := xcodeCountersTop
			t.Cleanup(func() {
				xcodeCountersFormat = oldFormat
				xcodeCountersTop = oldTop
			})

			xcodeCountersFormat = tt.format
			xcodeCountersTop = tt.top

			err := runXcodeCounters(nil, []string{"missing.gputrace"})
			if err == nil {
				t.Fatalf("runXcodeCounters succeeded, want %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q does not contain %q", err, tt.wantErr)
			}
			if strings.Contains(err.Error(), "failed to open trace") {
				t.Fatalf("runXcodeCounters reached trace open before validation: %v", err)
			}
		})
	}
}

func TestXcodeCountersFilterTopEncodersNonPositiveTop(t *testing.T) {
	data := &gputrace.XcodeCounterData{
		Encoders: []gputrace.XcodeEncoderCounters{
			{
				Index: 1,
				Counters: map[string]float64{
					"ALU Utilization": 1,
				},
			},
			{
				Index:    2,
				Counters: map[string]float64{},
			},
			{
				Index: 3,
				Counters: map[string]float64{
					"ALU Utilization": 3,
				},
			},
		},
	}

	tests := []struct {
		name string
		top  int
	}{
		{name: "zero", top: 0},
		{name: "negative", top: -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterTopEncoders(data, "ALU Utilization", tt.top)
			if len(got) != 2 {
				t.Fatalf("filterTopEncoders top %d returned %d encoders, want 2", tt.top, len(got))
			}
			if got[0].Index != 3 || got[1].Index != 1 {
				t.Fatalf("indexes = [%d %d], want [3 1]", got[0].Index, got[1].Index)
			}
		})
	}
}

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
