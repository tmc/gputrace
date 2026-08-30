# Residency A/B: python mlx-lm vs mlx-go, Qwen3-0.6B-bf16 on Apple M4 Max

Date: 2026-08-29. Both arms captured back to back in one session on
m4max.local (same-host confirmed by `gate --compare` provenance).
16-token greedy decode, prompt "Hello", temp 0. Bundles (not in git):
`~/tmp/resid-python.gputrace`, `~/tmp/resid-go.gputrace`, with
GT_TIMING_OUT sidecars.

This is the Metal rewrite of the CUDA staging check that found the 0-vs-310
HtoD gap. Observation-only: structural counts and allocation records; no
performance claims (capture overhead makes throughput meaningless here).

## Caveats — read first

- [?] MLX version parity NOT established: the python arm runs mlx 0.31.0;
  the Go arm links its own mlx build whose version was not extracted.
  Per the campaign stop conditions, every delta below is descriptive.
- [?] Token-level output parity not verified (both arms emit coherent
  `<think>` text at these settings; IDs not compared).
- The Go arm's live trajectory declines monotonically (334→255 ms/CB,
  16.1% excursion → stationarity FAIL): the captured window includes
  settling. Structural counts are load-independent and unaffected.

## Result 1 — storage mode does NOT separate the runtimes [V]

Every buffer in both arms is created shared; neither runtime places any
buffer private, managed, or memoryless:

| arm | buffer creations | storage modes |
|-----|-----------------|----------------|
| python mlx-lm | 2,177 | 2,177 shared |
| mlx-go        | 3,180 | 3,180 shared |

(Decoded from Culul+0x18, see METAL_STORAGE_MODE_RECORDS.md.)

Per the playbook: this is a checked assumption now, not an unchecked one.
The `weightLoadStream` comment's premise — unified memory makes the CUDA
staging failure mode inapplicable on Metal — survives its first direct
observation at the allocation-placement level. Unobserved residency
channels remain: purgeable state, residency sets, heap placement details,
and blit activity (absent from raw captures; needs profile-replay
streamData or a two-arm profiled export).

## Result 2 — the Go arm allocates 46% more buffers [V]

3,180 vs 2,177 buffer-creation records for the same 16-token decode.
All shared, so this is allocation churn, not placement. Candidate line of
attack for the resource/lifetime attribution backlog.

## Result 3 — structural kernel deltas (gate: 16/16 argmax both arms)

Totals near parity: 9,860 (P) vs 9,758 (G) dispatches; 438 vs 459 CBs.
`gputrace diff --allow-cross-environment` headline rows:

- **Fusion gap (SwiGLU):** P runs a fused sigmoid·multiply kernel 503×;
  G runs an unfused pair (sigmoid-broadcast-multiply 476× + separate
  `vv_Multiplybfloat16` 476×).
- **Sampler precision divergence:** `looped_logsumexp_bfloat16` (P) vs
  `looped_logsumexp_float32` (G), 16× each — the two runtimes compute the
  sampling logsumexp in different precision.
- **GEMM selection:** `steel_gemm_splitk_*` 110× only in P; G takes a
  different path (partially `archive:`-named, 197×).
- gemv/rmsnorm counts differ ~6% (3,332 vs 3,136; 2,031 vs 1,921),
  consistent with the fusion/selection differences above.

## Gate verdicts

```
resid-python: completeness ok 16/16 argmax · 2177 shared · live stationarity flat 6.0%
resid-go:     completeness ok 16/16 argmax · 3180 shared · live stationarity FAIL 16.1% (declining)
```
