package difftrace

import "testing"

func TestBuildPipelinePairs(t *testing.T) {
	a := &TraceData{
		Dispatches: []Dispatch{
			{FunctionName: "foo", ThreadgroupSig: "1x1x1/1x1x1", PipelineID: 10, PipelineHash: "ha", DurationUs: 30},
			{FunctionName: "foo", ThreadgroupSig: "1x1x1/1x1x1", PipelineID: 10, PipelineHash: "ha", DurationUs: 20},
			{FunctionName: "foo", ThreadgroupSig: "2x1x1/1x1x1", PipelineID: 11, PipelineHash: "skip", DurationUs: 99},
			{FunctionName: "bar", ThreadgroupSig: "1x1x1/1x1x1", PipelineID: 12, PipelineHash: "hb", DurationUs: 5},
		},
		Pipelines: map[int]PipelineInfo{
			10: {StaticCounters: StaticCounters{Instructions: 80, Registers: 6, Loads: 3, Stores: 1}},
			12: {StaticCounters: StaticCounters{Instructions: 10, Registers: 2}},
		},
	}
	b := &TraceData{
		Dispatches: []Dispatch{
			{FunctionName: "foo", ThreadgroupSig: "1x1x1/1x1x1", PipelineID: 20, PipelineHash: "hx", DurationUs: 10},
			{FunctionName: "foo", ThreadgroupSig: "1x1x1/1x1x1", PipelineID: 20, PipelineHash: "hx", DurationUs: 15},
			{FunctionName: "bar", ThreadgroupSig: "1x1x1/1x1x1", PipelineID: 21, PipelineHash: "hy", DurationUs: 7},
		},
		Pipelines: map[int]PipelineInfo{
			20: {StaticCounters: StaticCounters{Instructions: 92, Registers: 8, Loads: 3, Stores: 2}},
			21: {StaticCounters: StaticCounters{Instructions: 11, Registers: 2}},
		},
	}

	got := BuildPipelinePairs(a, b)
	if len(got) != 2 {
		t.Fatalf("pairs = %d, want 2", len(got))
	}
	top := got[0]
	if top.FunctionName != "foo" || top.ThreadgroupSig != "1x1x1/1x1x1" {
		t.Fatalf("top pair key = %q/%q", top.FunctionName, top.ThreadgroupSig)
	}
	if top.AUs != 50 || top.BUs != 25 || top.AbsDeltaUs != 25 {
		t.Fatalf("top timing = %+v", top)
	}
	if top.APipelineID != 10 || top.BPipelineID != 20 {
		t.Fatalf("pipeline ids = %d/%d", top.APipelineID, top.BPipelineID)
	}
	if top.StaticCounterDelta.Instructions != -12 || top.StaticCounterDelta.Registers != -2 || top.StaticCounterDelta.Stores != -1 {
		t.Fatalf("static counter delta = %+v", top.StaticCounterDelta)
	}
}
