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

## 2. Invariant Rules for Metal
- Do not default invariant symbols on Metal until established from real captures.
- Require `-k` explicitly.
- If an invariant symbol matches 0 dispatches, fail closed with exit status `2` (`NOT_EVALUABLE`).
