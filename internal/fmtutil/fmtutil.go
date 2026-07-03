// Package fmtutil provides small formatting helpers shared by internal packages.
package fmtutil

import "fmt"

// RepeatChar returns c repeated n times.
func RepeatChar(c byte, n int) string {
	if n <= 0 {
		return ""
	}
	b := make([]byte, n)
	for i := range b {
		b[i] = c
	}
	return string(b)
}

// TruncateString truncates s to maxLen, adding "..." when it fits.
func TruncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

// TruncateStringPlain truncates s to maxLen without adding an ellipsis.
func TruncateStringPlain(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

// FormatBytes returns bytes formatted with binary units and the given precision.
func FormatBytes(bytes int64, precision int) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)

	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.*f GB", precision, float64(bytes)/GB)
	case bytes >= MB:
		return fmt.Sprintf("%.*f MB", precision, float64(bytes)/MB)
	case bytes >= KB:
		return fmt.Sprintf("%.*f KB", precision, float64(bytes)/KB)
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}
