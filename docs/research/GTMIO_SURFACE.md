# GTMio shader-profiler surface

This inventory describes the `GTShaderProfiler` image loaded from
`/Applications/Xcode.app/Contents/PlugIns/GPUDebugger.ideplugin/Contents/Frameworks/GTShaderProfiler.framework/Versions/A/GTShaderProfiler`.
The class and method encodings below were read from `otool -ov` output for
that image. Runtime probes are opt-in because the processor starts
`GTLLVMHelper` and the model owns lazy data.

## Objective-C classes

The image contains 43 `GTMio*` Objective-C classes:

`GTMioCounterData`, `GTMioCounterDataPerDM`, `GTMioEncoderQuadData`,
`GTMioGPUInfo`, `GTMioHeatmapBuilder`, `GTMioHeatmapHistogram`,
`GTMioHeatmapImpl`, `GTMioInstructionALUSubPipeCountCounter`,
`GTMioInstructionTypeCountCounter`, `GTMioKVDataStore`, `GTMioMGPUTraceData`,
`GTMioNonOverlappingCounters`, `GTMioShaderAnalyzer`,
`GTMioShaderBinaryData`, `GTMioShaderExecutionHistory`,
`GTMioShaderExecutionHistoryCliqueNode`,
`GTMioShaderExecutionHistoryDefaultDelegate`,
`GTMioShaderExecutionHistoryFunctionNode`,
`GTMioShaderExecutionHistoryInstructionNode`,
`GTMioShaderExecutionHistoryLoopNode`, `GTMioShaderExecutionHistoryNode`,
`GTMioShaderExecutionHistoryRootNode`, `GTMioShaderProfilerEncoder`,
`GTMioShaderProfilerGPUCommand`, `GTMioShaderProfilerPipelineState`,
`GTMioShaderProfilerResult`, `GTMioShaderProfilerShaderFunction`,
`GTMioTimelineCounters`, `GTMioTraceAggregatedDrawTrack`,
`GTMioTraceAggregatedShaderTrack`, `GTMioTraceCliqueInstructionTraceTrack`,
`GTMioTraceCliqueTrack`, `GTMioTraceData`, `GTMioTraceDataHelper`,
`GTMioTraceDataObserverTokenInternal`, `GTMioTraceDataShaderStat`,
`GTMioTraceDataStats`, `GTMioTraceShaderCliqueInstructionTraceTrackGroup`,
`GTMioTraceTimelineData`, `GTMioTraceTrack`, `GTMioTraceTrackLane`,
`GTMioUSCTraceData`, and `GTMioWeakPerDrawCounterObserver`.

## Verified entry points

The processor path is implemented in
`internal/xcodebindings/process_streamdata_darwin.go`:

```text
dataFromArchivedDataURL:
initWithStreamData:llvmHelperPath:             @36@0:8@16@24{GTMioTraceDataBuilderOptions=BBBB}32
processStreamData                                  (processor API)
processShaderProfilerStreamData                    (processor API)
processTimelineStreamData                          (processor API)
waitUntilShaderProfilerFinished                    (processor API)
waitUntilTimelineFinished                          (processor API)
waitUntilFinished                                  (processor API)
mioData                                            (processor API)
```

For the four selectors whose encodings are used by the Go adapters, the
binary reports:

```text
GTMioTraceData enumeratePipelineStates:                         v24@0:8@?16
GTMioTraceData enumerateBinariesForPipelineState:enumerator:     v32@0:8Q16@?24
GTMioTraceData costForContext:cost:                              c32@0:8^{GTMioCostContext=SS(?=IIIII)(?=QIIIQ)}16^{GTMioCostInfo={GTMioCostContext=SS(?=IIIII)(?=QIIIQ)}d[10d]d[10d]Q[10Q]QQQ}24
GTMioTraceData costForScope:scopeIdentifier:cost:                c36@0:8S16Q20^{GTMioCostInfo={GTMioCostContext=SS(?=IIIII)(?=QIIIQ)}d[10d]d[10d]Q[10Q]QQQ}28
```

The full cost structure in the image is:

```text
GTMioCostContext = SS(?=IIIII)(?=QIIIQ)
GTMioCostInfo    = {GTMioCostContext}d[10d]d[10d]Q[10Q]QQQ
```

`GTMioTraceData` also exposes scalar accessors with encodings `Q16@0:8`:
`drawCount`, `encoderCount`, `costCount`, `pipelineStateCount`,
`gpuTime`, and `costTimeline` is an object return `@16@0:8`. The result record
classes expose scalar IDs and indexes as `Q16@0:8` or `I16@0:8` and object
collections as `@16@0:8`.

