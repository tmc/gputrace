# Go benchmark output

`bench`, `stats`, `profiler`, and `timing` produce output that can be read
directly by `golang.org/x/perf/benchstat` and `golang.org/x/perf/benchfmt`.
Each trace is one observation, so every benchmark line uses one iteration.
Repeated captures should be concatenated, not averaged before analysis.

Use `bench` for new integrations. It emits trace totals by default and requires
an explicit denominator before producing per-work units:

```sh
gputrace bench trace.gputrace --format benchfmt \
  --bench-name BenchmarkDecode \
  --bench-work 32 --bench-work-unit token \
  --bench-config arm=candidate > gpu.bench
```

Without `--bench-work`, the same command writes units ending in `/trace`.
Supported work units are `op`, `token`, `step`, and `byte`. A missing, zero, or
unsupported denominator is rejected rather than inferred from the trace name.

```sh
gputrace profiler trace.gputrace --benchfmt \
  --bench-config runtime=go \
  --bench-config model=Qwen2.5-0.5B \
  --bench-config prompt-tokens=30 \
  --bench-config capture-range=2:4 \
  --bench-config compile-mode=compiled \
  --bench-config cache-mode=warm \
  --bench-config mlx-version=0.32.0 > go.txt

benchstat -col runtime -ignore trace-uuid go.txt python.txt
```

The command infers `runtime`, `model`, `capture-range`, and `cache-mode` from
common trace path names. It prints `unknown` when a value is not stored in the
trace. Use repeatable `--bench-config key=value` flags to replace inferred
values. The trace UUID and payload class are read from the bundle.
Other lowercase experiment keys are accepted and emitted in sorted order.
Use `-col runtime` when comparing runtime values in one table; otherwise
benchstat treats differing file configuration as separate tables.

The benchmark line uses separate units for values with different meanings:

| Unit | Meaning |
| --- | --- |
| `dispatch_span_ns/trace` | Span of cumulative profiler dispatch offsets |
| `cb_active_ns/trace` | Sum of command-buffer active ranges |
| `cb_wall_ns/trace` | Wall span covered by command buffers |
| `effective_gpu_ns/trace` | Xcode APSTimelineData effective GPU time |
| `profiler_sample_cost_percent` | Per-function share of USC statistical profiler samples |
| `profiler_cost_samples/trace` | USC samples underlying statistical execution-cost attribution |
| `gprwcntr_samples/trace` | GPRWCNTR samples attached to dispatch records |
| `dispatches/trace` | GPU dispatch count |
| `command-buffers/trace` | Command-buffer count |
| `encoders/trace` | Compute-encoder count |

The span units are not aliases for active or effective GPU time.
`profiler_cost_samples/trace` is emitted by `profiler`, which reads the
`Profiling_f_*.raw` execution-cost records. `stats` and `timing` do not scan
those records. The same command emits one stable function-specific benchmark
row with `profiler_sample_cost_percent` for each attributed function. These
rows use benchfmt's name-based configuration form,
`BenchmarkGPUTrace/function=<name>-1`, so benchstat can project the `function`
field.
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

## Go package

Package `github.com/tmc/gputrace/tracebench` exposes the same sectioned report
without parsing command output. `Analyze` keeps structural and measured timing
evidence independent, `WriteJSON` and `WriteBenchfmt` provide stable encodings,
and `Report.ReportMetrics` writes directly through `testing.B.ReportMetric`.

```go
report, err := tracebench.Analyze(path, tracebench.Options{
	Work: &tracebench.Work{Count: uint64(b.N), Unit: "op"},
})
if err != nil {
	b.Fatal(err)
}
if err := report.ReportMetrics(b); err != nil {
	b.Fatal(err)
}
```

The caller owns the meaning of `b.N` and must ensure the trace contains exactly
that work. Capture and profiler arms should run outside the ordinary benchmark
timer; they are evidence observations, not untraced throughput samples.

For a complete Go workflow, use the public packages independently:

```go
tracePath, err := capture.Run(ctx, capture.Options{Output: "run.gputrace"}, argv...)
if err != nil {
	return err
}
profiled, err := profilereplay.Profile(ctx, tracePath, profilereplay.Options{
	Embed: true,
	Wait:  true,
})
if err != nil {
	return err
}
report, err := tracebench.Analyze(profiled, tracebench.Options{
	Work: &tracebench.Work{Count: 32, Unit: "token"},
})
```

`profilereplay.Options.Wait` serializes separate MTLReplayer processes. It does
not alter overlap among command buffers or encoders within the replayed trace.
Keep this capture/profile path outside the untraced statistical benchmark arm.

## Dependency-free client module

`github.com/tmc/gputrace/gpubench` is a nested module for benchmark suites that
should not inherit gputrace's parser and private-framework dependencies. It has
no module requirements and invokes a configured `gputrace` executable:

```go
client := gpubench.Client{Executable: "/path/to/gputrace"}
report, err := client.Analyze(ctx, profiled, gpubench.AnalyzeOptions{
	Work: &gpubench.Work{Count: 32, Unit: "token"},
})
if err != nil {
	return err
}
return report.ReportMetrics(b)
```

Use the parent `tracebench` package when in-process parsing is worth the larger
dependency graph. Use `gpubench` when process isolation and a stdlib-only Go
dependency are preferable. Both consume the same versioned report schema and
preserve the same denominator and timing-source rules.
