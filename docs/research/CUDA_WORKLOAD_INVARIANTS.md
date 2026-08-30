# CUDA Workload Invariants & Empirical Verification (NVIDIA L4)

Status: verified empirically on NVIDIA L4 (GCP `g2-standard-4`, Ada Lovelace `sm_89`, Driver 580.173.02, CUDA 12.9 Toolkit + CUPTI 12.9).

## 1. Establishing Step Invariants on CUDA

To score completeness without relying on CUPTI's internal dropped-record counter (which reads zero when buffers are uncompleted or stranded), captures must be gated against a known workload invariant operation that executes deterministically per step.

### [V] Token Step Invariant (`harness_token_step`)

- **Harness:** `testdata/harness/cuda_token_harness.cu`
  - Defines a distinctively-named kernel `harness_token_step` invoked exactly once per loop iteration (representing token argmax / sampling), plus auxiliary compute (`harness_aux_compute`) and configurable weight staging (`HtoD` transfer).
  - Supports flags:
    - `--tokens N`: runs 1 prefill step + N decode steps (total $N+1$ launches).
    - `--no-staging`: skips HtoD transfer to verify absence reporting.
    - `--perturb`: injects a 25ms delay across iterations 48–80 to verify non-stationary trajectory gating.
    - `--drop-tail`: exits abruptly via `_exit(0)` before the final 20 iterations to verify tail-drop completeness gating.

### Empirical Gate Matrix Results on NVIDIA L4

1. **Clean Baseline Run (128 tokens, staged weights, steady trajectory):**
   ```bash
   $ gputrace capture -o /tmp/captures/clean.gpucapture -- /tmp/harness --tokens 128
   $ gputrace gate -t 128 -k harness_token_step /tmp/captures/clean.gpucapture
   clean.gpucapture: completeness ok    129/129 harness_token_step (want 129 = 128 tokens + 1 prefill)
   clean.gpucapture: staging: 1 HtoD transfers (1.0 MB)
   clean.gpucapture: stationarity ok    flat within 3%  [5.46 5.26 5.51 5.25 5.43 5.21 5.40 5.31]
   # Exit code: 0 [V]
   ```

2. **Absence Reporting (128 tokens, no staging):**
   ```bash
   $ gputrace capture -o /tmp/captures/nostage.gpucapture -- /tmp/harness --tokens 128 --no-staging
   $ gputrace gate -t 128 -k harness_token_step /tmp/captures/nostage.gpucapture
   nostage.gpucapture: completeness ok    129/129 harness_token_step (want 129 = 128 tokens + 1 prefill)
   nostage.gpucapture: staging: 0 HtoD transfers (0.0 MB recorded)
   nostage.gpucapture: stationarity ok    flat within 6%  [5.40 5.25 5.36 5.28 5.55 5.30 5.63 5.25]
   # Exit code: 0 [V]
   ```

3. **Stationarity Excursion Detection (perturbed mid-run):**
   ```bash
   $ gputrace capture -o /tmp/captures/perturbed.gpucapture -- /tmp/harness --tokens 128 --perturb
   $ gputrace gate -t 128 -k harness_token_step /tmp/captures/perturbed.gpucapture
   perturbed.gpucapture: completeness ok    129/129 harness_token_step (want 129 = 128 tokens + 1 prefill)
   perturbed.gpucapture: staging: 1 HtoD transfers (1.0 MB)
   perturbed.gpucapture: stationarity FAIL  376% excursion  [5.15 5.36 5.56 25.81 25.85 5.39 5.26 5.46]
   # Exit code: 1 [V]
   ```

4. **Completeness Loss Detection (tail drop):**
   ```bash
   $ gputrace capture -o /tmp/captures/dropped.gpucapture -- /tmp/harness --tokens 128 --drop-tail
   $ gputrace gate -t 128 -k harness_token_step /tmp/captures/dropped.gpucapture
   dropped.gpucapture: completeness FAIL  108/129 harness_token_step (16% missing)
   dropped.gpucapture: staging: 1 HtoD transfers (1.0 MB)
   dropped.gpucapture: stationarity ok    flat within 2%  [5.41 5.33 5.46 5.25 5.37 5.33]
   # Exit code: 1 [V]
   ```

5. **Not Evaluable Symbol:**
   ```bash
   $ gputrace gate -t 128 -k non_existent_symbol /tmp/captures/clean.gpucapture
   clean.gpucapture: invariant symbol "non_existent_symbol" matched 0 dispatches/kernels: cannot evaluate
   clean.gpucapture: staging: 1 HtoD transfers (1.0 MB)
   clean.gpucapture: stationarity UNSCORED (need >= 2 timestamps)
   # Exit code: 2 [V]
   ```

## 2. NVIDIA L4 (sm_89) Hardware & Profile Observations

- **Clock Synchronization Alignment:**
  - Emitted `clock_sync` event:
    `{"kind":"clock_sync","unix_ns":1788054620381609315,"cupti_ns":1788054620381608547}`
  - Delta is 768 ns, confirming that `CLOCK_REALTIME` and `cuptiGetTimestamp` share the same timebase on Ada Lovelace as on GB10.
- **Launch Latency Dynamics:**
  - Launch latency statistics measured via `gputrace analyze`:
    - `queued -> submitted`: mean 4.2 µs, p50 4.0 µs, p95 6.3 µs
    - `submitted -> start`: mean 319 ns, p50 320 ns, p95 352 ns
    - `queued -> start`: mean 4.6 µs, p50 4.3 µs, p95 6.6 µs
  - Timestamps satisfied monotonic invariant $queued \le submitted \le start$.
- **NVML Sampling:**
  - `nvml_sampler` recorded P-states (`P8` -> `P0`), SM clocks (210 MHz idle -> 2040 MHz boost), memory clocks (405 MHz -> 6251 MHz), and cumulative energy in mJ.
