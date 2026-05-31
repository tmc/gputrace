# Environment Variables

gputrace works without environment configuration for ordinary analysis
commands. These variables adjust local development, Xcode automation, and
source lookup behavior:

| Variable | Effect |
| --- | --- |
| `GPUTRACE_DEBUG` | Enables extra debug logging from shader metrics helpers. |
| `GPUTRACE_SHADER_SEARCH_PATHS` | Adds platform-specific path-list entries to shader source lookup before the built-in MLX search paths. |
| `GPUTRACE_SKIP_MACGO` | Skips macgo app-bundle setup for capture and Xcode profiler automation, using the current process identity instead. |
| `GPUTRACE_XCODE_APP` | Selects the app name passed to `open -a` when opening traces in Xcode automation. |

Test-only environment variables are documented in
[`TESTING.md`](./TESTING.md).
