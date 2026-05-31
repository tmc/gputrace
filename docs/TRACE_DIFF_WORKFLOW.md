# Trace Diff Workflow

`gputrace diff` compares two profiled traces at dispatch granularity and explains where GPU time changed.

## Use Cases

- Which kernels got slower
- Which compute encoder the slowdown is in
- Whether spikes are one-off or repeated
- Whether extra unnamed dispatches changed structure
- Whether delta is work cost or command stream overhead

## Inputs

`gputrace diff` expects traces with profiler data (`streamData`), typically `-perfdata.gputrace` bundles.

## Quick Start

```bash
gputrace diff go_decode-perfdata.gputrace py_decode-perfdata.gputrace --explain
```

Auto-discover newest Go/Python pair from a benchmark directory:

```bash
gputrace diff --bench-dir ~/bench-traces --quick
```

Explicit overrides:

```bash
gputrace diff --left /path/to/go-perfdata.gputrace --right /path/to/py-perfdata.gputrace
```

## Views

```bash
# Function-level contributors
gputrace diff A.gputrace B.gputrace --by function

# Encoder-level deltas
gputrace diff A.gputrace B.gputrace --by encoder

# Pipeline-level deltas
gputrace diff A.gputrace B.gputrace --by pipeline

# Dispatch outliers with source indices
gputrace diff A.gputrace B.gputrace --by dispatch --limit 50 --min-delta-us 30

# Matched dispatch rows
gputrace diff A.gputrace B.gputrace --by matches

# Timeline spike windows
gputrace diff A.gputrace B.gputrace --by timeline-windows

# Inserted/deleted/unmatched dispatches
gputrace diff A.gputrace B.gputrace --by unmatched --show-unmatched

# Per-occurrence alignment rows
gputrace diff A.gputrace B.gputrace --by occurrences --show-occurrences

# Encoder dominance triage
gputrace diff A.gputrace B.gputrace --by-encoder
```

## Machine-Readable Output

```bash
# Stable schema with schema_version
gputrace diff A.gputrace B.gputrace --json > diff.json

# CSV for one view
gputrace diff A.gputrace B.gputrace --csv --by function > function_deltas.csv

# Shareable markdown report for PR/issues
gputrace diff A.gputrace B.gputrace --md-out /tmp/gputrace_diff_report.md
```

The JSON object follows `internal/difftrace.Report`. Top-level keys include:

- `schema_version`
- `trace_a_path`
- `trace_b_path`
- `summary`
- `top_function_deltas`
- `top_dispatch_outliers`
- `encoder_deltas`
- `encoder_reports`
- `pipeline_deltas`
- `unnamed_dispatch_deltas`
- `timeline_spike_windows`
- `occurrence_matches`
- `matched_pairs` (contains exact `source_index_a` and `source_index_b` for every match)
- `unmatched`
- `warnings`

## Interpretation Heuristics

Summary includes one likely-cause line:

- `one-time warmup/growth spike`
- `repeated per-step slowdown`
- `structural command stream overhead`

## Example (Go decode vs Python decode)

```text
Total GPU delta (A-B): +2483us  |  A=31838us B=29355us
Dispatch delta (A-B): +96       |  matched=+1400us unmatched=+1083us
Likely cause: structural command stream overhead
```

Top contributors include expected kernels such as:

- `affine_qmm_t_float16_t_gs_64_b_4_alN_true_batch_0`
- `gg2_copyfloat16float16`
- `gather_frontfloat16_int32_int_2`
- `g2_Addfloat16`

Spike windows cluster in encoder `2` for this pair.
