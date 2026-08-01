# GTShaderProfiler capability matrix

This matrix is generated from the supplied method index at
`~/tmp/gputrace-blockers/gtmio-index.txt` (1,934 methods across 93 runtime
classes) and a live class-load probe against the Xcode framework. It records
the complete method inventory shape for every class without invoking unsafe
arbitrary ABIs. Pointer-returning methods are counted separately and were not
messaged as objects.

The measured baseline fixture is a streamData archive with 574 draws, 12
encoders, 18 pipelines, 980 binaries, and 45,977 instructions.

## Demonstrated capabilities

### Additional archived constant-calculation fields

The store and stream-data pipeline-statistics dictionaries archive two fields
that were previously dropped by `assignPipelineStatFields`: `Constant
calculation temporary register count` and `Constant calculation phase
present`. The shared parser now exposes them as
`PipelineStats.ConstantCalculationTemporaryRegisterCount` and
`PipelineStats.ConstantCalculationPhasePresent`. On the four single-kernel
fixtures and `06-six-encoders`, every pipeline reproduced
`constantCalculationTemporaryRegisterCount=1` and
`constantCalculationPhasePresent=true`; the test cross-check is the archived
plist value itself. These are constant-calculation metadata, not live-register
or occupancy measurements, and are not substituted for those parity fields.

### Trace-database construction boundary

The database thread was probed against every available payload in the 4.6 GB
capture, with each result repeated twice. The exact outcomes are:

- `-[GTMioTraceData initWithStreamData:llvmHelperPath:options:]`
  (`@36@0:8@16@24{GTMioTraceDataBuilderOptions=BBBB}32`) returned a real
  `GTMioTraceData` for every four-byte option value `0` through `15`, but every
  run returned zeros: `drawCount=0`, `encoderCount=0`, `costCount=0`,
  `pipelineStateCount=0`, `shaderBinaryInfoCount=0`, `drawTraceCount=0`, and
  empty `binaries`, `timelineCounters`, `nonOverlappingCounters`, `uscs`, and
  `mGPUs`. `streamData` was a `GTMutableShaderProfilerStreamData` object. The
  complete output reproduced across the two runs for each option, so this is an
  empty model, not a usable database index.
- `+[GTMioTraceData traceDataFromURL:error:]`
  (`@32@0:8@16^@24`) returned `NSError` code 4864 / `NSCocoaErrorDomain` for
  `store0` (input starts with zlib `0x78 0x5e`) and `capture` (input starts with
  `MTSP`), both described as an incomprehensible keyed archive. The bundle path
  returned code 256 because it is a directory. The streamData URL throws
  `NSInvalidUnarchiveOperationException` because its root is
  `GTMutableShaderProfilerStreamData`, not `GTMioTraceData`.
- `-[GTMioKVDataStore initWithURL:]` (`@24@0:8@16`) returned `nil` for
  `store0`, `capture`, and the bundle directory on both runs. It therefore does
  not expose a database reader for the supplied files.

The direct archive constructors remain empty or rejecting, but the processor
model does expose 40 real `GTMioUSCTraceData` objects through `uscs`, each with
a non-zero `databaseInternal`. `GTMioTraceDataStats initWithTraceData:` accepts
one USC object and `build` returns cleanly. Its shader-stat query returns nil
on the ordinary empty model. With `_setupDataPath`, USC cliques populate
(260541 on usc[0], with 1961 kicks and 2441 tiles), and the pipeline-state ID
join reproduces across two runs. `firstBinaryIndexForCliqueAtIndex:` drifts,
so no binary attribution or `high_register` value is wired from it. Stats
`build` crashes on the populated model and remains gated.

The decoded timeline constructor was tested as another database entry point:
`-[GTMioTraceTimelineData initWithDecodedDictionary:streamData:parentData:]`
(`@40@0:8@16@24@32`) received the first object from
`unarchivedAPSTimelineData`, the live `GTMutableShaderProfilerStreamData`, and
the processor-built `GTMioTraceData`. It returned `nil` on two independent
runs. No timeline counts or database handle were read from this route.

The framework's own archive round-trip was then tested. Calling
`-[GTMioTraceData archivedData:error:]` (`@28@0:8c16^@20`) with `false`
produced an `NSData` archive of about 133.9 MB. Feeding that object to
`-[GTMioTraceData initWithArchivedData:error:]` (`@32@0:8@16^@24`) returned a
populated `GTMioTraceData` on two runs: draws 574, encoders 12, costs 575,
pipelines 18, binaries 980, USCs 40, and mGPUs 1. This proves the archive
format is usable, but not that it contains the pipeline index.

