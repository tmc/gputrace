# Metal Integration Roadmap

**Date:** 2025-11-03
**Status:** Phase 1 Complete (Bridge), Phase 2-4 Planned

## Overview

Complete roadmap for integrating Metal Bridge with gputrace replay engine to enable end-to-end GPU trace replay with performance counter collection and validation.

## Architecture Vision

```
Trace File (.gputrace)
        ↓
ReplayEngine (analysis)
        ↓
Metal Bridge (CGo)
        ↓
GPU Hardware Execution
        ↓
Performance Counters
        ↓
Xcode Counters.csv
```

## Phases

### ✅ Phase 1: Metal Bridge Foundation (COMPLETE)

**Bead:** N/A (completed in this session)
**Status:** ✅ Complete
**Commit:** b1f061d

**Achievements:**
- Complete CGo bridge to Metal API
- Device management and command queues
- Buffer creation and management
- Runtime shader compilation
- Compute pipeline execution
- GPU synchronization
- Test validation (3 tests, all passing)

**Files:**
- `metal_bridge.go` (485 lines)
- `metal_bridge_test.go` (167 lines)
- `docs/METAL_BRIDGE.md` (373 lines)

**Key Capabilities:**
```go
bridge, _ := NewMetalBridge()
buffer, _ := bridge.CreateBuffer(data)
function, _ := bridge.CreateFunction(source, "kernel")
pipeline, _ := bridge.CreatePipeline(function)

cmdBuffer := bridge.CreateCommandBuffer()
encoder := cmdBuffer.CreateComputeEncoder()
encoder.SetPipeline(pipeline)
encoder.SetBuffer(buffer, 0)
encoder.Dispatch(grid..., threadgroup...)
encoder.EndEncoding()

cmdBuffer.Commit()
cmdBuffer.WaitUntilCompleted()
```

### ✅ Phase 2: ReplayEngine Integration (COMPLETE)

**Bead:** gputrace-70
**Status:** ✅ Complete
**Priority:** P1
**Commit:** 299f1cf

**Goal:** Connect Metal Bridge with ReplayEngine for actual GPU execution

**Tasks:**
1. **Buffer Restoration**
   - Read MTLBuffer-* files from trace
   - Create Metal buffers with `bridge.CreateBuffer()`
   - Map buffer addresses to Metal handles
   - Store in `ReplayState.Buffers`

2. **Shader Compilation**
   - Extract Metal source from trace
   - Compile with `bridge.CreateFunction()`
   - Create pipelines with `bridge.CreatePipeline()`
   - Cache compiled pipelines

3. **Command Encoding**
   - Parse MTSP records for commands
   - Encode with Metal Bridge APIs
   - Set pipeline states
   - Bind buffers
   - Dispatch threadgroups

4. **Execution & Validation**
   - Execute command buffers on GPU
   - Wait for completion
   - Read back results
   - Compare with expected outputs

**Integration Points:**
```go
// In replay_state.go
func (rs *ReplayState) RestoreBuffersWithMetal(bridge *MetalBridge) error {
    for addr, data := range rs.BufferData {
        buffer, err := bridge.CreateBuffer(data)
        rs.MetalBuffers[addr] = buffer
    }
}

// In replay.go
func (re *ReplayEngine) ExecuteWithMetal(bridge *MetalBridge) error {
    cmdBuffer := bridge.CreateCommandBuffer()
    encoder := cmdBuffer.CreateComputeEncoder()

    for _, cmd := range re.Commands {
        switch cmd.Type {
        case "dispatch":
            encoder.SetPipeline(pipelines[cmd.PipelineAddr])
            for i, bufAddr := range cmd.BufferBindings {
                encoder.SetBuffer(buffers[bufAddr], i)
            }
            encoder.Dispatch(cmd.Grid..., cmd.Threadgroup...)
        }
    }

    encoder.EndEncoding()
    cmdBuffer.Commit()
    cmdBuffer.WaitUntilCompleted()
}
```

**Achievements:**
- ✅ MetalReplayEngine implemented (replay_metal.go, 435 lines)
- ✅ Buffer restoration with RestoreBuffersToMetal() and address correlation
- ✅ Shader compilation with RestoreFunctionsToMetal() and pipeline creation
- ✅ Command encoding with encodeCommand() supporting compute dispatches
- ✅ GPU execution with ExecuteReplayPlan() and encoder grouping
- ✅ Validation framework with ValidateExecution() and buffer comparison
- ✅ CLI command with replay-metal --validate --verbose flags
- ✅ Comprehensive test suite (5 tests, all passing)
- ✅ Real GPU execution validated (256-element vector addition)

**Acceptance Criteria:**
- ✅ Trace buffers restored to Metal
- ✅ Shaders compiled and pipelines created
- ✅ Commands executed on GPU
- ✅ Results match expected outputs
- ✅ No crashes or memory leaks

### 🔬 Phase 3: Counter Sampling (PLANNED)

**Bead:** gputrace-71
**Status:** Open (Blocked by gputrace-70)
**Priority:** P1
**Depends On:** Phase 2 complete

**Goal:** Collect real GPU performance counters during replay

