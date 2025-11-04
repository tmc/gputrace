package timing

import (
	"github.com/tmc/mlx-go/experiments/gputrace/internal/trace"
)

// Type aliases for commonly used trace types
type (
	Trace      = trace.Trace
	MTSPRecord = trace.MTSPRecord
)

// Function aliases from trace package
var (
	NewKDebugParser   = trace.NewKDebugParser
	NewSignpostParser = trace.NewSignpostParser
)
