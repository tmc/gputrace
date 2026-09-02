# Local Perfetto Viewer Specification

## Status

`gputrace timeline --format perfetto --open` implements the local viewer code
described here. It exports one clock domain, binds to loopback, serves a pinned
local UI or an explicitly selected remote UI, transfers the trace after the
embedding PING/PONG handshake, and shuts down with the command context.
Exact kernel/occurrence focus and explicit initial time ranges are implemented.
A packaged UI and the MLX plugin remain proposed. The same
timeline command without `--open` writes native Perfetto protobuf and populates
native GPU tables; `--format chrome` retains Chrome Trace JSON compatibility.
Automated visual verification is `tools/perfetto-ui-smoke.sh`. It serves a
trace with `--ui-dir`, drives a headless Chromium over the DevTools protocol,
and fails when the pinned UI renders no named tracks. It needs a browser, Node,
and a pinned UI build, so it runs during release qualification rather than in
`go test`. Handler, protocol, routing, and lifecycle behavior are tested
separately and are not treated as visual proof.

The smoke gate proves the trace becomes visible and, for profiled input,
requires the pipeline-first, strict-encoder, and uncertain-association groups
to appear in the pinned UI. It does not assert on every virtualized child row
or on the selection detail panel: those assertions need viewport automation
that this repository has not verified against a pinned revision. The fixture
qualification gate independently proves complete pipeline coverage,
overlap-safe lanes, strict versus uncertain encoder partitioning, numeric
encoder ordering, dispatch identity, and capture-only instants.

The design has two independent deliverables:

1. serve and open a trace in an embedded Perfetto UI;
2. emit native Perfetto GPU packets so the UI's GPU plugins recognize the
   trace as GPU activity.

The viewer can be implemented before the native writer, but it must describe
Chrome JSON as compatibility input until the native writer ships.

The MLX semantic hierarchy, evidence model, track layout, counter policy, and
plugin experience are specified in
[MLX_PERFETTO_RENDERING_SPEC.md](MLX_PERFETTO_RENDERING_SPEC.md).

## Goals

- Open a `.gputrace` in Perfetto with one command.
- Keep trace bytes on the local machine by default.
- Bind the server to loopback and choose an unused port by default.
- Support a pinned, self-hosted Perfetto UI build.
- Retain the hosted Perfetto UI as an explicit lightweight alternative.
- Preserve gputrace's busy-time and wall-time separation.
- Open a selected kernel or time range when the trace provides the required
  timestamps.
- Shut down the server and any temporary files cleanly on interrupt.

## Non-goals

- Do not expose the server on a non-loopback address by default.
- Do not upload trace bytes or enable sharing by default.
- Do not invent a mapping between cumulative GPU-busy offsets and
  command-buffer wall timestamps.
- Do not create syscall, CPU scheduling, frequency, or system-memory packets
  from a Metal capture that does not contain that evidence.
- Do not emit GPU dependency arrows or host-to-GPU correlations without stable
  source and destination identifiers in the capture.

## Command line

The command is:

```text
gputrace timeline TRACE --format perfetto --open
```

Options:

```text
--listen 127.0.0.1:0    listen address; port zero selects an unused port
--serve                 serve without opening a browser
--ui-dir DIR            serve a pinned Perfetto UI build from DIR
--remote-ui             embed https://ui.perfetto.dev instead of a local UI
--clock busy|wall       exported clock domain; default busy
--kernel NAME           focus an exact kernel name; ambiguity is an error
--kernel-occurrence N   zero-based occurrence; required for repeated names
--time-start SECONDS    initial absolute viewport start
--time-end SECONDS      initial absolute viewport end
```

`--open` and `--serve` are mutually exclusive. `--ui-dir` and `--remote-ui`
are mutually exclusive. A packaged UI may later
become the default, but the first implementation should require `--ui-dir` for
self-hosting rather than download mutable assets implicitly.

## Server lifecycle

The command performs these steps:

1. Validate the input trace and selected clock domain.
2. Export the trace to the requested output, or `timeline.pftrace` by default.
3. Listen on `127.0.0.1:0` unless `--listen` selects another loopback address.
4. Serve the host page, trace bytes, and optionally the pinned Perfetto UI.
5. Open the host-page URL for `--open`; leave it unopened for `--serve`.
6. Wait until interrupted or until the server fails.
7. Close the listener when the command context is canceled.

The command prints the exact URL and generated trace path before opening the
browser. A non-loopback `--listen` value requires an explicit warning because
the server provides access to trace data.

## HTTP surface

The server exposes only these routes:

```text
/                         embedding host page
/trace                     generated trace bytes
/ui/                       pinned Perfetto UI, when --ui-dir is used
/healthz                    plain-text readiness response
```

