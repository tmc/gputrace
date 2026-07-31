package admit

import (
	"fmt"
	"io"
)

// Status marks are plain words rather than symbols so a failing gate reads the
// same in a log, a diff, and a terminal without color.
const (
	markPass    = "PASS"
	markFail    = "FAIL"
	markBlocked = "UNKNOWN"
)

func (c Criterion) mark() string {
	switch {
	case c.Pass:
		return markPass
	case c.Blocked:
		return markBlocked
	default:
		return markFail
	}
}

// WriteReport renders one line per criterion and a verdict. A blocked
// criterion is reported as UNKNOWN and still withholds admission, since the
// question it asks is the one that would have supported the claim.
func WriteReport(w io.Writer, result Result) error {
	if _, err := fmt.Fprintf(w, "Raw:      %s\nProfiled: %s\n\n", result.RawPath, result.ProfiledPath); err != nil {
		return err
	}
	for _, c := range result.Criteria {
		if _, err := fmt.Fprintf(w, "%-8s %-34s %s\n", c.mark(), c.Name, c.Detail); err != nil {
			return err
		}
	}
	verdict := "NOT ADMITTED"
	if result.Admitted() {
		verdict = "ADMITTED"
	}
	if _, err := fmt.Fprintf(w, "\n%s\n", verdict); err != nil {
		return err
	}
	if !result.Admitted() {
		if _, err := fmt.Fprint(w, "This export does not support a measured-timing claim about the raw capture.\n"); err != nil {
			return err
		}
	}
	return nil
}
