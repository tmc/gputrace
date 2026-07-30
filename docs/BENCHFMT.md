# Go benchmark output

`stats`, `profiler`, and `timing` accept `--benchfmt`. The output can be read
directly by `golang.org/x/perf/benchstat` and `golang.org/x/perf/benchfmt`.
Each trace is one observation, so every benchmark line uses one iteration.
Repeated captures should be concatenated, not averaged before analysis.

```sh
gputrace profiler trace.gputrace --benchfmt \
  --bench-config runtime=go \
  --bench-config model=Qwen2.5-0.5B \
  --bench-config prompt-tokens=30 \
  --bench-config capture-range=2:4 \
  --bench-config compile-mode=compiled \
  --bench-config cache-mode=warm \
  --bench-config mlx-version=0.32.0 > go.txt

benchstat -ignore trace-uuid go.txt python.txt
```

The command infers `runtime`, `model`, `capture-range`, and `cache-mode` from
common trace path names. It prints `unknown` when a value is not stored in the
trace. Use repeatable `--bench-config key=value` flags to replace inferred
values. The trace UUID and payload class are read from the bundle.
Other lowercase experiment keys are accepted and emitted in sorted order.

The benchmark line uses separate units for values with different meanings:

| Unit | Meaning |
| --- | --- |
| `dispatch_span_ns/op` | Span of cumulative profiler dispatch offsets |
| `cb_active_ns/op` | Sum of command-buffer active ranges |
| `cb_wall_ns/op` | Wall span covered by command buffers |
| `effective_gpu_ns/op` | Xcode APSTimelineData effective GPU time |
| `profiler_sample_cost_percent` | Per-function share of USC statistical profiler samples |
| `profiler_cost_samples/op` | USC samples underlying statistical execution-cost attribution |
| `gprwcntr_samples/op` | GPRWCNTR samples attached to dispatch records |
| `dispatches/op` | GPU dispatch count |
| `command-buffers/op` | Command-buffer count |
| `encoders/op` | Compute-encoder count |

The span units are not aliases for active or effective GPU time.
`profiler_cost_samples/op` is emitted by `profiler`, which reads the
`Profiling_f_*.raw` execution-cost records. `stats` and `timing` do not scan
those records. The same command emits one stable function-specific benchmark
row with `profiler_sample_cost_percent` for each attributed function.
GPRWCNTR samples remain a separate unit.

Measured timing units are emitted only when profiler `streamData` supplies
them. `timing --benchfmt` fails if measured profiler data is absent. `stats
--benchfmt` may still emit structural counts for a raw trace, but it does not
emit extracted or synthetic timing. Unavailable metrics are omitted rather
than written as zero. The `payload` config remains `profiler-only` for bundles
without the raw capture and resources.

`trace-uuid` is intentionally a config line for artifact provenance. Because
it differs between independent captures, pass `-ignore trace-uuid` to
`benchstat` when UUID is not a comparison dimension.

`diff` does not support `--benchfmt`: benchstat compares repeated observations,
while `diff` already contains a precomputed two-trace delta.
