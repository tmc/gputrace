# Headless Metal Profiling

`gputrace headless-profile` can collect a non-GUI Metal System Trace, export
Metal GPU interval rows with `xctrace`, and write target-attributed interval
rows as JSON. This default path uses public command-line tooling.

Native `.gpuprofiler_raw/streamData` output is an explicit opt-in compatibility
path. Pass `--encode-streamdata` only when you need the existing gputrace
streamData readers or Xcode-like profiler payload shape; that path uses Xcode's
local private `GPUToolsReplay.framework` classes.

The reliable CLI path for long-running Metal programs is attach-after-launch:
start the target process, wait until a synchronization file proves the workload
is about to enter the interesting GPU phase, then attach `xctrace` to the real
process name. This avoids malformed traces that can occur when `xctrace`
directly launches a wrapper process and finalizes the trace as the target exits.

## Safety Gate

Before each heavy capture or export, check disk, memory, and stale profiler
processes:

```sh
df -h <output-volume> <system-data-volume>
memory_pressure -Q
ps -axo pid=,comm= | egrep '/(xctrace|gputrace|MTLReplayer|gt_replay_service_probe_helper|xctrace_streamdata_helper)$|GPUToolsReplayService' || true
```

Use an output volume with enough free space for trace bundles. Keep first
captures short.

## Recommended Capture Shape

Use `--attach-launched` when the target needs setup time before the profiled GPU
phase. Use `--attach-after-file` when the target can write a small
synchronization file immediately before the region of interest:

```sh
OUT=<output-volume>/gputrace-results/run-001
TMPROOT=<output-volume>/gputrace-results/tmp/run-001
mkdir -p "$OUT" "$TMPROOT"

TMPDIR="$TMPROOT" TEMP="$TMPROOT" TMP="$TMPROOT" \
gputrace headless-profile \
  --json \
  --attach-launched \
  --attach-after-file "$OUT/ready.json" \
  --attach-wait 120s \
  --out-dir "$OUT" \
  --trace-name capture.trace \
  --process target-process-name \
  --time-limit 10s \
  --timeout 2m \
  --min-out-dir-free-gib 24 \
  --min-memory-free-percent 10 \
  -- /path/to/target --write-ready-file "$OUT/ready.json" \
  > "$OUT/headless-profile.json" \
  2> "$OUT/headless-profile.stderr"
```

For targets that do not support a ready-file hook, omit `--attach-after-file`
and set `--attach-wait` long enough for the process to start.

## Successful Output

A successful timing capture has:

- `headless-profile.json` with `profile.timing_claims_allowed=true`
- `profile/xctrace_metal-gpu-intervals.xml`
- `profile/xctrace-interval-rows.json`
- `profile.toc.exportable=true`
- `profile.interval_rows.rows_matched > 0`

`counter_claims_allowed=false` is expected unless a separate counter pipeline
has proved non-empty, route-attributed counter samples.

## Private Framework Boundary

The default `headless-profile` and `xctrace-profile` path records with `xcrun
xctrace`, exports XML tables with `xcrun xctrace export`, and writes parsed
interval rows to JSON. It does not load Apple private frameworks.

`xctrace-streamdata`, and `headless-profile --encode-streamdata` /
`xctrace-profile --encode-streamdata`, encode exported Metal GPU interval rows
into `.gpuprofiler_raw/streamData` with Xcode's local
`GPUToolsReplay.framework` classes. This is an experimental compatibility path
for local profiler analysis, not a stable Apple API contract. Use it only if you
understand that dependency and need native streamData. The public branch
intentionally does not include private MTLReplayer probe commands, signed
replay-service probes, credentials, machine-local paths, or project-specific
benchmark artifacts.

## Native streamData Opt-In

Use this path only when you explicitly need native
`.gpuprofiler_raw/streamData`. It requires:

- Xcode command-line tools with `xcrun xctrace`.
- `clang`, used to compile the local helper.
- Xcode's local private `GPUToolsReplay.framework`.
- A trace with `metal-gpu-intervals` rows matching `--process`.

To capture and encode native streamData in one command:

```sh
OUT=<output-volume>/gputrace-results/run-001
TMPROOT=<output-volume>/gputrace-results/tmp/run-001
mkdir -p "$OUT" "$TMPROOT"

TMPDIR="$TMPROOT" TEMP="$TMPROOT" TMP="$TMPROOT" \
gputrace headless-profile \
  --json \
  --encode-streamdata \
  --out-dir "$OUT" \
  --trace-name capture.trace \
  --process target-process-name \
  --time-limit 10s \
  --timeout 2m \
  --min-out-dir-free-gib 24 \
  --min-memory-free-percent 10 \
  -- /path/to/target arg1 arg2 \
  > "$OUT/headless-profile.json" \
  2> "$OUT/headless-profile.stderr"
```

For a target that needs setup time before the interesting Metal work, combine
`--encode-streamdata` with the attach-after-launch shape shown above:

```sh
gputrace headless-profile \
  --json \
  --encode-streamdata \
  --attach-launched \
  --attach-after-file "$OUT/ready.json" \
  --attach-wait 120s \
  --out-dir "$OUT" \
  --trace-name capture.trace \
  --process target-process-name \
  --time-limit 10s \
  -- /path/to/target --write-ready-file "$OUT/ready.json"
```

To encode native streamData from an existing `.trace` bundle:

```sh
gputrace xctrace-profile \
  --json \
  --encode-streamdata \
  --trace "$OUT/capture.trace" \
  --process target-process-name \
  --out-dir "$OUT/profile" \
  > "$OUT/xctrace-profile.json"
```

Successful native streamData output has:

- `profile.streamData_requested=true`
- `profile.streamData.rows_encoded > 0`
- `profile.streamData.timing_usable=true`
- `profile.streamData.streamData.present=true`
- `profile/xctrace-profile.gpuprofiler_raw/streamData`

If the JSON shows matching interval rows but streamData is not usable, inspect
`profile.streamData.helper.stderr_preview`,
`profile.streamData.helper.stdout_preview`, and the helper directory:
`profile/xctrace-streamdata-helper/`.

## Diagnosing Export Failures

`xctrace-profile` exports the trace table of contents before exporting Metal GPU
tables. If even TOC export fails, the trace is not exportable by `xctrace`:

```sh
gputrace xctrace-profile \
  --json \
  --trace "$OUT/capture.trace" \
  --process target-process-name \
  --out-dir "$OUT/profile" \
  > "$OUT/xctrace-profile.json"
```

For malformed traces, the JSON reports:

```json
{
  "profile": {
    "toc": {
      "exportable": false
    },
    "reason": "xctrace TOC export failed; trace is not exportable by xctrace: ..."
  }
}
```

If `toc.exportable=true` but `timing_claims_allowed=false`, inspect
`profile.toc.schemas`, table row counts, and process filters. The trace may be
valid but missing the requested process rows or the expected Metal interval
schema.

## Converting Interval Rows

Projects can consume `profile/xctrace-interval-rows.json` directly, consume
`profile/xctrace_metal-gpu-intervals.xml`, or convert either artifact with their
own route-label joiner. The rows contain process, label, command-buffer ID,
encoder ID, start time, and duration fields exported by Xcode Instruments.
