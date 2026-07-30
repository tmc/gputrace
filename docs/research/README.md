# Research Notes

This directory contains reverse-engineering notes, format investigations, and implementation status documents that support `gputrace`.

These files are useful when extending parsers or validating Xcode parity, but they are not the primary user documentation for the CLI.

Start with:

- [TRACE_FORMAT.md](./TRACE_FORMAT.md) - capture bundle structure
- [RECORD_FORMATS.md](./RECORD_FORMATS.md) - MTSP record notes
- [BINARY_FORMAT_REFERENCE.md](./BINARY_FORMAT_REFERENCE.md) - counter binary format notes
- [FIELD_OFFSET_QUICK_REFERENCE.md](./FIELD_OFFSET_QUICK_REFERENCE.md) - field lookup shortcuts
- [PERF_VS_NONPERF_TRACES.md](./PERF_VS_NONPERF_TRACES.md) - capture mode differences
- [PERFCOUNTERS_STATUS.md](./PERFCOUNTERS_STATUS.md) - counter support status
- [PERFCOUNTER_FIELD_OFFSET_MAP.md](./PERFCOUNTER_FIELD_OFFSET_MAP.md) - detailed field offset discoveries
- [GPU_PROFILING_APIS_DISCOVERED.md](./GPU_PROFILING_APIS_DISCOVERED.md) - profiler API notes
- [INSTRUMENTS_TIMING_INVESTIGATION.md](./INSTRUMENTS_TIMING_INVESTIGATION.md) - timing investigation
- [CRASH_ANALYSIS_LIMITERS.md](./CRASH_ANALYSIS_LIMITERS.md) - crash analysis and limiters
- [COUNTER_FILE_MAPPING.md](./COUNTER_FILE_MAPPING.md) - counter file mapping
- [BUFFER_FEATURES_STATUS.md](./BUFFER_FEATURES_STATUS.md) - buffer features status
- [BUFFER_FILE_ANALYSIS.md](./BUFFER_FILE_ANALYSIS.md) - buffer file analysis
- [matching-xcode-gputools-parity.md](./matching-xcode-gputools-parity.md) - feature parity tracking

Private-framework binding notes:

- [GTMIO_SURFACE.md](./GTMIO_SURFACE.md) - `GTShaderProfiler.framework` class and selector surface
- [GTMIO_CAPABILITY_MATRIX.md](./GTMIO_CAPABILITY_MATRIX.md) - what each binding can supply
- [GTMIO_INIT_SMOKE.md](./GTMIO_INIT_SMOKE.md) - initializer smoke results
- [GTShaderProfiler_BINDING_GAPS.md](./GTShaderProfiler_BINDING_GAPS.md) - unbound selectors and known gaps
- [PRIVATE_BINDING_ERGONOMICS.md](./PRIVATE_BINDING_ERGONOMICS.md) - calling conventions for private bindings
- [UPSTREAM_OBJC_REQUESTS.md](./UPSTREAM_OBJC_REQUESTS.md) - requests against `github.com/tmc/apple`
- [XCODE_PARITY_LOOP.md](./XCODE_PARITY_LOOP.md) - the capture/compare loop behind `gputrace xcode-parity`

The tables in `matching-xcode-gputools-parity.md` are maintained by hand and
drift; `gputrace xcode-parity` reports live coverage for a given trace.
