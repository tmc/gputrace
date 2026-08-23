//go:build darwin

package gpuevent

// metalAvailable reports whether the Metal capture stack is usable.
// On darwin the interposer and replay stack ship with gputrace itself,
// so availability follows the platform rather than a probe.
func metalAvailable() bool { return true }
