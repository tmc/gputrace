package counter

import (
	"errors"
	"reflect"
	"testing"
)

func TestAssembleAPSCounterShard(t *testing.T) {
	tests := []struct {
		name         string
		timestamps   []uint64
		counterIDs   []uint64
		sampleCounts []uint64
		groupIDs     []uint8
		wantDescents int
		wantErr      bool
	}{
		{
			name:       "monotone",
			timestamps: []uint64{10, 11, 11, 12}, counterIDs: []uint64{7, 8},
			sampleCounts: []uint64{4, 4}, groupIDs: []uint8{1, 2},
		},
		{
			name:       "wrap",
			timestamps: []uint64{20, 30, 10, 11}, counterIDs: []uint64{7},
			sampleCounts: []uint64{4}, groupIDs: []uint8{1}, wantDescents: 1,
		},
		{
			name:       "several descents",
			timestamps: []uint64{3, 2, 1}, counterIDs: []uint64{7},
			sampleCounts: []uint64{3}, groupIDs: []uint8{1}, wantDescents: 2,
		},
		{
			name:       "mismatched counts",
			counterIDs: []uint64{7}, sampleCounts: nil, groupIDs: []uint8{1}, wantErr: true,
		},
		{
			name:       "mismatched groups",
			counterIDs: []uint64{7}, sampleCounts: []uint64{1}, groupIDs: nil, wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := assembleAPSCounterShard(tt.timestamps, tt.counterIDs, tt.sampleCounts, tt.groupIDs, 2, 3, 4)
			if (err != nil) != tt.wantErr {
				t.Fatalf("assembleAPSCounterShard error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got.TimestampDescents != tt.wantDescents {
				t.Fatalf("TimestampDescents = %d, want %d", got.TimestampDescents, tt.wantDescents)
			}
			if !reflect.DeepEqual(got.SystemTimestamps, tt.timestamps) {
				t.Fatalf("SystemTimestamps = %v, want %v", got.SystemTimestamps, tt.timestamps)
			}
			if len(got.Series) != len(tt.counterIDs) {
				t.Fatalf("len(Series) = %d, want %d", len(got.Series), len(tt.counterIDs))
			}
		})
	}
}

func TestAPSCounterShardCounterValuesFailsClosed(t *testing.T) {
	shard := &APSCounterShard{Series: []APSCounterSeriesShape{{SampleCount: 3}}}
	if values, err := shard.CounterValues(0); values != nil || !errors.Is(err, ErrAPSCounterValuesBinding) {
		t.Fatalf("CounterValues = %v, %v; want nil, ErrAPSCounterValuesBinding", values, err)
	}
	if _, err := shard.CounterValues(1); err == nil {
		t.Fatal("CounterValues accepted an out-of-range series")
	}
	var nilShard *APSCounterShard
	if _, err := nilShard.CounterValues(0); err == nil {
		t.Fatal("CounterValues accepted a nil shard")
	}
}
