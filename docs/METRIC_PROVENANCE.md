# Metric provenance

Every number gputrace publishes, and where it comes from. Audited 2026-07-31
against `qwen25-05b-staticmask-warm-tokens2-4-rep1` and Xcode's own Counters.csv
export of the same capture (247 columns, 23 encoders).

Categories:

- **MEASURED** — read from trace data at an established field or offset.
- **DERIVED** — computed from measured values by a formula stated here.
- **SCAVENGED** — parsed out of another tool's output and passed through.
  Says whose.
- **FABRICATED** — invented. None remain; the ones that existed are listed at
  the bottom with the commit that deleted them.

Confidence markers follow `docs/STREAMDATA_FORMAT.md`: `[V]` verified against
Xcode or a framework type encoding, `[D]` derived by a check that could fail,
`[?]` inferred from a single archive.

## MEASURED

| Metric | Surface | Source |
|---|---|---|
| `duration_us`, `duration_ms`, `cumulative_us` | timeline, timing, profiler, pprof | `[V]` streamData `gpuCommandInfoData`, cumulative offset at `[16:24]`, differenced |
| encoder durations | timeline, timing, profiler | `[V]` streamData `encoderInfoData` cumulative offsets |
| `pipeline_id` | timeline, pprof | `[V]` streamData `pipelineStateInfoData` |
| `pipeline_state` (address) | timeline, pprof | `[V]` streamData `pipelineStateInfoData` |
| `function_name` | all | `[V]` streamData function-name strings, joined by pipeline ID |
| `allocated_registers` (`Temporary register count`) | timeline, pprof, shaders | `[V]` streamData `pipelinePerformanceStatistics` |
| `uniform_registers` | timeline, pprof, shaders | `[V]` same |
| `spilled_bytes` | timeline, pprof, shaders | `[V]` same |
| `threadgroup_memory` | timeline, pprof, shaders | `[V]` same |
| `instruction_count` and the ALU/FP32/FP16/INT32/INT16/branch counts | timeline, pprof, shaders | `[V]` same. These are compiler static counts, not dynamic issue counts; Xcode's `Kernel ALU Instructions` is dynamic and will not match |
| `start_ticks`, `end_ticks` | timeline | `[?]` streamData dispatch records |
| command-buffer active and wall time | profiler, timeline | `[D]` `APSTimelineData` command-buffer timestamps |
| `gprwcntr_sample_count` | timeline, pprof | `[D]` `APSTimelineData` encoder profiles |
| `xcode_cost_pct` / `profiling_cost_pct` | timeline, pprof, profiler | `[D]` `Profiling_f_*.raw` pipeline-ID sampling |
| encoder / dispatch / command-buffer / pipeline counts | all | `[V]` reproduces Xcode's 23 / 958 / 24 / 18 exactly |
| `Kernel Invocations` | Counters.csv | `[?]` `Counters_f_*.raw` offset `0x0064` divided by 27.75. The divisor was fitted to one observation (28416/1024). Not validated against Xcode's 8058 for encoder 0. Treat as unconfirmed |

## DERIVED

| Metric | Formula | Notes |
|---|---|---|
| `Cost %` in `shaders`, `timing`, `profiler` | dispatch duration / total dispatch duration | `[D]` Not Xcode's Execution Cost, which is sampling-based. The commands say so on stdout |
| `simd_groups` | ceil(threadgroups x threads-per-group / 32) | `[V]` 32 is the Apple-documented simdwidth. Needs a full trace for the geometry |
| `sampling_density` | GPRWCNTR samples / dispatch duration | `[D]` |
| `Memory Read BW` / `Memory Write BW` | device-memory bytes / encoder duration | `[D]` Only as good as the byte counters, which are themselves `[?]` |
| `Limiter: Memory` | L1 + last-level-cache + texture-read limiters | `[?]` Summing limiters is not obviously meaningful; the inputs are unvalidated |
| `avg`, `min`, `max`, percentiles in every table | over measured durations | `[V]` |

## SCAVENGED

| Metric | Whose output |
|---|---|
| `CSVEncoderMetrics.KernelOccupancy` and the rest of `csv_import.go` | Xcode's own Counters.csv, when the user supplies one. Recorded, not propagated into published metrics |
| Xcode tab exports under `testdata/` | Xcode |