On that reconstructed object,
`-[GTMioTraceData binaryForPipelineState:programType:]`
(`@28@0:8Q16S24`) returned an empty `NSArray` for every pipeline ID at program
type 0 on both runs; each had zero binaries, instructions, and live-register
maximum. Passing the reconstructed object to
`GTMioTraceDataStats -initWithTraceData:` (`@24@0:8@16`) threw
`NSInvalidArgumentException` because it sent `databaseInternal` to
`GTMioTraceData`, on both runs. The archive round-trip therefore remains a
reproducible populated-model path, not a database-backed pipeline index.

Wrapping the archive in `GTMioKVDataStore -initWithData:` (`@24@0:8@16`) and
selecting its `costTimeline` child with `getChild:` (`@24@0:8@16`) lets
`GTMioTraceTimelineData -initWithSerializedData:streamData:parentData:`
(`@40@0:8@16@24@32`) construct a real timeline. It has a nonzero
`databaseInternal` and stable draw=574, encoder=12, cost=575, pipeline=18
counts. `binaryForPipelineState:programType:` is empty for all 18 pipelines
across program types 0 through 5 on two runs; `GTMioTraceDataStats` initializes
but adds no attributed binary data. The handle is therefore a cost/timeline
store handle, not the missing pipeline index.

With the scratch harness pointed at the raw `.gpuprofiler_raw` directory (not
the inner `streamData` file) and `_setupDataPath` enabled, the same sequence
reproduced populated data twice: archive size about 2.345 GB, reconstructed
model costCount=606 and computePositionCount=10187132, and the `costTimeline`
object had those same counts. The inner `streamData` URL alone leaves the
ordinary costCount=575 model; the directory URL is part of the loading
contract.

The repo now has an opt-in `GPUTRACE_MIO_TIMELINE_DATA=1` path that performs
this reconstruction and reads only scalar, attributed selectors from the
timeline. On two complete runs of the external fixture it reported
costCount=606, computePositionCount=10187132, 18 pipeline draw records whose
counts summed exactly to 574, 12 encoder draw records (12 each), and draw
With `GPUTRACE_MIO_SETUP_DATA_PATH=1` and the raw profiler directory, it returned
durations at data master 2 of `[14303, 13698, 1575]` for draws 0, 1, and 2.
Without raw-data setup, the same selectors are callable but return three zeros;
the length alone is not evidence that duration data is populated.
The packed `draws` array (`^{GTMioDrawMetadata=IIIIiIQIII}16@0:8`) can be
attributed without assuming the natural C stride: the opt-in path searches
candidate layouts and accepts one only when its pipeline-ID multiset exactly
matches `numDrawsForPipelineState:`. On two setup-backed runs this yielded
per-pipeline GPU time, including `0xaac` = 2,025,751 (65.99%), with total
3,069,644. If the multiset check fails, per-pipeline timing is omitted.
The exact selectors are `numDrawsForPipelineState:` (`Q24@0:8Q16`),
`numDrawsForEncoder:` (`Q20@0:8I16`), and `durationForDraw:dataMaster:`
(`Q24@0:8I16S20`). `kickDurationForEncoder:dataMaster:`
(`Q24@0:8I16S20`) was reproducibly zero for all 12 encoders, so it is
reported but not interpreted as effective GPU time. The test asserts the
pipeline sum, encoder count, and bounded duration length.

Passing `uscs[0].databaseInternal` (`Q16@0:8`) to
`-[GTMioTraceData initWithTraceDatabase:deallocator:]`
(`@32@0:8Q16@?24`) with a nil deallocator caused a SIGSEGV before returning.
That handle is a USC-local database pointer, not a top-level trace-database
handle; this route is stopped rather than retried with guessed pointers.

`GTMioTraceDataHelper initWithTraceData:` (`@24@0:8@16`) is a usable adjacent
surface on the populated model. With the opt-in track path, two full runs
reproduced `generateTopDrawTracks` count 574, `generateTopBinaryTracks` count
592, `generateTopKickTracks` count 3, and `generateTopRIATracks` count 0.
The returned `GTMioTraceTrack` samples had stable `firstIndex`, `duration`, and
`isEmpty=false` for draw and kick tracks. The binary track count was stable at
592, but binary sample order changed within one in-process repeat, so binary
samples are retracted and only the count is exposed. Per-encoder draw and per-pipeline shader aggregate tracks were
real objects but had traceCount zero. `generateShaderTrackForProgramTypes`
throws `NSInvalidArgumentException` (`dataType` unrecognized) on this model.

`GTMioTraceTrack -lanes` (`@16@0:8`) exposes object-valued lane metadata on
populated top draw, binary, and kick tracks. Reading each lane's `laneId`
(`i16@0:8`), `indexCount` (`Q16@0:8`), and `isEmpty` (`c16@0:8`) produced
non-empty lanes on two complete processor runs. The fixture reported lane ID
0 with one index on sampled draw and binary tracks; a sampled kick track
reported lane IDs 0 and 1 with 945 and 17 indexes. The raw `indexes` property
is a C pointer and was intentionally not messaged. The opt-in
`ProcessStreamData` track summary reports this safe metadata and checks that
sampled lanes are populated.

