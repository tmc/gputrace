# Research Notes

This directory contains reverse-engineering notes, format investigations, and implementation status documents that support `gputrace`.

These files are useful when extending parsers or validating Xcode parity, but they are not the primary user documentation for the CLI.

Start with:

- [HEADLESS_PROFILING_AND_G16_COUNTERS.md](./HEADLESS_PROFILING_AND_G16_COUNTERS.md) - the verified MTLReplayer headless replay/profile recipe, its option surface and dispatch precedence, and why G16 ships no derived-counter plist (read this before hunting for one)
- [AGXPS_COUNTER_ORACLE.md](./AGXPS_COUNTER_ORACLE.md) - exact-name counter oracle, provenance, coverage, and the raw-counter-name-only join rule
- [XDIC_INDEX_FORMAT.md](./XDIC_INDEX_FORMAT.md) - `index` (xdic) format, API-call markers, timing investigation
- [RECORD_FORMATS.md](./RECORD_FORMATS.md) - MTSP record notes
- [BINARY_FORMAT_REFERENCE.md](./BINARY_FORMAT_REFERENCE.md) - counter binary format notes
- [FIELD_OFFSET_QUICK_REFERENCE.md](./FIELD_OFFSET_QUICK_REFERENCE.md) - field lookup shortcuts
- [PERF_VS_NONPERF_TRACES.md](./PERF_VS_NONPERF_TRACES.md) - capture mode differences
- [PERFCOUNTERS_REFERENCE.md](./PERFCOUNTERS_REFERENCE.md) - counter record layout, field offsets, metric catalog, parsing status
- [GPU_PROFILING_APIS_DISCOVERED.md](./GPU_PROFILING_APIS_DISCOVERED.md) - profiler API notes
- [INSTRUMENTS_TIMING_INVESTIGATION.md](./INSTRUMENTS_TIMING_INVESTIGATION.md) - timing investigation
- [CRASH_ANALYSIS_LIMITERS.md](./CRASH_ANALYSIS_LIMITERS.md) - crash analysis and limiters
- [COUNTER_FILE_MAPPING.md](./COUNTER_FILE_MAPPING.md) - counter file mapping
- [BUFFER_FEATURES_STATUS.md](./BUFFER_FEATURES_STATUS.md) - buffer features status
- [BUFFER_FILE_ANALYSIS.md](./BUFFER_FILE_ANALYSIS.md) - buffer file analysis

Linux/NVIDIA notes:

- [LUMINAL_TRACING_COMPARISON.md](./LUMINAL_TRACING_COMPARISON.md) - luminal's first-party CUDA graph tracing (event-record nodes, tracing-crate spans, Perfetto merge) vs. our CUPTI observer approach, and what to adopt from it

Private-framework binding notes:

- [agxps-signatures.yaml](./agxps-signatures.yaml) - the verified agxps C signatures, element widths, and return-register classes, each with how it was established. The tmc/apple agxps bindings are name-derived guesses and roughly three in six were wrong, so treat any export absent from this file as unsourced.
- [GTMIO_CAPABILITY_MATRIX.md](./GTMIO_CAPABILITY_MATRIX.md) - what each binding can supply
- [GTShaderProfiler_BINDING_GAPS.md](./GTShaderProfiler_BINDING_GAPS.md) - unbound selectors and known gaps
- [PRIVATE_BINDING_ERGONOMICS.md](./PRIVATE_BINDING_ERGONOMICS.md) - calling conventions for private bindings
- [XCODE_PARITY_LOOP.md](./XCODE_PARITY_LOOP.md) - the capture/compare loop behind `gputrace xcode-parity`

There is no hand-maintained Xcode parity table. Run `gputrace xcode-parity` on
a trace for live coverage, and `gputrace xcode-bindings --json` to probe which
private selectors are bound on the current host.
