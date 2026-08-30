# Reader drift across the Metal readers

Confidence markers follow the repo convention: [V] verified against the code or
a run, [D] derived by a check that could fail, [?] inferred.

Every command that prints a shader name reimplemented the "what do I call an
unnamed dispatch" rule. The reimplementations disagreed on the format *and* on
the key, so the same dispatch appeared as `(pipeline_12)` in one command,
`pipeline_12` in another, and `(pipeline_3)` in a third — 12 being a pipeline
ID and 3 the pipeline index of the same record. A reader comparing two
commands' output had no way to tell that the rows were the same kernel.

The canonical decoder is `counter.DispatchInfo.DisplayName()`
(internal/counter/streamdata.go), precedence: streamData FunctionName →
`(pipeline_<PipelineID>)` → `(pipeline_index_<PipelineIndex>)` →
`(pipeline_unknown)`. Before this audit only profiler, summary, pprof, the
sampling table and shader-metrics used it.

## Divergences

File:line are as found at audit time.

### D1 — shaders dropped unnamed pipeline rows [V]

cmd/gputrace/cmd/shaders.go:477 and :486 did `continue` when
`p.FunctionName == ""`, so a pipeline with no name produced no row. Its
duration stayed in `totalDispatchTime`, the share denominator, so the missing
rows were still being counted against every printed percentage: the table
summed to less than 100% and did not say why.

Fixed by naming the row with the new pipeline-level `DisplayName` instead of
skipping it.

### D2 — six inline fallbacks, three formats, two keys [V]

| site | printed | key |
|---|---|---|
| cmd/gputrace/cmd/kernels.go:99 | `(pipeline_N)` | PipelineIndex |
| cmd/gputrace/cmd/stats.go:237 | `pipeline_N` | PipelineIndex |
| cmd/gputrace/cmd/timing.go:335 | `(pipeline_N)` | PipelineIndex |
| cmd/gputrace/cmd/shaders.go:467 | `(pipeline_N)` | PipelineIndex |
| cmd/gputrace/cmd/shaders.go:310 | `(dispatch_N)` | dispatch ordinal |
| cmd/gputrace/cmd/timeline.go:2277, :2487 | `(pipeline_N)` | PipelineID |
| cmd/gputrace/cmd/timeline.go:4545 | `pipeline N` | PipelineID |
| internal/counter/counter.go:772 | `pipeline_N` | PipelineID |
| internal/counter/sampling.go:807 | `(unknown)` | none |

All now call `DisplayName`. The pipeline-keyed sites (shaders' pipeline loop,
timeline's `PipelineCompilerStats` lane, counter.go's pipeline index) call the
new `PipelineStats.DisplayName`, which uses the same precedence minus the
pipeline-index step: a pipeline record carries no index. `sampling.go`'s
`DispatchCounterMetrics` carries no pipeline identity at all, so its
`DisplayName` falls straight to `(pipeline_unknown)`.

### D3 — shaders and kernels disagreed on what a shader name is [V]

streamData's function-name array carries library records in the same field as
function records. kernels split them out (`splitKernelRows`, using
`gputrace.IsLibraryUUID`) and shaders did not, so `gputrace shaders` on
qwen-decode-16.gputrace listed three bare UUIDs as if shaders by those names
had run, while `gputrace kernels` on the same bundle listed them separately
under "3 library UUIDs (not kernel names)".

The classifier already existed (internal/trace/cs.go:149-163, re-exported as
`gputrace.IsLibraryUUID` / `gputrace.IsArchiveFunctionName`); shaders now
consults it and matches kernels' presentation: UUID rows below the table under
their own header, archive-named rows counted in a note that says the capture
recorded the archive, not the name.

### D4 — gate matched the raw field [V]

internal/gate/gate.go's profiler branch tested
`strings.Contains(d.FunctionName, invariant)`. An unnamed dispatch has an empty
FunctionName, so it could never match `-k` — it was invisible to the invariant
count rather than counted and unmatched. It now matches against `DisplayName`.

The capture branch (`AnalyzeKernels`) is unchanged: it is keyed by a name the
capture parser already resolved.

### D5 — same-name join keys [D]

internal/counter/counter.go built its pipeline lookup under the literal key
`pipeline_<ID>` while every reader that consumed such a name printed
`(pipeline_<ID>)`. Nothing joined across the two spellings in practice, so no
wrong number is known to have reached output; the key is now `DisplayName` so a
future join cannot miss.

### D6 — a chrome/perfetto export could silently contain no dispatches [V]

`generateTimeline` falls back from streamData dispatches to capture dispatch
records, and when a bundle has neither the export still succeeds — it just
carries encoder and command-buffer spans and no dispatch slice at all. In the
viewer that reads as a run with no GPU work, not as a run whose work could not
be placed.

The export now counts the bundle's dispatches (`Timeline.bundleDispatches`) and
prints a warning to stderr when a chrome or perfetto export ends with zero
dispatch or kernel events while that count is nonzero. The export format is
unchanged.

## Verification

- `go build ./...`, `go test ./...` pass. [V]
- `gputrace shaders qwen-decode-16.gputrace`: 19 rows before, 16 shader rows
  plus a 3-UUID section after. [V]
- `gputrace kernels --all qwen-decode-16.gputrace`: unchanged, still 3 library
  UUIDs listed separately. [V]
- `gputrace gate -t 16 --exact-tokens -k argmax qwen-decode-16.gputrace`:
  completeness ok 16/16 argmax. [V]

## Not addressed

`gputrace shaders` on a capture-only bundle draws its rows from the library and
function tables, so it has no archive-named rows to label; the archive note is
in place but fires only where such names reach the report. Whether the shaders
row set should equal the kernels row set on such bundles is a data-source
question, not a naming one, and is left open. [?]