`GTMioShaderProfilerResult` is constructible with:

```text
initWithTraceData:        @24@0:8@16
loadFromTraceData:        v24@0:8@16
pipelineStates            @16@0:8
encoders                  @16@0:8
gpuCommands               @16@0:8
shaderBinaries            @16@0:8
```

Its lookup methods are `pipelineStateForId:` (`@24@0:8Q16`),
`encoderForFunctionIndex:` (`@24@0:8Q16`), and
`gpuCommandForFunctionIndex:subCommandIndex:` (`@28@0:8Q16i24`).

## Measured fixture evidence

The archived pipeline-statistics dictionaries also carry
`Constant calculation temporary register count` and `Constant calculation
phase present`. The parser exposes both fields in `PipelineStats`; across the
four single-kernel fixtures and `06-six-encoders`, the values reproduced as
`1` and `true` for every pipeline. They describe the constant-calculation
phase and are intentionally distinct from allocated registers, live registers,
occupancy, and ALU utilization.

With `/Users/tmc/go_trace_tokens_2_to_3-perfdata.gputrace`, the proven
processor sequence yields:

```text
drawCount    = 574
encoderCount = 12
costCount    = 575
```

`encoderCount` agrees with the archived `streamData` encoder count (12).
The processor must remain alive while lazy cost or binary collections are
read; reading a lazy cost accessor after `GTLLVMHelper` exits has crashed.

The same fixture gives a populated result graph:

```text
gpuTime         = 2836541
gpuName         = Apple M4 Max
metalPluginName = AGXMetalG16X
performanceState = 2
gpuGeneration   = 2
unixTimestamp   = 1775189551
shaderBinaries  = NSDictionary, count 980
gpuCommands     = NSArray, count 574
pipelineStates  = NSArray, count 18
```

The 18 pipeline records' `numGPUCommands` sum to 574. This is the accepted
per-pipeline attribution check for the profiler-only fixture.

With `GPUTRACE_MIO_TRACE_TRACKS=1`, `GTMioTraceDataHelper
initWithTraceData:` (`@24@0:8@16`) produces the framework's top-level track
model from that same processor result. Two complete runs reproduced these
values exactly:

```text
generateTopDrawTracks   (@16@0:8) -> NSArray count 574
generateTopBinaryTracks (@16@0:8) -> NSArray count 592
generateTopKickTracks   (@16@0:8) -> NSArray count 3
generateTopRIATracks    (@16@0:8) -> NSArray count 0
```

The first three objects in the draw and kick lists are `GTMioTraceTrack`
objects; their `firstIndex`, `duration`, and `isEmpty` selectors reproduced
exactly. For example, draw samples were `(585,63878,false)`,
`(584,331755,false)`, and `(583,2666,false)`, while kick samples were
`(3,6631786,false)`, `(1,5370160,false)`, and `(0,25378857,false)`. The repo
records those bounded draw/kick samples and the stable binary count, never the
raw C-pointer track payloads. Binary track sample order is not stable within a
single process: a repeated repo integration run changed the third sample while
keeping count 592. Binary samples are therefore deliberately not exposed.
The per-encoder `generateAggregatedDrawTrackForEncoder:` objects and
per-pipeline `generateAggregatedShaderTrackForPipelineState:programType:`
objects both returned real track objects but `traceCount=0` for every tested
encoder/pipeline; they are documented as empty rather than exposed as useful
attribution. `generateShaderTrackForProgramTypes` throws
`NSInvalidArgumentException` (`dataType` unrecognized) on the processor model,
so it is not retried.

The returned tracks also expose object-valued `lanes` (`@16@0:8`). For each
lane, the safe scalar selectors `laneId` (`i16@0:8`), `indexCount`
(`Q16@0:8`), and `isEmpty` (`c16@0:8`) reproduced populated metadata on two
complete runs: sampled draw and binary lanes had ID 0 and one index, while a
sampled kick track had IDs 0 and 1 with 945 and 17 indexes. The raw `indexes`
property is a C pointer and was not read. `ProcessedStreamData.Tracks` reports
these lane summaries only when `GPUTRACE_MIO_TRACE_TRACKS=1`.

The USC-specific helper generators were tested separately with USC index `0`
after `_setupDataPath`. `generateKickTracksForUSC:`,
`generateTileTracksForUSC:`, `generateCliqueTracksForUSC:`,
`generateAggregatedCliqueTrackForUSC:`, and
`generateCliqueInstructionTracksForUSC:` (all `@20@0:8I16`) each returned an
empty `NSArray` with `count=0` on both runs. This remains true alongside the
populated USC counts (260541 cliques, 1961 kicks, 2441 tiles), so the empty
track-family result is a framework/model boundary rather than evidence that
the raw USC data is absent.

