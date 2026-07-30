package xcodebindings

// Measured represents a value produced by a framework call where a zero value
// might indicate either a measured zero or an unpopulated/absent result.
type Measured[T any] struct {
	V  T
	OK bool
}

// MeasuredVal creates a Measured value marked as populated.
func MeasuredVal[T any](v T) Measured[T] {
	return Measured[T]{V: v, OK: true}
}

// Unmeasured creates an empty Measured value marked as absent/unpopulated.
func Unmeasured[T any]() Measured[T] {
	return Measured[T]{OK: false}
}
