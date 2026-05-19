package cmd

import (
	"fmt"
	"strings"
)

// Standard separator line character
const separatorChar = "─"

// TableSeparator returns a thin separator line of the specified width.
func TableSeparator(width int) string {
	return strings.Repeat(separatorChar, width)
}

// FormatCount formats an integer count with comma separators for readability.
// Example: 12345 -> "12,345"
func FormatCount(n int) string {
	if n < 0 {
		return "-" + FormatCount(-n)
	}
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return FormatCount(n/1000) + "," + fmt.Sprintf("%03d", n%1000)
}

// FormatBytes formats a byte count with auto-scaling units.
// Examples: 1024 -> "1.00 KB", 1048576 -> "1.00 MB"
func FormatBytes(bytes uint64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)

	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.2f GB", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%.2f MB", float64(bytes)/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.2f KB", float64(bytes)/float64(KB))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// FormatDuration formats a duration in microseconds with appropriate units.
// Examples: 500 -> "500 us", 1500 -> "1.50 ms", 1500000 -> "1.50 s"
func FormatDuration(us int) string {
	switch {
	case us >= 1000000:
		return fmt.Sprintf("%.2f s", float64(us)/1000000)
	case us >= 1000:
		return fmt.Sprintf("%.2f ms", float64(us)/1000)
	default:
		return fmt.Sprintf("%d us", us)
	}
}

// FormatDurationNs formats a duration in nanoseconds with appropriate units.
func FormatDurationNs(ns uint64) string {
	switch {
	case ns >= 1_000_000_000:
		return fmt.Sprintf("%.2f s", float64(ns)/1_000_000_000)
	case ns >= 1_000_000:
		return fmt.Sprintf("%.2f ms", float64(ns)/1_000_000)
	case ns >= 1_000:
		return fmt.Sprintf("%.2f us", float64(ns)/1_000)
	default:
		return fmt.Sprintf("%d ns", ns)
	}
}

// FormatPercent formats a percentage with one decimal place.
// Example: 24.567 -> "24.5%"
func FormatPercent(pct float64) string {
	return fmt.Sprintf("%.1f%%", pct)
}

// Pluralize returns singular or plural form based on count.
// Example: Pluralize(1, "encoder", "encoders") -> "encoder"
func Pluralize(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}

// FormatSummaryLine formats a summary line like "6 encoders (18.5 ms)".
func FormatSummaryLine(count int, singular, plural string, context string) string {
	word := Pluralize(count, singular, plural)
	if context != "" {
		return fmt.Sprintf("%d %s (%s)", count, word, context)
	}
	return fmt.Sprintf("%d %s", count, word)
}
