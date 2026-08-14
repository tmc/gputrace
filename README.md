# gputrace

gputrace parses and analyzes Apple Metal GPU trace files (`.gputrace` bundles).

## Installation

```bash
go install github.com/tmc/gputrace/cmd/gputrace@latest
```

Verify installation:

```bash
gputrace version
```

## Quick Start

```bash
# Show trace statistics (dispatch counts, kernel names, timing)
gputrace stats trace.gputrace

# Full profiler breakdown (timing, pipelines, execution cost)
gputrace profiler trace.gputrace

# Export to pprof format for use with go tool pprof
gputrace pprof trace.gputrace -o trace.pb
go tool pprof -http=:8080 trace.pb

# Export the readable, cumulative-GPU-busy native Perfetto timeline (default)
gputrace timeline trace.gputrace --format perfetto -o trace.pftrace

# Inspect command-buffer scheduling on its separate wall-clock axis
gputrace timeline trace.gputrace --format perfetto --clock wall -o command-buffers.pftrace

# Compare two traces
gputrace diff A.gputrace B.gputrace --explain

# Permit descriptive deltas when exact environment evidence is unavailable
gputrace diff A.gputrace B.gputrace --allow-cross-environment

# Serve a native trace through the hosted Perfetto UI without uploading it
gputrace timeline trace.gputrace --format perfetto --open --remote-ui

# Reproducible mode with a pinned local Perfetto UI build
# The directory must contain index.html and perfetto-ui.json; see below.
gputrace timeline trace.gputrace --format perfetto --open \
  --ui-dir /path/to/perfetto-ui

# Focus one exact occurrence; repeated names require the occurrence flag
gputrace timeline trace.gputrace --format perfetto --open --remote-ui \
  --kernel rmsbfloat16 --kernel-occurrence 0

# Write stable PerfettoSQL views for trace_processor_shell
gputrace timeline trace.gputrace --format perfetto \
  --sql-out gputrace.sql -o trace.pftrace
```

A local Perfetto UI directory must identify the upstream build in
`perfetto-ui.json`:

```json
{"schema":"gputrace.perfetto-ui/v1","revision":"UPSTREAM_REVISION"}
```

Perfetto has one global time axis. `--clock busy` therefore contains encoders,
dispatches, and only counter series whose timestamps are proven in that
domain; `--clock wall` contains APSTimelineData command buffers and wall-clock
profiler events. Per-encoder APS GPU cycles and derived cost remain selectable
encoder details because their counter clock is not joined to the busy clock.
Lossless busy exports also provide one presentation-only dispatch row per
encoder so kernel detail is visible without deep zoom. Native `gpu_slice` rows
remain the accounting source; constrained exports omit the duplicate rows.
When profiler timing is unavailable, capture launch records instead appear as
generic instant events with pipeline identity and dispatch geometry. They do
not enter `gpu_slice`, and CS/debug labels are reported separately as observed
annotations rather than encoder or dispatch instances.
Dispatch details and the `gputrace_pipeline` SQL view include every static
compiler statistic carried by the attributed pipeline record, including
register, spill, instruction-family, threadgroup, and compilation-time facts.
The `metrics_source` argument identifies the backing trace section.
The `gputrace_dispatch` view normalizes timing provenance, attribution,
geometry, source location, and profiler-sample coverage across measured GPU
events and capture-only launch records; unavailable fields remain `NULL`.
GPRWCNTR sample counts include their scaled-window attribution basis and raw
mach-absolute tick bounds; they are not presented as measured dispatch timing.
It also exposes capture command-buffer membership and byte offset as structural
identity. Those fields do not imply that wall-clock command-buffer spans
contain busy-clock dispatch intervals.
Per-launch SIMD groups remain dispatch facts. The `gputrace_function` view
holds one row per function for aggregate duration, total SIMD work, and work
share, avoiding repeated aggregates that produce misleading sums. Profiled
function names retain their `gpuCommandInfoData` attribution; capture-only
names retain their capture attribution. Source-reported aggregate SIMD work is
kept in separate `source_aggregate_*` columns and remains `NULL` when absent.
The `gputrace_encoder` view exposes profiled encoder timing and archive-backed
cycle aggregates, including their derivation, coverage, and unjoined counter
clock status. Capture-only traces do not manufacture encoder rows.
The `gputrace_command_buffer` view exposes measured APSTimelineData wall spans
when present. Capture-only command buffers retain their record index and byte
offset but leave wall timing `NULL`. Encoder detail tracks are ordered by their
first event, so multi-digit encoder names do not sort ahead of earlier work.
gputrace does not invent a mapping between these domains.

