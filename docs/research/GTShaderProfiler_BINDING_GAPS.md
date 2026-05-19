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

## Current Probe Results

On the qwen-native trace, `gputrace xcode-parity --json` loads stream data
through `GTShaderProfilerStreamData.dataFromArchivedDataURL:` and reports:

- 436 GPU command records, 8 pipeline states, and 8 functions.
- APS timeline and counter dictionaries contain `ReplayerGPUTime`, but both
  values are `0`.
- APS timeline and counter dictionaries contain `Binaries` with 734 entries.
- The APS counter dictionary contains `Derived Counter Sample Data` with 16
  groups and an empty `Derived Counters Info Data` dictionary.
- Nested sampling shows each sampled derived-counter group is an array of 5
  arrays. The first sampled group contains NSData payloads sized 40448,
  443520, 230208, 192192, 193600, 80640, 41856, 34944, 35200, and 80896
  bytes across the sampled children.

The dispatch occupancy gap is closed for this trace through the encoder counter
fallback. The dispatch ALU utilization gap is also closed for this trace:
`Counters_f_12.raw` is source-backed, exports zero for all encoder rows, and
gputrace now carries that zero into all kernel events and pprof samples with
counter-source provenance. The remaining exporter gaps are:

- `high_register`: binary blobs are present in stream data, but gputrace does
  not yet have a safe adapter from those blobs to per-kernel live register
  values.
- effective GPU time: `ReplayerGPUTime` is archived as zero for this trace, so
  gputrace keeps reporting the command-buffer active-time fallback.

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
- `GTMioShaderBinaryData` should not be constructed from a `Binaries` NSData
  byte pointer with a nil parent object. An isolated probe produced a non-nil
  object, but `InstructionInfoCount` returned garbage and
  `LiveRegisterForInstructionAtIndex(0)` crashed. The high-register adapter
  needs the correct parent trace object, likely from processed stream data, or a
  separate offline binary decoder.

## Implementation Direction

Keep the risky Objective-C calls behind an internal adapter that can be probed
independently from the timeline and pprof exporters. Export paths should keep
reporting metric provenance so missing Xcode-equivalent values are visible in
Perfetto, the web UI, and pprof comments rather than silently appearing as zero.

On this machine, `gputrace xcode-bindings --json` reports all four target
classes and all 42 checked selectors present. The remaining gaps are adapter
work rather than missing Objective-C bindings.
