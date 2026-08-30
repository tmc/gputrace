# CUPTI capture: current state and roadmap to best-in-class

Status: proposal, 2026-08-25. Facts below were established by reading the
code on branch `api-forwarders` and by probing the local toolchain
(NVIDIA GB10, driver 580.95.05, CUDA 12.6 + 13.0 toolkits, sbsa-linux).
Items marked [verified] were checked empirically on this host; items
marked [header] were confirmed present in the local CUPTI headers; the
rest is design judgment.

## 1. What exists today

The Linux/NVIDIA path is four small pieces:

| Piece | Location | Role |
|---|---|---|
| Capture shim | `internal/cupticapture/embed/shim.c` | LD_PRELOADed C library; arms CUPTI activity tracing, writes JSONL |
| Bundle + build | `internal/cupticapture` | compiles/caches the shim, `.gpucapture` bundle layout, env plumbing |
| Event model | `internal/gpuevent` | vendor-neutral events/samples, JSONL decode, heuristic findings |
| Presentation | `internal/cuptitrace`, `cmd/gputrace/cmd/{cupti,capture_linux,analyze}.go` | demangling, Perfetto projection, stats/top, analyze/optimize loop |

What the shim records per activity kind:

- **Kernels** (`CUPTI_ACTIVITY_KIND_KERNEL`, falling back to
  `CONCURRENT_KERNEL`): raw symbol, start/end, grid, block,
  registers/thread, device/stream/correlation IDs. Cast as
  `CUpti_ActivityKernel4`.
- **Memcpy/memset**: direction (HtoD/DtoH/DtoD), bytes, timing, IDs.
- **Runtime/driver API calls** (gated on `GPUTRACE_CAPTURE_API` env, not
  exposed as a CLI flag): cbid, timing, thread ID, correlation ID, with a
  hardcoded ~14-entry cbid→name table.

Flushing rides on interposed `cudaDeviceSynchronize`,
`cudaStreamSynchronize`, `cudaEventSynchronize`, and `cudaMemcpy`; the
destructor deliberately does not flush (deadlock avoidance).

Alongside the shim, `gputrace capture --samples` runs `nvml_sampler`
(`tools/nvml-sampler-main.go`, a separate binary looked up on PATH),
which samples device 0: power, util%, clocks, temperature, memory,
pstate, throttle reasons, cumulative energy.

Downstream: `gputrace analyze` computes per-kernel stats
(count/mean/p50/p95/share), classifies a heuristic bound
(compute/memory/latency), and emits evidence-backed findings mapped to a
playbook (`internal/optimize`); `gputrace optimize run/compare` closes
the loop with noise-aware verdicts; `gputrace cupti` renders a Perfetto
trace with optional per-kernel tracks and NVML counter overlays.

This is a solid skeleton: correct injection strategy, honest provenance,
a real closed loop. The rest of this doc is about the distance between
this skeleton and what Nsight Systems + Nsight Compute give a human —
and what neither gives an agent.

## 2. Defects found during this review

Worth fixing before any new capability; all are in `shim.c` or the
capture command unless noted.

1. **Serialized-kernel default distorts the workload.** `arm_cupti`
   enables `CUPTI_ACTIVITY_KIND_KERNEL` first and only falls back to
   `CONCURRENT_KERNEL`. Per CUPTI docs, enabling KERNEL *serializes all
   kernel launches*, so the profile no longer reflects real stream
   concurrency — the tool changes the very overlap behavior it should be
   measuring. CONCURRENT_KERNEL must be the first choice, KERNEL the
   fallback.
   **DONE (934bedb7):** concurrent-first with genuine fallback; verified
   `concurrent_kernel:true` in capture_meta on GB10.
2. **The fallback itself is unreachable in debug mode.** The
   CONCURRENT_KERNEL fallback sits in the `else` branch of the debug
   check, so setting `GPUTRACE_CAPTURE_DEBUG` disables the fallback
   path entirely.
   **DONE (934bedb7):** branch restructured; both paths reachable.
