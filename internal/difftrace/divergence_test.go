package difftrace

import "testing"

func TestAnalyzeEncoderDivergence(t *testing.T) {
	tests := []struct {
		name       string
		a          []EncoderInfo
		b          []EncoderInfo
		threshold  int
		wantFirst  int
		wantSlopeA float64
		wantSlopeB float64
	}{
		{
			name:       "finds first divergent encoder and tail slope",
			a:          encoderInfos(640, 1156, 1150, 1157, 1151, 1256, 1317, 1374, 1269),
			b:          encoderInfos(639, 1156, 1150, 1157, 1151, 1160, 1157, 1150, 1157),
			threshold:  20,
			wantFirst:  5,
			wantSlopeA: 9.6,
			wantSlopeB: -1.6,
		},
		{
			name:       "no divergence",
			a:          encoderInfos(10, 20, 30),
			b:          encoderInfos(11, 19, 31),
			threshold:  20,
			wantFirst:  -1,
			wantSlopeA: 0,
			wantSlopeB: 0,
		},
		{
			name:       "fills missing encoder indexes",
			a:          []EncoderInfo{{Index: 1, DurationUs: 50}},
			b:          []EncoderInfo{{Index: 1, DurationUs: 10}},
			threshold:  20,
			wantFirst:  1,
			wantSlopeA: 0,
			wantSlopeB: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AnalyzeEncoderDivergence(tt.a, tt.b, tt.threshold)
			if got.FirstDivergentIndex != tt.wantFirst {
				t.Fatalf("first divergent index = %d, want %d", got.FirstDivergentIndex, tt.wantFirst)
			}
			if !almostEqual(got.TailSlopeAUsPerEncoder, tt.wantSlopeA) {
				t.Fatalf("slope A = %.3f, want %.3f", got.TailSlopeAUsPerEncoder, tt.wantSlopeA)
			}
			if !almostEqual(got.TailSlopeBUsPerEncoder, tt.wantSlopeB) {
				t.Fatalf("slope B = %.3f, want %.3f", got.TailSlopeBUsPerEncoder, tt.wantSlopeB)
			}
		})
	}
}

func encoderInfos(times ...int) []EncoderInfo {
	out := make([]EncoderInfo, len(times))
	for i, t := range times {
		out[i] = EncoderInfo{Index: i, DurationUs: t}
	}
	return out
}

func almostEqual(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 0.05
}
