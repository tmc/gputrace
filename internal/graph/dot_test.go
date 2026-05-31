package graph

import "testing"

func TestDotLabelEscapesDynamicText(t *testing.T) {
	got := dotLabel("kernel \"main\"\npath\\buffer")
	want := `kernel \"main\"\npath\\buffer`
	if got != want {
		t.Fatalf("dotLabel() = %q, want %q", got, want)
	}
}

func TestSanitizeIDReplacesGraphvizDelimiters(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "simple_add", want: "simple_add"},
		{in: `shader "main"/0.1`, want: "shader__main__0_1"},
		{in: "", want: "node"},
	}

	for _, tt := range tests {
		if got := sanitizeID(tt.in); got != tt.want {
			t.Fatalf("sanitizeID(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