The helper's USC-specific track family was also tested with USC index `0` on
the `_setupDataPath` model, twice. Each of
`generateKickTracksForUSC:` (`@20@0:8I16`), `generateTileTracksForUSC:`
(`@20@0:8I16`), `generateCliqueTracksForUSC:` (`@20@0:8I16`),
`generateAggregatedCliqueTrackForUSC:` (`@20@0:8I16`), and
`generateCliqueInstructionTracksForUSC:` (`@20@0:8I16`) returned an empty
`NSArray` (`count=0`). This is a reproducible empty result even though the
same model reports 260541 cliques, 1961 kicks, and 2441 tiles for USC 0; the
family is therefore not exposed as an attributed capability.

The opt-in USC summary reads `cliquesCount`, `kicksCount`, and `tilesCount`
(`Q16@0:8`) and the stable clique selectors
`pipelineStateIdForCliqueAtIndex:` and `firstPCForCliqueAtIndex:` (both
`Q20@0:8I16`). Two runs reproduce the 40-core aggregate and bounded samples;
the first six samples attribute to the processor's pipeline IDs. The related
`firstBinaryIndexForCliqueAtIndex:` (`I20@0:8I16`) drifts across runs and is
explicitly excluded. `GTMioTraceDataStats build` is safe on the empty model but
crashes with populated cliques, so shader-stat extraction remains gated.

### Raw counter ingestion boundary

The capture does contain raw performance data: its `.gpuprofiler_raw` directory
has 40 `Counters_f_*.raw`, 40 `Profiling_f_*.raw`, and 40 `Timeline_f_*.raw`
files. The archived stream object also exposes
`unarchivedAPSCounterData` (`@16@0:8`, array count 142) and
`unarchivedAPSTimelineData` (`@16@0:8`, array count 135). With the ordinary
processor sequence the Mio cost model remains zero-filled. Calling the private
`GTShaderProfilerStreamData -_setupDataPath` (`@16@0:8`) before constructing the
processor changes that result: two independent runs produced `costCount=606`,
`computePositionCount=10187132`, non-zero `gpuCost`, and scalar scope totals
`scope=0,dataMaster=2 -> 100` and `scope=4,dataMaster=2 -> 0.396351`. The repo
path is opt-in via `GPUTRACE_MIO_SETUP_DATA_PATH=1` and records only safe scalar
queries; it does not reinterpret the raw C arrays. This proves counter-derived
cost ingestion, but the occupancy/ALU semantic mapping is not yet established.

An opt-in mmap probe called
`-[XRGPUAPSDataProcessor loadAPSCounters:counterSet:]`
(`c32@0:8^v16Q24`) on `Counters_f_0.raw`, `_4.raw`, `_12.raw`, and `_39.raw`,
using counter sets 0 through 3. Repeated runs, and GPU generation 2/16 with
variant and revision 0/1, returned `true` but always reported
`numUSCs=0`, `numValidUSCs=0`, `numAPSRawCounters=0`,
`numAPSDerivedCounters=0`, `firstAPSTimestamp=UINT64_MAX`, and
`lastAPSTimestamp=0`. `loadCounterGraphConfig` returned a dictionary, while
`processorFromConfig:options:` returned nil. The BOOL therefore does not prove
that a raw file was accepted.

The correct registration seam is
`-[XRGPUAPSDataProcessor addBufferAtUSCIndex:buffer:length:]`
(`v36@0:8I16*20Q28`), with the `_f_N` suffix as `uscIndex`. Adding all 40
mmaped `Counters_f_N.raw` buffers reproduced `numUSCs=40`,
`numValidUSCs=40`, and `isValidUSC:N=true` for every index. Parsing them with
`parseData:length:uscIndex:` (`c36@0:8*16Q24I32`) still returned false for all
40, and no raw/derived counter samples appeared.

The container constructor
`-[XRGPUAPSDataContainer initWithConfig:baseFolder:variant:]`
(`@40@0:8@16@24Q32`) returned a real container only for variant 1. After adding
all 40 USC and 40 RDE buffers, it reported `numUSCs=40` and `numRDEs=40`, and
`encode` returned 3,389,981,890 bytes. Passing that NSData directly to
`GTMioTraceTimelineData initWithSerializedData:streamData:parentData:` was
wrong-class input (`getChild:` was sent to NSData); wrapping it in
`GTMioKVDataStore initWithData:` returned nil. Calling
`processorFromDataContainer:options:` on the filled container crashed before
returning for options 0, 1, 2, and 3 on separate runs. This route is not landed
or treated as a capability; the crash is deterministic at the framework seam,
not a usable empty result.

