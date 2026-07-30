//go:build darwin

package cmd

import "testing"

func TestStableSummaryOCRMatch(t *testing.T) {
	base := summaryOCRMatch{
		Text:       "Show Performance",
		Confidence: 0.98,
		X:          900,
		Y:          700,
		Width:      140,
		Height:     22,
	}
	tests := []struct {
		name string
		edit func(*summaryOCRMatch)
		want bool
	}{
		{name: "same", want: true},
		{name: "normalized whitespace", edit: func(m *summaryOCRMatch) { m.Text = " show  performance " }, want: true},
		{name: "center within tolerance", edit: func(m *summaryOCRMatch) { m.X += 3; m.Y -= 3 }, want: true},
		{name: "wrong text", edit: func(m *summaryOCRMatch) { m.Text = "Show Dependencies" }},
		{name: "center moved", edit: func(m *summaryOCRMatch) { m.X += 5 }},
		{name: "width changed", edit: func(m *summaryOCRMatch) { m.Width += 5 }},
		{name: "height changed", edit: func(m *summaryOCRMatch) { m.Height += 5 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			right := base
			if test.edit != nil {
				test.edit(&right)
			}
			if got := stableSummaryOCRMatch(base, right, 4); got != test.want {
				t.Fatalf("stable = %v, want %v", got, test.want)
			}
		})
	}
}

func TestScreenRectContainsStrictInterior(t *testing.T) {
	rect := screenRect{X: 300, Y: 150, Width: 1000, Height: 700}
	if !rect.contains(900, 700) {
		t.Fatal("interior point rejected")
	}
	for _, point := range [][2]float64{
		{300, 700},
		{1300, 700},
		{900, 150},
		{900, 850},
	} {
		if rect.contains(point[0], point[1]) {
			t.Fatalf("edge point accepted: %v", point)
		}
	}
}

func TestNormalizeOCRTextRequiresExactPhrase(t *testing.T) {
	if got := normalizeOCRText(" Show   Performance "); got != "show performance" {
		t.Fatalf("normalized text = %q", got)
	}
	if got := normalizeOCRText("Show Performance Now"); got == "show performance" {
		t.Fatalf("substring normalized as exact: %q", got)
	}
}
