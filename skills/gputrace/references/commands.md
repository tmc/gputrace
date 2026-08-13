# Command guide

Use this reference after identifying the trace question. Confirm flags against
`gputrace <command> --help` because the CLI may have evolved.

## Input and timing model

`gputrace` accepts `.gputrace` bundles. Some profiling commands also accept a
`.gpuprofiler_raw` directory. Profiled or `-perfdata.gputrace` bundles contain
`streamData`, which enables dispatch-level replay timing.

Timing evidence has this order:

1. profiler `streamData`, including `APSTimelineData` replay time and related
   command-buffer, encoder, and dispatch offsets;
2. capture timing derived from available kdebug or signpost data;
3. synthetic timing for visualization.

The latter two are approximate. Hardware counter files are annotations unless
they can be correlated through a supported timing source.

## Overview and structure

```bash
gputrace stats trace.gputrace
gputrace stats trace.gputrace --json
gputrace kernels trace.gputrace --stats
gputrace kernels trace.gputrace --filter gemm
gputrace command-buffers trace.gputrace --detailed
gputrace encoders trace.gputrace
gputrace api-calls trace.gputrace
```

Use `dump` only when focused commands do not expose the required records; raw
output can be large.

## Profiling and shaders

```bash
gputrace profiler trace.gputrace --kernels
gputrace profiler trace.gputrace --limiters
gputrace timing trace.gputrace
gputrace timing trace.gputrace \
  --json ~/tmp/gputrace-task/timing.json \
  --csv ~/tmp/gputrace-task/timing.csv
gputrace shaders trace.gputrace --all
gputrace shaders trace.gputrace --format json
gputrace insights trace.gputrace --min-level high
```

`--estimate` on `shaders` exposes estimated fields. Keep estimates labeled and
do not present them as source-backed measurements.

## pprof and timelines

```bash
gputrace pprof trace.gputrace -o ~/tmp/gputrace-task/trace.pprof
go tool pprof -top ~/tmp/gputrace-task/trace.pprof

gputrace timeline trace.gputrace --format text
gputrace timeline trace.gputrace --format perfetto \
  -o ~/tmp/gputrace-task/timeline.pftrace \
  --sql-out ~/tmp/gputrace-task/gputrace.sql
gputrace timeline trace.gputrace --format perfetto --open --remote-ui \
  --kernel rmsbfloat16 --kernel-occurrence 0
gputrace timeline trace.gputrace --format html \
  -o ~/tmp/gputrace-task/timeline.html
```

Native Perfetto output keeps cumulative GPU-busy and command-buffer wall clocks
separate. Per-encoder APS cycle and cost aggregates are event details, not
sampled counter tracks, until their clock is joined. `--sidecar` requires exact
trace identity and explicit occurrence links; an MLX runtime receipt alone is
not attachable. Use `--sql-out` for the stable capture, dispatch, pipeline,
semantic-node, semantic-link, counter, and unmatched views.
Self-hosted `--ui-dir` mode requires `index.html` and `perfetto-ui.json` with
schema `gputrace.perfetto-ui/v1` and a non-empty upstream revision.

## Buffer analysis

```bash
gputrace buffers trace.gputrace --sort size
gputrace buffers trace.gputrace --resources --format json
gputrace buffers trace.gputrace --bindings --min-size 1MB
gputrace buffer-access trace.gputrace --verbose
gputrace buffer-timeline trace.gputrace --format summary
gputrace buffer-timeline trace.gputrace --format chrome \
  -o ~/tmp/gputrace-task/buffers.json
```

Use `buffers --inspect NAME` only after choosing a specific captured buffer.
Buffer contents may contain user data; do not reproduce more than the task
requires.

## Trace comparison

`diff` expects profiler data for dispatch-level alignment.

```bash
gputrace diff baseline.gputrace candidate.gputrace --quick --explain
gputrace diff baseline.gputrace candidate.gputrace \
  --by function,encoder,pipeline --limit 25
gputrace diff baseline.gputrace candidate.gputrace \
  --by dispatch --min-delta-us 30 --limit 50
gputrace diff baseline.gputrace candidate.gputrace \
  --by unmatched --show-unmatched
gputrace diff baseline.gputrace candidate.gputrace \
  --json > ~/tmp/gputrace-task/diff.json
gputrace diff baseline.gputrace candidate.gputrace \
  --md-out ~/tmp/gputrace-task/report.md
gputrace diff baseline.gputrace candidate.gputrace \
  --perfetto-out ~/tmp/gputrace-task/diff-perfetto.json
gputrace diff baseline.gputrace candidate.gputrace \
  --allow-cross-environment --json > ~/tmp/gputrace-task/descriptive.json
```

For benchmark directories:

```bash
gputrace diff --bench-dir ~/bench-traces --quick
gputrace diff --bench-dir ~/bench-traces \
  --left baseline.gputrace --right candidate.gputrace
```

Prefer explicit paths for reproducible work. Review total time, top function and
encoder deltas, dispatch outliers, spike windows, unnamed work, and
matched/unmatched counts together.

`diff` fails closed when exact environment gates differ or are unavailable.
Use `--allow-cross-environment` only for descriptive deltas; the report remains
labeled `cross-environment, not causally attributable`.

`brief` produces a compact JSON or Markdown comparison payload:

```bash
gputrace brief baseline.gputrace candidate.gputrace \
  --format md --label-a baseline --label-b candidate --token-budget 20
```

## Xcode profiling on macOS

Xcode automation uses macOS Accessibility APIs and may also require Screen
Recording permission.

```bash
gputrace xcode-profile check-permissions
gputrace xcode-profile open trace.gputrace
gputrace xcode-profile run trace.gputrace \
  -o ~/tmp/gputrace-task/trace-perfdata.gputrace
```

Inspect `gputrace xcode-profile --help` and subcommand help before automation.
Use `--no-prompt` for noninteractive checks. Do not override locks with
`--force` until confirming that no active profiling operation owns them.

Replay completion does not imply that Performance data has finished loading.
Treat replay, Performance-view loading, and export as separate phases. During
those phases:

- leave the owning process and Xcode window undisturbed;
- inspect the lock owner, process state, export sheet, destination, and file
  growth before declaring a hang;
- use an event-driven or bounded stable wait instead of repeatedly restarting;
- preserve replay results and repair export-sheet interaction before replaying.

Require the save sheet to expose the exact absolute destination; a displayed
basename such as `tmp` cannot distinguish `/private/tmp` from `~/tmp`. After
Save, require the requested output path to exist.

With multiple untitled GPU-debugger workspaces, bind actions to the requested
source trace rather than any generic Performance control. Verify the exported
UUID against the source trace.

Classify payload completeness independently from profiler identity:

- a full bundle contains capture data, raw resource payloads, and profiler
  `streamData`;
- a profiler-only bundle can support aggregate timing but not full structural
  or threadgroup comparisons.

Preserve incomplete exports for diagnosis, report their usable evidence, and
do not present them as self-contained.

## Installation and repository development

Install the public command:

```bash
go install github.com/tmc/gputrace/cmd/gputrace@latest
gputrace version
```

Inside the repository:

```bash
go run ./cmd/gputrace --help
go install ./cmd/gputrace
go test ./...
go vet ./...
```

On macOS, `make reinstall` installs the bundled application flow when that is
needed for permissions or Xcode automation.