See [MLX GPU Trace Rendering in Perfetto](docs/MLX_PERFETTO_RENDERING_SPEC.md)
for the native Perfetto roadmap and proposed MLX semantic view.
`--format perfetto` writes binary protobuf; `--format chrome` retains Chrome
Trace JSON compatibility.
The optional SQL file defines `gputrace_capture`, `gputrace_command_buffer`, `gputrace_dispatch`,
`gputrace_dispatch_arg`, `gputrace_encoder`, `gputrace_encoder_arg`, `gputrace_function`, `gputrace_pipeline`, `gputrace_semantic_node`, `gputrace_semantic_link`,
`gputrace_counter_series`, `gputrace_unattributed_counter`, `gputrace_evidence_gap`, and
`gputrace_unmatched` views over the native trace.
`gputrace_capture` provides typed trace identity, environment, clock, coverage,
timing-summary, and loss-receipt columns. Timing columns keep encoder span,
dispatch span, command-buffer active time, command-buffer wall span, restore
timing, display duration, and optional Xcode Effective GPU Time distinct.
Profiled traces also carry the archive's exact Metal device name, Metal plugin
name, and GPU generation into canonical JSON, native GPU metadata, and typed
SQL columns. GPU generation is nullable so a recorded zero remains distinct
from absence. Capture-only traces report streamData identity as unavailable.
The same projection retains streamData archive version, source trace name,
timestamp, profiling-mode scalars, capture-range scalars, completeness flags,
and blit-call count. Private enum and range meanings remain explicitly
uninterpreted; recorded zero and false values remain distinct from absence.
For each fixed-record streamData table, the manifest and SQL expose byte
length, declared record size, computed record count, trailing remainder, and an
integrity status. This makes truncation and record-layout mismatches visible.
Archive-family inventory separately reports top-level APS, timeline, counter,
shader-profiler, GPU-timeline, and batch-filtered array entry counts. These are
presence counts, not decoded samples; an explicit zero differs from absence.
Source inventory counts remain stable across clock selection; separate
projected counts report what was placed on the selected axis.
When APSTimelineData supplies them, the same view exposes `absolute_time`,
`timebase_numer`, and `timebase_denom` with an explicit wall-domain source and
conversion formula. These fields convert source ticks within the wall domain;
they do not align the wall and cumulative GPU-busy timelines. Missing inputs
remain `NULL` with a `clock_conversion_availability` reason.
The raw `continuous_time` field is retained separately with an availability
receipt and an explicitly unverified clock relationship. gputrace does not use
it to move or align events.
The APSTimelineData `pstate` value is likewise retained as a raw replay
performance-state scalar. Its unit and operating-point mapping are not assumed;
the nullable representation preserves a recorded zero without mistaking it for
missing evidence.
Wall-clock exports retain each APSTimelineData `Restore Timestamps` range on a
separate replay-restore track. These intervals describe replay restore
activity, not GPU execution, and are queryable through the
`gputrace_restore_interval` PerfettoSQL view.
Busy-clock encoder rows retain APSCounterData batch and sample-index identities
when the TraceId tables cover that execution ordinal. The relationship is
positional only: TraceId values are not equated with GRC encoder or kick IDs,
and these identities do not join the counter and busy clocks.
`gputrace_manifest_arg` exposes every manifest field
as a key/value row, including per-class loss fields added by constrained
exports and fields introduced by newer exporters.
With `--clock wall --include-raw-samples`, `gputrace_profiler_stream` exposes
raw stream aggregates and `gputrace_raw_profiler_sample` exposes GPRWCNTR
source record ordinals, original mach-absolute ticks, the seven fixed GRC
fields, exact ShaderProfilerData source and ring-buffer identity, variable
record stride, and hardware-counter column count. Hardware
counter columns remain uninterpreted and are not exported as named metrics.
`gputrace_raw_profiler_sample_arg` retains each payload value by its recorded
zero-based ordinal without assigning a counter name, unit, or meaning. Its
decimal `raw_value_uint64` text preserves the full unsigned range; the
companion `raw_value_int64` column is Perfetto's signed integer projection.
The untimed `gputrace_counter_catalog` view preserves every recorded
APSCounterData pass-column name with its group and column ordinal. Names beyond
the seven fixed GRC fields remain opaque; the catalog supplies no unit, decoded
value series, encoder attribution, or clock mapping.
`gputrace_counter_trace_id` preserves each recorded APSCounterData TraceId,
batch ID, and sample index as untimed source evidence. Only the row ordinal has
a positional relation to encoder execution order; TraceId itself is not a GRC
encoder or kick ID and carries no timing relationship. `trace_id_uint64`
preserves its full unsigned decimal value; `trace_id_int64` is Perfetto's
signed SQL projection.
`gputrace_track_event_arg` retains every argument for low-volume generic events
such as command buffers and profiler streams; its `event_id` is a trace-local
join key, not a persistent source identity. These are raw profiler input,
not decoded counter values or GPU encoder intervals. They are never joined to
busy-domain dispatches by comparing their displayed timestamps.
Original-execution timing attached with `--clock live --live-timing` appears in
`gputrace_live_command_buffer`, including the command buffer's GPU interval and
the separately reported kernel start and duration; its run, sidecar digest,
and match counts are also in `gputrace_capture`. Verified `--host-correlation` events appear in
`gputrace_host_signpost` with both artifact digests, clocks, bridge identity,
and declared maximum error.
MLX semantic nodes expose parent identity, and links expose their sidecar link
id and exact target index. `gputrace_semantic_arg` retains arbitrary node
attributes such as dtype and shape as key/value rows; filter `event_kind` to
distinguish untimed declarations from timed target projections.
`gputrace_dispatch` includes Xcode's workload type and view classifications.
`gputrace_dispatch_arg` and `gputrace_encoder_arg` expose every event argument
as key/value rows, including fields also available through typed columns.
Pipeline identity includes a numeric address, its capture-local scope, and the
archive record that supplied it. `gputrace_pipeline` groups by that identity
and reports total, measured, and recorded-only dispatch counts plus measured
duration. Pipeline addresses and IDs are not stable cross-trace identifiers.
Pipeline counter rows that lack a capture-backed encoder identity remain
untimed and appear in `gputrace_unattributed_counter`; arbitrary metric values
remain available through `gputrace_unattributed_counter_arg`. Evidence families
that cannot be placed on the selected clock appear in `gputrace_evidence_gap`.
Native Perfetto and timeline JSON exports also content-identify regular files
in the resolved profiler directory. `gputrace_raw_profiler_artifact` exposes
the basename, family, optional numeric index, byte size, and SHA-256 as untimed
evidence; `gputrace_capture` carries the deterministic inventory digest and
aggregate size. The exporter does not retain host directory paths or follow
symlinks. Hashing a large profiler directory may add several seconds to export.
For `Timeline_f_*.raw`, `gputrace_raw_profiler_timeline` also exposes the
fixed header's raw identity, counter count, data-section byte offset, entry
count, and profiler-sampling timestamp. That timestamp remains raw and
unaligned; it is not command-buffer time or cumulative GPU-busy time.

