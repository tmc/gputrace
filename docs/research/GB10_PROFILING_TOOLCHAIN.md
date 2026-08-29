# GB10 profiling toolchain: what works, what lies

Established 2026-08-29 on NVIDIA GB10, driver 580.95.05, aarch64 sbsa,
CUDA 12.6 + 13.0 toolkits. Confidence markers follow the repo convention:
[V] verified empirically on this host, [D] derived from a check that could
fail, [?] inferred from one observation.

The failures below matter because none of them is loud. Each produces a
capture or a report that looks healthy and is empty, or a number that
looks measured and is an artifact.

## 1. CUPTI latency timestamps are set for some launches, stale for others

`cuptiActivityEnableLatencyTimestamps(1)` makes CUPTI fill `queued` and
`submitted` on kernel activity records. It does not fill them for every
launch, and the unfilled case is not clean.

[V] On an MLX capture of 46,138 kernels
(`~/tmp/gputrace/go-06-api.gpucapture`), 45,943 kernels carried an
**identical** `queued` value (1787716468739751915) with `submitted` at 0.
Reading `start - queued` over that population gives a p50 of 1,162,307,115
ns — the 1.16 s of phantom "launch latency" that motivated this work. The
195 kernels with both fields set report a p50 queue-to-submit of 3.5 us
and submit-to-start of 352 ns, which are plausible.

[V] The cause is not a struct-offset mismatch. `offsetof` for `queued` and
`submitted` is identical (120, 128) in `CUpti_ActivityKernel4` and
`CUpti_ActivityKernel9` under CUPTI_API_VERSION 130001, so the shim's
prefix-compatible cast reads the right fields. CUPTI simply leaves them at
`CUPTI_TIMESTAMP_UNKNOWN` (0, per cupti_activity.h:1986) for launches it
cannot time, and a record buffer reused across activities can retain a
nonzero `queued` from an earlier record.

[V] The launches CUPTI cannot time are CUDA-graph node launches. A fixture
issuing 16 plain launches, 32 graph-node launches (an 8-node graph
launched 4 times), and 1 trailing launch produced exactly 17 kernels with
latency timestamps and 32 with `graph_id` set and no latency timestamps.

**Consequence for any consumer:** treat a nonzero `queued` alone as
meaningless. Require `queued != 0 && submitted != 0 && queued <=
submitted <= start`, and report coverage alongside any aggregate. gputrace
gates emission in the shim and stores the derived durations rather than
the raw timestamps, so a consumer cannot read them against a normalized
clock.

## 2. nsys 2026.1.1 `-t cuda` drops every kernel record here

[V] Four nsys installs exist on this host: 2026.1.1 (on PATH), 2025.3.2,
and two 2024.5.1 (one bundled at `/usr/local/cuda-12.6/bin/nsys`).

- **2026.1.1** enables HES (hardware event system) tracing by default for
  `-t cuda`. On GB10 the support probe reports success, GPU timestamps are
  never retrieved, and every kernel and memset record is dropped as
  "incomplete" while all CPU-side tables populate normally. The tells are
  the diagnostics `Hardware tracing used for CUDA tracing` and `Number of
  incomplete CUPTI events dropped: N`, where N equals the kernel count.
  Use `-t cuda-sw`. Add `--cuda-graph-trace=node` for MLX, which routes
  nearly everything through CUDA graphs.
- **2025.3.2** detects the platform correctly and falls back to software
  trace, so `-t cuda` works.
- **2024.5.1** bundles CUPTI 12.6 against a 13.0 driver and produces zero
  CUPTI events. It is what root's PATH picks up, which makes a sudo test
  look like confirmation of whatever theory prompted it.

`gputrace doctor` reports all of this per install, marking the one the
user's PATH resolves to.

## 2a. Node-mode graph tracing fails on MLX, and not because of HES

The natural reading of §2 is that `-t cuda-sw` is the safe path and the
hardware event system is the whole problem. That reading is wrong, and the
correction cost a measurement.

[V] MLX (`mlx-lm-generate`, Qwen3-0.6B, 64 tokens) under nsys 2026.1.1
with **`-t cuda-sw --cuda-graph-trace=node`** — the software path, node
granularity — produced:

- 36,085 `GetGraphId(data.originalGraph, ...) returned 1:
  CUPTI_ERROR_INVALID_PARAMETER`
- 36,085 `GetGraphNodeId(data.originalNode, ...)` with the same error
- no `CUPTI_ACTIVITY_KIND_KERNEL` table at all, and
  `Number of CUDA events collected: 1`

So the failure is on the software path, not HES. The `originalGraph` /
`originalNode` parameter names locate it: this is the clone-to-original
mapping CUPTI performs when a graph is instantiated or updated. MLX
re-instantiates graphs continuously, so those handles are stale by the
time CUPTI resolves them.