3. **API records are captured but dropped end-to-end.** The shim emits
   `{"kind":"api",...}` records, `gpuevent.DecodeJSONL` keeps them, and
   then both `cuptitrace.Build` and `gpuevent.Analyze` silently skip the
   unknown kind. Nothing consumes them.
   **DONE (feced95b, 513f9904, 38a01732):** decoded into `APIEvent`,
   joined to kernels by correlation ID, reported as launch_overhead by
   analyze, rendered as host-API tracks in Perfetto.
4. **Multi-process captures corrupt the events file.** `LD_PRELOAD` and
   `GPUTRACE_CAPTURE_OUT` are inherited by children (torchrun, MPI,
   dataloader workers, `python -m`), every process `fopen(path, "a")`s
   the same file, and stdio flushes interleave mid-line. Fix: per-PID
   files (`events.<pid>.jsonl`) merged at read time, single-`write(2)`
   lines with `O_APPEND`.
   **DONE (d40d9a89):** shim writes per-PID shards; readers glob
   events*.jsonl* and concatenate.
5. **Tail-event loss for apps that never synchronize.** With no
   destructor flush and flush keyed to interposed runtime calls, a
   driver-API-only or statically-linked-runtime app loses everything
   still buffered at exit. Needs an investigated exit path
   (`atexit` + forced `cuptiActivityFlushAll(CUPTI_ACTIVITY_FLAG_FLUSH_FORCED)`
   before CUDA teardown, or a driver-callback-based flush on context
   destroy) rather than "hope the app called cudaDeviceSynchronize".
   **DONE (2026-08-29):** a flush thread calling
   `cuptiActivityFlushAll(0)` every 10 ms at arm time. This was worse
   than described: MLX links the CUDA runtime statically and launches
   through graphs resolved by `cuGetProcAddress`, so *no* interposition
   point exists at any level — capturing it yielded zero kernels while
   the workload ran.
   `cuptiActivityFlushPeriod` was tried first and is not sufficient: it
   delivers only buffers that filled, which strands the rest and makes a
   larger buffer lose *more* — at 16 MiB nothing fills and the capture is
   empty. The explicit non-forced flush completes partial buffers and
   takes a 128-token MLX decode from 102 to 127 of its 129 required
   argmax launches, at no measurable cost, with the buffer size no longer
   mattering. Loss is now bounded by the flush interval rather than
   unbounded. See `docs/research/GB10_PROFILING_TOOLCHAIN.md` §2c,
   including why driver-level launch interposition was implemented,
   measured at zero hits, and deleted.
6. **NVML overlay clock alignment is unproven in general.**
   [verified] On this host `cuptiGetTimestamp` == CLOCK_REALTIME within
   tens of ns, so joining CUPTI activity timestamps with the sampler's
   `time.Now().UnixNano()` via `Capture.Normalize` happens to be
   correct. CUPTI does not document this timebase; on another
   driver/platform the overlay could be silently offset by an arbitrary
   constant. Cheap insurance: the shim emits one
   `{"kind":"clock_sync","unix_ns":...,"cupti_ns":...}` record at init,
   and Normalize uses it when the domains disagree.
   **PARTLY DONE (feced95b):** clock_sync record emitted at init and
   decoded into `Capture.ClockSync`; Normalize does not yet consume it
   (domains currently verified equal on this host).
7. **Sampler deployment is fragile.** `nvml_sampler` must be separately
   built and on PATH or `--samples` silently degrades. It should be a
   `gputrace` subcommand (the capture command re-execs itself), removing
   the second binary entirely. Also: it samples device 0 only, and its
   default interval (200ms) disagrees with the capture flag default
   (25ms).
   **PARTLY DONE (6a26940b):** sampler source hosted in-tree
   (`tools/nvmlsampler`, installable alongside gputrace); still a
   separate binary looked up on PATH. All-devices sampling remains open.
8. **`meta.json` records no hardware provenance.** Command and versions,
   but no GPU name/UUID, driver, CUDA/CUPTI version, clocks, or ECC
   state — exactly the fields `optimize compare` needs to refuse
   comparing runs from different machines or driver versions.
   `internal/nvidia` already knows all of it.
   **DONE (db5ce95d):** gpu_name/gpu_uuid/driver_version recorded from
   NVML at bundle creation. CUPTI version and clocks remain open.
