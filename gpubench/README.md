# gpubench

Package `github.com/tmc/gputrace/gpubench` integrates retained GPU traces with
Go benchmarks without depending on the parent gputrace module. Its `go.mod` has
no requirements; it communicates with an installed `gputrace` executable using
the stable `gputrace bench --format json` contract.

```go
client := gpubench.Client{} // finds gputrace on PATH
report, err := client.Report(ctx, "decode-perfdata.gputrace", gpubench.ReportOptions{
	Work: &gpubench.Work{Count: 32, Unit: "token"},
})
if err != nil {
	b.Fatal(err)
}
if err := report.ReportMetrics(b); err != nil {
	b.Fatal(err)
}
```

The module also exposes `Client.Capture` and `Client.Profile`. Profiling is
headless; set `ProfileOptions.Wait` to queue separate, non-overlapping
MTLReplayer jobs. This does not remove command-buffer or encoder overlap inside
one workload.

Capture and profiling should happen outside the untraced statistical benchmark
timer. A trace is one evidence observation. Metrics remain `/trace` unless the
caller declares a positive work count and one of `op`, `token`, `step`, or
`byte`.

Every collection and analysis operation executes the configured `gputrace`
binary. Set `Client.Executable` to pin it; the package never imports or links
the parent module. JSON decoding and `ReportMetric` emission remain local.

Benchmarks should skip only when the optional executable is unavailable:

```go
if err := client.Available(); err != nil {
	if errors.Is(err, gpubench.ErrUnavailable) {
		b.Skip(err)
	}
	b.Fatal(err)
}
report, err := client.Report(ctx, tracePath, opts)
if err != nil {
	b.Fatal(err) // corrupt traces and tool failures are not skips
}
```
