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

## The counter stream does not sit in either published timebase [V]

Attributing the 137 counter series per encoder requires joining the
`Counters_f_*.raw` sample clock to `encoderInfoData` offsets. It does not
currently join, and the reason is a measured disagreement rather than a missing
field.

For `qwen25-05b-python-producer-tokens1-3-perfdata.gputrace`, the archive
publishes its own timebase record:

    absoluteTime    5044475728398
    continuousTime  5181167935604
    offset          136692207206     (continuous - absolute)

    sysTS[0]        5180152293797    span 62161289 ticks = 2590.054 ms
    cb[0].start     5044483113510    span 71500426 ticks = 2979.184 ms

Applying the archive's own offset to the first counter sample:

    sysTS[0] - offset  = 5043460086591
    cb[0].start - that = 1023026919 ticks = 42.626 s   (@24 MHz)

The clock rate is not the error: the mean sample period is 909.2 ticks =
37.883 us at 24 MHz, self-consistent with the observed sample count over the
span.

The two windows have comparable spans (2590 ms vs 2979 ms), so they describe the
same ~3 s of activity. Two such windows cannot be 42.6 s apart, so the transform
is wrong rather than the data: `sysTS` is not anchored to `continuousTime` by
subtracting `continuousTime - absoluteTime`.

**Do not close this as "no shared anchor exists."** That conclusion was reached
once by comparing `cb[0].start` to the profiler ring start (they agree to
49.8 us) — but both of those come from `APSTimelineData` and were already known
to share a clock, so the comparison says nothing about the counter stream. The
open question is which of {domain, sign, epoch field} is misidentified.

Reproduce both sides with `TestStreamDataTimebaseProbe`
(`GPUTRACE_PROBE_STREAMDATA`) and `TestCounterFileParse`
(`GPUTRACE_PROBE_COUNTERS`).
