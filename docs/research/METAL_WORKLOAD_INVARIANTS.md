# Metal Workload Invariants & Kernel Provenance (Apple Silicon)

Status: verified empirically on Apple silicon (macOS Darwin, arm64).

## 1. Establishing Once-Per-Token Invariants on Metal

To score completeness on Metal captures without relying on tool drop counters (which read zero when buffers are uncompleted or stranded), captures must be gated against a workload invariant op that executes deterministically per step.

### [V] MLX Argmax Invariant (`argmax_float32`)

- **Workload:** 32-step MLX Greedy Generation Loop
  ```python
  import mlx.core as mx
  x = mx.random.normal((1, 32000))
  for i in range(32):
      y = mx.argmax(x, axis=-1)
      mx.eval(y)
      x = x + 1.0
  ```
- **Capture:** Captured via `gputrace capture -o test-mlx-argmax32.gputrace -- python3 ...`
- **Result:** `gputrace kernels` extracted exactly 73 dispatches across 9 unique pipelines:
  - `argmax_float32`: **32 dispatches** [V] (exactly 1 per iteration)
  - `vs_Addfloat32`: **32 dispatches** [V] (exactly 1 per iteration)
  - Initialization ops: `rbitsc` (2x), `sv_Multiplyfloat32` (2x), `ss_Subtractfloat32` (1x), `v_ErfInvfloat32float32` (1x), `v_copyuint32float32` (1x), `vs_Dividefloat32` (1x), `vs_Minimumfloat32` (1x).

### Gate Evaluation
```bash
$ gputrace gate -t 31 -k argmax test-mlx-argmax32.gputrace
test-mlx-argmax32.gputrace: completeness ok    32/32 argmax (want 32 = 31 tokens + 1 prefill)
test-mlx-argmax32.gputrace: blit calls: absent from streamData
test-mlx-argmax32.gputrace: stationarity UNSCORED (timing data absent from raw capture; add with profile-replay)
```

### [V] MLX-LM Real Decode Invariant (`argmax_bfloat16` on Qwen3-0.6B)

- **Workload:** 16-token generation with `mlx-community/Qwen3-0.6B-bf16` via `mlx_lm.generate`:
  ```bash
  gputrace capture -o qwen-decode-16.gputrace -- \
    python3 -m mlx_lm.generate \
    --model mlx-community/Qwen3-0.6B-bf16 \
    --prompt "Hello" \
    --max-tokens 16 \
    --temp 0
  ```
- **Capture:** `qwen-decode-16.gputrace` (9,860 total dispatches across 20 pipelines).
- **Result:** `gputrace kernels` confirmed exactly 16 dispatches for the step-invariant operations:
  - `argmax_bfloat16`: **16 dispatches** [V] (1 per generated token)
  - `looped_logsumexp_bfloat16`: **16 dispatches** [V] (1 per generated token)
  - `gather_frontbfloat16_uint32_int_2`: **16 dispatches** [V] (1 per generated token)
  - `vsn_Subtractbfloat16`: **16 dispatches** [V] (1 per generated token)
  - Autoregressive step layers: `gemv_bfloat16_*` (3,349x across 28 layers), `rmsbfloat16` (2,031x), `vv_Addbfloat16` (1,006x).

### Gate Evaluation
```bash
$ gputrace gate -t 16 --exact-tokens -k argmax qwen-decode-16.gputrace
qwen-decode-16.gputrace: completeness ok    16/16 argmax
qwen-decode-16.gputrace: blit calls: absent from streamData
qwen-decode-16.gputrace: stationarity UNSCORED (timing data absent from raw capture; add with profile-replay)
```

## 2. Replay Timing vs Live-Run Timing

[V] Stationarity scored on a Metal bundle uses `streamData` dispatch timing
(`CumulativeUs`), which Xcode produces by **profile-replaying** the capture.
It describes the replay execution, not the original live run: a mid-run
excursion during the live workload (contention, thermal throttling) is
invisible in replay timing. `gputrace gate` labels such results
`(replay timing — does not witness the live run)`.

[V] `CumulativeUs` is stored as integer microseconds in the raw record
(`internal/counter/streamdata.go`, u64 at offset 16), so trajectory values
have 1 µs source resolution — a block median printed as `0.04` ms is exactly
40 µs at source, not display rounding (gate prints 4 significant figures).

Live-run stationarity on Metal needs timestamps taken during the original
execution. The capture interposer's timing sidecar (`GT_TIMING_OUT`,
`internal/capture/inject.objc`) records per-command-buffer
`GPUStartTime`/`GPUEndTime` wall-clock plus `sampleTimestamps:` clock pairs at
schedule/complete — that is live timing, but at command-buffer granularity
with no kernel symbols, so it cannot yet be matched against a `-k` invariant.
[V] `gate --timing <sidecar>` scores live stationarity from per-command-buffer
gaps, valid when each decode step maps to a stable number of command buffers
(MLX greedy decode: one CB per step). Verified end to end:

```bash
$ gputrace capture -o live.gputrace --timing-sidecar live.jsonl --run-id r1 -- python3 <40-step argmax loop>
$ gputrace gate -t 39 -k argmax --timing live.jsonl --block-size 8 live.gputrace
live.gputrace: completeness ok    40/40 argmax (want 40 = 39 tokens + 1 prefill)
live.gputrace: stationarity FAIL  42.0% excursion  [0.7933 0.6148 0.5587 0.52 0.5256] ms  (live command-buffer timing)
```

(That FAIL is real: the loop has no warmup skip, and the settling shape is
exactly the gate-2 "high, then settling" signature.)

[V] Caveat found while verifying this: the sidecar interposer labels command
buffers `gputrace.live.cb.<n>`, and that label surfaces as a CS record in the
capture. The kernels reader used to mistake it for an encoder, which silently
destroyed pipeline attribution — every kernel name became the CB label and
`-k` matched 0 dispatches. Fixed in internal/trace/kernel_stats.go by
excluding the interposer's own labels from encoder detection.

## 3. Nested-Range Monotonicity (`gate --ranges`)

The only check that catches a capture hook sitting downstream of already
queued GPU work: capture nested half-open token ranges and require invariant
dispatch counts to grow strictly with range width.

[V] Verified on three real captures of the MLX argmax loop at 8/16/24 steps:

```bash
$ gputrace gate --ranges 0:8,0:16,0:24 -k argmax nest-8.gputrace nest-16.gputrace nest-24.gputrace
Nested-range monotonicity:
  [0,8)         8 argmax dispatches  (nest-8.gputrace)
  [0,16)       16 argmax dispatches  (nest-16.gputrace)
  [0,24)       24 argmax dispatches  (nest-24.gputrace)
  monotonicity ok    counts grow with range width
  [0,8) -> [0,16): +8 tokens, +8 dispatches ok
  [0,16) -> [0,24): +8 tokens, +8 dispatches ok
```

Passing the same bundle twice fails with "count did not grow" (exit 1);
an arm with 0 invariant matches is NOT_EVALUABLE (exit 2). Each width
increase must add at least (added tokens - slack) invariant dispatches.

## 4. Invariant Rules for Metal
- Do not default invariant symbols on Metal until established from real captures.
- In `mlx_lm` and MLX transformer models, `argmax` (specifically `argmax_bfloat16` / `argmax_float32`) reliably executes once per decode step during greedy sampling.
- Require `-k` explicitly.
- If an invariant symbol matches 0 dispatches, fail closed with exit status `2` (`NOT_EVALUABLE`).