9. **Kernel struct version.** The `CUpti_ActivityKernel4` cast is
   prefix-safe today ([header] Kernel10's layout is identical through
   every field the shim reads), but Kernel4 stops at `name`, forfeiting
   the fields sections 3–4 below want. Select the struct by
   `CUPTI_API_VERSION` at compile time.
   **DONE (934bedb7, partially):** Kernel4 retained for prefix-safe
   fields plus queued/submitted latency timestamps now emitted when
   nonzero. Newer-struct-only fields remain open.

   **REVISED (2026-08-29):** emitting queued/submitted whenever *either*
   was nonzero published stale values from reused record buffers. See
   `docs/research/GB10_PROFILING_TOOLCHAIN.md` §1: 45,943 of 46,138
   kernels shared one queued timestamp, implying a p50 launch latency of
   1.16 s. Emission is now gated on a consistent
   queued <= submitted <= start triple; the offsets themselves are fine
   ([V] `queued` and `submitted` sit at 120 and 128 in both Kernel4 and
   Kernel9 under CUPTI_API_VERSION 130001), so the prefix-safe cast was
   never the problem.

## 3. Free upgrades: fields CUPTI already hands us

The activity records the shim already receives carry substantially more
than it emits. Zero new activity kinds, near-zero overhead — just wider
JSON: [header]

- **`queued`/`submitted` timestamps** (with
  `cuptiActivityEnableLatencyTimestamps(1)`): per-launch queue and
  submission latency. This is the measurement that separates "kernel is
  slow" from "kernel waited in the stream queue" — the single most
  common misdiagnosis in CUDA timelines.
- **`staticSharedMemory` / `dynamicSharedMemory` /
  `localMemoryPerThread`**: with registers (already captured) and block
  size, these are the complete inputs to theoretical occupancy (§5.1).
- **`completed`**: distinguishes device-side completion from end for
  graph kernels.
- **`contextId`, `channelID`, `graphId`, `graphNodeId`**: multi-context
  and CUDA-graph attribution.
- **`clusterX/Y/Z`**: thread-block cluster geometry on Hopper+.
- **Memcpy `srcKind`/`dstKind`** (pageable / pinned / device / array):
  turns "transfers rival compute" into "…and 84% of transfer time moved
  *pageable* memory — pin these buffers", which is the difference
  between a finding and an answer.
- **cbid→name via `cuptiGetCallbackName`** [header]: delete the
  hardcoded 14-entry table; every runtime/driver cbid resolves at
  capture or read time.

## 4. New activity kinds worth arming

All present in the local CUPTI headers. Each should be a capture flag
(with a `--full` preset), because record volume is the cost axis:

| Kind | What it buys | Flag sketch |
|---|---|---|
| `RUNTIME`/`DRIVER` (exists, env-gated) | host-side launch/sync timing; launch-bound detection | promote to `--api` |
| `MARKER` + `MARKER_DATA` (NVTX) | semantic phases: PyTorch `emit_nvtx`, cuDNN/cuBLAS/TensorRT built-in ranges, user annotations | **DONE:** `--nvtx`, with `NVTX_INJECTION64_PATH` set in the child env; markers pair into spans at decode |
| `OVERHEAD` | CUPTI's own overhead, attributed instead of polluting kernels | on with `--api` |
| `MEMORY2` / `MEMORY_POOL` | cudaMalloc/Free timeline, pool grow/shrink — allocation churn and fragmentation findings | `--memory` |
| `UNIFIED_MEMORY_COUNTER` | UM page faults, HtoD/DtoH migration bytes, thrashing | `--um` |
| `GRAPH_TRACE` | whole-graph execution spans for CUDA Graphs workloads (increasingly the norm in inference) | on by default when graphs observed |
| `MEMCPY2` | peer-to-peer copies for multi-GPU | on with multi-GPU |

NVTX deserves emphasis: it is the only source of *application semantics*
("layer 12 attention", "optimizer step", "decode token 47") and it is
what makes a timeline navigable and a findings report speak the user's
vocabulary. Every serious CUDA profiler treats it as first-class.

