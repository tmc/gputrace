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

## 2. Invariant Rules for Metal
- Do not default invariant symbols on Metal until established from real captures.
- In `mlx_lm` and MLX transformer models, `argmax` (specifically `argmax_bfloat16` / `argmax_float32`) reliably executes once per decode step during greedy sampling.
- Require `-k` explicitly.
- If an invariant symbol matches 0 dispatches, fail closed with exit status `2` (`NOT_EVALUABLE`).

