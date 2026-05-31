package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tmc/gputrace/internal/trace"
)

func TestAnalyzeUsageDOTDirections(t *testing.T) {
	events := []trace.DependencyEvent{
		{Offset: 10, Type: trace.EventCS, Label: "Writer"},
		{
			Offset:  11,
			Type:    trace.EventBind,
			Address: 0x2000,
			Name:    "Shared",
			Usage:   trace.MTLResourceUsageWrite,
		},
		{Offset: 20, Type: trace.EventCS, Label: "Reader"},
		{
			Offset:  21,
			Type:    trace.EventBind,
			Address: 0x2000,
			Name:    "Shared",
			Usage:   trace.MTLResourceUsageRead,
		},
		{Offset: 30, Type: trace.EventCS, Label: "Mutator"},
		{
			Offset:  31,
			Type:    trace.EventBind,
			Address: 0x2000,
			Name:    "Shared",
			Usage:   trace.MTLResourceUsageRead | trace.MTLResourceUsageWrite,
		},
	}

	var out strings.Builder
	if err := writeAnalyzeUsageDOT(&out, collectAnalyzeUsage(events)); err != nil {
		t.Fatalf("writeAnalyzeUsageDOT returned error: %v", err)
	}
	got := out.String()

	for _, want := range []string{
		`"kernel:Writer" -> "buffer:0x2000" [label="Write"];`,
		`"buffer:0x2000" -> "kernel:Reader" [label="Read"];`,
		`"kernel:Mutator" -> "buffer:0x2000" [label="ReadWrite", dir=both];`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("DOT output missing %q:\n%s", want, got)
		}
	}
}

func TestAnalyzeUsageEventUseIsRead(t *testing.T) {
	events := []trace.DependencyEvent{
		{Offset: 20, Type: trace.EventUse, Address: 0x3000},
		{Offset: 10, Type: trace.EventCS, Label: "UsesImplicitly"},
		{Offset: 30, Type: trace.EventUse, Address: 0x3000},
	}

	stats := collectAnalyzeUsage(events)[0x3000]
	if stats == nil {
		t.Fatal("missing buffer stats for EventUse")
	}
	kernel := stats.Kernels["UsesImplicitly"]
	if kernel == nil {
		t.Fatal("missing kernel stats for EventUse")
	}
	if kernel.Reads != 2 || kernel.Writes != 0 {
		t.Fatalf("EventUse counts = reads:%d writes:%d, want reads:2 writes:0", kernel.Reads, kernel.Writes)
	}
}

func TestWriteAnalyzeUsageJSON(t *testing.T) {
	events := []trace.DependencyEvent{
		{Offset: 10, Type: trace.EventCS, Label: "Writer"},
		{
			Offset:  11,
			Type:    trace.EventBind,
			Address: 0x2000,
			Name:    "Shared",
			Usage:   trace.MTLResourceUsageWrite,
		},
		{Offset: 20, Type: trace.EventCS, Label: "Reader"},
		{
			Offset:  21,
			Type:    trace.EventBind,
			Address: 0x2000,
			Name:    "Shared",
			Usage:   trace.MTLResourceUsageRead,
		},
	}

	var out strings.Builder
	if err := writeAnalyzeUsageJSON(&out, collectAnalyzeUsage(events)); err != nil {
		t.Fatalf("writeAnalyzeUsageJSON returned error: %v", err)
	}
	if got := out.String(); !strings.HasSuffix(got, "\n") {
		t.Fatalf("JSON output missing trailing newline: %q", got)
	}

	var got []analyzeBufferUsageJSON
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("JSON output did not decode: %v\n%s", err, out.String())
	}
	if len(got) != 1 {
		t.Fatalf("buffer count = %d, want 1", len(got))
	}
	if got[0].Address != "0x2000" || got[0].Dispatches != 2 || len(got[0].Kernels) != 2 {
		t.Fatalf("buffer JSON = %+v", got[0])
	}
}

func TestAnalyzeUsageFormat(t *testing.T) {
	tests := []struct {
		name    string
		format  string
		wantErr string
	}{
		{name: "text", format: "text"},
		{name: "dot", format: "dot"},
		{name: "json", format: "json"},
		{name: "unknown", format: "yaml", wantErr: `unknown analyze-usage format "yaml"`},
		{name: "empty", wantErr: `unknown analyze-usage format ""`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeAnalyzeFormat(tt.format)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("normalizeAnalyzeFormat returned nil error, want %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error %q does not contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeAnalyzeFormat returned error: %v", err)
			}
			if got != tt.format {
				t.Fatalf("normalizeAnalyzeFormat(%q) = %q, want %q", tt.format, got, tt.format)
			}
		})
	}
}
