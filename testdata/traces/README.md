Canonical trace fixtures live here.

The public repository keeps a minimal in-repo set:

- `01-single-encoder` is the baseline fixture. It is small, stable, and suitable for parser smoke tests and trace diff examples.
- `06-six-encoders` exercises multi-encoder parsing paths and shader/debug output on a still-small trace.

The larger research corpus is intentionally not checked in. Use `testdata/trace-generator` or your own local captures when you need broader scenario coverage, `.gpuprofiler_raw` profiler data, or shader source attribution.
