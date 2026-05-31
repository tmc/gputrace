package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/tmc/gputrace"
)

func TestWriteAPICallsJSON(t *testing.T) {
	apiList := &gputrace.APICallList{
		InitCalls: []gputrace.InitCall{
			{CallNumber: 0, Type: "newBuffer", Address: 0x10, Info: "[Device newBufferWithLength:16 options:0]"},
		},
		CommandBuffers: []gputrace.CommandBufferCalls{
			{
				Index:        1,
				Address:      0x20,
				QueueAddress: 0x30,
				Calls: []gputrace.FormattedAPICall{
					{CallNumber: 1, Type: "dispatch", Details: "dispatchThreads:1,1,1 threadsPerThreadgroup:1,1,1"},
				},
			},
		},
	}

	var out bytes.Buffer
	if err := writeAPICallsJSON(&out, apiList); err != nil {
		t.Fatalf("writeAPICallsJSON: %v", err)
	}
	if !strings.HasSuffix(out.String(), "\n") {
		t.Fatalf("JSON output missing trailing newline: %q", out.String())
	}

	var got gputrace.APICallList
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("JSON output did not decode: %v\n%s", err, out.String())
	}
	if len(got.InitCalls) != 1 || got.InitCalls[0].Type != "newBuffer" {
		t.Fatalf("decoded init calls = %+v", got.InitCalls)
	}
	if len(got.CommandBuffers) != 1 || len(got.CommandBuffers[0].Calls) != 1 {
		t.Fatalf("decoded command buffers = %+v", got.CommandBuffers)
	}
}