The direct timeline constructor
`-[GTMioTraceTimelineData initWithAPSTraceData:timelineData:streamData:timelineType:options:parentData:]`
(`@56@0:8^v16^v24@32I40{GTMioTraceDataBuilderOptions=BBBB}44@48`) was also
tested with mmaped `Counters_f_0.raw`/`Timeline_f_0.raw` buffers, and with the
inner `streamData` archive, while keeping both mappings alive. It terminated
with SIGSEGV before returning on both runs. The `^v` buffers are therefore not
treated as accepted raw-file inputs; this constructor is stopped rather than
wrapped in an opt-in path.

- The Mio pipeline returns the baseline above. `mcaBinaryForBinaryKey:` accepts
  the members of each command's nested `NSSet`, returning
  `GTShaderProfilerMCABinary`. The measured first non-empty record had
  `allocatedGPRCount=98`, `highRegisterCount=98`, `programType=3`, and
  `uniqueIdentifier=723710`. The indexed encodings are
  `mcaBinaryForBinaryKey:` `@24@0:8@16`, `allocatedGPRCount` `i16@0:8`,
  `highRegisterCount` `i16@0:8`, `programType` `I16@0:8`, and
  `uniqueIdentifier` `Q16@0:8`.
- **Retracted.** Those per-pipeline maxima do not reproduce. Three runs over the
  same capture attributed the same values to different pipelines: `0xaac` read
  98, then 60, then 66; `0xaa8` read 113, then 113, then 98; `0xab8` read 0,
  then 16, then 113. Values absent from one run (9, 10, 13, 27, 60) appear in
  another. The binary keys reachable from a GPU command do not identify that
  command's pipeline, and MCA analysis is asynchronous
  (`-generateMCAOutput:callback:` `v28@0:8c16@?20` against
  `-_generateMCAOutputSync:` `{MCAOutput=@@}20@0:8c16`), so the walk races it.
  `mcaBinaryForBinaryKey:` returns real MCA binaries; what it does not provide
  is an attribution.
- The pipeline-keyed accessor is
  `-[GTShaderProfilerMCABinaryList initWithShaderProfilerResult:pipelineStateId:programType:]`
  (`@36@0:8@16Q24I32`), which needs no key join and is reproducible. On a model
  built by `GTShaderProfilerStreamDataProcessor` it constructs but reports
  `mcaBinaries` empty for all 18 pipelines at every program type 0 through 5, so
  `highRegisterCount` and `allocatedGPRCount` are zero. The pipeline-keyed MCA
  index appears to belong to a trace database rather than a processed stream —
  the same boundary `-[GTMioTraceDataStats initWithTraceData:]` hits.
- Per-kernel register pressure is closed as an unavailable attribution, not an
  open parser search. Four attempted binary edges all fail the determinism
  requirement: command `binaryKeys` plus `mcaBinaryForBinaryKey:` returns
  plausible but run-dependent assignments; `MCABinaryList` is reproducibly
  empty; `firstBinaryIndexForCliqueAtIndex:` drifts; and the stable
  first-PC/address join yields duplicate binary objects and does not complete
  reproducibly. A complete nested-key sweep found no archived live/high
  register field per function (only allocated/temporary-register fields).
  `liveRegisterForInstructionAtIndex:` therefore supports only the honest
  whole-capture aggregate (96 on the measured fixture), not a per-kernel
  `high_register` value. The parity field remains unwired and this question is
  not retried through those four routes.
- The parallel AGX2 processor class is present and its exact
  `initWithStreamData:` (`@24@0:8@16`) path runs, but on this Mio stream it returns a
  `GTAGX2ShaderProfilerResult` with zeros/empty collections:
  `gpuGeneration=0`, `performanceState=0`, `gpu=4`,
  `timelineGPUDuration=0`, empty plugin name, and zero shader binaries,
  commands, pipelines, encoders, derived counters, and timing info. This is
  an implemented path with an empty result, not a nil or thrown result.
  The input boundary was checked explicitly: `_setupDataPath` (`@16@0:8`) was
  called first, then the exact archived arrays were fed through `process:`
  (`v24@0:8@16`) and through
  `processShaderProfilerStreamedResult:` (`@24@0:8@16`) /
  `processBatchIdData:` (`@24@0:8@16`). The arrays contained 54 APS, 142 APS
  counter, 135 APS timeline, and 9 shader-profiler objects. Both dedicated
  feeds and the generic feed still produced the same empty result on two runs;
  no AGX2 capability is claimed for this capture.
- The lower-level `GTAGX2ShaderProfiler` initializer
  `initWithStreamData:forTargetIndex:` (`@28@0:8@16i24`) returned real
  profiler objects for target indices 0 and 1 on both runs. Its exact
  object-returning accessors `effectiveKickTimes`,
  `averagePerDrawKickDurations`, `loadActionTimes`, `storeActionTimes`,
  `perRingPerFrameLimiterData`, and `timingInfo` (all `@16@0:8`) were nil or
  empty in both runs. This confirms the lower-level AGX2 entry point is also
  capture-input empty, not a usable alternate result path.
