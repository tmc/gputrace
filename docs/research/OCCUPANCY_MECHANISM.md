# Kernel Occupancy: mechanism and normalization constant

Confidence markers: `[disasm]` read out of a binary, `[runtime]` observed in a
real capture, `[inference]` reasoned, could be wrong.

## Result

`Kernel Occupancy` and `Compute SIMD Groups Inflight per Core` are the SAME
measured quantity. The first is the second divided by the maximum simdgroups
inflight per shader core, which is **96**.

    Kernel Occupancy (%) = Compute Simdgroups Inflight Per Shader Core / 96 * 100

Occupancy is therefore a MEASURED counter, not a derived one, and not derivable
from anything we currently parse (L1 residency correlates at r=0.98 but the
ratio to occupancy ranges 22.2..49.7 — no formula). Recovering it requires
decoding the vendor counter out of `Counters_f_*.raw`.

## Evidence

### 1. The two counters are defined as the same numerator [disasm]

`GTShaderProfiler` (Xcode 26,
`Xcode.app/Contents/PlugIns/GPUDebugger.ideplugin/Contents/Frameworks/GTShaderProfiler.framework`)
carries both vendor counter descriptions verbatim in its string table:

    Compute Occupancy
      The number of compute simdgroups running concurrently on the shaders
      cores, normalized to max simdgroups inflight supported by the GPU.
    Compute Simdgroups Inflight Per Shader Core
      The number of compute simdgroups running concurrently per shader core.

`Resources/GPUCounterGraph.plist` (455 counters, the full Xcode counter graph)
binds the UI names to those vendor counters:

    "Kernel Occupancy"  vendorCounters: ShaderCoreComputeUtilization,
                                        ComputeOccupancy, Compute Occupancy
                        unit: "Percentage of Shader Core Resources"
    "Compute SIMD Groups Inflight per Core"
                        vendorCounters: Compute Simdgroups Inflight Per Shader Core
                        unit: "SIMD Groups"        <- a COUNT, not a percentage

So the only difference is the normalization, and the divisor is a device
property ("max simdgroups inflight supported by the GPU").

### 2. The divisor is the literal 96.0 [disasm]

`+[GTShaderProfilerRegisterPressureView maxTheoriticalOccupancyWithRegisterCount:gpu:]`
computes the same normalization for the static case. Decompiled:

    if regCount == 0 || gpu > 4: return 0
    maxThreadsPerCore = (gpu == 0) ? 2048 : 3072          # 0x800 / 0xc00
    registerFile      = (gpu <  2) ? 98304.0 : 53248.0    # 0x47c00000 / 0x47500000
    perSimdgroup      = ceil(regCount/8) * 512
    regLimitedThreads = floor(registerFile / perSimdgroup) * 64
    threads           = min(maxThreadsPerCore, regLimitedThreads)
    return floor(threads / 32) / 96.0                     # 0x42c00000 == 96.0f

`floor(threads/32)` converts threads to simdgroups; the divisor `96.0` is the
per-core simdgroup capacity. It is consistent with `maxThreadsPerCore = 3072`
(3072 / 32 = 96) for every GPU enum except 0.

### 3. The one available runtime point agrees to within its rounding [runtime]

Xcode Timeline, one encoder of
`qwen25-05b-staticmask-warm-tokens2-4-rep1-perfdata3.gputrace` (M4 Max,
AGXMetalG16X):

    Kernel Occupancy                        46.33
    Compute SIMD Groups Inflight per Core   44.48

    44.48 / 0.4633 = 96.0069

Propagating the display rounding (46.33 +- 0.005, 44.48 +- 0.005) gives an
implied capacity of 95.986 .. 96.028, which brackets 96 and excludes 95 and 97.

Residual: this is ONE data point. The 12 Xcode tab exports in the oracle contain
`Kernel Occupancy` but no SIMD-inflight column, so the relation cannot currently
be checked across the other 22 encoders. The disassembly, not the point, is what
carries the claim; the point only confirms the disassembled constant is the one
actually applied at runtime.

## What this does and does not buy us

- It refutes "occupancy is unobtainable". It is obtainable, from one counter.
- It does not by itself produce a number. `Compute Simdgroups Inflight Per
  Shader Core` is a sampled hardware counter living in `Counters_f_*.raw`, which
  we do not yet decode. Nothing in `streamData` or the parsed records carries it
  (grepped: no `occupancy`/`simdgroup`/`inflight` strings in either).
- Do NOT reintroduce a computed occupancy from L1 residency or anything else.
  Until the counter is decoded, gputrace should emit no occupancy at all.

## Open

- Which `gpu` enum value the M4 Max maps to (affects the register-file size used
  by the static max-theoretical calculation, not the 96 divisor). [inference]
- A second runtime point would settle the constant independently of the
  disassembly: any Xcode Timeline reading showing Kernel Occupancy and Compute
  SIMD Groups Inflight per Core together for a different encoder.

## Side finding: GPUCounterGraph.plist is a full counter dictionary

    .../GTShaderProfiler.framework/Versions/A/Resources/GPUCounterGraph.plist
    (identical copy under Instruments.app/.../GPUPlugin.xrplugin)

455 counters with `name`, `description`, `unit`, `counterType`, and the
`vendorCounters` each maps to, plus `groups` (the tab layouts) and
`timelineGroups` (the Timeline tracks). This is the authoritative UI-name to
vendor-counter mapping for the whole oracle, useful for the counter-decode and
parity work.
