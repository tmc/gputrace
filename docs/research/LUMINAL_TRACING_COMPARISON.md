# Luminal tracing vs. the gputrace/mlx-go CUPTI approach

Written 2026-08-25 from a read of `~/go/src/github.com/luminal-ai/luminal`
(crates `luminal_tracing`, `luminal_cuda_lite`) and
`~/go/src/github.com/luminal-ai/explicit-tracing-perfetto`. Compared
against `docs/CUPTI_ROADMAP.md` here and the mlx-go design docs
(`docs/design/native-cupti-capture.md`,
`docs/design/pprof-label-gpu-attribution.md` in that repo), including
the 2026-08-25 spike results recorded there.

## What luminal does

Three pieces:

1. **Host spans via the Rust `tracing` ecosystem.**
   `#[tracing::instrument]` throughout the compiler and runtime
   (graph compile, egglog passes, resource planning), composed through
   `tracing-subscriber` layers into a Perfetto `.pftrace`
   (`luminal_tracing`, a thin wrapper over `tracing-perfetto` that also
   re-exports the Perfetto protobuf schema for post-processing).
   `explicit-tracing-perfetto` is a sibling crate for writing
   already-timestamped records onto named timelines through
   `tracing::info!` events.

2. **GPU timing by instrumenting the CUDA graph they build.** When
   TRACE is enabled, the runtime inserts a `cuGraphAddEventRecordNode`
   before every kernel node in the graph it constructs
   (`luminal_cuda_lite/src/kernel/to_host.rs`, graph-build path), then
   reads `cuEventElapsedTime` chains after execution to recover
   per-kernel start/end offsets. Two derived host metrics accompany
   each graph execution (`CudaGraphTiming` in `kernel/cuda_graph.rs`):
   `setup_duration_ns` (host span entry to launch call) and
   `launch_latency_ns` (launch call to first kernel on device).

3. **Post-hoc trace merge.** `record_cuda_perfetto_trace`
   (`runtime.rs`) decodes the finished pftrace, locates each host
   `cuda_graph` span by a UUID debug annotation, and splices per-kernel
   slices in as nested children positioned at
   `span_start + setup + latency + offset`
   (`record_cuda_graph_timings` in `kernel/mod.rs`). One merged file,
   GPU work nested under the host span that caused it.

## The core difference

**Luminal instruments the graph it owns; we observe the device.**
Luminal is the compiler and the runtime, so attribution is by
construction — it knows which HLIR op produced each kernel node before
it runs. gputrace/mlx-go sit outside the runtimes they measure (MLX's
C++ scheduler, arbitrary CUDA binaries), so attribution is inferential:
temporal join, correlation IDs, and the CUPTI graph-node table.
Luminal has no "which op made this kernel?" problem; for us that is the
one rung that needs upstream MLX cooperation.

Costs of the ownership approach that CUPTI does not pay:

- **It measures a modified workload.** Event-record nodes are real
  graph nodes with real dependency edges; the timed graph is not the
  shipped graph.
- **It disables itself exactly when things get interesting.** Their
  `tracing_enabled` requires
  `cublaslt_ops.is_empty() && flashinfer_ops.is_empty()` — any
  cuBLASLt or FlashInfer child graph turns per-kernel timing off
  entirely. The mlx-go spike's top kernel was `cutlass::Kernel2` from
  a library matmul: CUPTI sees it; their mechanism structurally
  cannot.
- **No absolute device timestamps.** Kernel slices are positioned
  relative to the host span via measured offsets — a reconstruction
  that sidesteps clock sync but strains under cross-stream concurrency
  inside a graph. CUPTI records carry real device start/end in one
  timestamp domain.
- **Kernel-time only.** No memcpys, no API records, no occupancy
  inputs, no counters, no path to PM/PC sampling.

Where luminal is ahead of us:

- **Compiler-phase visibility.** Their spans cover the whole host
  pipeline. We have nothing comparable inside MLX and cannot get it
  without upstream work.
