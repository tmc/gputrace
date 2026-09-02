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

### 3. Three runtime points at three operating points all give 96 [runtime]

All from `qwen25-05b-staticmask-warm-tokens2-4-rep1-perfdata3.gputrace`
(M4 Max, AGXMetalG16X).

Point 1 — Timeline tooltip for one encoder:

    Kernel Occupancy                        46.33
    Compute SIMD Groups Inflight per Core   44.48
    44.48 / 0.4633 = 96.0069

Display rounding (both +- 0.005) puts the implied capacity in 95.986 .. 96.028,
which brackets 96 and excludes 95 and 97.

Points 2 and 3 — Timeline right-hand Counters panel, Occupancy filter tab, which
shows per-counter-group AVERAGES over the whole timeline window, so the two
groups below are directly comparable:

    SIMD Groups Inflight per Core (count)   Occupancy (% shader core resources)
      Total     30.60                         Total   31.87
      Vertex     0.02                         VS       0.02
      Fragment   1.30                         FS       1.35
      Compute   29.28                         Kernel  30.50

    Compute / Kernel = 29.28 / 0.3050 = 96.00
    Total   / Total  = 30.60 / 0.3187 = 96.01

The vertex and fragment rows are internally consistent as well (0.02 count vs
0.02%, 1.30 vs 1.35%), so the relation holds per stage, not only for the compute
aggregate. Three points, three distinct operating points, 96.00 .. 96.01.

#### Xcode prints the count with a stray percent sign

The Timeline renders `Compute SIMD Groups Inflight` as e.g. "29.28%", but
`GPUCounterGraph.plist` gives its unit as "SIMD Groups" and 29.28 simdgroups/core
against a 96 capacity IS 30.50% occupancy. The trailing `%` is spurious. This is
why the ratio comes out as 96 rather than 0.96; anyone re-deriving the relation
will hit the same confusion.

#### Do not cross aggregation windows

Pairing a Timeline panel average against a per-encoder value from the Counters
CSV export is invalid — different aggregation windows. Doing it (panel 29.28 vs
per-encoder Kernel Occupancy 41.67% for encoder 0x79f00fac0) yields 70.3 and
looks like a refutation. Compare panel to panel, or tooltip to tooltip, only.

#### Remaining limit

Per-encoder residuals across all 23 encoders still cannot be computed. No export
we have carries both quantities: the 12 oracle tab exports and the Counters-tab
CSV (`Counters.csv`, 247 cols x 23 encoders) contain `Kernel Occupancy` and
`Occupancy Manager Target` but no SIMD-inflight column at all (grepped for
"SIMD", "Inflight", "Active Cores" — zero hits). The Timeline panel is currently
the only surface showing both, and it reports window averages, not per-encoder
values. So the claim rests on the disassembly plus three window-level points, not
on 23-row coverage.

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
- Per-encoder validation across the 23 encoders, which needs a source that
  reports SIMD-inflight per encoder. None of our exports does.

## Side finding: GPUCounterGraph.plist is a full counter dictionary

    .../GTShaderProfiler.framework/Versions/A/Resources/GPUCounterGraph.plist
    (identical copy under Instruments.app/.../GPUPlugin.xrplugin)

455 counters with `name`, `description`, `unit`, `counterType`, and the
`vendorCounters` each maps to, plus `groups` (the tab layouts) and
`timelineGroups` (the Timeline tracks). This is the authoritative UI-name to
vendor-counter mapping for the whole oracle, useful for the counter-decode and
parity work.
