# Linux / NVIDIA workflows

The repo's agent skill (`skills/gputrace/SKILL.md`) covers Metal traces.
This reference extends it for NVIDIA hosts where capture is CUPTI-based.

## Inventory the host

```bash
gputrace devices            # backend capabilities
gputrace nvidia             # per-GPU status; --json for machines
```

## Capture kernel activity

Capture happens in-process: a CUPTI probe shared library is loaded by the
workload (ctypes from Python, or a Go c-shared import). It writes
newline-delimited JSON activity records:

```json
{"kind":"kernel","raw_symbol":"_ZN3mlx...","start_ns":...,"end_ns":...,"grid":"112x1x1","block":"32x8x1","registers":40}
{"kind":"memcpy","start_ns":...,"end_ns":...,"bytes":9216}
{"kind":"memset","start_ns":...,"end_ns":...,"bytes":4096}
```

Concurrently sample device counters (25 ms cadence works well):

```bash
nvml_sampler nvml_samples.jsonl 25ms &
```

Sample records carry `timestamp_ns`, `power_mw`, `gpu_util_pct`,
`mem_util_pct`, `temp_c`, `mem_used_bytes`.

## Analyze

```bash
gputrace analyze events.jsonl                       # findings + per-kernel table
gputrace analyze events.jsonl --suggest             # playbook actions with verify clauses
gputrace analyze events.jsonl --samples nvml_samples.jsonl   # join device state
gputrace analyze events.jsonl --json                # machine-readable report
```

Findings are ranked high/medium/low and pair evidence with hypotheses.
Bound classification (compute/memory/latency) derives from launch geometry;
report it as heuristic, not measured.

## Render for humans

```bash
gputrace cupti events.jsonl --samples nvml_samples.jsonl \
  --per-kernel-tracks -o trace.pftrace
```

Open at ui.perfetto.dev: one track per kernel with `--per-kernel-tracks`,
transfers on their own lane, NVML series as counter tracks.

## Close the loop

See the gputrace-optimize skill: `optimize run` -> apply one action ->
`optimize run` again -> `optimize compare`. Only separated-IQR verdicts
count as proven.