Across the 980 binary records, `liveRegisterForInstructionAtIndex:` yields a
maximum of `96`; this is an aggregate whole-capture observation only. A
per-kernel `high_register` attribution is closed as unavailable on this
capture. Four attempted edges fail the two-run rule: command-key
`mcaBinaryForBinaryKey:` returns run-dependent assignments, the pipeline-keyed
`MCABinaryList` is empty, `firstBinaryIndexForCliqueAtIndex:` drifts, and the
stable first-PC/address join encounters duplicate binary objects and does not
complete reproducibly. A complete nested store-key sweep found no archived
live/high-register field per function. The aggregate value must not be copied
to every kernel event or wired into xcode-parity.

## Evidence boundaries

With the ordinary processor sequence, the cost model is allocated but not
populated: `costCount` is 575, while all 575 `GTMioCostInfo` records and
`gpuCost` are zero-filled, `derivedCountersData` is an empty dictionary, every
encoder kick duration is zero, and scope-cost queries return no non-zero values.
This does not show that the capture lacks counters: its `.gpuprofiler_raw` directory contains 40
`Counters_f_*.raw`, 40 `Profiling_f_*.raw`, and 40 `Timeline_f_*.raw` files.
The stream object also exposes `unarchivedAPSCounterData` (142 dictionaries) and
`unarchivedAPSTimelineData` (135 dictionaries). Calling the private
`-_setupDataPath` selector (`@16@0:8`) before constructing the processor resolves
the raw directory and changes the same run to a populated model. Two independent
runs both produced `costCount=606`, `computePositionCount=10187132`, non-zero
`gpuCost`, and `totalCostForScope:scopeIdentifier:dataMaster:` values of
`scope=0,dataMaster=2 -> 100` and `scope=4,dataMaster=2 -> 0.396351`.
The repo exposes this only with `GPUTRACE_MIO_SETUP_DATA_PATH=1`; it records
safe scalar totals and does not reinterpret the raw C cost arrays. This proves
counter-derived cost ingestion, but does not establish the semantic mapping of
an individual field to Xcode's occupancy or ALU percentages, so those parity
fields remain explicit until that mapping is measured.

An opt-in scratch probe mmaped `Counters_f_0.raw`, `_4.raw`, `_12.raw`, and
`_39.raw` and called `loadAPSCounters:counterSet:` with counter sets 0 through
3. Across repeated runs and GPU generation/variant/revision combinations, the
method returned `true` but reported `numUSCs=0`, `numValidUSCs=0`,
`numAPSRawCounters=0`, `numAPSDerivedCounters=0`,
`firstAPSTimestamp=UINT64_MAX`, and `lastAPSTimestamp=0`. Thus the BOOL is not
an acceptance signal. The actual USC registration seam,
`addBufferAtUSCIndex:buffer:length:` (`v36@0:8I16*20Q28`), accepted all 40
`Counters_f_N.raw` mappings and reproduced `numUSCs=40`, `numValidUSCs=40`, and
`isValidUSC:N=true` for every index. `parseData:length:uscIndex:`
(`c36@0:8*16Q24I32`) returned false for every buffer, so no samples were
produced.

`XRGPUAPSDataContainer initWithConfig:baseFolder:variant:`
(`@40@0:8@16@24Q32`) returned a real variant-1 container. Filling it with all
USC and RDE buffers produced `numUSCs=40`, `numRDEs=40`, and an `encode` result
of 3,389,981,890 bytes. The next database conversion attempt crashed inside
`processorFromDataContainer:options:`; options 0 through 3 each crashed at the
same conversion boundary on separate runs. It is explicitly not exposed as a
repo capability until that framework contract is understood.

The direct timeline constructor
`initWithAPSTraceData:timelineData:streamData:timelineType:options:parentData:`
(`@56@0:8^v16^v24@32I40{GTMioTraceDataBuilderOptions=BBBB}44@48`) was tested
with mmaped `Counters_f_0.raw` and `Timeline_f_0.raw` buffers, and with the
inner `streamData` archive, while retaining both mappings. It SIGSEGVed before
returning on both runs. Those `^v` arguments are not treated as accepted
raw-file inputs; the constructor is deliberately not exposed.

`effective_gpu_time` remains unavailable from this surface alone. The image
contains kick timing (`effectiveKickTimes` in the profiler side) and the
trace-data `gpuTime` accessor, but no proof yet establishes that either is
Xcode's effective GPU-time calculation for this capture.

