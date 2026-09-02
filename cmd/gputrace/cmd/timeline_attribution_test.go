package cmd

import (
	"reflect"
	"testing"
)

func cbEvent(index int, startTicks, endTicks uint64) TimelineEvent {
	return TimelineEvent{
		Name:     "CB",
		Category: "command_buffer",
		Args: map[string]interface{}{
			"index":       index,
			"start_ticks": startTicks,
			"end_ticks":   endTicks,
		},
	}
}

func tickKernel(encoder int, startTicks uint64) KernelInfo {
	return KernelInfo{
		Name:    "k",
		Encoder: encoder,
		Args:    map[string]interface{}{"start_ticks": startTicks, "end_ticks": startTicks + 1},
	}
}

func encoderIndexes(encoders []EncoderInfo) []int {
	out := make([]int, 0, len(encoders))
	for _, e := range encoders {
		out = append(out, e.Index)
	}
	return out
}

func TestAttributeEncodersToCBs(t *testing.T) {
	encoders := []EncoderInfo{{Index: 0}, {Index: 1}, {Index: 2}}

	tests := []struct {
		name             string
		cbs              []TimelineEvent
		kernels          []KernelInfo
		want             map[int][]int
		wantUnattributed []int
	}{{
		// The whole point of the report: encoders land under a command buffer.
		name: "ticks place each encoder in its command buffer",
		cbs:  []TimelineEvent{cbEvent(0, 100, 199), cbEvent(1, 200, 299)},
		kernels: []KernelInfo{
			tickKernel(0, 150),
			tickKernel(1, 250),
			tickKernel(2, 260),
		},
		want: map[int][]int{0: {0}, 1: {1, 2}},
	}, {
		// Encoder-span kernels carry no ticks, but with one command buffer
		// there is nowhere else for them to be.
		name:    "single command buffer takes tickless encoders",
		cbs:     []TimelineEvent{cbEvent(0, 0, 0)},
		kernels: []KernelInfo{{Name: "k", Encoder: 0}, {Name: "k", Encoder: 1}, {Name: "k", Encoder: 2}},
		want:    map[int][]int{0: {0, 1, 2}},
	}, {
		// Ambiguous: several command buffers and nothing to place them by.
		// They must still be reported, not silently dropped.
		name:             "unattributable encoders are reported",
		cbs:              []TimelineEvent{cbEvent(0, 100, 199), cbEvent(1, 200, 299)},
		kernels:          []KernelInfo{{Name: "k", Encoder: 0}},
		want:             map[int][]int{},
		wantUnattributed: []int{0, 1, 2},
	}, {
		name:             "ticks outside every window stay unattributed",
		cbs:              []TimelineEvent{cbEvent(0, 100, 199), cbEvent(1, 200, 299)},
		kernels:          []KernelInfo{tickKernel(0, 5000)},
		want:             map[int][]int{},
		wantUnattributed: []int{0, 1, 2},
	}, {
		// Args survive a JSON round trip as float64.
		name: "float args from a decoded timeline",
		cbs: []TimelineEvent{{Args: map[string]interface{}{
			"index": float64(3), "start_ticks": float64(100), "end_ticks": float64(199),
		}}},
		kernels: []KernelInfo{{Name: "k", Encoder: 0, Args: map[string]interface{}{"start_ticks": float64(150)}}},
		want:    map[int][]int{3: {0, 1, 2}},
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			timeline := &Timeline{Encoders: encoders, Kernels: tt.kernels}
			byCB, unattributed := attributeEncodersToCBs(timeline, tt.cbs)

			got := make(map[int][]int, len(byCB))
			for idx, encs := range byCB {
				got[idx] = encoderIndexes(encs)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("byCB = %v, want %v", got, tt.want)
			}
			if gotUn := encoderIndexes(unattributed); !reflect.DeepEqual(gotUn, tt.wantUnattributed) &&
				!(len(gotUn) == 0 && len(tt.wantUnattributed) == 0) {
				t.Errorf("unattributed = %v, want %v", gotUn, tt.wantUnattributed)
			}
		})
	}
}