`diff` fails closed when workload, device/driver, runtime, capture mode, or
timing-source gates differ or are unavailable. The explicit
`--allow-cross-environment` override labels the result
`cross-environment, not causally attributable`; it does not turn the result
into a controlled regression.

## Commands

| Group | Command | Description |
|-------|---------|-------------|
| **Overview** | `stats` | Comprehensive trace statistics |
| | `api-calls` | API call sequences |
| | `dump` | Raw API call dump |
| **Kernel & Shader** | `shaders` | Shader performance metrics |
| | `kernels` | Kernel functions and pipeline mappings |
| | `shader-source` | Source-level performance attribution |
| **Timing & Profiling** | `timing` | Timing metrics export |
| | `profiler` | GPU profiler data extraction |
| | `pprof` | pprof format export |
| | `correlate` | Correlate timing with hardware metrics |
| **Command Buffers** | `command-buffers` | Command buffer analysis |
| | `encoders` | Compute encoder listing |
| **Buffer Analysis** | `buffers` | Buffer listing and properties |
| | `buffer-access` | Buffer access patterns |
| | `buffer-timeline` | Buffer allocation timeline |
| **Visualization** | `timeline` | Text timeline and Chrome/Perfetto export |
| | `graph` | Graph visualization |
| | `tree` | Execution tree view |
| | `diff` | Compare two traces |
| | `insights` | Actionable performance insights |
| **Capture** | `capture` | Run a Metal workload under the capture interposer |
| | `profile-replay` | Replay a capture under the profiler to add timing |
| | `xcode-profile` | Xcode GPU profiler automation |
| | `xcode-bindings` | Inspect private Xcode GTShaderProfiler bindings |
| | `xcode-parity` | Audit Xcode metric parity for a trace |
| **Utilities** | `mtlb` | Metal Library Binary inspection |
| | `clear-buffers` | Zero out buffers to reduce trace size |
| | `version` | Print build version |

