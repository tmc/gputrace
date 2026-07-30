# GTShaderProfiler zero-argument initializer smoke results

This is the isolated smoke pass for the 20 classes whose supplied index contains an exact `-init` encoding of `@16@0:8`. Only indexed no-argument methods with scalar/object (never `^{...}` pointer) returns were sent. Each class ran in its own process under `LockOSThread` and one autorelease pool. `nil`, zero, and empty object results are intentionally preserved as different observations.

```text
class=DYGPUDerivedEncoderCounterInfo init=object
  derivedCounterNames=object:nil
  derivedCounters=object:nil
  encoderInfos=object:nil
class=DYGPUTimelineInfo init=object
  numPeriodicSamples=0
  timestamps=object:nil
  derivedCounters=object:nil
  derivedCounterNames=object:nil
  activeShadersPerPeriodicSample=object:nil
  activeCoreInfoMasksPerPeriodicSample=object:nil
  numActiveShadersPerPeriodicSample=object:nil
  encoderTimelineInfos=object:nil
  metalFXTimelineInfo=object:nil
class=DYTimelineCounterGroup init=object
  timestamps=object:nil
  counters=object:nil
  counterNames=object:nil
class=DYWorkloadGPUTimelineInfo init=object
  createCounterGroup=object:DYTimelineCounterGroup
  isMio=false
  version=9
  timeBaseNumerator=0
  timeBaseDenominator=0
  mGPUTimelineInfos=object:__NSArrayM
  aggregatedGPUTimelineInfo=object:DYGPUTimelineInfo
  perRingSampledDerivedCounters=object:nil
  coreCounts=object:nil
  derivedEncoderCounterInfo=object:nil
  profiledState=0
  consistentStateAchieved=false
  restoreTimestamps=object:nil
  coalescedEncoderInfo=object:nil
  counterGroups=object:__NSArrayM
class=GRCPerFrameDataClass init=object
class=GTAGX2InstructionPCStatInfoClass init=object
class=GTAGX2ShaderAnalyzer init=object
class=GTAGX2ShaderProfilerEncoder init=object
  objectId=0
  pointerId=0
  index=0
  loadTime=0
  storeTime=0
  timingInfo=object:nil
  functionIndex=0
  gpuCommandStartIndex=0
  numGPUCommands=0
class=GTAGX2ShaderProfilerPipelineState init=object
  binaryKeys=object:nil
  allBinaryKeys=object:nil
  shaderFunctions=object:nil
  timingInfo=object:nil
  objectId=0
  pointerId=0
  index=0
  numGPUCommands=0
  functionIndex=0
class=GTAGX2ShaderProfilerResult init=object
  profilerMode=0
  gpu=4
  mioData=object:nil
  gpuGeneration=0
  metalPluginName=object:nil
  performanceState=0
  wasPerformanceStateConsistent=false
  unixTimestamp=0
  shaderBinaries=object:nil
  gpuCommands=object:nil
  pipelineStates=object:nil
  encoders=object:nil
  derivedCountersData=object:nil
  timingInfo=object:nil
  timelineGPUDuration=0
class=GTJSScriptingContext init=object
  virtualMachine=object:JSVirtualMachine
  context=object:JSContext
class=GTMioKVDataStore init=object
  serialize=object:NSConcreteMutableData
  description=object:__NSCFString
  compressBlocks=false
class=GTMutableShaderProfilerStreamData init=object
class=GTShaderProfilerBinaryAnalysisResult init=object
  instructionCount=0
  clauseCount=0
  binaryRangeCount=0
  binaryLocationCount=0
  branchTargetCount=0
  registerInfoCount=0
  maxOffset=0
  version=3
  instructionData=object:nil
  clauseData=object:nil
  branchTargetData=object:nil
  binaryRangeData=object:nil
  binaryLocationData=object:nil
  registerInfoData=object:nil
class=GTShaderProfilerDiassemblyRegisterPressure init=object
  highRegisterIndex=0
  liveRegisters=0
  allocs=object:GTShaderProfilerRegisterUsage
  defs=object:GTShaderProfilerRegisterUsage
  lastUses=object:GTShaderProfilerRegisterUsage
  uses=object:GTShaderProfilerRegisterUsage
  live=object:GTShaderProfilerRegisterUsage
class=GTShaderProfilerSessionRequest init=object
  profilerMode=0
  performanceState=2
  executionMode=0
  streamDataToLoad=object:nil
class=GTShaderProfilerStreamData init=object
  gpuCommandInfoCount=0
  encoderInfoCount=0
  pipelineStateInfoCount=0
  commandBufferInfoCount=0
  functionInfoCount=0
  unarchivedShaderProfilerData=object:nil
  unarchivedGPUTimelineData=object:nil
  unarchivedAPSData=object:nil
  unarchivedAPSCounterData=object:nil
  unarchivedAPSTimelineData=object:nil
  unarchivedBatchIdFilteredCounterData=object:nil
  _setupDataPath=object:NSURL
  shortDescription=object:__NSCFString
  description=object:__NSCFString
  version=5
  blitCallCount=0
  gpuCommandInfoData=object:nil
  encoderInfoData=object:nil
  pipelineStateInfoData=object:nil
  commandBufferInfoData=object:nil
  archivedGPUTimelineData=object:nil
  archivedShaderProfilerData=object:nil
  archivedAPSData=object:nil
  archivedAPSTimelineData=object:nil
  archivedAPSCounterData=object:nil
  functionInfoData=object:nil
  strings=object:nil
  dataSourceHasUnusedResources=false
  archivedBatchIdFilteredCounterData=object:nil
  batchIdFilterableCounters=object:nil
  gpuGeneration=0
  metalPluginName=object:nil
  pipelinePerformanceStatistics=object:nil
  traceName=object:nil
  supportsFileFormatV2=false
  unixTimestamp=0
  dataFileURL=object:NSURL
  isPreSiData=false
  preSiBundleURL=object:nil
  metalDeviceName=object:nil
  deviceInfo=object:nil
  profiledPerformanceState=0
  profiledProfilerMode=0
  profiledExecutionMode=0
class=GTShaderProfilerStringCache init=object
  strings=object:__NSArrayM
class=XRGPUAGXShaderTimelineSignposts init=object
  encode=object:NSConcreteMutableData
  start=false
class=XRGPUATRCImporter init=object
  agxTraceConfig=object:nil
  agxDriverConfig=object:nil
  load=object:nil
```

