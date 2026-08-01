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

Neither the dispatch occupancy gap nor the dispatch ALU utilization gap is
closed. [V] This section previously claimed both were, on the grounds that the
encoder counter fallback carried a value into every kernel event and pprof
sample "with counter-source provenance". The value it carried was zero, and the
zero came from an `EncoderCounterMetrics` that nothing had written to -- not
from `Counters_f_12.raw`, which gputrace does not decode.

[V] Xcode reports ALU Utilization of 1.59, 1.87, 1.58, 2.12, 1.47, 1.39, 2.10,
1.50, 2.03, 2.70, 0.08, 0.02, 1.91, 2.47, 1.91, 2.00, 1.50, 1.78, 1.58, 1.97,
1.69, 3.35 and 0.48 percent for the 23 encoders of
`qwen25-05b-staticmask-warm-tokens2-4-rep1`, in both its CSV export and its live
Counters inspector. gputrace emitted 0.00 for all 23.

A field carrying a value is not parity, and a value nobody compared against
Xcode is not evidence. Treat a gap as closed only when a nonzero value has been
checked against Xcode's for the same encoder.

The exporter gaps are:

- `alu_utilization_pct`: `Derived Counter Sample Data` is present in stream
  data but is not decoded. `Counters_f_12.raw` is named by
  `GPUCounterGraph.plist` as the ALU Utilization file but its record layout is
  not established, so no offset can be read from it.
- `occupancy_pct`: Kernel Occupancy is a sampled hardware counter and is not
  archived. See the occupancy notes in the parity documentation.

- `occupancy_pct`: not archived anywhere in the trace bundle. Xcode's
  Occupancy is a GPU performance counter sampled at capture time; the string
  does not appear anywhere in `.gpuprofiler_raw`. Xcode's separate *max
  theoretical occupancy* is computed by the Metal compiler and is likewise not
  archived - only its inputs (register counts, threadgroup memory) are. A
  static residency model cannot fill the gap either: there is no published
  max-resident-threads-per-core denominator for any Apple family, and on
  Apple9 (M3/M4) registers and threadgroup memory are allocated dynamically
  from L1, so a per-family table is the wrong model rather than merely an
  unmeasured one. Closing this requires counter sampling at capture time.
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