- `GTMioShaderAnalyzer` rejects every tested scope at construction, including
  the `_setupDataPath` model. The exact
  pipeline initializer (`@40@0:8Q16S24c28@32`) returned nil for all 18 pipeline
  IDs × program types 0–5 × base/non-base. The encoder initializer
  (`@36@0:8I16S20c24@28`) returned nil for all 12 encoder indices over the same
  matrix, and the draw initializer (`@36@0:8I16S20c24c28`) returned nil for all
  574 draw indices over the same matrix. The full sweep reproduced on a second
  run. Its four raw C-pointer histograms (`instructionTypeInfo`,
  `instructionScopeInfo`, `instructionDataTypeInfo`, and
  `instructionMemoryTypeInfo`, each `^{?=SIQQQd}16@0:8`) were never
  dereferenced. These are clean constructor rejections, not zero-filled
  analyzers.
- Calling the analyzer's explicit build methods did not change that boundary.
  On each of the 18 pipeline IDs, `buildPipeline:programType:traceData:`
  (`c36@0:8Q16S24@28`) with program type 0 returned `false`, and on draws 0,
  1, and 573 `buildDraw:programType:traceData:` (`c32@0:8I16S20@24`) also
  returned `false`; all four histogram counts remained zero. The complete
  output was byte-identical across two runs. These methods therefore reject
  the processor-built model rather than exposing a usable histogram path.
- `GTMioShaderExecutionHistory` accepts pipeline, draw, and clique generation:
  `generateDrawIndex:programType:` (`c24@0:8I16S20`) returned true for draws
  0, 1, and 573, and `generateCliqueIndex:uscIndex:`
  (`c24@0:8I16I20`) returned true for USC 0 cliques 0, 1, and 260540 on both
  runs. `nodeForStyle:` (`@20@0:8I16`) remained nil for styles 0 through 7
  after every request. These are accepted-but-unmaterialized requests, not
  execution-history data.
- The model-level builders do not materialize the tree either. With
  `executionHistoryForPipelineState:programType:delegate:progressController:`
  (`v44@0:8Q16S24@28@36`) for pipeline `0xab5`, and
  `executionHistoryForDraw:programType:delegate:progressController:`
  (`v40@0:8I16S20@24@32`) for draw 0, both calls returned void without error;
  styles 0 through 7 remained nil on two runs. No pending-history wait method
  was available on the Mio model. This closes the obvious builder route for
  the capture.
- `GTMioEncoderQuadData initWithTraceData:encoderFunctionIndex:programType:options:`
  (`@40@0:8@16I24S28Q32`) returned nil for all encoder indices 0 through 11
  with `(mioData, index, 0, 0)` on two ordinary processor-model runs. The
  setup-path first run also returned nil for all 12, but its second expensive
  process terminated before the probe; the reproducible classification is
  therefore the ordinary-model nil result only. No quad data is exposed.
- `GTJSScriptingContext +sharedContext` (`@16@0:8`) returned a real context.
  `setValue:value:` (`@32@0:8@16@24`) followed by `getValue:`
  (`@24@0:8@16`) round-tripped an `NSNumber` value `42.5` as a `JSValue`;
  `virtualMachine` (`@16@0:8`) returned `JSVirtualMachine`. The result was
  identical on two runs. This is a confirmed auxiliary capability, but no
  profiler-specific model surface has yet been found through the bridge, so
  it remains documented rather than exposed by the adapter.
- `GTLLVMConnectionManager initWithGPUName:withTargetIndex:binaryPath:withGen:withSocketName:forNumClients:`
  (`@52@0:8@16i24@28C36@40I48`) with
  `("Apple M4 Max", 0, GTLLVMHelper, 16, "", 1)` returned a manager on both
  runs: `version=3`, `nLLVMClients=1`, `targetIndex=0`, and
  `gpuName="Apple M4 Max"`. `createLLMVAnalyzerForFilePath:`
  (`I24@0:8@16`) on the helper executable returned `UINT32_MAX`;
  `isLLVMValid:` returned false and `binarySize:` returned 0. The dump
  selectors returned nil. The manager surface is live, but this non-shader
  input is rejected and produces no analysis data.
- `GTMioTraceData gpuInfo` (`@16@0:8`) was nil on two direct-model runs
  (`initWithStreamData:llvmHelperPath:options:` with option `0`). The only
  standalone `GTMioGPUInfo` initializer takes a raw
  `GTMioGPUInfoInternal` pointer (`@24@0:8r^{GTMioGPUInfoInternal=IIIIII}16`),
  so it was not called with a guessed layout; this surface remains
  unavailable from the processor-built model.
