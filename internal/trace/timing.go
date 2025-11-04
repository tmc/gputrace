package trace

// EncoderTiming represents GPU timing information for a compute encoder.
// This is a core type used throughout the system for representing timing data.
type EncoderTiming struct {
	Label          string
	KernelName     string  // Added for compatibility
	StartTimestamp uint64  // Mach absolute time
	EndTimestamp   uint64  // Mach absolute time
	DurationNs     uint64  // Duration in nanoseconds
	DurationMs     float64 // Duration in milliseconds
	Percentage     float32 // Percentage of total GPU time
}
