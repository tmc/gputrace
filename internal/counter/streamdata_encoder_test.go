package counter

import (
	"encoding/binary"
	"testing"
)

// gpuCommandInfoData carries two plausible-looking encoder columns. [24:28]
// holds the constant 2 in every record of every archive examined, so reading
// it there produces a field that is uniform rather than obviously wrong, and a
// timeline that silently stacks every dispatch on one encoder. The record
// below is built so the two columns disagree: a parser reading the wrong one
// cannot pass.
func gpuCommandRecord(encoder, pipeline, cumUs uint32) []byte {
	rec := make([]byte, 32)
	binary.LittleEndian.PutUint32(rec[4:8], encoder)
	binary.LittleEndian.PutUint64(rec[8:16], uint64(pipeline)<<32)
	binary.LittleEndian.PutUint64(rec[16:24], uint64(cumUs))
	binary.LittleEndian.PutUint32(rec[24:28], 2)
	return rec
}

func TestDispatchEncoderIndexComesFromOffset4(t *testing.T) {
	var data []byte
	want := []int{0, 1, 7, 20}
	for i, enc := range want {
		data = append(data, gpuCommandRecord(uint32(enc), uint32(i), uint32(10*(i+1)))...)
	}
	objects := []any{map[string]any{"NS.data": data}}

	dispatches := extractDispatchInfoWithMap(objects, 0, 32, nil, nil)
	if len(dispatches) != len(want) {
		t.Fatalf("got %d dispatches, want %d", len(dispatches), len(want))
	}
	for i, d := range dispatches {
		if d.EncoderIndex != want[i] {
			t.Errorf("dispatch %d: EncoderIndex = %d, want %d", i, d.EncoderIndex, want[i])
		}
	}
}