Binary enumeration and `GTMioShaderBinaryData` are present in the image. The
processed result contains 980 shader binaries in an `NSDictionary`; use
`allValues`, not `objectAtIndex:`. Likewise, `shaderFunctions` and
`shaderBinaries` are dictionaries, while `pipelineStates` and `gpuCommands`
are arrays.

Several `GTMioTraceData` properties are raw C pointers, not Objective-C
objects: `costs`, `gpuCost`, `encoders`, `draws`, `shaderBinaryInfo`,
`computePositions`, and `fragmentPositions` have `^{...}16@0:8` encodings.
Sending object selectors such as `count` to them crashes. Only properties
whose `otool -ov` return encoding is `@16@0:8` are treated as objects.

The processor-built model exposes 40 real `GTMioUSCTraceData` objects through
`uscs` (`@16@0:8`), each with a non-zero `databaseInternal` (`Q16@0:8`). Without
`-_setupDataPath`, their cliques are zero. With the setup path, `usc[0]` has
260541 cliques, 1961 kicks, 2441 tiles, and costCount 2565; the populated
counts reproduce across two runs. `pipelineStateIdForCliqueAtIndex:`
(`Q20@0:8I16`) is stable and returns real pipeline IDs. The binary-index
accessor drifts across runs and is not used. `GTMioTraceDataStats
initWithTraceData:` (`@32@0:8@16`) followed by `build` is safe on the empty
model but crashes on the populated 260k-clique model, so it remains gated and
no shader-stat values are claimed.

The decoded timeline constructor
`initWithDecodedDictionary:streamData:parentData:`
(`@40@0:8@16@24@32`) was given the first `unarchivedAPSTimelineData`
dictionary, the live streamData object, and the processor-built parent. It
returned `nil` on two runs, so this database-adjacent input does not construct
a usable timeline model.

The framework archive round-trip is usable but does not add the missing index.
`archivedData:error:` (`@28@0:8c16^@20`) with `false` produced an approximately
133.9 MB `NSData`; `initWithArchivedData:error:` (`@32@0:8@16^@24`) rebuilt a
populated model twice with draws=574, encoders=12, costs=575, pipelines=18,
binaries=980, USCs=40, and mGPUs=1. On that model,
`binaryForPipelineState:programType:` (`@28@0:8Q16S24`) returned an empty array
for all 18 pipeline IDs at program type 0 on both runs. Finally,
`GTMioTraceDataStats -initWithTraceData:` (`@24@0:8@16`) threw
`-[GTMioTraceData databaseInternal] unrecognized selector` on both runs. The
archive is therefore a populated serialization path, not the missing trace
database.

The archive's KV structure contains a `costTimeline` child. Passing that child
from `GTMioKVDataStore -getChild:` (`@24@0:8@16`) to
`GTMioTraceTimelineData -initWithSerializedData:streamData:parentData:`
(`@40@0:8@16@24@32`) constructs a real timeline with a nonzero database handle
and stable draw=574, encoder=12, cost=575, pipeline=18 counts. Testing
`binaryForPipelineState:programType:` for all program types 0 through 5 and all
18 pipeline IDs still returns empty arrays on both runs. This is a usable
cost/timeline store, not a pipeline-to-binary index.

For the populated path, pass the `.gpuprofiler_raw` directory—not its inner
`streamData` file—to `dataFromArchivedDataURL:` before `_setupDataPath`. The
archive/KV/`costTimeline` sequence reproduced twice with archive size about
2.345 GB, costCount=606, and computePositionCount=10187132 on both the
reconstructed model and timeline object. Using the inner file alone gives the
ordinary costCount=575 model.

The repository exposes this as `GPUTRACE_MIO_TIMELINE_DATA=1`. It archives the
live model, opens the `costTimeline` KV child, and reads only scalar selectors.
Two complete fixture runs reproduced 18 pipeline draw counts summing to 574,
12 encoder draw counts of 12 each. With `GPUTRACE_MIO_SETUP_DATA_PATH=1` and
the raw profiler directory, draw durations were `[14303, 13698, 1575]`; without
that setup, the selectors returned three zeros.
The packed `draws` array (`^{GTMioDrawMetadata=IIIIiIQIII}16@0:8`) is attributed
only after its candidate stride/offset reproduces the framework's complete
pipeline draw-count multiset. Two setup-backed runs produced per-kernel GPU
time at data master 2; `0xaac` accounted for 2,025,751 (65.99%) of 3,069,644.
at data master 2. The attribution selectors are
`numDrawsForPipelineState:` (`Q24@0:8Q16`), `numDrawsForEncoder:`
(`Q20@0:8I16`), and `durationForDraw:dataMaster:` (`Q24@0:8I16S20`).
`kickDurationForEncoder:dataMaster:` (`Q24@0:8I16S20`) returned zero for all
12 encoders on both runs and is not promoted to effective GPU time.

