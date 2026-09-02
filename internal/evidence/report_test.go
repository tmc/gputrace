package evidence

import (
	"encoding/binary"
	"testing"

	"github.com/tmc/gputrace/internal/counter"
	"github.com/tmc/gputrace/internal/trace"
)

func TestBuildDoesNotCountLabelsAsEncoders(t *testing.T) {
	stats := &counter.StreamDataStats{
		NumEncoders: 2,
		Dispatches: []counter.DispatchInfo{
			{EncoderIndex: 0, FunctionName: "a", DurationUs: 3},
			{EncoderIndex: 0, FunctionName: "a", DurationUs: 2},
			{EncoderIndex: 1, FunctionName: "b", DurationUs: 5},
		},
		TotalDispatchTimeUs: 10,
	}
	report, err := Build(nil, stats)
	if err != nil {
		t.Fatal(err)
	}
	if report.ComputeEncoders != 2 || report.Dispatches != 3 {
		t.Fatalf("counts = %d encoders, %d dispatches", report.ComputeEncoders, report.Dispatches)
	}
	if report.Functions[0].Name != "a" || report.Functions[0].Dispatches != 2 {
		t.Fatalf("functions = %+v", report.Functions)
	}
}

func TestBuildLabelVolumeDoesNotChangeEncoderCount(t *testing.T) {
	for _, labels := range []int{1, 997} {
		tr := &trace.Trace{}
		for i := 0; i < labels; i++ {
			tr.CaptureData = append(tr.CaptureData, "CS\x00\x00"...)
			tr.CaptureData = binary.LittleEndian.AppendUint64(tr.CaptureData, uint64(i+1))
			tr.CaptureData = append(tr.CaptureData, "label\x00"...)
		}
		report, err := Build(tr, &counter.StreamDataStats{NumEncoders: 23})
		if err != nil {
			t.Fatal(err)
		}
		if report.ComputeEncoders != 23 || report.CSLabels != labels {
			t.Fatalf("%d labels: got %d encoders and %d labels", labels, report.ComputeEncoders, report.CSLabels)
		}
	}
}