- The direct `XRGPUAPSDataProcessor` route was tested with generation `2` and
  `16`, variant `0`, revision `0`, the bundled `loadCounterGraphConfig`
  object, and options `0`. All 40 `Counters_f_N.raw` buffers were retained and
  sent through `addBufferAtUSCIndex:buffer:length:` (`v36@0:8I16*20Q28`) and
  `parseData:length:uscIndex:` (`c36@0:8*16Q24I32`). Both runs returned
  `false` for every parse while reporting `numUSCs=40` and
  `numValidUSCs=40`; raw/derived counter counts stayed zero. Four
  `loadAPSCounters:counterSet:` calls (`c32@0:8^v16Q24`) returned true but
  remained vacuous, and `loadCounters:` returned false. Counter-config
  lookups were empty dictionaries. No raw APS values are claimed from this
  route.
- `loadShaders:uscIndex:` (`c28@0:8^v16I24`) was then tried on the retained
  `Profiling_f_N.raw` mappings. It returned true for USC 0 and then produced a
  SIGSEGV before USC 1 on both isolated runs. No shader values were read and
  this crash boundary is intentionally not exposed by gputrace.
- `XRGPUAPSDataContainer -initWithConfig:baseFolder:variant:`
  (`@40@0:8@16@24Q32`) with variant `1` accepted all 40 buffers through
  `addDataForUSCAtIndex:data:` (`v28@0:8I16@20`) and reported `numUSCs=40`.
  The correctly targeted `+[XRGPUAPSDataProcessor
  processorFromDataContainer:options:]` (`@28@0:8@16I24`) returned nil for
  options 0 and 1 on both runs. The container route therefore produces no
  counter model for this capture.
- `mGPUs` (`@16@0:8`) returned one `GTMioMGPUTraceData`; its `index`
  (`Q16@0:8`) was 0, while `kicksCount` and `costCount` were zero on both
  runs. This is an allocated-but-empty MGPU model, not an additional timing
  source.
- `loadTimeline` and `loadCostTimeline` (`v16@0:8`) completed on the
  setup-path model. Across two runs, `loadingCostTimeline=false`,
  `isMio=true`, `consistentStateAchieved=true`, and
  `hasSeparateCostsTimeline=true`. `costTimeline`, `overlappingTimeline`, and
  `nonOverlappingTimeline` (`@16@0:8`) each returned a real
  `GTMioTraceTimelineData`, but all had count 0. This is a successful lazy
  load with no additional samples, not a usable timeline export.
- [V] There are two populated counter dictionaries, not one, and they share no
  names. `nonOverlappingTimeline.timelineCounters` carries 19 counters, all
  plaintext, all memory-side: `AF Bandwidth`, `L2 Cache Limiter`,
  `MMU Utilization`, `Texture Cache Utilization`, and so on, each with 112972
  samples on the 11-encoder `qwen25-05b-static_tokens_2_to_3-wperfdata`
  archive. The `costTimeline` child reached through the archive seam carries a
  different 30, 17 plaintext and 13 opaque 64-hex, and those are the
  shader-side ALU counters. Intersecting the two name sets yields nothing.
  Which timeline object you ask decides which family you get, and the opaque
  identifiers are confined to `costTimeline`.

  Reading the 19 needs care: `Texture Read Limiter` reports a peak of
  8.99e10 where every sibling limiter is a percentage bounded near 101. Treat
  that column as unread until the encoding is established, not as a large
  measurement.
- On the setup-path model, `timelineCounters` (`@16@0:8`) returned a real
  `GTMioTimelineCounters` whose counter dictionary was empty. That result stands
  only for the top-level model; see the entry above for the two timeline
  children, which are populated. `nonOverlappingCounters`
  (`@16@0:8`) returned a real object with 832 encoder, 72 draw, and 72 pipeline
  slots; its name arrays included `ALU Total Instructions`, `ALU F16
  Instructions`, and `ALU F32 Instructions`. The first derived objects were
  `GTMioCounterDataPerDM`, with sample counts 12/574 but zero-valued `^d`
  arrays and DBL_MAX/0 min/max. These values reproduced across two runs and
  are classified as allocated-but-zero, not usable parity data.
- `GTMioShaderExecutionHistory` initialization returns a real object via
  `initWithTraceData:style:options:delegate:` (`@40@0:8@16I24I28@32`). Twice
  over all 18 pipeline IDs, `generatePipelineStateId:programType:`
  (`c28@0:8Q16S24`) returned `true`, but `nodeForStyle:` (`@20@0:8I16`)
  returned `nil` for styles `0..7` for every pipeline. The generator therefore
  accepts the request but materializes no tree on this capture; no execution
  history data is exported. Heatmap construction returns nil.
  The processor-built model exposes 40 USC objects through `uscs`; each has a
  non-zero `databaseInternal`. Without `_setupDataPath`, cliques are zero. With
  it, `usc[0]` has 260541 cliques, 1961 kicks, and 2441 tiles, and
  `pipelineStateIdForCliqueAtIndex:` plus `firstPCForCliqueAtIndex:` are stable
  across two runs. `GTMioTraceDataStats initWithTraceData:` accepts one USC
  object followed by `build` only on the empty model; on the populated model
  it crashes, so no shader-stat values are claimed. The original
  `databaseInternal unrecognized` result came only from passing the top-level
  `GTMioTraceData` to a USC/database-backed initializer.

  A capture containing USC data would provide the structural attribution that
  MCA lacks: `pipelineStateIdForCliqueAtIndex:` (`Q20@0:8I16`),
  `firstBinaryIndexForCliqueAtIndex:` (`I20@0:8I16`),
  `firstPCForCliqueAtIndex:` (`Q20@0:8I16`), and
  `pcForInstruction:binaryIndex:` (`Q24@0:8I16I20`). It would also expose
  per-USC costs and `GTMioTraceDataStats shaderStatForShader:programType:`
  (`@28@0:8Q16S24`). The USC objects are present here, but their clique count is
  zero, so this is a
  capture requirement, not an API failure.