`GPUTRACE_MIO_USC_CLIQUES=1` exposes a bounded `USCSummary`: all 40 USC cores,
the aggregate clique/kick/tile counts, and six cliques each from the first two
USCs. It records only the reproducible `(USC index, clique index,
pipelineStateId, firstPC)` fields. This is real per-kernel execution
attribution: the first samples map to pipeline IDs `0xaac`, `0xab1`, and
`0xaaa`, matching the processor's pipeline records. The unstable
`firstBinaryIndexForCliqueAtIndex:` field is intentionally omitted. The
opt-in regression compares the summary across two runs.

## Trace-database construction probe

The available payloads were tested twice each under a locked OS thread and one
autorelease pool. The exact method encodings and outcomes were:

* `-[GTMioTraceData initWithStreamData:llvmHelperPath:options:]`
  `@36@0:8@16@24{GTMioTraceDataBuilderOptions=BBBB}32`: option values `0..15`
  all returned a `GTMioTraceData`, but all were reproducibly empty
  (`drawCount=0`, `encoderCount=0`, `costCount=0`, `pipelineStateCount=0`,
  `shaderBinaryInfoCount=0`, `drawTraceCount=0`, with empty object
  collections). This is not a populated database-backed model.
* `+[GTMioTraceData traceDataFromURL:error:]` `@32@0:8@16^@24`: `store0` and
  `capture` returned `NSCocoaErrorDomain` code 4864, “incomprehensible archive”
  for their zlib and `MTSP` headers; the bundle directory returned code 256.
  The streamData URL throws `NSInvalidUnarchiveOperationException` because its
  root class is `GTMutableShaderProfilerStreamData`, not `GTMioTraceData`.
* `-[GTMioKVDataStore initWithURL:]` `@24@0:8@16`: returned `nil` for
  `store0`, `capture`, and the bundle directory on both runs.

* `-[GTMioTraceData initWithTraceDatabase:deallocator:]`
  `@32@0:8Q16@?24`: passing the real non-zero `databaseInternal` handle from
  `uscs[0]` (`0xcc5df8000` in the probe) with a nil deallocator caused a
  deterministic SIGSEGV before returning. It is not valid to treat a USC's
  internal handle as a top-level trace-database handle; this route is stopped.

The top-level `GTMioTraceDataStats` wrong-class failure is resolved by passing a
USC object rather than `GTMioTraceData`. The pipeline-keyed MCA index and
per-kernel `high_register` remain unavailable; no value from this route is used
by parity.

If a future capture contains USC data, the safe structural join is exposed by
`GTMioUSCTraceData`: `pipelineStateIdForCliqueAtIndex:` (`Q20@0:8I16`),
`firstBinaryIndexForCliqueAtIndex:` (`I20@0:8I16`),
`firstPCForCliqueAtIndex:` (`Q20@0:8I16`), and
`pcForInstruction:binaryIndex:` (`Q24@0:8I16I20`). That route can attribute
cliques to pipeline and binary without asynchronous MCA key matching, and also
offers USC costs and `GTMioTraceDataStats shaderStatForShader:programType:`
(`@28@0:8Q16S24`). The present fixture has cliques with a stable pipeline join,
but the binary-index leg is not reproducible; firstPC is stable and is the next
candidate durable binary identity.

The binary traversal selector is `v32@0:8Q16@?24`. Its callback receives a
`GTMioShaderBinaryData` object. Verified scalar accessors on that object are
`address` (`Q16@0:8`), `index` (`Q16@0:8`), `programType` (`S16@0:8`), and
`instructionInfoCount` (`Q16@0:8`). The initializer, which is not called by
the adapter, is `initWithBinaryData:parent:index:` with encoding
`@40@0:8^v16@24Q32`.

`+[GTShaderProfilerBinaryAnalysisResult analyzeBinary:targetIndex:isaPrinter:]`
(`@36@0:8@16i24@28`) was also tested against those binary objects. It throws
`NSInvalidArgumentException` because `GTMioShaderBinaryData` does not respond
to `bytes`; the analyzer expects a different binary input class. This was
reproduced in two isolated runs and is not treated as a capability.

