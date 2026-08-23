# GPU Optimization Playbook

This playbook maps measured findings from `gputrace analyze` to concrete,
verifiable optimization actions. It is written for agents driving the
optimize loop and for developers reading a capture.

Every action follows the same contract:

1. **One change per iteration.** A change that alters two things cannot
   attribute its result.
2. **Measure before and after** with `gputrace optimize run` using identical
   `--iterations`/`--warmups`.
3. **Trust only separated verdicts.** `noisy-change` means "unknown", not
   "probably fine". Rerun with more iterations.
4. **Cite evidence.** An improvement claim without the compare output is an
   anecdote.

Findings come from launch geometry and timing heuristics, not hardware
counters; treat them as ranked starting points.

---

## dominance — one kernel owns ≥30% of GPU time

**Evidence cited:** launch count, share of total kernel time, mean/p95,
modal grid×block, registers.

| Bound | Actions to consider (one at a time) |
|---|---|
| compute | Tile for cache/tensor-core reuse; check whether the algorithm can use a vendor library (cuBLAS/cuDNN/cutlass) instead of hand-rolled math; reduce redundant launches by batching |
| memory | Wider vectorized loads (128-bit); improve coalescing so adjacent threads touch adjacent addresses; fuse with adjacent kernels to keep data in registers/shared memory; compress data in flight |
| latency | Expose more parallelism first (see launch-shape below) — dominance on a tiny grid is a shape problem before it is a kernel problem |

**Verify:** share of the dominant kernel should drop or total kernel time
should fall with unchanged outputs.

## launch-shape — too few threads to fill the device

**Evidence:** grid × block threads per launch, mean duration.

- Batch independent work items into one larger launch instead of many small ones.
- Split sequential loops across blocks; move loop-carried state off the critical path.
- Overlap independent small kernels on separate streams while keeping correctness invariants.
- If the work is genuinely serial, say so and stop: not every kernel is parallelizable, and forcing a bigger grid can regress it.

**Verify:** the kernel's mean duration should drop or its bound should
reclassify away from latency-bound.

## long-tail — p95 far above mean

**Evidence:** p95 vs mean over launch count.

- Check for input-dependent branching (data-dependent early exit, irregular memory).
- Check for clock throttling during the window (`gputrace nvidia` power/temp series).
- Check contention with concurrent streams or other processes on the same device.
- If the tail is inherent (rare large inputs), document it and stop.

## transfer-heavy — memcpy time rivals kernel time

**Evidence:** memcpy count and total vs kernel time.

- Batch small host↔device copies into fewer larger transfers.
- Use pinned (page-locked) host memory for recurring transfers.
- Keep data resident on-device across kernels instead of round-tripping per step.
- Prefer async copies overlapped with compute where dependencies allow.

## What NOT to do

- Do not apply two playbook actions between measurements; attribution dies.
- Do not conclude from a noisy-change verdict, however promising the median looks.
- Do not optimize a kernel below 5% of GPU time while a dominant kernel exists.
- Do not change workload semantics (different inputs, fewer iterations) to make numbers look better; that is measurement fraud, not optimization.
