# Research Notes

This directory contains reverse-engineering notes, format investigations, and implementation status documents that support `gputrace`.

These files are useful when extending parsers or validating Xcode parity, but they are not the primary user documentation for the CLI.

Start with:

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

Private-framework binding notes:

- [GTMIO_CAPABILITY_MATRIX.md](./GTMIO_CAPABILITY_MATRIX.md) - what each binding can supply
- [GTShaderProfiler_BINDING_GAPS.md](./GTShaderProfiler_BINDING_GAPS.md) - unbound selectors and known gaps
- [PRIVATE_BINDING_ERGONOMICS.md](./PRIVATE_BINDING_ERGONOMICS.md) - calling conventions for private bindings
- [XCODE_PARITY_LOOP.md](./XCODE_PARITY_LOOP.md) - the capture/compare loop behind `gputrace xcode-parity`

There is no hand-maintained Xcode parity table. Run `gputrace xcode-parity` on
a trace for live coverage, and `gputrace xcode-bindings --json` to probe which
private selectors are bound on the current host.