## Extraction that is neither a field read nor a formula

`counterConfigs` in `internal/counter/counter.go` maps a metric to one
`Counters_f_N.raw` file using `GPUCounterGraph.plist`, then takes float32 words
from that file that fall in the metric's range and averages them per encoder.

`[V]` The file-to-metric mapping is real: the plist names all 455 counters.
`[?]` The value extraction is not a field read. No offset in these records has
been established. A value it produces may be right, and there is currently no
way to tell from inside gputrace. It produces nothing at all on the
profiler-only traces checked so far.

Do not describe anything from this path as measured, and do not reuse
`findAllFloatsInRange` on a file whose counter is unknown.

## Unit corrections from GPUCounterGraph.plist

`GTShaderProfiler.framework/.../GPUCounterGraph.plist` gives every counter a
unit. Check it before publishing a number under an Xcode counter's name.

- `[V]` `Buffer L1 Read Accesses` and `Buffer L1 Write Accesses` are
  "Percentage of Total L1 Read/Write Accesses", not counts. gputrace extracted
  them as counts over a 0-10000 range. Xcode reports 93.48 and 0.12 for encoder
  0 here. Fixed.
- `[V]` `Compute SIMD Groups Inflight per Core` has unit "SIMD Groups", a raw
  count, despite Xcode rendering it with a percent sign. gputrace does not
  publish it; do not publish it as a percentage.
- `[V]` `Kernel ALU Performance` is byte-identical to `Kernel ALU Instructions`
  in all 23 oracle rows. A count under a performance label. Nothing to recover.

## Deleted as FABRICATED

| Metric | What it was | Where |
|---|---|---|
| `occupancy_pct`, `Occupancy` | median of rare float32 words in `Profiling_f_*.raw` | occupancy series, see below |
| `Occupancy Manager` | `occupancy * 0.95`. Xcode's `Occupancy Manager Target` is a real distinct counter reading 86.12 to 100.00 here | occupancy series |
| `Instruction Throughput` track | `(occupancy + alu_util) / 2` | occupancy series |
| `Active Cores` | SIMD groups / 100, clamped to [1, 8] | `179237b` |
| `Shader Launch Limiter` track | allocated registers / 256, as a percent | `179237b` |
| `Limiter: Compute` | a launch limiter plus an ALU utilization | `179237b` |
| synthetic kernel timing | duration from a substring table: "matmul" 5 ms, "rope" 2 ms, else 1 ms | `179237b` |
| `estimateShaderDuration` | total threads x 10 ns, floored at 100 us | `179237b` |
| `generateSyntheticCountersSimple` | a table of constants written into Counters.csv: ALU Utilization 65.00, Kernel Occupancy 75.00, Buffer L1 Miss Rate 10.57 | `73e293c` |
| `CacheHitRate` default | 90.0, published as three miss-rate columns of exactly 10.00 | `73e293c` |
| `ComputeUtilization` | aliased to ALU utilization as a "proxy" | `73e293c` |
| `estimateDurationNs` | cycles / 1.3 GHz | `73e293c` |
| per-record float scan | first word in a plausible range became whichever of ~20 metrics was still empty | `73e293c` |
| ~240 Counters.csv columns of "0.00" | unset columns, indistinguishable from measured zeros | `73e293c` |
| `alu_utilization_pct` zero fallback | zero from an unread struct, labelled "encoder counter fallback" and counted as a closed parity gap | `970ad8a` |

The occupancy series is `7770dcb 78df36a 64437c9 d433a9b 012e98b`.

## Rules

1. A number published under an Xcode counter's name must come from that
   counter. Right name plus invented value is worse than no value: it looks
   checkable and is not.
2. Zero is a claim. Do not write one for a metric that was not read. Blank in a
   CSV, absent from a JSON object, absent from a track.
3. Presence of a field is not parity. Parity is a value compared against
   Xcode's for the same encoder.
4. A caveat does not fix an invented number. Flags do not survive into charts.
5. Check the unit in `GPUCounterGraph.plist` before publishing under an Xcode
   name.
