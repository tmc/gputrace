package timing

import "strings"

// repeatStr repeats a string n times.
func repeatStr(s string, n int) string {
	return strings.Repeat(s, n)
}

// toLowerSimple converts ASCII string to lowercase without allocations.
func toLowerSimple(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		} else {
			b[i] = c
		}
	}
	return string(b)
}

// containsSubstring checks if s contains substr (simple implementation).
func containsSubstring(s, substr string) bool {
	return strings.Contains(s, substr)
}
