//go:build darwin && gputrace_private_bindings

package xcodebindings

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
)

func TestProbeTraceDataPerfFixture(t *testing.T) {
	path := os.Getenv("GPUTRACE_TRACE_DATA_PROBE")
	if path == "" {
		t.Skip("set GPUTRACE_TRACE_DATA_PROBE to a trace or archive path")
	}
	summary, err := ProbeTraceData(path)
	if err != nil {
		t.Logf("GTMioTraceData rejected %q: %v", path, err)
		return
	}
	if summary.ObjectID == "" {
		t.Fatal("GTMioTraceData probe returned no object")
	}
	t.Logf("GTMioTraceData object=%s pipelines=%d costs=%d streamData=%s", summary.ObjectID, summary.PipelineStateCount, summary.CostCount, summary.StreamDataID)
}

// TestProbeGTMioTraceDataStreamInit is an opt-in child-process experiment.
// The initializer has no NSError out-parameter, so an unsupported option or
// archive must not be allowed to take down the parent test process.
func TestProbeGTMioTraceDataStreamInit(t *testing.T) {
	path := os.Getenv("GPUTRACE_TRACE_DATA_INIT_PROBE")
	if path == "" {
		t.Skip("set GPUTRACE_TRACE_DATA_INIT_PROBE to a streamData archive")
	}
	if os.Getenv("GPUTRACE_TRACE_DATA_INIT_CHILD") == "1" {
		err := WithStreamData(path, func(parent objc.ID) error {
			options, _ := strconv.ParseUint(os.Getenv("GPUTRACE_TRACE_DATA_INIT_OPTIONS"), 2, 4)
			helperPath := os.Getenv("GPUTRACE_TRACE_DATA_LLVM_PATH")
			class := objc.GetClass("GTMioTraceData")
			if class == 0 {
				return fmt.Errorf("GTMioTraceData class not found")
			}
			instance := objc.Send[objc.ID](objc.ID(class), objc.Sel("alloc"))
			model := objc.Send[objc.ID](instance, objc.Sel("initWithStreamData:llvmHelperPath:options:"),
				parent,
				foundation.NSStringFromID(objc.String(helperPath)),
				uint32(options),
			)
			t.Logf("initializer returned object=0x%x options=%d helper=%q", uintptr(model), options, helperPath)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestProbeGTMioTraceDataStreamInit$", "-test.v")
	cmd.Env = append(os.Environ(), "GPUTRACE_TRACE_DATA_INIT_CHILD=1")
	output, err := cmd.CombinedOutput()
	t.Logf("isolated initializer output:\n%s", output)
	if ctx.Err() != nil {
		t.Logf("isolated initializer stopped: %v", ctx.Err())
	} else if err != nil {
		t.Logf("isolated initializer exited with: %v", err)
	}
}
