Canonical trace fixtures live here.

The public repository keeps a small in-repo set:

- `01-single-encoder` is the baseline fixture. It is small, stable, and suitable for parser smoke tests and trace diff examples.
- `02-two-encoders`, `03-three-encoders`, `04-four-encoders`, and `06-six-encoders` exercise multi-encoder parsing paths.
- `known-invocations-1000` and `known-invocations-10000` cover dispatch-count scenarios.
- `low-alu-simple-add` and `high-alu-complex-math` cover ALU-utilization contrast cases.
- `low-occupancy-high-registers` and `high-occupancy-low-registers` cover occupancy/register-pressure contrast cases.

The larger research corpus is intentionally not checked in. Use `testdata/trace-generator` or your own local captures when you need broader scenario coverage, `.gpuprofiler_raw` profiler data, Xcode `Counters.csv` exports, or shader source attribution.
