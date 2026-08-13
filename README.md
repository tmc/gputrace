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
Source inventory counts remain stable across clock selection; separate
projected counts report what was placed on the selected axis.
`gputrace_manifest_arg` exposes every manifest field
as a key/value row, including per-class loss fields added by constrained
exports and fields introduced by newer exporters.
With `--clock wall --include-raw-samples`, `gputrace_profiler_stream` exposes
raw stream aggregates and `gputrace_raw_profiler_sample` exposes GPRWCNTR
record headers and original mach-absolute ticks. These are raw profiler input,
not decoded counter values or GPU encoder intervals. They are never joined to
busy-domain dispatches by comparing their displayed timestamps.
Original-execution timing attached with `--clock live --live-timing` appears in
`gputrace_live_command_buffer`; its run, sidecar digest, and match counts are
also in `gputrace_capture`. Verified `--host-correlation` events appear in
`gputrace_host_signpost` with both artifact digests, clocks, bridge identity,
and declared maximum error.
MLX semantic nodes expose parent identity, and links expose their sidecar link
id and exact target index. `gputrace_semantic_arg` retains arbitrary node
attributes such as dtype and shape as key/value rows; filter `event_kind` to
distinguish untimed declarations from timed target projections.
`gputrace_dispatch` includes Xcode's workload type and view classifications.
`gputrace_dispatch_arg` and `gputrace_encoder_arg` expose every event argument
as key/value rows, including fields also available through typed columns.
Pipeline counter rows that lack a capture-backed encoder identity remain
untimed and appear in `gputrace_unattributed_counter`; arbitrary metric values
remain available through `gputrace_unattributed_counter_arg`. Evidence families
that cannot be placed on the selected clock appear in `gputrace_evidence_gap`.

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
