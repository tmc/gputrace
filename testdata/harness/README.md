# CUDA Workload Invariant Test Harness

`cuda_token_harness.cu` is a standalone CUDA program designed to produce ground-truth execution patterns for testing and verifying `gputrace capture` and `gputrace gate` on CUDA targets.

## Building

Targeting NVIDIA L4 / Ada Lovelace (`sm_89`):
```bash
nvcc -arch=sm_89 -O2 -o harness cuda_token_harness.cu
```

Targeting NVIDIA GB10 / Blackwell / Hopper (`sm_90` / `sm_100`):
```bash
nvcc -arch=native -O2 -o harness cuda_token_harness.cu
```

## Usage & Test Modes

The harness executes 1 prefill step followed by $N$ decode steps (total $N+1$ launches of `harness_token_step`).

1. **Standard Baseline ($N$ tokens, staged weights, steady execution):**
   ```bash
   ./harness --tokens 128
   ```

2. **Absence Testing (no HtoD weight staging):**
   ```bash
   ./harness --tokens 128 --no-staging
   ```

3. **Non-Stationary Perturbation (injects 25ms delay across middle iterations):**
   ```bash
   ./harness --tokens 128 --perturb
   ```

4. **Incomplete / Tail-Drop (abrupt exit before loop completion):**
   ```bash
   ./harness --tokens 128 --drop-tail
   ```
