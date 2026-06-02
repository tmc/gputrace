---
name: headless-metal-profile
description: Use when an agent needs to run or explain gputrace headless Metal profiling on macOS, including public xctrace interval JSON capture/export and the explicit opt-in private-framework native streamData path.
metadata:
  short-description: Headless Metal profiling with gputrace
---

# Headless Metal Profile

Use this skill to capture or export Metal GPU timing evidence with `gputrace`
without the Xcode GUI.

Read `docs/HEADLESS_METAL_PROFILE.md` before running commands. It is the source
of truth for flags, expected outputs, and failure cases.

## Tool

Prefer `gputrace` from `PATH`. If missing and you are in the repository, build a
local binary:

```sh
mkdir -p ./bin
go build -o ./bin/gputrace ./cmd/gputrace
GPUTRACE=./bin/gputrace
```

Otherwise:

```sh
GPUTRACE=$(command -v gputrace)
```

## Resource Gate

Before heavy capture or export, record resources and stale profiler processes:

```sh
df -h <output-volume> <system-data-volume>
memory_pressure -Q
ps -axo pid=,comm= | egrep '/(xctrace|gputrace|MTLReplayer|xctrace_streamdata_helper)$|GPUToolsReplayService' || true
```

Do not start a capture if disk or memory is obviously low. Keep first captures
short. Do not delete trace artifacts or caches unless the user explicitly asks.

## Public Timing Path

Use this by default. It records with `xcrun xctrace`, exports Metal tables, and
writes target-attributed interval rows as JSON. It does not load Apple private
frameworks.

```sh
OUT=<output-volume>/gputrace-results/run-001
TMPROOT=<output-volume>/gputrace-results/tmp/run-001
mkdir -p "$OUT" "$TMPROOT"

TMPDIR="$TMPROOT" TEMP="$TMPROOT" TMP="$TMPROOT" \
"$GPUTRACE" headless-profile \
  --json \
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

Expected success:

- `headless-profile.json` has `profile.timing_claims_allowed=true`
- `profile/xctrace_metal-gpu-intervals.xml` exists
- `profile/xctrace-interval-rows.json` exists
- `profile.interval_rows.rows_matched > 0`

## Attach-After-Launch

For targets with setup time, use `--attach-launched` and, when available, a
ready-file emitted immediately before the GPU region of interest:

```sh
"$GPUTRACE" headless-profile \
  --json \
  --attach-launched \
  --attach-after-file "$OUT/ready.json" \
  --attach-wait 120s \
  --out-dir "$OUT" \
  --trace-name capture.trace \
  --process target-process-name \
  --time-limit 10s \
  -- /path/to/target --write-ready-file "$OUT/ready.json"
```

## Existing Trace Export

For an existing `.trace` bundle:

```sh
"$GPUTRACE" xctrace-profile \
  --json \
  --trace "$OUT/capture.trace" \
  --process target-process-name \
  --out-dir "$OUT/profile" \
  > "$OUT/xctrace-profile.json"
```

## Native streamData Opt-In

Only use this when the user explicitly asks for native `streamData`, full
profiler compatibility data, or the private-framework path. This path compiles a
local helper with `clang`, loads Xcode's local private `GPUToolsReplay.framework`,
and writes `.gpuprofiler_raw/streamData`.

Add `--encode-streamdata` to `headless-profile` or `xctrace-profile`:

```sh
"$GPUTRACE" xctrace-profile \
  --json \
  --encode-streamdata \
  --trace "$OUT/capture.trace" \
  --process target-process-name \
  --out-dir "$OUT/profile" \
  > "$OUT/xctrace-profile.json"
```

Expected native streamData success:

- `profile.streamData_requested=true`
- `profile.streamData.rows_encoded > 0`
- `profile.streamData.timing_usable=true`
- `profile.streamData.streamData.present=true`
- `profile/xctrace-profile.gpuprofiler_raw/streamData` exists

If it fails, inspect `profile.streamData.helper.stderr_preview`,
`profile.streamData.helper.stdout_preview`, and
`profile/xctrace-streamdata-helper/`.

## Claim Rules

- Timing claims are allowed only from non-empty target interval rows or usable
  native streamData.
- Counter or RAM-bandwidth claims are not allowed unless non-empty,
  route-attributed counter rows are present.
- Generic labels like `Command Buffer 0:Compute Command N` are valid timing
  rows, but not route attribution by themselves.