On this fixture, `GTMioShaderProfilerPipelineState` `binaryKeys` and
`allBinaryKeys` both have encoding `@16@0:8` and are empty for all 18
pipelines. `GTMioShaderProfilerGPUCommand` exposes the same two selectors
(`@16@0:8`), with one key per command, and
`pipelineStateObjectId` (`Q16@0:8`) resolves the owning pipeline. Looking up
those command keys directly in the result's 980-entry `shaderBinaries` dictionary
does not resolve a binary. Each command key is instead an `NSSet` of string
members. Passing each member to `mcaBinaryForBinaryKey:` (`@24@0:8@16`)
returns `GTShaderProfilerMCABinary` objects. The first non-empty result reports
`allocatedGPRCount=98`, `highRegisterCount=98`, `programType=3`, and
`uniqueIdentifier=723710`.

That edge does not attribute those binaries to pipelines. Three runs over this
one fixture disagreed: `0xaac` reported 98, then 60, then 66; `0xaa8` reported
113, then 113, then 98; `0xab8` reported 0, then 16, then 113. MCA analysis is
asynchronous — `-generateMCAOutput:callback:` (`v28@0:8c16@?20`) beside
`-_generateMCAOutputSync:` (`{MCAOutput=@@}20@0:8c16`) — and the key walk races
it, so the numbers are real MCA output landing against arbitrary pipelines.

The reproducible accessor is
`-[GTShaderProfilerMCABinaryList initWithShaderProfilerResult:pipelineStateId:programType:]`
(`@36@0:8@16Q24I32`), which takes the pipeline state ID the model already
reports and exposes `mcaBinaries`, `highRegisterCount` and `allocatedGPRCount`
(`i16@0:8`). It constructs on a processor-built model but holds no binaries, for
all 18 pipelines at every program type 0 through 5. The pipeline-keyed MCA index
looks to require a trace database, matching where
`-[GTMioTraceDataStats initWithTraceData:]` refuses a processed stream.

Per-kernel register pressure is therefore unavailable through the four tested
edges above. `GPUTRACE_MIO_MCA=1` is retained only as a diagnostic for the
retracted, non-attributed MCA output; it is not a parity data path.

`GTMioHeatmapBuilder` has
`initWithTraceData:encoderFunctionIndex:programType:options:` with encoding
`@40@0:8@16I24S28Q32`. Calling it with the processor data and
`(0, 0, 0)` returns nil without an error or exception, so no heatmap claim is
made. `GTMioShaderExecutionHistory` has
`initWithTraceData:style:options:delegate:` (`@40@0:8@16I24I28@32`); the
same data and `(0, 0, nil)` return a real object. Its
`generatePipelineStateId:programType:` selector (`c28@0:8Q16S24`) returns
true for `(0xab5, 0)`. The node tree was then probed through the safe
generation selectors `generateDrawIndex:programType:`
(`c24@0:8I16S20`) for draws 0, 1, and 573, and
`generateCliqueIndex:uscIndex:` (`c24@0:8I16I20`) for USC 0 cliques 0, 1,
and 260540. Every generator returned true on both runs, but
`nodeForStyle:` (`@20@0:8I16`) remained nil for styles 0 through 7 after each
request. The generator accepts the request without materializing a tree;
Root/Function/Loop/Instruction/Clique nodes remain unavailable for this
capture.

The model-level builders were also tried: `executionHistoryForPipelineState:
programType:delegate:progressController:` (`v44@0:8Q16S24@28@36`) for pipeline
`0xab5`, and `executionHistoryForDraw:programType:delegate:progressController:`
(`v40@0:8I16S20@24@32`) for draw 0, with nil delegate and progress controller.
Both void calls completed on two runs, but styles 0 through 7 remained nil and
the Mio object exposed no pending-history wait method. No execution-history
tree is available from this capture.

Execution-history traversal was repeated over all 18 pipeline IDs. The
initializer `initWithTraceData:style:options:delegate:` has encoding
`@40@0:8@16I24I28@32`; it returned a `GTMioShaderExecutionHistory` object.
`generatePipelineStateId:programType:` (`c28@0:8Q16S24`) returned `true` for
each pipeline ID on both runs. The draw and clique generators likewise
returned `true`, but `nodeForStyle:` (`@20@0:8I16`) remained `nil` for styles
0 through 7 after every request. The generator is therefore not evidence of a
populated execution-history tree for this capture; Root/Function/Loop/
Instruction/Clique nodes remain unavailable.

