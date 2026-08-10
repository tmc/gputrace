// Package exp provides experimental Metal interposing and GPU trace capture facilities.
//
// It compiles and manages a lightweight Objective-C interposing dynamic library (libgputrace_capture.dylib)
// that injects into Metal applications via DYLD_INSERT_LIBRARIES. The interposer hooks device creation,
// command queues, and command buffer dispatches to produce structured JSON trace logs without requiring
// source modification of target applications or heavy Xcode infrastructure.
package exp
