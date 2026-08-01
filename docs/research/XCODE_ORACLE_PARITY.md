# The 2026-07-31 Xcode oracle export

Where the counter-tab oracle came from, what it measured, and why it can no
longer be scored per encoder. Recorded because the underlying captures are
gone and the result is not reproducible.

## Status: unjoinable [V]

Twelve Xcode counter-tab TSV exports live at
`~/tmp/gputrace-xcode-oracle-20260731` (23 encoders x 274 columns,
deterministic across repeat exports). They cannot be scored against gputrace,
and the reason is worth keeping rather than rediscovering.

Coverage of those exports, measured 2026-08-01:

- 205 distinct columns
- 115 carry NO SIGNAL for this workload (graphics counters on a compute capture)
- 7 are oracle-suspect
- **83 columns carry real signal**; gputrace reproduces 3

The 3 vs 83 figure is a *column* count and is sound. What is not computable is a
per-encoder value match, because the join fails outright:

    oracle encoder keys      546 / 1501 / 2615 / 3851   (23 encoders)
    surviving trace keys     581 / 1593 / 2845 / 4097   (21 encoders)
    overlap                  0

The keys are `encoderInfoData` cumulative end offsets, so zero overlap means a
different capture, not a parsing difference. The source bundle for the oracle
was a Go-side profiled export
(`qwen25-05b-static_tokens_2_to_3-wperfdata.gputrace` or the `-rep1-perfdata2`
sibling). Both are gone: confirmed absent 2026-08-01 by checking every bundle
on the machine with `gputrace stats` rather than by filename. The surviving
`*_tokens_2_to_3.gputrace` bundles are the raw siblings, reporting
`Profiler Data: No` and no encoder counts, so they cannot substitute.

**Do not re-export the counter tabs from a different capture to fill this gap.**
That silently swaps the workload underneath the numbers, producing a match rate
that looks measured and is not. No number is the correct outcome here.

The bundles were lost to a routine reboot, taking ~60 GB and ~52 minutes of
replay with them, and this is the second parity question blocked by that same
loss. A slim timing-only export would both survive and be cheap enough to keep
many runs of, which would additionally give every single-capture timing claim in
this workstream a measurable variance instead of an n of 1.
