package cmd

import (
	"fmt"
	"io"
	"strings"
)

const defaultHumanLimit = 20

func resolveHumanLimit(limit int, all bool) (int, error) {
	if all {
		return -1, nil
	}
	if limit <= 0 {
		return 0, fmt.Errorf("--limit must be greater than zero")
	}
	return limit, nil
}

func limitedCount(total, limit int) int {
	if limit < 0 || total <= limit {
		return total
	}
	return limit
}

func writeLimitedLines(w io.Writer, text string, limit int, noun string) error {
	if text == "" {
		return nil
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	n := limitedCount(len(lines), limit)
	for _, line := range lines[:n] {
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	if n < len(lines) {
		if _, err := fmt.Fprintf(w, "... %d more %s omitted (use --all)\n", len(lines)-n, noun); err != nil {
			return err
		}
	}
	return nil
}
