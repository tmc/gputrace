# Testing

Run the default suite with:

```bash
go test ./...
```

Maintainer validation should also exercise the build-tagged and cross-compiled
paths that the default suite does not load:

```bash
go vet ./...
go vet -tags metal ./...
go test ./...
GOOS=linux GOARCH=amd64 go test -exec=true ./...
go test -tags metal ./...
go test -race ./cmd/gputrace/cmd -count=1
go test -race ./internal/... -count=1
go build ./cmd/gputrace
```

The default suite uses the small checked-in fixtures under `testdata/traces`
when they are present and skips fixture-dependent cases when they are not.
Set `GPUTRACE_REQUIRE_PERF_FIXTURES=1` to make missing optional perf fixtures
under the legacy test paths fail instead of skip.

The checked-in fixture set covers structural traces and focused scenario
captures. It does not include `.gpuprofiler_raw` profiler exports or Xcode
`Counters.csv` files; tests that need those assets skip by default unless a
local fixture is supplied through the variables below.

## One variable for the capture-backed tests

Most of the opt-in tests want the same thing: a local `.gputrace` bundle. Point
`GPUTRACE_TEST_TRACE` at one and the capture-shaped variables below derive from
it:

```bash
GPUTRACE_TEST_TRACE=/path/to/capture.gputrace go test ./...
```

On a profiler-enabled capture that takes the suite from 77 skips to 62 and
exercises the streamData, counter-archive, execution-cost, and private-binding
paths that otherwise never run. Add `GPUTRACE_MIO_SETUP_DATA_PATH=1` to also
populate the timeline counter dictionary, which is empty without it.

`GPUTRACE_TEST_TRACE` is only a default. Any specific variable still wins when
set, so a test can be aimed at a different capture than the rest.

Captures on the development machine live in `~/tmp/gputrace-captures/`, with
symlinks left in `/private/tmp` so older hardcoded paths keep resolving. They
are not in `/tmp` itself: macOS sweeps that nightly and clears it on reboot,
and a profiled capture is 8-14 GB and cannot be regenerated from the repository.
The two with committed Xcode oracles under `testdata/` are
`qwen25-05b-static_tokens_2_to_3-wperfdata` (11 encoders) and
`...rep1-perfdata3` (23 encoders).

Derived from `GPUTRACE_TEST_TRACE` when unset: `GPUTRACE_AGX2_STREAMDATA`,
`GPUTRACE_PERF_FIXTURE`, `GPUTRACE_PROBE_COUNTERS_DIR`,
`GPUTRACE_PROBE_STREAMDATA`, `GPUTRACE_PROCESS_STREAMDATA`, and
`GPUTRACE_TEST_GPUPROFILER_DIR`. See `internal/testtrace`.

## Everything else

Some integration tests need local traces or host capabilities that are not
checked in. They are opt-in through environment variables:

| Variable | Used for |
| --- | --- |
| `GPUTRACE_ANALYZE_TEST_TRACE` | `internal/analysis` trace structure report |
| `GPUTRACE_API_CALL_TRACE` | `internal/trace` API-call integration parsing |
| `GPUTRACE_API_CALL_EXPECTED` | `internal/trace` API-call golden output comparison |
| `GPUTRACE_COUNTER_INTEGRATION_TRACE` | `internal/counter` perf-counter integration coverage |
| `GPUTRACE_COUNTER_STREAMDATA_DIR` | `internal/counter` `streamData` parsing |
| `GPUTRACE_COUNTER_TIMELINE_DIR` | `internal/counter` profiler timeline directory parsing |
| `GPUTRACE_COUNTER_TIMELINE_RAW` | `internal/counter` single `Timeline_f_*.raw` parsing |
| `GPUTRACE_COUNTERS_CSV_TRACE` | `internal/counter` Xcode `Counters.csv` import coverage |
| `GPUTRACE_CS_TEST_TRACE` | `internal/trace` real-trace CS parser coverage |
| `GPUTRACE_DIFFTRACE_GO_TRACE` | `internal/difftrace` Go trace regression input |
| `GPUTRACE_DIFFTRACE_PY_TRACE` | `internal/difftrace` Python trace regression input |
| `GPUTRACE_MTLB_TEST_FILE` | `internal/metallib` Metal library parser comparison |
| `GPUTRACE_PERF_FIXTURE` | `internal/counter`, `internal/shader`, and `internal/xcodebindings` coverage that needs a profiled `.gputrace` bundle |
| `GPUTRACE_TRACE_TEST_TRACE` | `internal/trace` real-trace open coverage |

Private-framework probe tests in `internal/counter` and `internal/xcodebindings`
are gated by further variables that are documented at their use sites:
`GPUTRACE_APS_PROFILE_PROBE`, `GPUTRACE_DUMP_STORE_KEYS`,
`GPUTRACE_MIO_USC_PROBE`, `GPUTRACE_MIO_USC_STATS`, and the
`GPUTRACE_TRACE_DATA_*` family.

These variables should point to local, developer-supplied files or directories.
Raw trace dumps, profiler exports, generated screenshots, and local binaries
should not be committed unless they are intentional test fixtures.
