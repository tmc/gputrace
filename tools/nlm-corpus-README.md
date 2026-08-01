# gputrace NLM corpus — 2026-08-01

Everything gputrace has established about Apple Metal GPU trace archives, plus
the probes that established it and their raw output. Assembled so a NotebookLM
notebook can be asked how to bring our Perfetto output up to Xcode's view.

## Layout

- `probes/` — the probe sources. These are Go manual tests, env-gated, that
  drive `GTShaderProfiler.framework` through purego. They are the only way most
  of this data was obtained.
- `probe-output/` — what those probes actually printed, against the capture in
  `CAPTURE_PATH.txt`. This is measured data, not documentation.
- `docs/` — the format documentation. Every field carries a confidence marker:
  `[V]` verified, `[D]` derived by a check that could fail, `[?]` inferred from
  one archive. **The markers matter — check one before relying on a field.**
- `collab/` — working notes exchanged between agents. Most findings appear here
  first, and some were later falsified. Treat as a lab notebook, not as truth.
- `oracle/` — Xcode counter-tab exports, 23 encoders x 274 columns. This is the
  reference for what Xcode's view actually contains.
- `perfetto/timeline.json` — our current Perfetto output, the thing to improve.
- `session-history-*.jsonl` — full working transcript, including the reasoning
  behind each conclusion and each retraction.

## How to read this corpus

These formats are undocumented and reverse-engineered. The corpus contains
claims at very different confidence levels, and several documents contradict
each other because later measurement overturned earlier assumption. Two known
examples, both recorded rather than quietly deleted:

- `docs/research/COUNTER_FILE_MAPPING.md` is **falsified**. It claimed
  `Counters_f_12.raw` is "ALU Utilization"; the file holds 137 counters.
- The counter stream's timebase is **open**. Applying the archive's own offset
  leaves a 42.6 s residual against the command-buffer clock. It has been
  wrongly closed once already.

The dominant failure mode on this project is a plausible reading that is wrong
and fails silently. Prefer `probe-output/` over any prose describing it.

## The question being asked of this corpus

Our Perfetto trace should be as good as Xcode's render/view. What does Xcode's
timeline show that ours does not, what track structure would express GPU work
better, and which of the 274 oracle columns belong in a timeline at all versus
in a table?
