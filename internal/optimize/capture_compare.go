package optimize

import (
	"github.com/tmc/gputrace/internal/gpuevent"
)

// CompareReports diffs two capture analyses kernel by kernel. It is a thin
// re-export so the command layer can compare captures without importing
// gpuevent's report internals directly.
func CompareReports(base, variant *gpuevent.Report) *gpuevent.CaptureComparison {
	return gpuevent.CompareCaptures(base, variant)
}
