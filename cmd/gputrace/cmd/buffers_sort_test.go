package cmd

import (
	"reflect"
	"testing"
)

func TestSortBuffers(t *testing.T) {
	// Equal sizes on purpose: the order of those rows is what used to depend
	// on how the buffers happened to be collected.
	input := []BufferInfo{
		{ID: "3", Filename: "c", Size: 100},
		{ID: "1", Filename: "a", Size: 100},
		{ID: "2", Filename: "b", Size: 900},
	}

	tests := []struct {
		sortBy string
		want   []string
	}{
		{sortBy: "size", want: []string{"2", "1", "3"}},
		{sortBy: "id", want: []string{"1", "2", "3"}},
		{sortBy: "name", want: []string{"1", "2", "3"}},
	}

	for _, tt := range tests {
		t.Run(tt.sortBy, func(t *testing.T) {
			// Feed a different starting permutation each time to prove the
			// result does not depend on input order.
			for _, start := range [][]BufferInfo{input, {input[2], input[0], input[1]}} {
				buffers := append([]BufferInfo(nil), start...)
				sortBuffers(buffers, tt.sortBy)

				got := make([]string, len(buffers))
				for i, b := range buffers {
					got[i] = b.ID
				}
				if !reflect.DeepEqual(got, tt.want) {
					t.Errorf("sortBuffers(%q) = %v, want %v", tt.sortBy, got, tt.want)
				}
			}
		})
	}
}