Run `gputrace [command] --help` for details on any command.

## Headless timing

A capture records what a Metal workload did and carries no timing. `profile-replay`
replays it on the GPU under Apple's MTLReplayer with the profiler attached, which
takes seconds and opens no window:

```
gputrace capture -o run.gputrace -- python3 bench.py
gputrace profile-replay run.gputrace          # writes run-perfdata.gputrace
gputrace profiler run-perfdata.gputrace
```

`capture --timing-sidecar timing.jsonl --run-id ID` records command-buffer
intervals from the original execution. If Metal writes resources but no
replayable command stream, capture still fails and the sidecar ends with a
`capture_attempt` record whose status is `timing_only`. That record identifies
the attempted bundle; it does not make the intervals attributable to trace
commands or eligible for host-to-GPU projection.

Replay processes are exclusive. A second invocation normally returns a busy
error; pass `--wait` to queue it and guarantee non-overlapping replay. Go
programs can use `github.com/tmc/gputrace/capture` and
`github.com/tmc/gputrace/profilereplay` for the same operations.

This serializes separate MTLReplayer jobs. It does not force command buffers or
encoders inside a captured workload to execute without overlap. The replay is
headless: MTLReplayer is an agent process, no Xcode window opens, and the
frontmost application does not change.

The default output is self-contained: it preserves the capture and adds the
profiler payload, so Xcode and capture-dependent commands can open it. Use
`--profiler-only` to write the smaller `.gpuprofiler_raw` payload when only
`profiler`, `timing`, `timeline`, or `pprof` is needed. Profiler-only output
cannot be opened by Xcode.

This produces no derived counters. Utilization, limiter and occupancy values are
unavailable on recent GPU generations; see `docs/research/` for why.

Commands that need performance data say so on stderr when a trace lacks it,
and name the command that would add it.

To reproduce Xcode's All Shaders `Cost` column, use its processed pipeline
timing rather than the default SIMD-group share:

```bash
gputrace shaders run-perfdata.gputrace --xcode-cost
```

This runs Xcode's private stream-data processor and can take several seconds.
It follows `DEVELOPER_DIR` or `xcode-select`; `GPUTRACE_XCODE_APP` pins a
different Xcode and causes the command to restart itself with that framework.

## Trace Diff

Compare two profiled traces and explain performance deltas at dispatch, kernel, encoder, and timeline-window levels:

```bash
# Human-readable summary
gputrace diff A.gputrace B.gputrace --explain

# Function and encoder views
gputrace diff A.gputrace B.gputrace --by function,encoder --limit 25

# Dispatch outliers (with source indices)
gputrace diff A.gputrace B.gputrace --by dispatch --min-delta-us 30 --limit 50

# JSON or CSV output
gputrace diff A.gputrace B.gputrace --json > diff.json
gputrace diff A.gputrace B.gputrace --csv --by function > function_deltas.csv

# Auto-discover newest trace pair and run quick triage
gputrace diff --bench-dir /path/to/bench-traces --quick

# Write markdown report
gputrace diff A.gputrace B.gputrace --md-out /tmp/report.md
```

See [docs/TRACE_DIFF_WORKFLOW.md](./docs/TRACE_DIFF_WORKFLOW.md) for the full workflow and sample output.

## Go benchmark output

`bench`, `stats`, `profiler`, and `timing` can write Go benchmark format for direct use
with `benchstat`:

```bash
gputrace profiler trace.gputrace --benchfmt \
  --bench-config runtime=go \
  --bench-config model=Qwen2.5-0.5B > go.txt
benchstat -ignore trace-uuid go.txt python.txt
```

For new integrations, `gputrace bench` emits trace-scoped totals by default and
normalizes only when given `--bench-work` and `--bench-work-unit`. Go programs
can use `github.com/tmc/gputrace/tracebench` to obtain the same sectioned report
and report values directly through `testing.B.ReportMetric`.

```bash
gputrace bench run-perfdata.gputrace \
  --format benchfmt \
  --bench-name BenchmarkDecode \
  --bench-work 32 --bench-work-unit token \
  --bench-config arm=candidate > gpu.bench
benchstat gpu.bench
```

The Go-facing packages are deliberately separate:

- `capture` runs an eligible workload under the Metal capture interposer.
- `profilereplay` adds measured profiler data headlessly and supports queued,
  non-overlapping replay with `Options.Wait`.
- `tracebench` analyzes retained artifacts, writes JSON or benchfmt, and reports
  metrics directly to `testing.B` without parsing CLI prose.

Benchmark suites that do not want the parent module's dependencies can instead
require `github.com/tmc/gputrace/gpubench`. It is a nested, standard-library-only
module that invokes an installed `gputrace` binary and consumes the stable JSON
report:

```go
client := gpubench.Client{}
report, err := client.Report(ctx, tracePath, gpubench.ReportOptions{
	Work: &gpubench.Work{Count: 32, Unit: "token"},
})
if err != nil {
	b.Fatal(err)
}
if err := report.ReportMetrics(b); err != nil {
	b.Fatal(err)
}
```

See [docs/BENCHFMT.md](./docs/BENCHFMT.md) for the unit and provenance mapping.

## Testing

```bash
go test ./...
```

The repository includes small canonical fixtures under `testdata/traces`:

- `01-single-encoder` for basic parsing and diff smoke tests
- `02-two-encoders`, `03-three-encoders`, `04-four-encoders`, and
  `06-six-encoders` for multi-encoder parsing
- `known-invocations-*`, `low-alu-*`, `high-alu-*`, `low-occupancy-*`, and
  `high-occupancy-*` for focused counter and shader-metric scenarios

Some success paths require capabilities that are not shipped in the small in-repo fixtures:

- `profiler` requires profiled traces with `.gpuprofiler_raw/streamData`
- perf-counter validation and CSV import require local `.gpuprofiler_raw`
  counter records or Xcode `Counters.csv` exports
- `shader-source` requires traces with source attribution data

See [docs/TESTING.md](./docs/TESTING.md) for opt-in integration test
environment variables and fixture handling.

## Documentation

Detailed format and workflow documentation lives in `docs/`:

- [README.md](./docs/README.md) -- docs index
- [ENVIRONMENT.md](./docs/ENVIRONMENT.md) -- environment variables
- [BENCHFMT.md](./docs/BENCHFMT.md) -- Go benchmark and benchstat output
- [TESTING.md](./docs/TESTING.md) -- test fixtures and opt-in integration tests
- [TRACE_DIFF_WORKFLOW.md](./docs/TRACE_DIFF_WORKFLOW.md) -- trace diff workflow and output interpretation
- [STREAMDATA_FORMAT.md](./docs/STREAMDATA_FORMAT.md) -- streamData plist format
- [trace-format.md](./docs/trace-format.md) -- trace format overview

Reverse-engineering notes and implementation status documents live in [`docs/research/`](./docs/research/README.md).

## GPU Timing Methodology

`.gputrace` files do not contain pre-computed timing percentages. Xcode Instruments derives
shader cost by replaying captured GPU workloads with performance counters enabled. This library
uses `.gpuprofiler_raw/streamData` for measured timing when profiler data is present:
APSTimelineData `ReplayerGPUTime`, command-buffer timestamps, and encoder/dispatch
cumulative offsets. Execution-cost sampling from `Profiling_f_*.raw` and GPRWCNTR
encoder profiles are reported as counter/profile annotations, not as wall-clock timing
sources. Non-profiled traces may emit approximate `extracted` or `synthetic` timing for
visualization and triage; treat those values as estimates.

## Developer Convenience

For local macOS reinstall and permission setup:

```bash
make reinstall
```

## License

MIT License. See [LICENSE](LICENSE) for details.
