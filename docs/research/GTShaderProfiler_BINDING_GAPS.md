# GTShaderProfiler Binding Gaps

This note records the private Xcode binding surface needed to close the
remaining Xcode parity gaps in gputrace output.

The current module imports `github.com/tmc/apple/private/xcode/gtshaderprofiler`
from `github.com/tmc/apple v0.5.5`. The package exposes the main Xcode profiler
classes, but gputrace only wraps the C-style AGXPS entry points in
`internal/agxps`.

## Useful Bound Bindings

Run `gputrace xcode-bindings --json` to inspect these classes and selectors on
the local Xcode installation. The command only checks runtime availability; it
does not instantiate profiler objects or parse capture data.

- `GTShaderProfilerStreamData` exposes archived stream data entry points and
  properties for `encoderInfoData`, `gpuCommandInfoData`,
  `pipelineStateInfoData`, and `pipelinePerformanceStatistics`.
- `XRGPUAPSDataProcessor` exposes APS/RDE raw and derived counter processing,
  including shader loading, counter loading, timestamp conversion, and raw or
  derived counter buffer accessors.
- `GTMioCounterData` exposes counter metadata: name, index, sample count,
  sample interval, scope, timestamps, and values.
- `GTMioShaderBinaryData` exposes shader binary cost, source mapping, ISA, trace,
  and register-pressure accessors, including
  `LiveRegisterForInstructionAtIndex`.

## Remaining Metric Gaps

- `high_register`: use `GTMioShaderBinaryData.LiveRegisterForInstructionAtIndex`
  after gputrace can map pipeline or shader binary data to the corresponding
  kernel event.
- `occupancy_pct`: use `XRGPUAPSDataProcessor` derived counters instead of
  relying only on streamData plist keys and raw counter fallback.
- `alu_utilization_pct`: use `XRGPUAPSDataProcessor` derived counters for the
  Xcode display value when streamData does not already carry it.
- effective GPU time: prefer APSTimelineData `ReplayerGPUTime` when present.
  Some traces do not archive that key; gputrace currently reports the fallback
  source explicitly.

## Generated Signature Risks

The generated surface is present, but some signatures need a narrow adapter
before gputrace should call them in normal export paths.

- `GTMioCounterData.Values` is generated as `[]objc.ID`; the selector appears
  to expose numeric counter storage. A wrapper should read it as typed numeric
  data using `SampleCount` and `ValueType`.
- `XRGPUAPSDataProcessor.GetBufferAtRDESourceIndexRdeBufferIndexBufferLength`
  and `GetBufferAtUSCIndexBufferLength` model output buffers as Go strings.
  A gputrace adapter should pass explicit byte storage and lengths.
- `XRGPUAPSDataProcessor` raw and derived counter accessors return timestamp and
  count metadata separately from caller-owned buffers. Wrappers should allocate
  buffers, validate returned counts, and name the counter source.

## Implementation Direction

Keep the risky Objective-C calls behind an internal adapter that can be probed
independently from the timeline and pprof exporters. Export paths should keep
reporting metric provenance so missing Xcode-equivalent values are visible in
Perfetto, the web UI, and pprof comments rather than silently appearing as zero.

On this machine, `gputrace xcode-bindings --json` reports all four target
classes and all 42 checked selectors present. The remaining gaps are adapter
work rather than missing Objective-C bindings.
