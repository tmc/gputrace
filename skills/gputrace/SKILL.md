---
name: gputrace
description: Inspect, profile, compare, and export Apple Metal GPU trace files with the gputrace CLI. Use for .gputrace or .gpuprofiler_raw inputs, GPU timing or shader analysis, command-buffer and buffer investigation, pprof or Perfetto export, performance-regression comparisons, and Xcode GPU-profiler automation.
---

# GPUTrace

Use `gputrace` to extract evidence from Apple Metal traces. Preserve the input
trace, record the exact command used, and distinguish measured profiler timing
from approximate fallback timing.

## Start safely

1. Identify the trace paths and the question to answer.
2. Run `gputrace version` and `gputrace <command> --help`. If working in the
   gputrace repository and no installed binary is suitable, use
   `go run ./cmd/gputrace` or install with `go install ./cmd/gputrace`.
3. Inspect with `stats` before selecting a deeper workflow:

   ```bash
   gputrace stats trace.gputrace
   gputrace stats trace.gputrace --json
   ```

4. Write generated reports to a task-specific directory under `~/tmp/`.
5. Treat trace bundles as read-only. Do not run `clear-buffers` unless the user
   explicitly asks to create a reduced copy.

## Choose a workflow

- For a quick inventory, use `stats`, then `kernels`, `command-buffers`, or
  `buffers`.
- For measured GPU cost, prefer a profiled trace and use `profiler`, `timing`,
  `shaders`, or `pprof`.
- For ordering and concurrency, use `timeline`, `tree`, `graph`, or
  `command-buffers`.
- For memory questions, use `buffers`, `buffer-access`, and `buffer-timeline`.
- For a regression, use `diff` on two comparable profiled traces.
- For Xcode replay and export on macOS, inspect permissions first and use
  `xcode-profile`.

Read [references/commands.md](references/commands.md) for command selection,
examples, and output guidance.

## Analyze a single trace

Begin broad, then narrow:

```bash
gputrace stats trace.gputrace
gputrace profiler trace.gputrace --kernels
gputrace insights trace.gputrace
```

Use JSON when another tool or agent will consume the result. Prefer focused
queries over dumping the entire capture. For example, filter `kernels` by name
or use the buffer inspection flags only after identifying a relevant buffer.

## Compare two traces

Confirm that the traces represent comparable workloads, capture modes, devices,
and run conditions. Put the baseline on the left and candidate on the right.

```bash
gputrace diff baseline.gputrace candidate.gputrace --quick --explain
gputrace diff baseline.gputrace candidate.gputrace \
  --by function,encoder --limit 25
gputrace diff baseline.gputrace candidate.gputrace \
  --by dispatch --min-delta-us 30 --limit 50
```

Use explicit `--left` and `--right` paths when auto-discovery could be
ambiguous. Use `--json`, `--csv`, or `--md-out` for durable results. Examine
unmatched dispatches and unnamed work before attributing a delta to a kernel.

## Report evidence honestly

State:

- the input trace paths and exact commands;
- whether profiler `streamData` was present;
- the reported timing source and whether it was approximate;
- the largest observed contributors or deltas;
- unmatched, unnamed, or missing data that limits attribution.

`APSTimelineData` replay time and related profiler timestamps are measured
timing. Extracted capture fallbacks and synthetic timing are approximate and are
suitable for visualization or triage, not a device-duration or performance
parity claim. Counter/profile annotations alone are not wall-clock timing.

Do not infer causality from a single trace. Phrase `insights` output as
diagnostic hypotheses unless corroborated by the trace structure, counters, or
a controlled comparison.

## Handle failures

- If `profiler` or `diff` lacks dispatch timing, verify that the input contains
  `.gpuprofiler_raw/streamData`; a plain capture may not.
- If names are missing, inspect `kernels`, MTLB sidecars, and debug labels
  before grouping anonymous work.
- If Xcode automation fails, run
  `gputrace xcode-profile check-permissions` and report the missing macOS
  capability rather than bypassing it.
- Treat replay and export as separate phases. Xcode replay may finish quickly
  while Performance-view loading and the export sheet take several minutes.
  Before retrying, inspect the active process, lock, Xcode window, destination,
  and file growth. Do not restart replay or disturb an active export merely
  because no output file exists yet.
- Preserve any completed replay state when export stalls. Diagnose the Export
  button or sheet detection first, and retry replay only when that state cannot
  be recovered.
- Do not trust a basename-only save-sheet location. Require the exact absolute
  destination and verify the requested output exists after Save.
- When Xcode has several untitled GPU-debugger workspaces, bind actions to the
  requested source trace. Revalidate the exported UUID before accepting it.
- Classify the exported payload separately from profiler identity. A
  profiler-only bundle can provide aggregate timing but cannot support full
  structural or threadgroup comparison; preserve it, report the limitation,
  and do not call it a self-contained export.
- Report profiler dispatch span, command-buffer active time, command-buffer
  wall span, and Xcode Effective GPU Time as distinct metrics.
- If a command or flag differs, trust `gputrace <command> --help` from the
  selected binary over this skill.
