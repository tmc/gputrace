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

# Export Chrome/Perfetto timeline
gputrace timeline trace.gputrace -o trace.json

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
| **Visualization** | `timeline` | Chrome/Perfetto timeline export |
| | `graph` | Graph visualization |
| | `tree` | Execution tree view |
| | `diff` | Compare two traces |
| | `insights` | Actionable performance insights |
| **Capture** | `capture` | Capture GPU trace from a command |
| | `headless-profile` | Headless Metal System Trace capture and public interval JSON export |
| | `xctrace-profile` | Export Metal tables from a `.trace` bundle |
| | `xcode-profile` | Xcode GPU profiler automation |
| **Utilities** | `serve` | Web server for trace browsing |
| | `mtlb` | Metal Library Binary inspection |
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

The repository includes a small canonical fixture set under `testdata/traces`:

- `01-single-encoder` for basic parsing and diff smoke tests
- `06-six-encoders` for multi-encoder parsing and shader/debug coverage

Some success paths require capabilities that are not shipped in the small in-repo fixtures:

- `profiler` requires traces with `.gpuprofiler_raw`
- `shader-source` requires traces with source attribution data

## Documentation

Detailed format and workflow documentation lives in `docs/`:

- [README.md](./docs/README.md) -- docs index
- [TRACE_DIFF_WORKFLOW.md](./docs/TRACE_DIFF_WORKFLOW.md) -- trace diff workflow and output interpretation
- [HEADLESS_METAL_PROFILE.md](./docs/HEADLESS_METAL_PROFILE.md) -- non-GUI Metal profiling and export workflow
- [STREAMDATA_FORMAT.md](./docs/STREAMDATA_FORMAT.md) -- streamData plist format
- [trace-format.md](./docs/trace-format.md) -- trace format overview

Reverse-engineering notes and implementation status documents live in [`docs/research/`](./docs/research/README.md).

## GPU Timing Methodology

`.gputrace` files do not contain pre-computed timing percentages. Xcode Instruments derives
shader cost by replaying captured GPU workloads with performance counters enabled. This library
extracts timing from profiler streamData (dispatch/kernel duration, execution cost sampling,
and GPRWCNTR encoder profiles) when a `.gpuprofiler_raw` directory is present. For traces
without profiler data, only structural information (kernels, encoders, buffers) is available.

## Developer Convenience

For local macOS reinstall and permission setup:

```bash
make reinstall
```

## License

MIT License. See [LICENSE](LICENSE) for details.
