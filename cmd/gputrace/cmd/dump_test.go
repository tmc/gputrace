package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace"
)

func TestFilterDumpAPICallListCombinesCategoryFiltersAsUnion(t *testing.T) {
	opts := dumpOptions{
		buffersOnly:        true,
		dispatchOnly:       true,
		commandBufferIndex: -1,
	}

	got := filterDumpAPICallList(testDumpAPICallList(), opts)
	if len(got.InitCalls) != 1 {
		t.Fatalf("init calls = %d, want 1", len(got.InitCalls))
	}
	if got.InitCalls[0].Type != "newBuffer" {
		t.Fatalf("init call type = %q, want newBuffer", got.InitCalls[0].Type)
	}
	if len(got.CommandBuffers) != 2 {
		t.Fatalf("command buffers = %d, want 2", len(got.CommandBuffers))
	}
	for _, cb := range got.CommandBuffers {
		if len(cb.Calls) != 1 {
			t.Fatalf("command buffer %d calls = %d, want 1", cb.Index, len(cb.Calls))
		}
		if cb.Calls[0].Type != "dispatch" {
			t.Fatalf("command buffer %d call type = %q, want dispatch", cb.Index, cb.Calls[0].Type)
		}
	}
}

func TestFilterDumpAPICallListCombinesCommandBufferAndPattern(t *testing.T) {
	opts := dumpOptions{
		filter:             "{2, 1, 1}",
		dispatchOnly:       true,
		commandBufferIndex: 1,
	}

	got := filterDumpAPICallList(testDumpAPICallList(), opts)
	if len(got.InitCalls) != 0 {
		t.Fatalf("init calls = %d, want 0", len(got.InitCalls))
	}
	if len(got.CommandBuffers) != 1 {
		t.Fatalf("command buffers = %d, want 1", len(got.CommandBuffers))
	}
	cb := got.CommandBuffers[0]
	if cb.Index != 1 {
		t.Fatalf("command buffer index = %d, want 1", cb.Index)
	}
	if len(cb.Calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(cb.Calls))
	}
	if got := cb.Calls[0].Details; !strings.Contains(got, "{2, 1, 1}") {
		t.Fatalf("call details = %q, want dispatch with {2, 1, 1}", got)
	}
}

