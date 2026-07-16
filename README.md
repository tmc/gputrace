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

# View text timeline or export Chrome/Perfetto timeline
gputrace timeline trace.gputrace --format perfetto -o trace.json

# Compare two traces
gputrace diff A.gputrace B.gputrace --explain
```

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
| **Capture** | `xcode-profile` | Xcode GPU profiler automation |
| | `xcode-bindings` | Inspect private Xcode GTShaderProfiler bindings |
| | `xcode-parity` | Audit Xcode metric parity for a trace |
| **Utilities** | `mtlb` | Metal Library Binary inspection |
| | `clear-buffers` | Zero out buffers to reduce trace size |
| | `version` | Print build version |

Run `gputrace [command] --help` for details on any command.

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