**Tasks:**
1. **MTLCounterSampleBuffer CGo Wrapper**
   - Extend `metal_bridge.go` with counter APIs
   - Wrap `MTLCounterSet` and `MTLCounterSampleBuffer`
   - Add counter set queries (timestamp, stage_utilization, statistics)
   - Implement `createCounterSampleBuffer()` C function

2. **Counter Buffer Creation**
   - Query available counter sets from device
   - Create sample buffers for each encoder
   - Allocate sufficient samples (2x per encoder for before/after)
   - Configure storage mode and sample count

3. **Sample Insertion**
   - Insert `sampleCounters(at: index)` before encoder start
   - Insert `sampleCounters(at: index+1)` after encoder end
   - Use barriers for synchronization
   - Track sample indices per encoder

4. **Counter Resolution**
   - Call `resolveCounterRange()` after GPU execution
   - Parse binary counter data
   - Extract metric values (timestamps, utilization, etc.)
   - Map to standard metric names

5. **Integration with CounterSampler**
   - Populate `CounterSample` structures with real data
   - Calculate deltas (end - start)
   - Aggregate encoder metrics
   - Compute derived metrics (bandwidth, throughput, etc.)

**New Metal Bridge APIs:**
```go
// In metal_bridge.go (additions)
func (mb *MetalBridge) QueryCounterSets() ([]*MetalCounterSet, error)
func (mb *MetalBridge) CreateCounterSampleBuffer(counterSet string, count int) (*MetalCounterSampleBufferHandle, error)

// In MetalComputeEncoderHandle
func (h *MetalComputeEncoderHandle) SampleCounters(buffer *MetalCounterSampleBufferHandle, index int)

// In MetalCommandBufferHandle
func (h *MetalCommandBufferHandle) ResolveCounterRange(buffer *MetalCounterSampleBufferHandle, start, count int) ([]byte, error)
```

**Acceptance Criteria:**
- ✓ Counter sample buffers created successfully
- ✓ Samples inserted at correct encoder boundaries
- ✓ Counter data resolved after execution
- ✓ Metrics extracted and structured
- ✓ Data matches Metal debugger readings

### 📊 Phase 4: Xcode CSV Export (PLANNED)

**Bead:** gputrace-72
**Status:** Open (Blocked by gputrace-71)
**Priority:** P1
**Depends On:** Phase 3 complete

**Goal:** Export Metal replay counters in Xcode Counters.csv format

**Tasks:**
1. **Counter Name Mapping**
   - Map MTLCounter names to Xcode column names
   - Handle 241 metric columns
   - Create mapping table for all counter sets
   - Document mapping methodology

2. **Data Aggregation**
   - Aggregate samples per encoder
   - Calculate summary statistics (min/max/avg)
   - Apply scaling factors if needed
   - Compute derived metrics

3. **CSV Generation**
   - Use existing `csv_export.go` infrastructure
   - Replace synthetic values with real counter data
   - Maintain exact Xcode column ordering
   - Format numeric values correctly

4. **File Output**
   - Write to `<trace> Counters.csv` format
   - Include proper CSV headers
   - Handle missing/zero counters gracefully
   - Validate CSV structure

5. **Validation**
   - Load original Xcode CSV for comparison
   - Compare Metal replay values vs Xcode
   - Calculate percentage differences
   - Document accuracy metrics
   - Generate validation report

**Enhanced csv_export.go:**
```go
// Enhance existing exporter
func (e *CountersCSVExporter) ExportCountersCSV(w io.Writer, metalData *MetalCounterData) error {
    writer := csv.NewWriter(w)

    // Write header (241 metrics)
    writer.Write(getCountersCSVHeader())

    // Write encoder rows with real Metal data
    for idx, encoder := range metalData.Encoders {
        row := e.generateCounterRow(idx, encoder)
        writer.Write(row)
    }
}

func (e *CountersCSVExporter) generateCounterRow(idx int, encoder *MetalEncoderCounters) []string {
    row := make([]string, 246)

    // Metadata
    row[0] = fmt.Sprintf("%d", idx+1)
    row[1] = fmt.Sprintf("%d", encoder.FunctionIndex)
    row[2] = encoder.CommandBufferLabel
    row[3] = encoder.EncoderLabel
    row[4] = ""

    // Real counter values from Metal
    for i, metricName := range getXcodeMetricNames() {
        metalCounterName := mapXcodeToMetalCounter(metricName)
        if val, exists := encoder.Counters[metalCounterName]; exists {
            row[5+i] = fmt.Sprintf("%.2f", val)
        } else {
            row[5+i] = "0.00"
        }
    }

    return row
}
```

**Acceptance Criteria:**
- ✓ CSV exports with 241 metric columns
- ✓ Real Metal counter data populated
- ✓ Format matches Xcode CSV exactly
- ✓ Validation shows <10% difference vs Xcode
- ✓ File can be imported by spreadsheet tools

## Testing Strategy

### Phase 2 Testing (ReplayEngine Integration)
```bash
# Test basic replay execution
go test -tags metal -run TestReplayWithMetal -v

# Test buffer restoration
go test -tags metal -run TestMetalBufferRestoration -v

# Test shader compilation
go test -tags metal -run TestMetalShaderCompilation -v

# Test full command execution
go test -tags metal -run TestMetalCommandExecution -v
```