- **Instrumentation-idiom reuse.** The `tracing` crate plays the role
  our pprof-labels design gives `runtime/pprof` — one native
  vocabulary feeding console, Perfetto, and filters. The two designs
  independently converge on the same thesis (reuse the language's
  idiom; don't invent one); theirs is more mature because `tracing`
  spans are richer than pprof labels.
- **No CUPTI client conflict.** CUDA events coexist with nsys/ncu; our
  in-process capture must detect the conflict and yield.
- **Zero dependencies**: works wherever CUDA works, no injection, one
  merged output file.

Where we are ahead: unmodified-workload measurement, library kernels,
memcpy/memset/API records, hardware timestamps, NVML overlay and the
PM/PC-sampling roadmap, pprof export with label-based `-tagfocus`
(no pprof story exists in luminal), capture of binaries we do not own
(LD_PRELOAD shim), and the downstream analyze/diff toolchain.

## Worth adopting

1. **Nested rendering.** Kernel slices as *children* of the host span
   that caused them is better UX than spans-alongside-kernels on
   separate tracks. Once the temporal/correlation join assigns kernels
   to spans, the Perfetto export should nest them.
2. **Per-eval setup/launch-latency decomposition.**
   `setup + launch latency + kernel time` is exactly the eval-overhead
   breakdown MLX users want; with API records plus first-kernel device
   timestamps we can compute it from real timestamps rather than
   reconstructed offsets.
3. **Event-record nodes as a fallback tier.** The CUPTI resource
   callbacks hand us the graph before instantiation, so injecting
   event nodes is technically available to us too — but CUPTI's
   per-kernel records already give per-node times without mutating the
   graph. Its one advantage is coexistence with nsys, so file it as a
   possible degraded mode when another tool owns the CUPTI slot, not a
   default.

## Verdict

Luminal built the best possible *first-party* tracer: perfect
attribution, zero external machinery, blind to whatever it does not
launch itself. gputrace/mlx-go build an *observer*: complete coverage
of what actually ran, with attribution as the hard-earned part. The
approaches are complementary; the mlx-go equivalent of luminal's
strengths is precisely the parts of our plan that need upstream MLX
hooks (node-to-primitive naming) plus the pprof-labels front-end
(their `tracing` analog).

Update, same day: node-to-primitive naming turns out NOT to need
upstream hooks. MLX's CUDA backend already wraps every primitive's
`eval_gpu` — the code that creates each graph node — in an NVTX range,
and the shipped `libmlx.so` carries the injection machinery. CUPTI
NVTX interception plus `GRAPHNODE_CREATED` resource callbacks yield a
by-construction node→op table. Plan:
`docs/design/luminal-quality-tracing.md` in the mlx-go repo.

## Parity status, 2026-08-26

All three "worth adopting" items now exist in gputrace on the
`api-forwarders` branch:

1. **Nested rendering — shipped.** `span` records decode into
   `gpuevent.Span`; `cuptitrace.BuildCapture` renders each span as a
   parent slice on its own track under an "Application spans" group,
   with temporally-attributed kernels nested as child slices on that
   track and span labels as debug annotations (`label.<key>`). Kernels
   matching no span stay on the flat tracks; a capture without spans
   renders exactly as before (golden-tested both ways). Attribution is
   stamped per kernel (`attribution:"temporal"`) rather than assumed.
2. **Per-eval setup/launch-latency decomposition — shipped.**
   `gputrace cupti --spans` reports, per span: setup (first launch-API
   start − span start, marked [V]; device-start fallback marked [D]),
   launch latency (kernel device start − API end), GPU time (sum of
   attributed kernel durations [V]), tail (span end − last kernel end),
   and join count. Same semantics as luminal's `setup_duration_ns`/
   `launch_latency_ns`, but computed from real device timestamps in one
   clock domain instead of reconstructed offsets — and it works for
   library kernels (cutlass/cuBLASLt) their instrumentation cannot see.
3. **Event-record fallback tier — not adopted, by design** per this
   document's own analysis: CUPTI per-kernel records already give
   per-node timing without mutating the graph. The one advantage
   (coexistence with nsys) remains a possible degraded mode.

The sidecar mechanism making spans available to non-mlx-go targets
(`GPUTRACE_APP_EVENTS`) also shipped: capture exports the env var,
targets append span JSONL, and the bundle merges it at close — so Go,
Python, or C++ workloads can all name their phases.
