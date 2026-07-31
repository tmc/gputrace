package admit

import (
	"strings"
	"testing"
)

func TestAdmitted(t *testing.T) {
	tests := []struct {
		name     string
		criteria []Criterion
		want     bool
	}{
		{
			name:     "all pass",
			criteria: []Criterion{{Pass: true}, {Pass: true}},
			want:     true,
		},
		{
			name:     "one fails",
			criteria: []Criterion{{Pass: true}, {Pass: false}},
			want:     false,
		},
		// The case the gate exists for: a check that could not run is not
		// a check that passed.
		{
			name:     "one blocked",
			criteria: []Criterion{{Pass: true}, {Blocked: true}},
			want:     false,
		},
		// Nothing was asked, so nothing is supported.
		{
			name:     "no criteria",
			criteria: nil,
			want:     false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := Result{Criteria: tt.criteria}
			if got := r.Admitted(); got != tt.want {
				t.Errorf("Admitted() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCriterionMark(t *testing.T) {
	tests := []struct {
		name string
		c    Criterion
		want string
	}{
		{"pass", Criterion{Pass: true}, markPass},
		{"fail", Criterion{}, markFail},
		{"blocked", Criterion{Blocked: true}, markBlocked},
		// A blocked criterion is never also a pass; passing wins only
		// because nothing sets both.
		{"pass beats blocked", Criterion{Pass: true, Blocked: true}, markPass},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.c.mark(); got != tt.want {
				t.Errorf("mark() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWriteReportRejected(t *testing.T) {
	result := Result{
		RawPath:      "raw.gputrace",
		ProfiledPath: "profiled.gputrace",
		Criteria: []Criterion{
			{Name: "exported UUID matches raw", Pass: true, Detail: "ABC"},
			{Name: "timing provenance is measured", Detail: "synthetic"},
		},
	}
	var b strings.Builder
	if err := WriteReport(&b, result); err != nil {
		t.Fatal(err)
	}
	out := b.String()

	if !strings.Contains(out, "NOT ADMITTED") {
		t.Errorf("verdict missing:\n%s", out)
	}
	if !strings.Contains(out, "does not support a measured-timing claim") {
		t.Errorf("rejection does not say what it withholds:\n%s", out)
	}
	if !strings.Contains(out, markFail) || !strings.Contains(out, markPass) {
		t.Errorf("per-criterion marks missing:\n%s", out)
	}
}

func TestWriteReportAdmitted(t *testing.T) {
	result := Result{
		Criteria: []Criterion{{Name: "only one", Pass: true, Detail: "fine"}},
	}
	var b strings.Builder
	if err := WriteReport(&b, result); err != nil {
		t.Fatal(err)
	}
	out := b.String()

	if !strings.Contains(out, "ADMITTED") || strings.Contains(out, "NOT ADMITTED") {
		t.Errorf("want an admitted verdict:\n%s", out)
	}
	if strings.Contains(out, "does not support") {
		t.Errorf("admitted report carries a rejection note:\n%s", out)
	}
}

// TestCheckMissingTracesReportsEveryCriterion pins that one unreadable input
// does not abort the run: a partial answer is more useful than none, and the
// verdict is withheld either way.
func TestCheckMissingTracesReportsEveryCriterion(t *testing.T) {
	result := Check(t.TempDir(), t.TempDir())
	if len(result.Criteria) != 5 {
		t.Errorf("got %d criteria, want 5: %+v", len(result.Criteria), result.Criteria)
	}
	if result.Admitted() {
		t.Error("two empty directories were admitted")
	}
	for _, c := range result.Criteria {
		if c.Detail == "" {
			t.Errorf("criterion %q gives no reason", c.Name)
		}
	}
}