[V] A synthetic contrast rules out "node mode is broken on GB10": a
fixture launching one static 8-node graph 4,000 times, same nsys, same
flags, produced 32,017 kernels with 32,000 carrying graph IDs across 8
distinct nodes, and **zero** errors. Our own shim recorded the same
32,000 / 4,000 launches / 8 nodes for that binary, with matching node IDs
(8589934592–8589934599 in both nsys's sqlite and our JSONL). Node
attribution works; what fails is node attribution over *churned* graphs.

## 2b. nsys collects nothing from this Go workload, node mode or not

[V] The control matters more than the experiment: MLX under plain
`-t cuda-sw`, no node mode, also yields no kernel table and
`Number of CUDA events collected: 1`. The GetGraphNodeId errors disappear;
the emptiness does not.

[D] The likely cause is the one §4 of `docs/CUPTI_ROADMAP.md` describes
for our own shim: `mlx-lm-generate` is a Go binary (go1.26.7), a Go
process crosses no interposed CUDA synchronization point and exits via
`exit_group`, and nsys's own diagnostic says "Buffers holding CUDA trace
data will be flushed on CudaProfilerStop". Nothing triggers that stop.
This is why mlx-go-lm carries an in-process `cuptiActivityFlushAll`
(`lmgenerate/appevents_cupti_linux.go`) — and why a gputrace capture of
the same workload from 2026-08-25 holds 46,138 kernels while nsys holds
one event.

[?] Not established: a *fresh* side-by-side of the two tools on MLX. The
installed `mlx-lm-generate` predates the flush commit and captures empty
(exactly as `gputrace doctor` warns for a Go target); a build from the
current branch panics before generating, with or without capture, against
the installed MLX library. The 46,138-kernel capture is historical
evidence, not a reproduction.

**If you take one operational rule from this section:** on a Go CUDA
target, a profiler reporting no kernels is reporting its own flush
problem, not the workload's behavior. Check the kernel count before
believing any result — `gputrace doctor <binary>` says so up front.

## 3. GPU performance counters are admin-restricted, and the usual probe fails

[V] `ncu` returns `ERR_NVGPUCTRPERM` for an ordinary user here, while
exiting 0 — the error is only in the output.

[V] The conventional check does not work on this driver:
`/proc/driver/nvidia/params` exists but is empty, and
`/sys/module/nvidia/parameters/NVreg_RestrictProfilingToAdminUsers` does
not exist. `ncu --query-metrics` answers definitively in 0.58 s, so
`gputrace doctor` asks ncu rather than the driver.

[V] `ncu --kernel-name-base demangled` matches against a name carrying the
parameter list (`saxpy(int, float, float *, float *)`), so a pattern built
from a bare kernel name matches nothing and ncu reports "No kernels were
profiled" rather than an error. `--kernel-name-base function` matches the
bare name.

[V] `dram__throughput` reads `n/a` on this part; unified memory exposes no
discrete-DRAM counter. gputrace's default metric set omits it and reports
any `n/a` metric as unsupported rather than dropping it.

## 4. The capture shim costs about 5% of wall time

[V] Measured with `gputrace overhead -n 6` on the CUDA fixture
(`fixture.cu`: 49 kernels, 3 copies, one 8-node CUDA graph, four NVTX
ranges):

| Mode | Baseline median | Instrumented median | Overhead |
|---|---|---|---|
| activity records | 372.82 ms | 391.84 ms | +5.1% |
| `--api --nvtx` | 367.61 ms | 388.38 ms | +5.7% |

Both are IQR-separated, so the delta is real rather than noise. This
matters because the parity effects under study are 5–13%: a captured
throughput number cannot carry a claim about an effect the capture itself
could have produced. `gputrace overhead --effect-size` states that verdict
directly.

[?] The figure is workload-shaped. A run dominated by long kernels will
see less; one dominated by launch churn will see more. Re-measure per
workload rather than quoting this number.

## 5. NVTX ranges route into CUPTI without target changes

[V] With `GPUTRACE_CAPTURE_NVTX=1` (the `--nvtx` capture flag), which arms
`CUPTI_ACTIVITY_KIND_MARKER` and points `NVTX_INJECTION64_PATH` at
libcupti, a target's existing `nvtxRangePush/Pop` calls arrive as marker
records. The fixture's four ranges paired into four spans that attracted
the expected kernels: 16 to `plain-launches`, 32 to `graph-launches`, 1 to
`after-gap`, 0 to `setup` (which contains only copies).

This is the same span machinery the `GPUTRACE_APP_EVENTS` sidecar feeds,
so any library already instrumented for Nsight — cuDNN, cuBLAS, TensorRT,
PyTorch's `emit_nvtx` — contributes phase attribution without gputrace
patching it.