## Complete class inventory

| class | loaded | methods | no-argument object-shaped methods | pointer returns | initializers |
|---|---:|---:|---:|---:|---:|
| DYGPUDerivedEncoderCounterInfo | true | 14 | 4 | 0 | 2 |
| DYGPUTimelineInfo | true | 27 | 9 | 0 | 2 |
| DYTimelineCounterGroup | true | 12 | 4 | 0 | 2 |
| DYWorkloadGPUTimelineInfo | true | 38 | 10 | 0 | 2 |
| GRCPerFrameDataClass | true | 3 | 2 | 0 | 1 |
| GTAGX2InstructionPCStatInfoClass | true | 4 | 2 | 0 | 1 |
| GTAGX2ShaderAnalyzer | true | 6 | 1 | 0 | 1 |
| GTAGX2ShaderBinary | true | 39 | 7 | 0 | 2 |
| GTAGX2ShaderBinaryInfo | true | 15 | 7 | 0 | 1 |
| GTAGX2ShaderBinaryLocation | true | 15 | 3 | 0 | 2 |
| GTAGX2ShaderBinaryRange | true | 19 | 4 | 0 | 2 |
| GTAGX2ShaderDiassembly | true | 17 | 2 | 0 | 2 |
| GTAGX2ShaderProfiler | true | 39 | 8 | 0 | 1 |
| GTAGX2ShaderProfilerEncoder | true | 23 | 2 | 0 | 2 |
| GTAGX2ShaderProfilerGPUCommand | true | 28 | 4 | 0 | 1 |
| GTAGX2ShaderProfilerPipelineState | true | 30 | 6 | 0 | 2 |
| GTAGX2ShaderProfilerResult | true | 39 | 10 | 0 | 2 |
| GTAGX2ShaderProfilerShaderFunction | true | 18 | 2 | 0 | 1 |
| GTAGX2ShaderProfilerTiming | true | 8 | 1 | 0 | 1 |
| GTAGX2StreamDataShaderProfilerProcessor | true | 31 | 4 | 0 | 1 |
| GTAGX2StreamDataTimelineProcessor | true | 16 | 2 | 0 | 1 |
| GTJSScriptingContext | true | 27 | 4 | 2 | 1 |
| GTLLVMConnectionManager | true | 27 | 2 | 0 | 1 |
| GTMioCounterData | true | 14 | 2 | 0 | 1 |
| GTMioCounterDataPerDM | true | 14 | 3 | 0 | 1 |
| GTMioEncoderQuadData | true | 37 | 2 | 5 | 3 |
| GTMioGPUInfo | true | 9 | 0 | 0 | 1 |
| GTMioHeatmapBuilder | true | 17 | 1 | 1 | 4 |
| GTMioHeatmapHistogram | true | 12 | 1 | 1 | 2 |
| GTMioHeatmapImpl | true | 30 | 2 | 3 | 1 |
| GTMioInstructionALUSubPipeCountCounter | true | 2 | 0 | 0 | 1 |
| GTMioInstructionTypeCountCounter | true | 2 | 0 | 0 | 1 |
| GTMioKVDataStore | true | 29 | 4 | 1 | 4 |
| GTMioMGPUTraceData | true | 13 | 3 | 2 | 1 |
| GTMioNonOverlappingCounters | true | 26 | 8 | 0 | 2 |
| GTMioShaderAnalyzer | true | 23 | 2 | 4 | 3 |
| GTMioShaderBinaryData | true | 64 | 4 | 14 | 1 |
| GTMioShaderExecutionHistory | true | 33 | 7 | 0 | 1 |
| GTMioShaderExecutionHistoryCliqueNode | true | 9 | 2 | 1 | 1 |
| GTMioShaderExecutionHistoryDefaultDelegate | true | 3 | 0 | 0 | 0 |
| GTMioShaderExecutionHistoryFunctionNode | true | 26 | 7 | 4 | 2 |
| GTMioShaderExecutionHistoryInstructionNode | true | 15 | 5 | 2 | 1 |
| GTMioShaderExecutionHistoryLoopNode | true | 17 | 4 | 2 | 1 |
| GTMioShaderExecutionHistoryNode | true | 34 | 7 | 0 | 1 |
| GTMioShaderExecutionHistoryRootNode | true | 28 | 5 | 0 | 1 |
| GTMioShaderProfilerEncoder | true | 10 | 1 | 0 | 1 |
| GTMioShaderProfilerGPUCommand | true | 15 | 3 | 0 | 1 |
| GTMioShaderProfilerPipelineState | true | 13 | 4 | 0 | 1 |
| GTMioShaderProfilerResult | true | 32 | 9 | 0 | 2 |
| GTMioShaderProfilerShaderFunction | true | 9 | 2 | 0 | 1 |
| GTMioTimelineCounters | true | 4 | 1 | 0 | 1 |
| GTMioTraceAggregatedDrawTrack | true | 6 | 1 | 1 | 0 |
| GTMioTraceAggregatedShaderTrack | true | 6 | 1 | 1 | 0 |
| GTMioTraceCliqueInstructionTraceTrack | true | 6 | 1 | 1 | 0 |
| GTMioTraceCliqueTrack | true | 6 | 1 | 0 | 0 |
| GTMioTraceData | true | 98 | 12 | 12 | 4 |
| GTMioTraceDataHelper | true | 44 | 9 | 0 | 1 |
| GTMioTraceDataObserverTokenInternal | true | 5 | 0 | 0 | 1 |
| GTMioTraceDataShaderStat | true | 5 | 1 | 0 | 1 |
| GTMioTraceDataStats | true | 6 | 1 | 0 | 1 |
| GTMioTraceShaderCliqueInstructionTraceTrackGroup | true | 12 | 1 | 2 | 1 |
| GTMioTraceTimelineData | true | 106 | 8 | 18 | 6 |
| GTMioTraceTrack | true | 15 | 2 | 1 | 1 |
| GTMioTraceTrackLane | true | 8 | 1 | 0 | 1 |
| GTMioUSCTraceData | true | 50 | 3 | 10 | 1 |
| GTMioWeakPerDrawCounterObserver | true | 4 | 1 | 0 | 1 |
| GTMutableShaderProfilerStreamData | true | 26 | 1 | 0 | 2 |
| GTShaderProfilerAnalyzer | true | 9 | 2 | 0 | 1 |
| GTShaderProfilerAnalyzerToolchain | true | 3 | 1 | 0 | 0 |
| GTShaderProfilerBinaryAnalysisResult | true | 46 | 8 | 0 | 2 |
| GTShaderProfilerCounterGroupInfo | true | 13 | 4 | 0 | 1 |
| GTShaderProfilerCounterInfo | true | 26 | 6 | 0 | 1 |
| GTShaderProfilerCounterSpec | true | 10 | 5 | 0 | 1 |
| GTShaderProfilerDebugDump | true | 5 | 0 | 0 | 1 |
| GTShaderProfilerDeviceInfo | true | 14 | 4 | 0 | 1 |
| GTShaderProfilerDiassemblyRegisterPressure | true | 11 | 6 | 0 | 2 |
| GTShaderProfilerMCABinary | true | 12 | 4 | 0 | 3 |
| GTShaderProfilerMCABinaryList | true | 5 | 1 | 0 | 1 |
| GTShaderProfilerProcessedData | true | 17 | 4 | 0 | 2 |
| GTShaderProfilerRegisterPressureView | true | 8 | 2 | 0 | 1 |
| GTShaderProfilerRegisterUsage | true | 6 | 1 | 0 | 1 |
| GTShaderProfilerSessionRequest | true | 10 | 2 | 0 | 1 |
| GTShaderProfilerStreamData | true | 84 | 30 | 0 | 4 |
| GTShaderProfilerStreamDataForMetadata | true | 3 | 0 | 0 | 1 |
| GTShaderProfilerStreamDataProcessor | true | 26 | 5 | 0 | 1 |
| GTShaderProfilerStringCache | true | 8 | 2 | 0 | 2 |
| GTShaderProfilerTimingInfo | true | 8 | 0 | 0 | 2 |
| XRGPUAGXShaderTimelineSignposts | true | 15 | 3 | 0 | 2 |
| XRGPUAPSDataContainer | true | 30 | 3 | 0 | 3 |
| XRGPUAPSDataProcessor | true | 84 | 8 | 0 | 1 |
| XRGPUAPSDerivedCounter | true | 6 | 2 | 0 | 1 |
| XRGPUATRCImporter | true | 15 | 4 | 0 | 2 |
| XRGPUShaderInfo | true | 22 | 4 | 0 | 1 |

The method index remains the authoritative selector and type-encoding ledger;
this table deliberately does not claim that a method was invoked merely because
its class loaded. All demonstrated sends used the index encoding, selector
availability guards, a locked OS thread, one autorelease pool, and a live
processor.
