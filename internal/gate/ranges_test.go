package gate

import "testing"

func TestParseTokenRange(t *testing.T) {
	tests := []struct {
		in      string
		want    TokenRange
		wantErr bool
	}{
		{in: "1:2", want: TokenRange{Lo: 1, Hi: 2}},
		{in: "0:16", want: TokenRange{Lo: 0, Hi: 16}},
		{in: "2:2", wantErr: true},
		{in: "3:1", wantErr: true},
		{in: "12", wantErr: true},
		{in: "a:b", wantErr: true},
	}
	for _, tt := range tests {
		got, err := ParseTokenRange(tt.in)
		if (err != nil) != tt.wantErr {
			t.Errorf("ParseTokenRange(%q) err = %v, wantErr %v", tt.in, err, tt.wantErr)
			continue
		}
		if !tt.wantErr && got != tt.want {
			t.Errorf("ParseTokenRange(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestScoreRangeTrend(t *testing.T) {
	arms := func(counts ...int) []RangeArm {
		var out []RangeArm
		for i, c := range counts {
			out = append(out, RangeArm{
				Range:        TokenRange{Lo: 1, Hi: 2 + i},
				MatchedCount: c,
			})
		}
		return out
	}
	tests := []struct {
		name   string
		counts []int
		slack  int
		want   Verdict
	}{
		{name: "strictly growing passes", counts: []int{2, 3, 4}, slack: 0, want: VerdictPass},
		{name: "flat count fails", counts: []int{3, 3, 4}, slack: 0, want: VerdictFail},
		{name: "shrinking count fails", counts: []int{4, 3, 5}, slack: 0, want: VerdictFail},
		{name: "zero matches not evaluable", counts: []int{0, 3, 4}, slack: 0, want: VerdictNotEvaluable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := &RangesResult{Arms: arms(tt.counts...)}
			if got := scoreRangeTrend(res, "argmax", tt.slack); got != tt.want {
				t.Errorf("scoreRangeTrend(%v) = %v, want %v (notes: %v)", tt.counts, got, tt.want, res.Notes)
			}
		})
	}
}
