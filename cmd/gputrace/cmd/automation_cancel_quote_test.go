package cmd

import "testing"

func TestAppleScriptString(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", `hello`, `"hello"`},
		{"quote", `say "hi"`, `"say \"hi\""`},
		{"backslash", `a\b`, `"a\\b"`},
		{"non-ascii kept raw", "café ✅", `"café ✅"`},
		{"newline becomes return", "a\nb", `"a" & return & "b"`},
		{"empty", ``, `""`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := appleScriptString(tt.in); got != tt.want {
				t.Errorf("appleScriptString(%q) = %s, want %s", tt.in, got, tt.want)
			}
		})
	}
}
