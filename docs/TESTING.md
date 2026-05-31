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

Some integration tests need local traces or host capabilities that are not
checked in. They are opt-in through environment variables:

| Variable | Used for |
| --- | --- |
| `GPUTRACE_AGXPS_PROFILER_RAW_DIR` | `internal/agxps` timeline raw parsing against a `.gpuprofiler_raw` directory |
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
| `GPUTRACE_MTLB_TEST_FILE` | `internal/mtlb` Metal library parser comparison |
| `GPUTRACE_TRACE_TEST_TRACE` | `internal/trace` real-trace open coverage |

These variables should point to local, developer-supplied files or directories.
Raw trace dumps, profiler exports, generated screenshots, and local binaries
should not be committed unless they are intentional test fixtures.
