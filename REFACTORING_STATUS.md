# Package Reorganization Status

## Progress Summary

Successfully reorganized gputrace into clean internal package structure. Core architecture in place, final import/method fixes remaining.

## ✅ Completed

1. **Created internal/ package structure** (9 focused packages)
2. **Moved 80+ files using git mv** (history preserved)
3. **Fixed all package declarations**
4. **Created clean public API** in root gputrace.go
5. **Resolved trace ↔ command cycle** by moving CommandBuffer types to trace
6. **Resolved trace ↔ timing cycle** by moving EncoderTiming to trace
7. **Moved DispatchThreads and ParseDispatchInRegion** to trace package

## 🔧 Remaining Work

### Critical Issue: Methods on Trace

**Problem**: Go doesn't allow defining methods on types from other packages.

**Solution Required**: Move ALL methods on `*trace.Trace` to the trace package.

### Files with Methods on trace.Trace to Move

**internal/buffer/**
- `access.go`: AnalyzeBufferAccess, parseAllBufferBindings, buildBufferAddressMapping, FormatBufferAccessReport
- `timeline.go`: GenerateBufferTimeline, FormatBufferTimelineJSON

**internal/command/**
- `count.go`: ParseDetailedCommandBuffer, CountDispatchInvocations
- `dispatch.go`: CountEncoderDispatches

**internal/analysis/**
- `device.go`: GetDeviceResources
- `insights.go`: GenerateInsights, FormatInsightsReport

**internal/counter/**
- `counter.go`: All methods (Parse PerfCounters, HasPerfCounters already moved)

**internal/export/**
- `pprof.go`, `pprof_enhanced.go`, `pprof_v2.go`, `pprof_source.go`: All ToPprof* methods

**internal/timing/**
- Already has methods via type alias (EncoderTiming)
- Enhanced timing uses Trace as field, not receiver

### Secondary Issues

1. **Undefined types needing imports**:
   - RecordTypeCt, RecordTypeCi (in trace)
   - ShaderMetrics, TimingMetrics (various)
   - ReplayPlan, ReplayCommand (in replay)

2. **Helper functions needing movement**:
   - isPrintable, isPrintableBytes
   - NewTimingMetricsExtractor, NewKDebugParser

## Recommended Approach

### Option 1: Complete Method Migration (Recommended)
Move all Trace methods to trace package files organized by domain:
- `trace/buffer_methods.go` - buffer analysis methods
- `trace/counter_methods.go` - counter methods
- `trace/export_methods.go` - export methods
- etc.

**Pros**: Clean, follows Go conventions, no cycles
**Cons**: Large trace package

### Option 2: Wrapper Functions
Keep methods in domain packages as regular functions taking `*trace.Trace` as first param:
```go
// Before:
func (t *trace.Trace) AnalyzeBufferAccess()

// After:
func AnalyzeBufferAccess(t *trace.Trace)
```

**Pros**: Keeps code in logical packages
**Cons**: Less idiomatic, breaks existing API

### Option 3: Composition Pattern
Use embedding/composition:
```go
type BufferAnalyzer struct {
    *trace.Trace
}
```

**Pros**: Clean separation
**Cons**: Requires wrapper types

## Current Build Status

- ❌ Does not compile
- Main issue: ~40 "cannot define new methods on non-local type" errors
- Once methods are moved: should build cleanly

## Next Steps

1. Choose approach (recommend Option 1)
2. Move methods systematically
3. Run `goimports -w -local github.com/tmc/mlx-go/experiments/gputrace .`
4. Fix any remaining undefined types
5. Test build with `go build ./...`
6. Update cmd files to use internal packages
7. Run test suite

## Package Structure (Final)

```
gputrace/
├── gputrace.go              # Public API (Open, types)
├── cmd/gputrace/            # CLI tool
└── internal/
    ├── trace/               # Core types + ALL Trace methods
    ├── buffer/              # Buffer analysis helpers
    ├── shader/              # Shader analysis helpers
    ├── counter/             # Counter types/helpers
    ├── timing/              # Timing types/helpers
    ├── replay/              # Replay engine
    ├── export/              # Export helpers
    ├── analysis/            # Analysis helpers
    └── command/             # Command types/helpers
```

## Time Estimate

- Moving methods: 2-3 hours
- Testing/fixing: 1-2 hours
- Total: ~4-5 hours of focused work

## Files Modified This Session

- 1310 files changed
- Created: REFACTORING_PLAN.md, this file
- Major reorganization committed as WIP

See REFACTORING_PLAN.md for complete design rationale.