Requirements:

- `/trace` uses `Content-Type: application/octet-stream` and
  `Cache-Control: no-store`.
- Paths below `/ui/` must stay below the configured UI directory after path
  cleaning; traversal is rejected.
- The server must not set `Cross-Origin-Opener-Policy: same-origin`, because
  that breaks the opener/message relationship used by the embedding API.
- No directory listing is exposed.
- The default server has no upload, mutation, or arbitrary-file route.

## Embedding protocol

The host page embeds one of these URLs:

```text
/ui/#!/?mode=embedded
https://ui.perfetto.dev/#!/?mode=embedded
```

The message channel is not buffered. The host therefore sends `PING` at a
bounded interval until it receives `PONG` from the iframe. Only then does it
fetch `/trace` as an `ArrayBuffer` and post the trace.

The following is illustrative browser code. Its message names and fields are
the supported Perfetto embedding API; error handling and UI presentation are
omitted here:

```js
const frame = document.querySelector("iframe");
const timer = setInterval(() => frame.contentWindow.postMessage("PING", "*"), 100);

window.addEventListener("message", async (event) => {
  if (event.source !== frame.contentWindow || event.data !== "PONG") return;
  clearInterval(timer);
  const buffer = await fetch("/trace", {cache: "no-store"}).then(r => r.arrayBuffer());
  frame.contentWindow.postMessage({
    perfetto: {
      buffer,
      title: document.title,
      fileName: "gputrace.pftrace",
      localOnly: true,
    },
  }, "*");
});
```

Posted traces remain in browser memory and are not uploaded by the Perfetto
UI. `localOnly` remains `true`; gputrace does not provide a sharing URL in the
initial implementation.

For a self-hosted UI, host and iframe are same-origin. When using the remote
UI, localhost is in Perfetto's trusted-origin set, so the embedding API does
not require its untrusted-origin consent dialog.

## Initial navigation

The embedding API supports two navigation mechanisms:

- URL parameters use nanoseconds: `visStart`, `visEnd`, `ts`, and `dur`.
- A post-load viewport message uses absolute seconds: `timeStart` and
  `timeEnd`.

The implementation must keep those units separate in its types. `--kernel`
selects only an exact function-name match. If no match exists, or several
matches exist and the caller did not provide an occurrence selector, the
command reports the ambiguity instead of choosing silently.

Startup commands and `pluginArgs` may be added after the basic viewer is
stable. Only commands documented in Perfetto's automation reference should be
used. The initial native-GPU implementation should enable the standard GPU
plugins through trace data, not depend on an undocumented UI command.

## Trace formats

### Compatibility format

The viewer may serve Chrome Trace JSON produced by:

```text
gputrace timeline TRACE --format chrome --clock busy
```

This retains current encoder, dispatch, and counter slices. It must be labeled
`chrome-json` in server status and diagnostic output. Naming the file
`.pftrace` does not make it native Perfetto data.

### Native Perfetto format

`gputrace timeline --format perfetto` writes binary Perfetto protobuf and is
distinct from `--format chrome`. The native writer maps only source-backed
evidence:

| gputrace evidence | Native Perfetto data |
| --- | --- |
| GPU identity | `GpuInfo` with a stable GPU id and available Apple metadata |
| Compute dispatch | `GpuRenderStageEvent` categorized as compute |
| Command encoder | render-stage or queue hierarchy when identity and time exist |
| Measured GPU counter | `GpuCounterDescriptor` and `GpuCounterEvent` |
| Proven queue wait | `event_wait_ids` |
| Timed host submission | track event with GPU correlation extension |
| Metal debug message | GPU log or annotated track event, according to semantics |

The writer does not emit Linux ftrace packets, syscall slices, scheduler
slices, CPU-frequency samples, or process/system memory counters unless a
separate input contains those measurements and a documented clock mapping
permits merging them.

Native packets allow Perfetto's GPU plugins to create the standard GPU group,
GPU counter groups, `gpu_render_stage` slices, and compute-kernel detail panes.
Generic Chrome JSON slices cannot provide that native table shape.

## Clock domains

The current trace format exposes at least two relevant domains:

- cumulative GPU-busy offsets for encoder and dispatch work;
- command-buffer scheduling timestamps for wall time.

The default viewer opens the busy trace because it contains kernel detail.
`--clock wall` opens command-buffer scheduling separately. The server never
places both on one axis without a measured clock snapshot or another verified
mapping. A future two-panel host page may open two independent Perfetto
iframes, one per domain.

## Versioning the UI

Self-hosting is the reproducible mode. The UI directory must contain a complete
Perfetto UI build and a `perfetto-ui.json` manifest recording its upstream
revision:

```json
{"schema":"gputrace.perfetto-ui/v1","revision":"UPSTREAM_REVISION"}
```

The command rejects a missing entry point, missing manifest, unknown schema,
or empty revision. The host page records the revision in its
`gputrace-perfetto-ui` metadata. The
gputrace repository should not commit an unreviewed generated UI tree or fetch
one during normal command execution.

A future packaging step may:

1. pin an upstream Perfetto revision;
2. fetch or build the UI through an explicit maintenance command;
3. record license and revision metadata;
4. package the result outside the normal Go source archive if its size makes
   embedding unsuitable.

The remote mode follows the latest `ui.perfetto.dev` release and is therefore
not reproducible. The command prints that distinction.

## Security and privacy

- Listen on loopback by default.
- Generate an unguessable per-run URL token if non-loopback serving is ever
  supported as more than an expert override.
- Never log trace contents or query parameters containing private labels.
- Use `localOnly: true` and omit `url` and `appStateHash` by default.
- Do not add permissive CORS headers to `/trace` in iframe/postMessage mode.
- Treat UI assets as executable third-party code and pin their revision.
- Escape the trace title and never interpolate it into executable JavaScript.

## Evidence manifest

The generated trace includes, or the server exposes alongside it, a manifest
with:

```text
input trace UUID
input trace path
export format and schema version
selected clock domain
timing source and approximation status
number of command buffers, encoders, and dispatches
native Perfetto packet families emitted
evidence families unavailable
Perfetto UI revision or remote-UI URL
```

The unavailable list is as important as the emitted list. It prevents an empty
CPU, syscall, frequency, or memory view from being mistaken for a measured
zero.

## Implementation slices

### Slice 1: local viewer (implemented)

- Add viewer flags to `gputrace timeline TRACE --format perfetto` with loopback
  serving and clean shutdown.
- Serve current Chrome JSON and label it accurately.
- Implement the PING/PONG handshake and `ArrayBuffer` post.
- Support `--ui-dir`, `--remote-ui`, `--open`, `--serve`, and `--clock`.
- Test routing, path traversal rejection, headers, shutdown, and generated host
  JavaScript.

### Slice 2: native GPU protobuf

- Add a small protobuf writer package with pinned Perfetto message definitions.
- Emit GPU metadata, compute render stages, and measured counters.
- Keep `--format chrome` unchanged.
- Change `--format perfetto` to binary output and add a compatibility note.
- Validate output with `trace_processor_shell` queries against `gpu`,
  `gpu_track`, `gpu_slice`, `gpu_render_stage`, and `gpu_counter_track`.

### Slice 3: focused opening (implemented)

- Add exact kernel selection, occurrence selection, and viewport messages.
- Add deep-link navigation for the selected event or explicit time range.
- Report unmatched and ambiguous selections without silently falling back.

### Slice 4: optional trace merging

- Accept a separate native Perfetto trace only when clock snapshots establish
  a verified mapping.
- Preserve the original system packets rather than reconstructing syscalls,
  scheduling, or memory data from gputrace.

## Acceptance criteria

The local-viewer slice is complete when:

- the server binds only to loopback by default;
- both pinned local UI and explicit remote UI modes open the trace;
- the trace is posted only after `PONG`;
- no trace bytes leave the local server in self-hosted mode;
- interrupt closes the listener; user-selected trace output remains;
- browser automation verifies a representative trace becomes visible
  (`tools/perfetto-ui-smoke.sh`).

The native-writer slice is complete when:

- `--format perfetto` is binary protobuf and `--format chrome` remains JSON;
- `trace_processor_shell` reports no parser errors;
- every profiled dispatch appears once as a compute GPU slice;
- native GPU metadata and counter tables contain only source-backed values;
- busy and wall clocks remain separate;
- missing syscalls, scheduler, frequency, and memory measurements are reported
  as unavailable rather than emitted as zero.

## References

- [Perfetto UI embedding API](https://perfetto.dev/docs/visualization/embedding-api-reference)
- [Deep linking to the Perfetto UI](https://perfetto.dev/docs/visualization/deep-linking-to-perfetto-ui)
- [Perfetto GPU data sources](https://perfetto.dev/docs/data-sources/gpu)
- [Perfetto system-call data source](https://perfetto.dev/docs/data-sources/syscalls)
- [Perfetto CPU scheduling data source](https://perfetto.dev/docs/data-sources/cpu-scheduling)
- [Perfetto memory counters and events](https://perfetto.dev/docs/data-sources/memory-counters)
- [Perfetto track events](https://perfetto.dev/docs/instrumentation/track-events)
- [Current exporter clock-domain design](./research/PERFETTO_TIMELINE_DESIGN.md)
