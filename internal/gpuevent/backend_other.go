//go:build !darwin

package gpuevent

// metalAvailable reports whether the Metal capture stack is usable. On
// non-darwin hosts it never is; the darwin variant lives in
// backend_darwin.go and checks for the interposer dylib build path.
func metalAvailable() bool { return false }