## 5. The counter story: from heuristic to measured

Today every bound classification is geometry-derived and honestly
labeled heuristic. The path to measured, in increasing order of cost:

### 5.1 Theoretical occupancy (no CUPTI cost at all)

Registers/thread (have), shared memory/block (§3), block size (have),
plus device properties captured once into `meta.json` (SM count,
regs/SM, shared/SM, max threads/SM) → theoretical occupancy per kernel,
computed at analyze time in Go. Upgrades `launch-shape` findings from
"2048-thread threshold" to "this launch can occupy at most 4 of 48 SMs;
the limiter is registers (254/thread)". Cheap, deterministic, and
exactly what the playbook needs to pick between "reduce registers" and
"increase grid".

### 5.2 PM sampling — continuous device metrics (`cupti_pmsampling.h`) [header]

CUPTI 12.6+ exposes the PerfWorks PM sampler: periodic device-level
counters (SM active %, tensor pipe active, DRAM read/write bytes, L2
hit rates) **without kernel replay and without serializing anything** —
the same mechanism behind Nsight Systems' "GPU metrics" row. This slots
directly into the existing `Sample`/counter-track machinery, replacing
NVML's 1-second-window `util%` (which is nearly meaningless for
sub-second workloads) with real sub-millisecond utilization. This is
the flagship counter feature: timeline-aligned truth about whether the
device was doing math, moving bytes, or idling, per kernel occurrence.

### 5.3 Range profiler — per-kernel/per-range metrics (`cupti_range_profiler.h`) [header]

Replay-based detailed metrics (achieved occupancy, memory throughput
%, pipe utilization, stall breakdown) for named ranges or auto per
kernel: Nsight-Compute-lite. Costly (replay perturbs timing), so it is
a second, targeted run: `gputrace profile --kernel <name>` after
`analyze` has picked the suspect. The optimize loop then cites measured
limiter data instead of hypotheses.

### 5.4 PC sampling — source-level attribution (`cupti_pcsampling.h`, `cupti_pcsampling_util.h`) [header]

Warp-stall PC samples correlated to SASS and (with `-lineinfo`) to
source lines. Combined with the existing pprof exporter this yields
what no NVIDIA tool ships: **`pprof`/flamegraph output for GPU stall
cycles by source line**, diffable across runs with the existing
compare machinery. This is the "as rich as possible" ceiling and a
genuine differentiator; it is also the most work (cubin handling,
`nvdisasm`, correlation tables), so it sits last.

## 6. Host/device correlation and presentation

What turns records into answers:

- **Wire `api` events through the model** (`gpuevent.KindAPI`), give
  them host-thread tracks in Perfetto, and link launch→kernel with
  Perfetto **flow events** over the correlation IDs both sides already
  carry. Arrows from `cudaLaunchKernel` to the kernel slice are the
  single highest-leverage UI improvement.
- **Per-stream and per-device track trees** (`streamId`/`deviceId` are
  already captured; today everything shares one lane, so concurrency is
  invisible). Group: device → stream → slices; NVTX ranges as a parent
  span track.
