# How Xcode Instruments Derives Shader Cost Percentages

**Investigation Date:** 2025-11-03
**Status:** RESOLVED

## Summary

Xcode Instruments calculates shader cost percentages (e.g., "61.40% steel_gemm, 2.24% block_softmax") by **replaying GPU commands and measuring actual execution time**. Capture-only `.gputrace` records do not contain those percentages; profiler exports can add `.gpuprofiler_raw/streamData` timing data after replay.

## Methodology

Used `fs_usage` to monitor filesystem access when opening and profiling a .gputrace file in Xcode Instruments:

```bash
sudo fs_usage -w -f filesys > /tmp/xcode-profile-access.log &
```

Monitored three stages:
1. Opening the .gputrace file in Instruments
2. Clicking "Replay" button
3. Collecting performance data

## Key Findings

### 1. GPUToolsReplayService Process

When collecting performance data, Xcode spawns `GPUToolsReplayService` which:
- Loads shader source code (hex-named files like `93549414277BA042`, `3858D86BD413F4F5`)
- Loads buffer data (`MTLBuffer-*` files)
- Loads heap data (`MTLHeap-*` files)
- Loads metadata and capture files

### 2. GPU Replay Mechanism

The service **re-executes GPU commands** on actual hardware:
- Reconstructs the Metal command buffers from the trace
- Dispatches the shaders to the GPU
- Measures execution time during replay
- Calculates cost percentages from measured timing

### 3. No MTSP Timing Percentages

Capture-only `.gputrace` records do NOT contain shader timing percentages:
- `store0` file is zlib-compressed but contains all zeros (16384 bytes decompressed)
- No execution timing in MTSP records (verified by parsing)
- No `.gpuprofiler_raw` directory unless the trace was exported with profiler data

When `.gpuprofiler_raw/streamData` is present, current parsing extracts replay-time timing from APSTimelineData and streamData payloads:
- `APSTimelineData ReplayerGPUTime` for Xcode Effective GPU Time
- `APSTimelineData Command Buffer Timestamps` for command-buffer active/wall time
- `encoderInfoData` and `gpuCommandInfoData` cumulative offsets for encoder/dispatch timing

### 4. File Access Pattern

```
GPUToolsReplayService accesses (per replay iteration):
  - /private/tmp/fast-llm-mlx-test.gputrace/93549414277BA042  (shader source)
  - /private/tmp/fast-llm-mlx-test.gputrace/3858D86BD413F4F5  (shader source)
  - /private/tmp/fast-llm-mlx-test.gputrace/MTLBuffer-100-0   (buffer data)
  - /private/tmp/fast-llm-mlx-test.gputrace/MTLBuffer-92-0    (buffer data)
  - ... (continues for all shaders and buffers)
```

## Implications

### For gputrace Library

1. **Cannot extract timing percentages from capture-only MTSP records**
   - The command stream documents submitted work, not shader cost percentages
   - Instruments generates profiler timing during replay and can persist it in `.gpuprofiler_raw/streamData`

2. **Approaches to get real timing:**

   **Option A: Implement Replay (Like Instruments)**
   - Load shaders and buffers from .gputrace
   - Reconstruct Metal command buffers
   - Execute on GPU and measure timing
   - Pros: Accurate, matches Instruments
   - Cons: Complex, requires Metal framework

   **Option B: Parse profiler streamData/APSTimelineData**
   - Capture/export with `.gpuprofiler_raw` enabled
   - Parse `streamData` for APSTimelineData and per-dispatch offsets
   - Pros: Uses persisted replay-time profiler data
   - Cons: Requires a profiled export, not just capture MTSP records

   **Option C: Capture with kdebug/signposts**
   - Use kernel debug events during original capture
   - Parse kdebug codes for GPU timing (see existing `timing_v2.go`)
   - Pros: One-time capture, no replay needed
   - Cons: Requires special capture setup

   **Option D: Replay-time Metal counters**
   - Capture with `.gpuprofiler_raw` enabled
   - Parse counter files for execution statistics, or add replay-time `MTLCounterSampleBuffer` support
   - Pros: Most detailed data
   - Cons: Large/complex data; direct IOReport channel timing is research-only and must fail closed until fixture-backed

### For Current Implementation

Current shader metrics prefer real profiler timing when available and label every timing path:
- `.gpuprofiler_raw/streamData` dispatch durations set `TimingSource: "streamData gpuCommandInfoData dispatch durations"` and `TimingApprox: false`
- APSTimelineData reports `TimingSource` values for `ReplayerGPUTime` or command-buffer active time in stream/timeline summaries
- Capture timestamp heuristics and synthetic kernel/thread estimates remain fallbacks and set `TimingApprox: true`
- Direct IOReport channel timing is not a supported timing source; IOReport-derived timing should fail closed unless it has fixture-backed duration semantics

## Recommendations

1. **Keep Documentation Source-Specific**
   - Clarify that Instruments uses GPU replay for timing
   - Distinguish capture-only MTSP records from profiled `.gpuprofiler_raw/streamData`
   - Document when timing is real (`TimingApprox: false`) versus heuristic/synthetic (`TimingApprox: true`)

2. **Consider Replay Implementation**
   - Could add `gputrace replay` command
   - Would require Metal framework integration
   - Could measure actual GPU execution time

3. **Enhance Existing Timing Extraction**
   - Continue using streamData/APSTimelineData when profiler data is present
   - The `timing_v2.go` path still supports kdebug/signpost parsing
   - Keep IOReport timing fail-closed until a concrete source-to-duration mapping is validated

## Related Files

- `internal/shader/metrics.go` - Shader metrics implementation, including `TimingSource` and `TimingApprox`
- `internal/counter/streamdata.go` - streamData/APSTimelineData timing extraction
- `internal/timing/v2.go` - kdebug/signpost timing extraction
- `internal/timing/store0.go` - Attempts to parse store0 (but it's empty)

## References

- Process: `GPUToolsReplayService` (part of Xcode GPU debugging tools)
- Location: `/Applications/Xcode.app/Contents/Developer/...`
- Trace format: [TRACE_FORMAT.md](./TRACE_FORMAT.md)
- MTSP records: [RECORD_FORMATS.md](./RECORD_FORMATS.md)
