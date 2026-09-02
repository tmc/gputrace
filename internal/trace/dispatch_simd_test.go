package trace

import "testing"

func TestDispatchThreadsSIMDGroups(t *testing.T) {
	tests := []struct {
		name           string
		d              DispatchThreads
		wantX          uint64
		wantY          uint64
		wantZ          uint64
		wantSIMDGroups uint64
	}{{
		name:           "one full simd group",
		d:              DispatchThreads{ThreadsX: 32, ThreadsY: 1, ThreadsZ: 1, ThreadsPerGroupX: 32, ThreadsPerGroupY: 1, ThreadsPerGroupZ: 1},
		wantX:          1,
		wantY:          1,
		wantZ:          1,
		wantSIMDGroups: 1,
	}, {
		// A partial threadgroup still runs, so both roundings are up.
		name:           "partial threadgroup and partial simd group",
		d:              DispatchThreads{ThreadsX: 33, ThreadsY: 1, ThreadsZ: 1, ThreadsPerGroupX: 32, ThreadsPerGroupY: 1, ThreadsPerGroupZ: 1},
		wantX:          2,
		wantY:          1,
		wantZ:          1,
		wantSIMDGroups: 2,
	}, {
		name:           "threadgroup smaller than a simd group",
		d:              DispatchThreads{ThreadsX: 8, ThreadsY: 1, ThreadsZ: 1, ThreadsPerGroupX: 8, ThreadsPerGroupY: 1, ThreadsPerGroupZ: 1},
		wantX:          1,
		wantY:          1,
		wantZ:          1,
		wantSIMDGroups: 1,
	}, {
		name: "three dimensions multiply",
		d: DispatchThreads{
			ThreadsX: 64, ThreadsY: 64, ThreadsZ: 2,
			ThreadsPerGroupX: 32, ThreadsPerGroupY: 32, ThreadsPerGroupZ: 1,
		},
		wantX:          2,
		wantY:          2,
		wantZ:          2,
		wantSIMDGroups: 8 * 1024 / 32,
	}, {
		// Metal records 1 for unused dimensions, so a zero threadgroup size
		// means the dispatch was never read; the thread count is unknown.
		name:           "unset threadgroup dimension",
		d:              DispatchThreads{ThreadsX: 1024, ThreadsPerGroupX: 32},
		wantX:          32,
		wantY:          1,
		wantZ:          1,
		wantSIMDGroups: 0,
	}, {
		name:           "zero dispatch",
		d:              DispatchThreads{},
		wantX:          1,
		wantY:          1,
		wantZ:          1,
		wantSIMDGroups: 0,
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			x, y, z := tt.d.Threadgroups()
			if x != tt.wantX || y != tt.wantY || z != tt.wantZ {
				t.Errorf("Threadgroups() = %d,%d,%d, want %d,%d,%d", x, y, z, tt.wantX, tt.wantY, tt.wantZ)
			}
			if got := tt.d.SIMDGroups(); got != tt.wantSIMDGroups {
				t.Errorf("SIMDGroups() = %d, want %d", got, tt.wantSIMDGroups)
			}
		})
	}
}
