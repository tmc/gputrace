# Linux / NVIDIA workflows

The repo's agent skill (`skills/gputrace/SKILL.md`) covers Metal traces.
This reference covers NVIDIA hosts, where capture is CUPTI-based and the
analysis/export loop runs natively.

## Inventory the host

```bash
gputrace devices            # backend capabilities (cuda / metal)
gputrace nvidia             # per-GPU status; --json for machines
```

## Capture a workload natively

```bash
gputrace capture -o run.gpucapture --samples -- <workload command...>
```

This compiles (once, then caches) a small CUPTI shim and preloads it into
the target. The bundle contains:

- `events.jsonl` — kernel/memcpy/memset activity with per-launch timing,
  grid/block geometry, registers, device/stream/correlation IDs
- `nvml_samples.jsonl` — concurrent power/util/temp/memory series
  (`--samples`; interval via `--sample-interval`)
- `meta.json` — provenance: exact command, timestamps, versions

Constraints: targets must link CUDA dynamically (`-cudart=shared` for nvcc;
Python/MLX/JAX are dynamic by default). Statically-linked CUDA runtimes
bypass interposition and yield an empty events file.

## Analyze

```bash
gputrace analyze run.gpucapture                       # findings + kernel table
gputrace analyze run.gpucapture --suggest             # playbook actions
gputrace analyze run.gpucapture --json                # machine-readable report
```

Findings rank high/medium/low and pair evidence lines with hypotheses.
Bound classification derives from launch geometry — report it as heuristic,
not counter-measured.

## Render for humans

```bash
gputrace cupti run.gpucapture --per-kernel-tracks -o trace.pftrace
```

Open at ui.perfetto.dev: one track per distinct kernel, transfers on their
own lane, NVML series as counter tracks.

## Close the loop

```bash
gputrace optimize run --iterations 7 -o base.json -- <workload>
# apply ONE playbook action
gputrace optimize run --iterations 7 -o variant.json -- <workload>
gputrace optimize compare base.json variant.json
```

Verdict rules: `improved`/`regressed` require separated IQRs;
`noisy-change` means unproven — rerun with more iterations, never claim it.
See docs/OPTIMIZATION_PLAYBOOK.md for the action catalog and guardrails.