func TestFormatDumpAPICallListHonorsNoNumbersAndNoIndent(t *testing.T) {
	opts := dumpOptions{
		noIndent:           true,
		noNumbers:          true,
		commandBufferIndex: 0,
	}
	list := filterDumpAPICallList(testDumpAPICallList(), opts)

	var out bytes.Buffer
	if err := formatDumpAPICallList(&out, list, opts); err != nil {
		t.Fatalf("formatDumpAPICallList: %v", err)
	}

	got := out.String()
	if strings.Contains(got, "#") {
		t.Fatalf("output contains call numbers:\n%s", got)
	}
	if strings.Contains(got, "\t") {
		t.Fatalf("output contains indentation:\n%s", got)
	}
	for _, want := range []string{
		"cb zero = [0x10 commandBuffer]\n",
		"encoder zero = [computeCommandEncoder]\n",
		"[dispatchThreads:{1, 1, 1} threadsPerThreadgroup:{32, 1, 1}]\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
}

func TestFormatDumpAPICallListMatchesTraceFormatters(t *testing.T) {
	tracePath := testDumpTracePath(t)
	tr, err := gputrace.Open(tracePath)
	if err != nil {
		t.Fatalf("open trace: %v", err)
	}
	apiList, err := tr.ParseAPICallList()
	if err != nil {
		t.Fatalf("parse API calls: %v", err)
	}

	t.Run("compact", func(t *testing.T) {
		var want bytes.Buffer
		if err := tr.FormatAPICallList(&want); err != nil {
			t.Fatalf("FormatAPICallList: %v", err)
		}

		var got bytes.Buffer
		opts := dumpOptions{commandBufferIndex: -1}
		if err := formatDumpAPICallList(&got, apiList, opts); err != nil {
			t.Fatalf("formatDumpAPICallList: %v", err)
		}
		if got.String() != want.String() {
			t.Fatalf("compact output mismatch\ngot:\n%s\nwant:\n%s", got.String(), want.String())
		}
	})

	t.Run("full", func(t *testing.T) {
		var want bytes.Buffer
		if err := tr.FormatAPICallListFull(&want); err != nil {
			t.Fatalf("FormatAPICallListFull: %v", err)
		}

		var got bytes.Buffer
		opts := dumpOptions{full: true, commandBufferIndex: -1}
		if err := formatDumpAPICallListFull(&got, apiList, opts); err != nil {
			t.Fatalf("formatDumpAPICallListFull: %v", err)
		}
		if got.String() != want.String() {
			t.Fatalf("full output mismatch\ngot:\n%s\nwant:\n%s", got.String(), want.String())
		}
	})
}

func TestRunDumpValidatesFlagsBeforeTraceIO(t *testing.T) {
	restoreDumpGlobals(t)
	dumpCommandBufferIndex = -2

	err := runDump(&cobra.Command{}, []string{filepath.Join(t.TempDir(), "missing.gputrace")})
	if err == nil {
		t.Fatal("runDump succeeded, want error")
	}
	if got, want := err.Error(), "--command-buffer must be >= -1"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestRunDumpJSONUsesCommandOutput(t *testing.T) {
	tracePath := testDumpTracePath(t)

	restoreDumpGlobals(t)
	dumpJSON = true
	dumpFilter = "dispatchthreads"

	var out bytes.Buffer
	command := &cobra.Command{}
	command.SetOut(&out)

	if err := runDump(command, []string{tracePath}); err != nil {
		t.Fatalf("runDump: %v", err)
	}
	if got := out.String(); !strings.HasSuffix(got, "\n") {
		t.Fatalf("json output does not end with newline: %q", got)
	}

	var got gputrace.APICallList
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("json output is invalid: %v\n%s", err, out.String())
	}
	if len(got.CommandBuffers) == 0 {
		t.Fatalf("json command buffers = 0, want at least 1:\n%s", out.String())
	}
	for _, cb := range got.CommandBuffers {
		for _, call := range cb.Calls {
			if call.Type != "dispatch" {
				t.Fatalf("json call type = %q, want dispatch", call.Type)
			}
		}
	}
}

func testDumpTracePath(t *testing.T) string {
	t.Helper()

	tracePath := "../../../testdata/traces/01-single-encoder/01-single-encoder-run1.gputrace"
	if _, err := os.Stat(tracePath); os.IsNotExist(err) {
		t.Skipf("trace fixture not available: %s", tracePath)
	}
	return tracePath
}

func restoreDumpGlobals(t *testing.T) {
	t.Helper()

	oldFilter := dumpFilter
	oldNoIndent := dumpNoIndent
	oldNoNumbers := dumpNoNumbers
	oldBuffersOnly := dumpBuffersOnly
	oldDispatchOnly := dumpDispatchOnly
	oldEncodersOnly := dumpEncodersOnly
	oldJSON := dumpJSON
	oldFull := dumpFull
	oldCommandBufferIndex := dumpCommandBufferIndex

	t.Cleanup(func() {
		dumpFilter = oldFilter
		dumpNoIndent = oldNoIndent
		dumpNoNumbers = oldNoNumbers
		dumpBuffersOnly = oldBuffersOnly
		dumpDispatchOnly = oldDispatchOnly
		dumpEncodersOnly = oldEncodersOnly
		dumpJSON = oldJSON
		dumpFull = oldFull
		dumpCommandBufferIndex = oldCommandBufferIndex
	})
}

func testDumpAPICallList() *gputrace.APICallList {
	return &gputrace.APICallList{
		InitCalls: []gputrace.InitCall{
			{
				CallNumber: 0,
				Type:       "newBuffer",
				Address:    0x100,
				Info:       "[Device newBufferWithLength:64 options:0]",
			},
			{
				CallNumber: 1,
				Type:       "newPipelineState",
				Address:    0x200,
				Info:       "[Device newComputePipelineStateWithFunction:k0 error:nil]",
			},
		},
		CommandBuffers: []gputrace.CommandBufferCalls{
			{
				Index:        0,
				Address:      0xc0,
				QueueAddress: 0x10,
				Label:        "cb zero",
				Calls: []gputrace.FormattedAPICall{
					{
						CallNumber: 2,
						Indented:   true,
						Type:       "encoder",
						Address:    0xe0,
						Details:    "computeCommandEncoder",
						Label:      "encoder zero",
					},
					{
						CallNumber: 3,
						Indented:   true,
						Type:       "setBuffer",
						Details:    "setBuffer:0x100 offset:0 atIndex:0",
					},
					{
						CallNumber: 4,
						Indented:   true,
						Type:       "dispatch",
						Details:    "dispatchThreads:{1, 1, 1} threadsPerThreadgroup:{32, 1, 1}",
					},
					{
						CallNumber: 5,
						Indented:   true,
						Type:       "endEncoding",
						Details:    "endEncoding",
					},
					{
						CallNumber: 6,
						Type:       "commit",
						Details:    "commit",
					},
				},
			},
			{
				Index:        1,
				Address:      0xc1,
				QueueAddress: 0x10,
				Label:        "cb one",
				Calls: []gputrace.FormattedAPICall{
					{
						CallNumber: 7,
						Indented:   true,
						Type:       "encoder",
						Address:    0xe1,
						Details:    "computeCommandEncoder",
						Label:      "encoder one",
					},
					{
						CallNumber: 8,
						Indented:   true,
						Type:       "dispatch",
						Details:    "dispatchThreads:{2, 1, 1} threadsPerThreadgroup:{32, 1, 1}",
					},
					{
						CallNumber: 9,
						Indented:   true,
						Type:       "endEncoding",
						Details:    "endEncoding",
					},
					{
						CallNumber: 10,
						Type:       "wait",
						Details:    "waitUntilCompleted",
					},
				},
			},
		},
	}
}