`GTMioEncoderQuadData initWithTraceData:encoderFunctionIndex:programType:options:`
(`@40@0:8@16I24S28Q32`) returned nil for every encoder index 0 through 11
with `(mioData, index, 0, 0)` on two ordinary processor-model runs. The
setup-path first run also returned nil for all 12, but its second expensive
process terminated before reaching the probe, so only the ordinary-model nil
result is treated as reproducible. No quad data is exposed.

The parallel `GTAGX2StreamDataShaderProfilerProcessor` class is present. Its
`initWithStreamData:` (`@24@0:8@16`) path runs on this archive and produces a
`GTAGX2ShaderProfilerResult`, but the result is empty: generation 0,
performance state 0, GPU 4, timeline duration 0, empty plugin name, and zero
shader binaries, commands, pipelines, encoders, derived counters, and timing
info. This is an empty result, not a nil or exception. The input boundary was
also tested directly: after `_setupDataPath` (`@16@0:8`), the 54 archived APS,
142 APS-counter, 135 APS-timeline, and 9 shader-profiler objects were sent
through `process:` (`v24@0:8@16`) and through the dedicated
`processShaderProfilerStreamedResult:` and `processBatchIdData:` selectors
(both `@24@0:8@16`). The dedicated and generic feeds produced the same empty
result across two runs, so this is a capture/input boundary rather than a
reproducible AGX2 capability. The four
`GTMioShaderAnalyzer` was swept at all three exact constructor scopes, both on
the ordinary processor model and after `_setupDataPath`. The
pipeline constructor is `@40@0:8Q16S24c28@32`, the encoder constructor is
`@36@0:8I16S20c24@28`, and the draw constructor is
`@36@0:8I16S20c24c28`. All 18 pipeline IDs, all 12 encoder indices, and all 574
draw indices were tested for program types 0 through 5 with both
`useBaseProgramType` values; every constructor returned `nil` on both complete
runs. Consequently the four histogram accessors
`instructionTypeInfo`, `instructionScopeInfo`, `instructionDataTypeInfo`, and
`instructionMemoryTypeInfo` (each `^{?=SIQQQd}16@0:8`) were never dereferenced.

The explicit build methods do not provide a fallback. On all 18 pipeline IDs,
`buildPipeline:programType:traceData:` (`c36@0:8Q16S24@28`) with program type
0 returned `false`; on draws 0, 1, and 573,
`buildDraw:programType:traceData:` (`c32@0:8I16S20@24`) also returned `false`.
All four histogram counts stayed zero and the complete output matched across
two runs. No analyzer capability is exposed.

The lower-level `GTAGX2ShaderProfiler` initializer
`initWithStreamData:forTargetIndex:` (`@28@0:8@16i24`) also returned real
objects for target indices 0 and 1 on both runs. Its object-returning
`effectiveKickTimes`, `averagePerDrawKickDurations`, `loadActionTimes`,
`storeActionTimes`, `perRingPerFrameLimiterData`, and `timingInfo` accessors
(all `@16@0:8`) were nil or empty on both runs. It does not provide an alternate
AGX2 result for this capture.

`GTJSScriptingContext` is a working auxiliary surface. `+sharedContext`
(`@16@0:8`) returned a `GTJSScriptingContext`; `setValue:value:`
(`@32@0:8@16@24`) stored an `NSNumber` value of `42.5`, and `getValue:`
(`@24@0:8@16`) returned a `JSValue` whose description was `42.5`. The same
context exposed a `JSVirtualMachine` through `virtualMachine` (`@16@0:8`).
The set/get result reproduced in two independent runs. This is a usable
scripting bridge, but it is not yet wired into gputrace because no model
property has been identified that it exposes more faithfully than the typed
Objective-C APIs above.

`GTLLVMConnectionManager` also constructs independently of the stream model.
Its initializer `initWithGPUName:withTargetIndex:binaryPath:withGen:withSocketName:forNumClients:`
(`@52@0:8@16i24@28C36@40I48`) with
`("Apple M4 Max", 0, GTLLVMHelper, 16, "", 1)` returned a manager whose
`version` (`I16@0:8`) was 3, `nLLVMClients` (`I16@0:8`) was 1,
`targetIndex` (`i16@0:8`) was 0, and `gpuName` (`@16@0:8`) was `Apple M4 Max`.
The result reproduced across two independent runs. Asking it to analyze the
GTLLVMHelper executable through `createLLMVAnalyzerForFilePath:`
(`I24@0:8@16`) returned `UINT32_MAX`; the corresponding guarded queries
`isLLVMValid:` (`B20@0:8I16`) returned false, `binarySize:`
(`I20@0:8I16`) returned 0, and both dump methods returned nil. This rules out
the helper executable as an analysis input; no MCA or register claim is made
from this manager probe.

