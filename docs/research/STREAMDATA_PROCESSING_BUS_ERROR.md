# streamData processing bus error on parity-asymmetric-perfdata

[V] The framework's own streamData processing faults on one archive and
completes on another. This is a property of the archive, not of any gputrace
code: the fault is inside GTShaderProfiler, upstream of every binding gputrace
calls, and it reproduces with the counter path never reached.

## What faults

[V] `TestTimelineDrawDurations` dies with `signal: bus error` on:

```sh
A=/Users/tmc/gputrace-fixtures/parity-asymmetric-perfdata.gputrace/parity-asymmetric.gputrace.gpuprofiler_raw
GPUTRACE_PROCESS_STREAMDATA=$A/streamData GPUTRACE_MIO_SETUP_DATA_PATH=1 \
  go test -run TestTimelineDrawDurations -v ./internal/xcodebindings/
```

[V] It also faults with `GPUTRACE_MIO_SETUP_DATA_PATH` unset, so the directory-
backed load is not the trigger.

[V] The same test passes end to end, in 36s, on the recapture archive:

```sh
B=/Users/tmc/tmp/gputrace-recapture/qwen25-05b-python-metaldebug_tokens_2_to_3-perfdata.gputrace/qwen25-05b-python-metaldebug_tokens_2_to_3.gputrace.gpuprofiler_raw
GPUTRACE_PROCESS_STREAMDATA=$B/streamData GPUTRACE_MIO_SETUP_DATA_PATH=1 \
  go test -run TestTimelineDrawDurations -v -timeout 880s ./internal/xcodebindings/
```

That path is read-only. Do not write into it.

## Where it faults

[D] Between the processing batch and `mioData`, localized by which log lines
appear. The last output is the framework's own two banners:

```text
Gen: 16   Type: G16C    Rev: B1    Num Cores: 40    Num Override Cores: 0    Num GPs: 16
```

and the test's `model draws=... mioGPUTime=... shaderGPUTime=...` line never
prints. That line is the first statement after the six-selector batch
(`processStreamData`, `processShaderProfilerStreamData`,
`processTimelineStreamData`, the three `waitUntil*`) and the `mioData` read.
The localization is by log ordering, not by a symbolicated frame, which is why
it is [D] and not [V].

[V] Nothing in the counter path runs. `CounterSeries` is first called far
below, from `readTimelineCounter`, and the cost timeline is never opened. A
bus error here says nothing about the counter accessors.

## The lead

[V] The two archives have the same file inventory: 121 entries each, the same
`Counters_f_N.raw` / `Profiling_f_N.raw` / `Timeline_f_N.raw` / `streamData`
shape, and no zero-length files in either. They differ in size by roughly two
orders of magnitude.

| | faults | completes |
|---|---|---|
| archive | 102M | 9.7G |
| streamData | 2.0M | 241M |
| Counters_f_0.raw | 900K | 92M |
| Profiling_f_0.raw | 660K | 29M |
| Timeline_f_0.raw | 1.4M | 128M |

[?] So the fault is not a missing or empty file. A truncated record inside an
otherwise well-formed archive would fit what is seen, and so would a capture
the framework considers incomplete for a reason not visible in the inventory.
Neither has been established. The next step is a record-level walk of the
faulting `streamData` against `docs/STREAMDATA_FORMAT.md`, not another run of
the test.

## Why this is written down

The archive is a checked-out fixture that other work reaches for by default
because it is the only one under `/Users/tmc/gputrace-fixtures` carrying a
`.gpuprofiler_raw`. A test that dies with a bus error on it reads as a
regression in whatever was last changed. It is not one.
