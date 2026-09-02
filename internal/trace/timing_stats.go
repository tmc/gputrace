package trace

// TimingStat holds timing information for a kernel.
type TimingStat struct {
	TotalTime   float64 // Total execution time in milliseconds
	AverageTime float64
	MinTime     float64
	MaxTime     float64
}
