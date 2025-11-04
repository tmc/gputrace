package timing

import "strings"

// repeatStr repeats a string n times.
func repeatStr(s string, n int) string {
	return strings.Repeat(s, n)
}

// repeatChar repeats a character n times.
func repeatChar(c byte, n int) string {
	if n <= 0 {
		return ""
	}
	b := make([]byte, n)
	for i := range b {
		b[i] = c
	}
	return string(b)
}

// truncateString truncates a string to maxLen, adding "..." if truncated.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
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