### Phase 3 Testing (Counter Sampling)
```bash
# Test counter buffer creation
go test -tags metal -run TestCounterSampleBuffer -v

# Test sample insertion
go test -tags metal -run TestCounterSampling -v

# Test counter resolution
go test -tags metal -run TestCounterResolution -v
```

### Phase 4 Testing (CSV Export)
```bash
# Test CSV generation
go test -run TestCSVExportWithMetalData -v

# Validate against Xcode
./gputrace replay-counters trace.gputrace -o replay_counters.csv
diff -u xcode_counters.csv replay_counters.csv
```

## End-to-End Workflow

Once all phases complete:

```bash
# 1. Capture trace with Xcode (includes Counters.csv)
# Xcode Instruments → GPU Trace → Save

# 2. Replay trace with Metal Bridge
./gputrace replay trace.gputrace --metal --counters -o replay_counters.csv

# 3. Compare replay vs Xcode
./gputrace validate-counters trace.gputrace --xcode xcode_counters.csv --replay replay_counters.csv

# Output:
# Validation Report:
#   Encoders compared: 6
#   Metrics compared: 241
#   Differences:
#     ALU Utilization: 0.5% avg diff
#     Kernel Occupancy: 1.2% avg diff
#     Memory Bandwidth: 2.1% avg diff
#   Overall accuracy: 98.7%
#   Status: PASS (within 5% tolerance)
```

## Success Criteria

### Phase 2 (Integration) - SUCCESS =
- [ ] Trace buffers loaded into Metal
- [ ] Shaders compiled successfully
- [ ] Commands execute without errors
- [ ] GPU produces correct results
- [ ] Memory properly managed (no leaks)

### Phase 3 (Counters) - SUCCESS =
- [ ] Counter buffers created
- [ ] Samples collected at encoder boundaries
- [ ] Counter data resolved
- [ ] Metrics match Metal debugger within 5%
- [ ] All 241 metrics accessible

### Phase 4 (Export) - SUCCESS =
- [ ] CSV generated in Xcode format
- [ ] Real counter data populated
- [ ] Validation shows <10% difference vs Xcode
- [ ] Round-trip test passes (import → export → import)
- [ ] Documentation complete

## Performance Targets

### Replay Overhead
- **Target:** <2x original execution time
- **Buffer creation:** <100ms for 1GB of buffers
- **Shader compilation:** <50ms per shader
- **Counter sampling:** <1μs per sample
- **Overall:** Should complete within seconds for typical traces

### Accuracy Targets
- **Timestamps:** <1% error
- **Utilization metrics:** <5% error
- **Count metrics:** Exact match
- **Bandwidth metrics:** <10% error
- **Overall:** >95% accuracy across all metrics

## Files Created/Modified

### Phase 1 (Complete)
- ✅ `metal_bridge.go` - CGo bridge
- ✅ `metal_bridge_test.go` - Tests
- ✅ `docs/METAL_BRIDGE.md` - Docs

### Phase 2 (Planned)
- `replay_metal.go` - Metal integration layer
- `replay_state.go` - Enhanced buffer/function restoration
- `replay_test.go` - Extended with Metal tests

### Phase 3 (Planned)
- `metal_bridge.go` - Add counter APIs
- `counter_sampling_metal.go` - Metal counter integration
- `counter_sampling_test.go` - Counter tests

### Phase 4 (Planned)
- `csv_export.go` - Enhanced with Metal data
- `counter_validation.go` - Validation utilities
- `cmd/gputrace/cmd/validate_counters.go` - CLI command

## Timeline Estimate

- **Phase 2:** 1-2 days (Core integration)
- **Phase 3:** 2-3 days (Counter APIs + testing)
- **Phase 4:** 1 day (CSV export + validation)
- **Total:** 4-6 days for complete Metal integration

## Risk Mitigation

### Technical Risks
1. **Shader source unavailable** - Mitigation: Use precompiled .metallib files
2. **Counter format changes** - Mitigation: Version detection and adaptation
3. **Memory leaks** - Mitigation: Comprehensive cleanup and instrumentation
4. **Performance overhead** - Mitigation: Profiling and optimization

### Validation Risks
1. **Counter mismatch** - Mitigation: Document known differences, tolerance thresholds
2. **Format differences** - Mitigation: Exact Xcode format matching with tests
3. **Platform variations** - Mitigation: Test on M1/M2/M3/M4 hardware

## References

- [METAL_BRIDGE.md](./METAL_BRIDGE.md) - Metal Bridge implementation
- [REPLAY_ENGINE.md](./REPLAY_ENGINE.md) - Replay engine architecture
- [XCODE_COUNTER_SUPPORT.md](./XCODE_COUNTER_SUPPORT.md) - Counter import/export
- [Apple Metal Performance Counters](https://developer.apple.com/documentation/metal/performance/counters)
- [MTLCounterSampleBuffer](https://developer.apple.com/documentation/metal/mtlcountersamplebuffer)
