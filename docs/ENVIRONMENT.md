# Environment Variables

gputrace works without environment configuration for ordinary analysis
commands. These variables adjust local development, Xcode automation, and
source lookup behavior:

| Variable | Effect |
| --- | --- |
| `GPUTRACE_APS_PRELOAD_BUNDLE` | Preloads `AGXGPURawCounterBundle` before APS source-group discovery, so a discovery failure reports the real reason. |
| `GPUTRACE_DEBUG` | Enables extra debug logging from shader metrics helpers. |
| `GPUTRACE_MIO_MCA` | Enables MCA register readback for pipelines in `streamData` model integration. |
| `GPUTRACE_MIO_SETUP_DATA_PATH` | Enables `_setupDataPath` on `GTShaderProfilerStreamData`, resolving sibling `.gpuprofiler_raw` files for scalar cost totals. |
| `GPUTRACE_MIO_TIMELINE_DATA` | Enables serialized `costTimeline` reconstruction via `GTMioKVDataStore` and `GTMioTraceTimelineData`, including automatic sibling-data setup. |
| `GPUTRACE_MIO_TRACE_TRACKS` | Enables top-level track model generation via `GTMioTraceDataHelper`, including automatic sibling-data setup. |
| `GPUTRACE_MIO_USC_CLIQUES` | Enables USC clique summary readback, including automatic sibling-data setup. |
| `GPUTRACE_PROCESS_STREAMDATA` | Specifies a `.gpuprofiler_raw/streamData` file for opt-in streamData model integration tests. |
| `GPUTRACE_SHADER_SEARCH_PATHS` | Adds platform-specific path-list entries to shader source lookup before built-in search paths. |
| `GPUTRACE_SKIP_MACGO` | Skips macgo app-bundle setup for capture and Xcode profiler automation, using current process identity instead. |
| `GPUTRACE_XCODE_APP` | Selects the app name passed to `open -a` when opening traces in Xcode automation. |
| `GPUTRACE_XCODE_DEVELOPER_DIR` | Specifies an explicit Xcode Developer directory override for private `GTShaderProfiler.framework` loading. |

Test-only environment variables are documented in
[`TESTING.md`](./TESTING.md).