The direct empty `GTMioTraceData` constructor was also checked for the GPU
metadata wrapper. `initWithStreamData:llvmHelperPath:options:`
(`@36@0:8@16@24{GTMioTraceDataBuilderOptions=BBBB}32`) with option `0`
returned a model, but its `gpuInfo` (`@16@0:8`) was nil on both runs. The
standalone `GTMioGPUInfo` initializer requires a raw
`GTMioGPUInfoInternal` pointer (`@24@0:8r^{GTMioGPUInfoInternal=IIIIII}16`),
so no guessed struct was sent and no GPUInfo values are claimed.

The direct raw APS route was tested separately. `XRGPUAPSDataProcessor`
`initWithGPUGeneration:variant:rev:config:options:` was called with
generation `2` and `16`, variant `0`, revision `0`, the framework's
`loadCounterGraphConfig` result, and options `0`. Each of the 40
`Counters_f_N.raw` mappings was retained, passed to
`addBufferAtUSCIndex:buffer:length:` (`v36@0:8I16*20Q28`), and passed to
`parseData:length:uscIndex:` (`c36@0:8*16Q24I32`). On two runs, every parse
returned false while `numUSCs` became 40 and `numValidUSCs` became 40;
`numAPSRawCounters` and `numAPSDerivedCounters` stayed zero and timestamps
remained unset. `loadAPSCounters:counterSet:` (`c32@0:8^v16Q24`) returned true
for sets 0 through 3 but remained vacuous; `loadCounters:` returned false for
all four. Counter-config queries returned empty dictionaries. This pins the
current failure to the raw-file/config boundary, not missing files; the
existing `_setupDataPath` route remains the only reproducible populated cost
path.

The same raw experiment also tried `loadShaders:uscIndex:` on the 40
`Profiling_f_N.raw` files. It returned true for USC 0 and then crashed with a
SIGSEGV before reaching USC 1 on both runs; the process was isolated and no
shader data was read. This is a reproducible crash boundary, not a usable
capability, so no retry or adapter was added.

The alternate container bridge accepts the bytes but does not construct a
processor. `XRGPUAPSDataContainer -initWithConfig:baseFolder:variant:`
(`@40@0:8@16@24Q32`) with variant `1` and the raw directory, followed by 40
`addDataForUSCAtIndex:data:` calls (`v28@0:8I16@20`), consistently reported
`numUSCs=40`. Calling the correctly located class method
`+[XRGPUAPSDataProcessor processorFromDataContainer:options:]`
(`@28@0:8@16I24`) with options `0` and `1` returned nil on both runs. Thus
the container is a byte holder, not a working bridge for this capture/config.

`mGPUs` (`@16@0:8`) on the setup-path `GTMioTraceData` returned one
`GTMioMGPUTraceData` object. Its `index` (`Q16@0:8`) was 0, but
`kicksCount` and `costCount` (`Q16@0:8`) were both zero on two runs. The
MGPU object is therefore allocated but carries no independent timing/cost
data in this capture.

The lazy timeline loaders also complete without error. `loadTimeline` and
`loadCostTimeline` (`v16@0:8`) were each sent on the setup-path model; on both
runs `loadingCostTimeline` (`c16@0:8`) was false,
`consistentStateAchieved` (`c16@0:8`) and `isMio` (`c16@0:8`) were true, and
`hasSeparateCostsTimeline` (`c16@0:8`) was true. The object accessors
`costTimeline`, `overlappingTimeline`, and `nonOverlappingTimeline`
(`@16@0:8`) returned real `GTMioTraceTimelineData` objects, but each had
collection count zero. The loaders therefore establish model state but do not
materialize additional timeline samples for this archive.

The populated setup-path model does expose counter-object structure through
`timelineCounters` (`@16@0:8`) and `nonOverlappingCounters` (`@16@0:8`). Two
runs reported an empty timeline-counter dictionary, but a real
`GTMioNonOverlappingCounters` with 832 encoder, 72 draw, and 72 pipeline
counter slots. Its name collections contained 208 encoder names and 18 draw
and pipeline names, including `ALU Total Instructions`, `ALU F16
Instructions`, and `ALU F32 Instructions`. The first
`derivedEncoderCounters` and `derivedGPUCommandCounters` objects were
`GTMioCounterDataPerDM`; their sample counts were 12 and 574 respectively,
but their values were all zero (`values` is `^d16@0:8`, read directly rather
than messaged), with min/max left at `DBL_MAX`/0. This is allocated structure,
not populated counter data, and is therefore documented but not exported as
parity metrics.
