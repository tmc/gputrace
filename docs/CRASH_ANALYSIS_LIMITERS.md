
# 2026-01-07 Xcode Crash Analysis: Limiter Timeseries

**Investigation Date:** 2026-01-07
**Trigger:** Xcode `26.2 (24553)` Crash in `GTShaderProfiler`

## Crash Context
A user-reported crash in `GTShaderProfiler` confirms that "Limiters" (e.g., Compute Shader Launch, L1 Cache) are processed as specific **timeseries** data structures within the APS pipeline.

### Stack Trace Analysis
```text
Thread 28 Crashed:
0   GTShaderProfiler  agxps_timeseries_get_length + 4
1   GTShaderProfiler  APSTraceData::CalculateRDELimiters(eAPSProfilingType) + 2696
2   GTShaderProfiler  APSTraceDataHelper::ProcessProfilingTimelingData(...)
```
- **`CalculateRDELimiters`**: Explicitly named function confirms that "Limiters" are a first-class concept in the profiler's data model, likely used to calculate "Relative Difference Error" (RDE) or similar metrics.
- **`agxps_timeseries_get_length`**: The crash occurred here because `x0` (the timeseries object pointer) was garbage (`0x...0A0A`). This implies the profiler expected a valid timeseries object for a Limiter but found uninitialized memory or a bad pointer.

## Implications for Reverse Engineering
1. **Limiters are Timeseries**: The "Limiter" float values discovered in `.gpuprofiler_raw` files are part of a larger "timeseries" structure, likely with headers defining length and type.
2. **0x4E Records**: The `0x4E` record type likely corresponds to these timeseries or their containers.
3. **Data Integrity**: Generating valid `.gpuprofiler_raw` files requires ensuring that all implicit timeseries pointers (like those for Limiters) are valid, even if the data segment is empty (length 0).