- **Gap/starvation analysis in `analyze`**: measure device-idle
  intervals inside the span; when idle gaps align with dense API
  activity or long `queued→start` latency, report *launch-bound* with
  numbers ("GPU idle 41% of steady state; mean launch overhead 14µs ×
  31k launches"). This is the most common real-world diagnosis for ML
  inference and currently unreachable because of defect §2.3.
- **New finding kinds** as data lands: `launch-bound`, `sync-heavy`
  (excessive `cudaDeviceSynchronize`), `pageable-transfer`,
  `alloc-churn`, `um-thrashing`, `queue-depth`. Each keeps the existing
  evidence/hypothesis/verify-with discipline.
- **Steady-state segmentation**: detect the iteration period (from NVTX
  when present, autocorrelation of kernel sequence when not), exclude
  warmup, and report per-iteration stats — `optimize compare` gets
  tighter IQRs for free.
- **In-process demangling**: replace the `c++filt` subprocess with
  `github.com/ianlancetaylor/demangle` (pure Go, used by pprof) —
  removes the binutils dependency and the per-symbol fork.

## 7. Tying Go-level labels and data into captures

The consumer that motivates this: mlx-go (MLX's CUDA backend on Linux),
whose workloads gputrace captures today as anonymous kernel streams.
mlx-go already has NVTX bindings (`internal/cuda/nvtx.go`, purego-loaded
`libnvToolsExt`, surfaced as `mlx/cuda` `Nsight.Range/Mark` gated on a
profiling switch) — annotations that today reach Nsight but not
gputrace. Four mechanisms, in increasing order of precision:

### 7.1 NVTX ranges (interop layer)

Tier 2's `--nvtx` makes the existing mlx-go annotations land in our
bundles: the shim enables `MARKER`/`MARKER_DATA`, and the capture
command sets `NVTX_INJECTION64_PATH` to libcupti in the child env so the
dynamically-loaded NVTX routes into CUPTI. One instrumentation shows up
in both Nsight and gputrace.

Two Go-specific corrections needed on the mlx-go side:

- **`RangePush`/`RangePop` are thread-scoped** and goroutines migrate
  between OS threads, so a push and its pop can land on different
  threads and corrupt nesting. mlx-go should add the process-scoped
  pair `nvtxRangeStartA`/`nvtxRangeEnd` (ID-based, goroutine-safe) and
  prefer it for any range that can cross a scheduling point.
- **MLX is lazy**: graph construction in Go does not launch kernels;
  `Eval` does, partly on MLX scheduler threads. Ranges must bracket
  eval — the natural hook is an `EvalWithLabel` mirroring the existing
  Metal-side `EvalWithCommandBufferLabel` — not model code that merely
  builds arrays.

### 7.2 Sidecar spans (vendor-neutral, pure Go, recommended default)

NVTX is CUDA-only; mlx-go is dual-backend and gputrace is dual-vendor.
The lowest-common-denominator mechanism needs neither cgo nor NVIDIA
libraries: `gputrace capture` exports `GPUTRACE_APP_EVENTS=<bundle>/
app_events.<pid>.jsonl`, and a tiny zero-dependency Go package (strawman:
`gputrace/mark`) writes span/instant records — epoch-ns timestamps,
name, and a `labels` map — with single-write appends, no-oping when the
env var is unset (the same stay-inert contract as the shim). [verified]
`time.Now().UnixNano()` and CUPTI activity timestamps share the
CLOCK_REALTIME timebase on this host, and the §2.6 clock-sync record
covers hosts where they do not.

At read time these become `gpuevent.KindSpan`, rendered as an
"Application" track tree in Perfetto and used by `analyze` to segment
per-phase statistics ("decode" vs "prefill" kernel time). The same file
format works from Python or C++ workloads — nothing about it is
Go-specific except the convenience package.

### 7.3 External correlation IDs (exact attribution)

Temporal overlap misattributes when streams interleave. CUPTI's precise
mechanism is `cuptiActivityPushExternalCorrelationId`: push an
app-chosen uint64 on the launching thread and CUPTI emits
`EXTERNAL_CORRELATION` records binding it to the `correlationId` of
every API record (and thus kernel) issued under it — turning "span
contains kernel, probably" into "kernel belongs to request #4712,
definitely". The constraint is *launching thread*: for mlx-go this
means pushing around the eval call path (or inside MLX's scheduler
worker via an upstream hook), not around lazy graph building. The shim
grows an `EXTERNAL_CORRELATION` decoder either way; other runtimes
(PyTorch profiler uses exactly this API) then correlate for free.

### 7.4 In-process capture for Go workloads (no shim at all)

For workloads that *are* Go, LD_PRELOAD is unnecessary ceremony:
`github.com/tmc/lib/nvidia/cupti` (§7.5) lets the process arm the
activity API itself and write the bundle directly — labels, pprof label
sets, goroutine IDs, and kernel records emitted by one process with
shared context. A `gputrace/selfcapture`-style package would make any
mlx-go binary capturable with one import plus an env check, and is also
the natural home for the runtime/trace bridge: merge Go's execution
trace (GC pauses, scheduler latency) as tracks beside the GPU timeline,
answering "is this GPU idle gap a Go GC pause?" — a diagnosis no
NVIDIA tool can make. The mlx-go side of this design is written up in
that repo as `docs/design/native-cupti-capture.md`.

### 7.5 Binding readiness: github.com/tmc/lib/nvidia/cupti

Audited 2026-08-25: the generated bindings cover the full surface this
roadmap needs — activity API including latency timestamps, external
correlation, flush period, and `ActivityEnableAndDump` late-attach;
callback API with `GetCallbackName`; PC sampling; PM sampling; range
profiler; profiler host/target; SASS metrics. Generated from CUDA 13.0
headers (`ActivityKernel11`-era), cgo-free via purego with `Has*`
presence guards for older runtime libcupti, and — the load-bearing
part — 2,618 generated layout assertions (size/align/offset per
struct, including the packed kernel records) passing on this
sbsa-linux host. [verified]

Two integration risks to retire before relying on them in-process:
purego callbacks invoked from CUPTI's own (non-Go) threads need a soak
test under load, and layout tests should also run on x86_64. Notably,
these bindings make Tier 3's PM sampling implementable in pure Go
inside the gputrace process (it is device-scoped, not target-scoped),
leaving the C shim needed only for in-target activity capture of
non-Go workloads.

## 8. Robustness and deployment

- **`CUDA_INJECTION64_PATH` as a second injection path.** NVIDIA's
  official mechanism: libcuda itself loads the library at `cuInit`,
  which works for statically-linked runtimes and containers where
  LD_PRELOAD is stripped. Support both; prefer injection when the
  driver honors it.
- **Attach-to-running / system-wide gaps are real but out of scope**
  for the shim model; document the boundary honestly (Nsight Systems
  can attach; we require launch-under).
- **Multi-GPU**: sampler iterates all devices (tag samples with
  `device_id`), per-device Perfetto counter tracks, and `MEMCPY2` for
  P2P. Multi-node (torchrun across hosts): rank-tagged bundles and a
  `gputrace merge` that joins on the clock-sync records.
- **Output volume**: JSONL is the right debuggable default; add
  optional zstd (`events.jsonl.zst`) for long runs — the decoder
  already tolerates the failure modes that matter.
- **Testing**: a tiny CUDA fixture (few kernels, one pageable and one
  pinned memcpy, one graph launch) compiled at test time when `nvcc`
  exists, driving rsc.io/script integration tests; golden JSONL for
  the decode/analyze path so non-GPU CI still covers everything after
  the shim.

## 9. What "best in class" means here

Nsight Systems and Nsight Compute are excellent human-driven GUIs; they
are weak exactly where gputrace is strong, and that asymmetry is the
strategy:

1. **Answers over timelines.** Findings with evidence, hypotheses, and
   verify-with steps — launch-bound %, pinned-vs-pageable byte counts,
   occupancy limiters — not a UI the user must interrogate.
2. **Agent-native.** Everything JSON, everything scriptable, capture →
   analyze → suggest → change → compare with noise-aware verdicts. No
   NVIDIA tool closes this loop.
3. **Vendor-neutral core.** The same `gpuevent` model and findings
   vocabulary spans Metal today and CUPTI now; ROCm (rocprofiler-sdk)
   projects into the same model later. Cross-vendor perf reports from
   one tool.
4. **Ecosystem output.** Perfetto for eyes, pprof for diffing and
   flamegraphs, benchfmt for statistics — open formats other tools
   already speak.

## 9a. Shipped since this doc was written

Established empirically on GB10; see
`docs/research/GB10_PROFILING_TOOLCHAIN.md` for how each was verified.

- **Launch latency as a computed, coverage-gated metric** (§2.9 revision).
  CUDA-graph node launches carry no latency timestamps at all [V], so the
  analysis reports coverage and refuses to characterize a capture from too
  few timed launches.
- **Busy/idle budget** (`gpuevent.UtilizationOf`): GPU busy time as an
  interval *union* — summing kernel durations double-counts overlapped
  streams — wall span, occupancy, per-gap attribution, and a `gpu-idle`
  finding. Surfaced by `analyze` and by `summary` on a bundle.
- **NVTX capture** (§4, §7.1): `--nvtx` arms markers and points
  `NVTX_INJECTION64_PATH` at libcupti; marker pairs become spans that
  attract kernels exactly as sidecar spans do.
- **CUDA graph attribution** (`gpuevent.AnalyzeGraphs`): kernels grouped
  by graph and node ID, with per-node time share. Node ID to source
  operation is *not* derivable from activity records — CUPTI reports the
  node's identity, not its provenance — so this stops at "node 7 of graph
  3 runs this kernel for 40% of the graph's time".
  [V] Verified against nsys on a static graph: identical node IDs and
  counts. [V] It does *not* survive MLX-style graph churn — nsys's node
  mode emits 36k `GetGraphNodeId ... INVALID_PARAMETER` errors there on
  the *software* path, so this is not an HES limitation; see
  `docs/research/GB10_PROFILING_TOOLCHAIN.md` §2a.
- **`gputrace doctor`**: per-install nsys verdicts, CUPTI-versus-driver
  compatibility, counter permission probed via ncu, shim build, and
  per-target capturability checks.
- **`gputrace diff` for two bundles**: per-kernel deltas ordered by GPU
  time moved, occupancy movement, the idle budget, and the kernels present
  in only one capture.
- **`gputrace ncu`**: escalation of the top-N kernels to hardware
  counters, merged back into the bundle as `ncu.json`.
- **`gputrace overhead`**: the shim's own cost against a stated effect
  size — +5.1% wall time for activity records on the local fixture, +5.7%
  with `--api --nvtx` [V].

Still open from the tiers below: PM sampling, the range profiler, PC
sampling, `cuptiGetCallbackName`, the in-process demangler, sampler as a
subcommand, `CUDA_INJECTION64_PATH`, and multi-GPU/multi-node merge.

## 10. Prioritized plan

Ordered by leverage per unit of work; each tier is shippable alone.

**Tier 0 — correctness (small, do first):** §2 defects: concurrent-first
kernel activity, fallback bug, per-PID output files, clock-sync record,
sampler as subcommand + all devices, hardware provenance in meta.json,
exit-flush investigation.

**Tier 1 status (2026-08-29):** shared/local memory, queued/submitted
latency timestamps, graph IDs, memcpy src/dst kinds, `--api`, and
theoretical occupancy are in. `cuptiGetCallbackName` and the in-process
demangler remain open.

**Tier 1 — free richness (small):** Kernel9/10 fields (shared/local
memory, queued/submitted via latency timestamps, graph IDs), memcpy
src/dst kinds, `cuptiGetCallbackName`, `--api` flag, theoretical
occupancy in analyze, in-process demangler.

**Tier 2 — correlation & semantics (medium):** KindAPI end-to-end,
per-stream/device tracks, correlation flows in Perfetto, gap/launch-bound
and pageable-transfer findings, NVTX capture + range tracks +
finding vocabulary, sidecar app spans (`GPUTRACE_APP_EVENTS` + the
`gputrace/mark` package, §7.2) with per-phase analyze segmentation,
mlx-go NVTX fixes (ID-based ranges, eval-scoped labels, §7.1),
steady-state segmentation.

**Tier 3 — measured counters (medium-large):** PM sampling counter
tracks (replaces NVML util as the utilization truth), then the range
profiler behind `gputrace profile --kernel` feeding measured limiters
into the playbook.

**Tier 4 — reach (large):** PC sampling → SASS/source attribution →
GPU stall pprof/flamegraphs; memory/UM/mempool findings; multi-GPU and
multi-node merge; `CUDA_INJECTION64_PATH`; zstd bundles.

The end state: `gputrace capture` runs anything from a matmul to a
torchrun job with negligible distortion; `analyze` says *why* it is
slow with measured evidence at whatever depth was captured; `profile`
drills into the suspect kernel with hardware counters down to source
lines; `optimize` proves whether the fix worked — all from a terminal,
all machine-readable, on NVIDIA and Apple silicon alike.
