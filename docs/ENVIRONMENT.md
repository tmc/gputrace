# Environment Variables

gputrace works without environment configuration for ordinary analysis
commands. These variables adjust local development, Xcode automation, and
source lookup behavior:

| Variable | Effect |
| --- | --- |
| `APPLE_GTSHADERPROFILER_FRAMEWORK_PATH` | Overrides generated `GTShaderProfiler` loading before process initialization. Accepts an OS path-list of binaries, framework bundles, or directories containing `GTShaderProfiler.framework`. |
| `DEVELOPER_DIR` | Selects the Xcode tools directory used for private-framework lookup before falling back to `xcode-select`. |
| `GPUTRACE_APS_PRELOAD_BUNDLE` | Preloads `AGXGPURawCounterBundle` before APS source-group discovery, so a discovery failure reports the real reason. |
| `GPUTRACE_APP_EVENTS` | Set by `gputrace capture` on the target process: path of a JSONL sidecar where the target may append span/instant records (`{"kind":"span","name",...,"start_ns","end_ns","labels":{...}}`). Records are merged into the bundle at capture close; timestamps must share the CUPTI clock (use `cupti.GetTimestamp` or stamp after first CUDA use). Absent file is fine — declaring nothing is normal. |
| `GPUTRACE_CAPTURE_API` | Set by `gputrace capture --api` on the target process: enables host-side runtime/driver API call records in the shim. |
| `GPUTRACE_CAPTURE_OUT` | Set by `gputrace capture` on the target process: output path for the shim's activity JSONL. |
| `GPUTRACE_CAPTURE_DEBUG` | Enables extra debug logging from the capture shim. |
| `GPUTRACE_MIO_MCA` | Enables MCA register readback for pipelines in `streamData` model integration. |
| `GPUTRACE_MIO_SETUP_DATA_PATH` | Enables `_setupDataPath` on `GTShaderProfilerStreamData`, resolving sibling `.gpuprofiler_raw` files for scalar cost totals. |
| `GPUTRACE_MIO_TIMELINE_DATA` | Enables serialized `costTimeline` reconstruction via `GTMioKVDataStore` and `GTMioTraceTimelineData`, including automatic sibling-data setup. |
| `GPUTRACE_MIO_TRACE_TRACKS` | Enables top-level track model generation via `GTMioTraceDataHelper`, including automatic sibling-data setup. |
| `GPUTRACE_MIO_USC_CLIQUES` | Enables USC clique summary readback, including automatic sibling-data setup. |
| `GPUTRACE_PROCESS_STREAMDATA` | Specifies a `.gpuprofiler_raw/streamData` file for opt-in streamData model integration tests. |
| `GPUTRACE_SHADER_SEARCH_PATHS` | Adds platform-specific path-list entries to shader source lookup before built-in search paths. |
| `GPUTRACE_SKIP_MACGO` | Skips macgo app-bundle setup for capture and Xcode profiler automation, using current process identity instead. |
| `GPUTRACE_XCODE_APP` | Selects the Xcode bundle used by automation, counter catalogs, and `shaders --xcode-cost`. The cost command restarts itself so the matching private framework loads before package initialization. |
| `GPUTRACE_XCODE_DEVELOPER_DIR` | Specifies an explicit `Xcode.app/Contents/Developer` override for private `GTShaderProfiler.framework` and its matching `GTLLVMHelper`. |

Test-only environment variables are documented in
[`TESTING.md`](./TESTING.md).
