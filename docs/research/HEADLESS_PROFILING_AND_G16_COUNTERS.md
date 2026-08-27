# Headless profiling and the G16 counter-definition problem

Status as of 2026-08-09. Machine: macOS 26.6.1 (25G76), Apple silicon,
`MetalPluginName = "AGXMetalG16X"`, Xcode 26.3 (24587) and an Xcode-rc install.
MTLReplayer 314.14.

Every claim carries a marker, per `docs/research/README.md`:

- `[V]` verified — a command was run; the command is given
- `[D]` derived — from a test that could have failed; what would have failed it is stated
- `[?]` untested hypothesis

Nothing here is generalized beyond this machine. Several findings below are
corrections of earlier beliefs held in this repo; those are marked as such,
because the wrong version survived long enough to direct real work.

## Why this document exists

Two problems were being worked at once, and they turned out to share an answer.

1. `gputrace xcode-profile run` drives Xcode's UI to produce a perfdata bundle.
   It works, and it takes over the machine for 5-45 minutes per run.
2. `TestParity` needs a counter oracle. The route we had planned went through
   Xcode's Counters tab, which is the same UI problem, at a larger scale.

Problem 1 is now solved: MTLReplayer replays and profiles headlessly. Problem 2
is not solved, and the reason it is not solved is a property of this GPU
generation rather than of our tooling.

## Part 1 — Headless replay and profiling via MTLReplayer

### The working recipe [V]

```
open -W -n -a /System/Library/CoreServices/MTLReplayer.app --args \
  -CLI in.gputrace -profileTrace -collectProfilerData -outputPath out.gputrace
```

Verified end to end on the `02-two-encoders` fixture: 5.8 s, exit 0, input
SHA-256 manifest unchanged, 60 MB output containing `in.gpuprofiler_raw` with
40 `Counters_f` shards and `streamData`. `gputrace profiler` read it as
`1 CB, 2 encoders, 2 dispatches`. iTerm2 remained frontmost for the whole run;
no Xcode activation or window event occurred.

### Two facts that are easy to get wrong

`-CLI` must be **argv[1]** exactly. [V] Not merely "before the archive path":
`MTLReplayer.mm:604` tests `strcmp("-CLI", argv[1])`, then shifts
`argv[1] = argv[0]` and begins the option scan at index 2. With the archive
first, `main` enters `NSApplicationMain` and the process sits in a GUI event
loop. An early 70-second "hang" in this investigation was this mistake and not
a tool failure.

The usage string is misleading and should not be trusted over the code. Line
619 prints `Invalid command-line arguments (usage: MTLReplayer archivePath
[options])`, which appears to contradict the above. It does not: it is emitted
*after* the `argv[1] = argv[0]` shift, so it describes the post-shift vector.
[V]

A single unknown token aborts the run — `ParseArguments` raises
`Unknown token: %s` and stops. There is no silent ignoring of bad flags, which
means a run that completes did at least parse everything you passed it. [V]

`--replay` is not required. [V] Ordinary replay is the default fallthrough;
the `0x40` bit it sets does not gate it. A run without `--replay` produced the
same 40 shards and identical `gputrace profiler` output.

### Launching it at all: AMFI [V]

Direct `exec` of the binary is SIGKILLed:

```
Launch Constraint Violation (enforcing), error info: c[1]p[1]m[1]e[14]
```

The constraint is on the **parent** process, not on MTLReplayer. LaunchServices
satisfies it, so `open -n -a` works where `exec` cannot. This was initially
misdiagnosed as a sandbox artifact of the agent harness; running with the
sandbox disabled produced the same kill, which is what ruled that out. [V]

MTLReplayer holds entitlements a hand-built Metal client cannot obtain:
`com.apple.private.agx.performance-spi`, `com.apple.private.gputools.client`,
`AGXDeviceUserClient`, `IOReportUserClient`, and mach-lookup for
`com.apple.gputools.service`. [V] It is `LSUIElement=true`, which is why it
never takes focus.

### The CLI option surface

The full 59-token option map, with parser offsets and downstream status, is in
`~/tmp/agent-collab/gputrace/20260809-8D2E1959-mtlreplayer-deep-flags.md`,
derived by reading the option table and `_GTMTLReplay_CLI` (at `0x24894276c`)
out of the arm64e slice. The distinctions that matter operationally:

> accepted is not parsed, parsed is not read, read is not effective output, and
> a written file is not a validated counter oracle.

Selected results:

| Flag | Reality |
|---|---|
| `-maxProfilingTime`, `-maxProfilingFrames` | parsed into `+0x1c`/`+0x20`, then **never read** by `_GTMTLReplay_CLI`. They do not bound the run. [V] |
| `--counter-plist` | parsed into `+0x48`; no read located. Profiler output was unchanged for real vs. nonexistent paths. [V] |
| `--script` | the located input to `loadDerivedCounterInfo`. [V] See Part 2 — unusable on G16. |
| `-collectRawCounters` | the only branch reaching `WriteToCSVFromCounterData` / `WriteToPlistFromCounterData`. [V] |
| `--counters`, `--derived-counters`, `-collectPerformanceTiming` | reach an early-finish branch that has **no writer**. They cannot produce a file. [V] |
| `-testLineProfiling`, `--list-devices` | accepted tokens whose parser cases mutate nothing. [V] |
| internal bit `0x400000` | read downstream, but no token in the table reaches the case that sets it. Not spellable through this CLI. [V] |

`-collectProfilerData` takes an **optional** performance-state argument. The
parser consumes the next token only when it exists and its first byte is not
`-`, otherwise storing `-1`. [V] This is why
`-collectProfilerData -outputPath OUT` does not swallow `-outputPath`.

### Dispatch precedence, and why flag combinations mislead [V]

`_GTMTLReplay_CLI` dispatches in this order:

1. `--gen-thumbnails` (`0x1`)
2. masked state `(flags & 0x620000) == 0x20000` — batch-filtered stream
3. derived stream (`0x100`)
4. internal, unspellable `0x400000`
5. profiler (`0x200000`)
6. later modes: GPU timeline, shader/profile, counters, raw counters, test
   profiling, pipeline statistics, performance timing, then ordinary replay

Profiler is step 5 and raw counters is step 6, so **adding profiler flags makes
the raw-counter writers unreachable.** A combined invocation that produces
profiler output is not evidence the counter flags did anything.

### A reachable, non-obvious output: pipeline statistics [V]

`-collectPipelinePerformanceStatistics` in its **own** dispatch mode — no
`-collectProfilerData`, so it is not preempted — writes `OUT/source.txt`.
On `02-two-encoders`: 0.09 s, 7,867 bytes,
SHA-256 `1ede3f78053920d6176e56bcb0ff1b47c3b402d16c6b81587a516c2413647437`,
reproduced byte-identical on a second run.

Control: the same replay with the same `-outputPath` and no flag created only
an empty directory. [V] The file is attributable to the flag rather than to
output-path setup or profiler nondeterminism.

Contents are an ASCII OpenStep property list holding an explicit
`Draw ID -> PipelineState ID` mapping (`0->8`, `1->10` on this fixture) and,
per pipeline: function name, compile-cache state and compile time, ALU/branch/
FP16/FP32 and device load/store/atomic instruction counts, register, spill and
threadgroup-memory usage, `ComputeBufferPrefetch` promotion state, and
LLVM-style YAML `Remarks` carrying `StackSize` and `InstructionMix` with source
line attribution.

Two consequences worth following up:

- `[D]` The YAML remarks are richer than gputrace's current normalized pipeline
  view, and are produced headlessly in under a tenth of a second.
- `[?]` The `Draw ID -> PipelineState ID` pairs are an **explicit** mapping.
  Positional pipeline-to-encoder joins are prohibited in this project; whether
  this constitutes a legitimate join key has not been established and must not
  be assumed.

Adding the flag to the profiler recipe is redundant: the streamData pipeline
payload was identical across three baselines. [V] The precedence table predicts
exactly that, and this is a behavioral confirmation of it.

Standalone `-gpuTimelineData` ran for 1.36 s and wrote no file on this fixture;
its profiler-mode `gpuTimelineData` array was also empty. [V] The fixture is
compute-only, which may be the reason. [?]

## Part 2 — The G16 counter-definition problem

This part corrects a belief that directed several days of work.

### The three-name contract was wrong [V]

We believed a derived-counter definition source required three files:
`tools-derived.js`, `tools-analysis.js`, and `tools-counters.plist`, and that
the absence of the last one from both Xcode installs was the blocker.

`GTMTLReplayClient_loadDerivedCounterInfo(a1, a2)` takes **two independent
prefixes**, and only the second is the `tools`-ish one:

```
a1 + "-counters.plist"  -> DerivedCounterDictionary   REQUIRED; nil -> returns nil
a2 + "-derived.js"      -> DerivedCounterScript
a2 + "-analysis.js"     -> DerivedCounterAnalysis     OPTIONAL; falls back to ""
```

Source: decompiled `GPUToolsReplay_27.mm:4275-4317` from a public iOS 26.1
restore-image pseudocode dump.

So `tools-counters.plist` never existed. `tools` is the *script* prefix only.
Exact-phrase GitHub search returns **0 hits in both the code and commits
indexes** for `"tools-counters.plist"`. [V] Xcode shipping the two `.js` files
and no plist is correct behavior, not a missing file.

`verifyCounterDictionary` only requires `DerivedCounters` to exist and be
non-empty; it validates no per-counter field. [V] A hand-authored plist would
pass. This matters — see "why synthesis is not a route" below.

### Where the plist actually comes from [V]

`DYMTLReplayFrameProfiler_loadAnalysis` builds both prefixes from IORegistry
and tries three path templates:

```
1. <AGXInternalPerfCounterResourcesPath>/%@/%@
   (fallback /AppleInternal/Library/AGX/Performance/%@/%@)
2. /System/Library/Extensions/%@.bundle/%@
3. /System/Library/Extensions/%@.bundle/Contents/Resources/%@
```

It retries with a synthesized `...External...` variant of the statistics name,
which is why on-disk files are named `AGXMetalStatisticsExternal<GEN>-counters.plist`.

The plist ships in the **GPU driver bundle**, not in Xcode.

### Apple stopped shipping it at G15 [V]

```
$ ls /System/Library/Extensions/AGXMetalG16X.bundle/Contents/Resources/
AGXMetalPerfCountersExternal.plist   ds.g16s   ds.g16s_a0   *.metallib

$ find /System/Library/Extensions -name "*G1[567]*-counters.plist"
(empty)
```

| Bundle | `-counters.plist` | `-derived.js` | `AGXMetalPerfCountersExternal.plist` |
|---|---|---|---|
| AGXMetal13_3, G13X, G14G, G14X | yes | yes | yes |
| **G15G/G15X, G16G/G16X, G17G/G17P/G17X** | **none** | **none** | yes |

The negative has positive controls: the same population search finds complete
G13/G14 triples and complete AMD (`GFX9Statistics-*`, `GFX10Statistics-*`) and
Intel triples. [V] It is not a broken search.

On this host `ioreg` exposes no `MetalStatisticsName`,
`MetalStatisticsScriptName`, or `AGXInternalPerfCounterResourcesPath`, and
`/AppleInternal/Library/AGX/Performance/` does not exist. [V]

**Therefore `--script` is a genuinely unavailable legacy route for G16 on this
machine.** Not a missing file to hunt — a mechanism that was retired.

### What G16 does still ship, and what it is not [V]

`AGXMetalPerfCountersExternal.plist` for G16X is 370,481 bytes and holds 3,905
obfuscated raw selector records:

```
DeviceCounters: [ "_<64 hex>", ... ]
"_<64 hex>": { Partition: int, Select: int, Flag: int? }
```

This is **hardware mux programming, not formulas.** It tells you how to select
a counter, not what it means.

A cross-generation comparison establishes the split between identity and
encoding: [D]

```
G16X raw hashes 3905, G14X 3943, shared 3272
of the 3272 shared, only 85 have identical {Partition, Select, Flag}
G14 -counters.plist reference 224 distinct raw hashes:
  224/224 present in G14X; 161/224 present in G16X
185 of 301 G14 derived counters are fully satisfiable against the G16X raw set
```

What would have falsified this: near-zero hash overlap (we saw 83%), or
identical `Partition`/`Select` everywhere (we saw 85 of 3,272). So the
`_<sha256>` name is a **stable semantic identity across generations** and
`Partition`/`Select`/`Flag` is the **per-generation hardware encoding**.

The hashes are not plain digests of display names: eight candidate name
spellings across sha256/sha1/md5 produced 0 hits against the G16X key set. [V]
Recovering the hash-to-name mapping is now a top prize.

### Why synthesizing a G16 plist is not a route

It is tempting: 185 of 301 G14 formulas are expressible in G16X's raw set, and
`verifyCounterDictionary` would accept the result. That combination is the
danger, not the opportunity. The formulas would be G14 semantics wearing a G16
filename, the loader would accept them without complaint, and the output would
be plausible numbers that mean nothing. Whether the 161 shared hashes denote
the *same physical counter* on G16 or merely the same name-identity with
different semantics is unresolved. [?]

This is the project's dominant defect class — silent wrongness — in its purest
available form, and a gap we can see is better than a number we cannot check.

## Part 3 — Where G16 definitions actually live: AGXPS

`[V]` G16 counter definitions live in a **compiled AGXPS registry** embedded in
Xcode's `GTShaderProfiler`, Instruments' `GPUPlugin`, and the system
`GPUToolsReplay`. In all three, `agxps_initialize`,
`agxps_derived_counter_gpu_descriptor_create`, and
`agxps_counter_compute_derived_counters` are **defined text symbols, not
imports**. `agxps_initialize` walks a global registry of 0x20-byte records and
constructs the counter/group registry from a GPU generation/variant/revision
descriptor.

`[V]` The registry accepts this GPU at runtime. Called with the
disassembly-verified 3-scalar ABI, `agxps_initialize` returns 1 and
`agxps_aps_gpu_is_supported(generation, variant, revision)` returns true for
G16 variants 3, 4, 5 and 6 — including this host's 16/6/1 — in **both**
installed Xcodes.

`[V]` On the arm64e command-queue path, `counterInfo` / `availableCounters`
return nil by construction, so that is not an alternative source.

So the definitions are present and reachable in principle; they are behind an
API and a compiled registry rather than a file. `[D]` Binding the
runtime-verified AGXPS accessors is the most credible remaining route to a G16
counter oracle, and unlike the plist it would also be a gputrace capability
rather than only a test fixture.

### The G16 derived-counter dictionary, reconstructed [V]

```
agxps_counter_get_name(184161) = "ALU Utilization"
```

Plaintext, with `agxps_load_counter_obfuscation_map` never called. **110
derived counters** enumerate for this GPU — 104 in group `One Pass`, 6 in
`One Pass GT` — each with ident, name, normalized flag, group membership, raw
counter dependencies, and a doc string:

```
183252  L2 Cache Utilization  norm=true  groups=[Utilizations|One Pass]  raw=107376,105502
184161  ALU Utilization       norm=true  groups=[Utilizations|One Pass]  raw=102800,102804,102796,102792
184444  F32 Utilization       norm=true  groups=[Utilizations|One Pass]  raw=102796
184249  Shader Core Limiter   norm=true  groups=[Limiters|One Pass]
```

`ALU Utilization` consuming four raw counters while `F32 Utilization` consumes
exactly one of those same four is internally consistent in a way a garbage read
would not be.

`[V]` Verified signature:

```c
bool agxps_counter_group_get_derived_counters(
        agxps_gpu gpu, const char *group_name,
        uint64_t **out_idents /* malloc'd, caller frees */, size_t *out_num);
```

The group lookup is keyed `(gen<<16)|variant`, so the registry is per-GPU-triple
and reached by name.

**The cross-check passed.** `[V]` For every raw counter referenced by the 110
derived counters, `agxps_counter_get_grc_enable_str(raw_ident)` was looked up as
a key in `AGXMetalG16X.bundle`'s `AGXMetalPerfCountersExternal.plist`:
**31 of 31, zero misses, against a 3,905-key namespace.** A wrong
`grc_enable_str` binding, a wrong ident space, or a mismatched namespace would
each have produced a low hit rate. The `Select` values are single- or few-bit
masks (2^39, 2^37, 2^32), which is the shape a counter-mux select field should
have.

This does not establish that we can *program* those selects, nor that
`agxps_counter_get_raw_counters_used_by_derived_counters` returns a complete
dependency list — 31 distinct enable strings across 110 derived counters is
plausible but unproven, and that signature is inferred from usage. `[?]`

Two traps recorded before they become bugs:

- Many doc strings read literally `"<blurb about the ALU utilization"`. That is
  Apple placeholder text in this build, not a decode error — the same accessor
  returns real sentences elsewhere. Do not ship the blurbs as documentation. [V]
- Raw counters' own names are **uppercase 64-hex with no leading underscore**,
  while their `grc_enable_str` is the lowercase, underscore-prefixed form the
  plist keys on. These are **different strings, not case variants**. [V]

Why the earlier probe saw only hashes: two independent mistakes of ours.
`agxps_initialize` takes **four** arguments and we passed none, and the ident
space is 0..196914, not 0..1925. [V]

### How this was reached [V]

On `Xcode-rc.app/…/GTShaderProfiler.framework/…/GTShaderProfiler`: [V]

```
nm | grep -c " T _agxps_"     ->  374 defined text symbols
                                  (a looser `grep -c agxps_` counts 510, including
                                   undefined and mangled forms)
_agxps_counter_get_name, _agxps_counter_get_ident,
_agxps_counter_deobfuscate_name, _agxps_load_counter_obfuscation_map   all T

strings -a | grep -x  ->  "AF Bandwidth", "ALU Utilization", "Occupancy"
```

Plaintext display names are compiled into the binary, and the accessor surface
is roughly 374 symbols against the ~20 `internal/agxps` currently covers.

`[?]` Hypothesis: the registry carries plaintext names reachable through
`agxps_counter_get_name`, and `agxps_counter_deobfuscate_name` plus an
AppleInternal CSV are needed only to map the obfuscated `_<64 hex>` raw idents
onto them. If so, then

```
agxps_initialize -> enumerate groups -> agxps_counter_group_get_derived_counters
  -> agxps_counter_get_name + agxps_counter_get_ident
     + agxps_counter_get_raw_counters_used_by_derived_counters
```

reconstructs the G16 derived-counter dictionary from the live registry — what
the retired plist used to provide — with no Apple-internal files and no
synthesis.

**This is already answered, in this repo.** `GTMIO_CAPABILITY_MATRIX.md:388-397`
records, `[V]`, that plaintext names come out of the live route on this machine
with no AppleInternal CSV: `nonOverlappingTimeline.timelineCounters` yields 19
counters, all plaintext and all memory-side (`AF Bandwidth`, `L2 Cache
Limiter`, `MMU Utilization`, `Texture Cache Utilization`, …) at 112,972 samples
each; `costTimeline` yields a different 30, of which 17 are plaintext and 13
are opaque 64-hex shader-side ALU counters. The two name sets do not intersect.

So the falsifier — `get_name` returning `_<64 hex>` — did not occur for 36 of
them, and **the deobfuscation problem is scoped to 13 identifiers confined to
`costTimeline`**, not to 3,905. It is a nice-to-have, not a prerequisite.

`AF Bandwidth` has three independent sightings: compiled into decompiled
`GPUToolsReplay_24.mm` immediately after the
`agxps_load_counter_obfuscation_map` call, in `strings` on the local
`GTShaderProfiler`, and in our own live extraction.

The blocker is therefore narrower than "can we get names". `[V]`
`nonOverlappingCounters` yields name arrays including `ALU Total Instructions`
/ `ALU F16 Instructions` / `ALU F32 Instructions`, but the
`GTMioCounterDataPerDM` objects came back with zero-valued `^d` arrays,
reproduced across two runs — allocated-but-zero, not usable data.

That question is now **answered, and it is not a wrong-binding zero**.
`_cacheValues` calls
`GTMioNonOverlappingCounterContainerInternal::allValues(u64)` at 0xc64a0 and,
on a NULL result, branches to `movi d0, #0` at 0xc64c4 and pushes that.
Apple substitutes 0.0 for a missing lookup, and downstream nothing
distinguishes it from a measured zero. The DBL_MAX/0 min/max half of the
original reading is withdrawn: min/max are ivars written only inside that same
loop, so the probe read constructor seeds by calling them before `values`.
Because `SampleCount()` on that capture was 12/574 rather than 0, the loop did
run — so this is a key or absent-data problem, not an unrun processing step.
See `GTMIO_CAPABILITY_MATRIX.md` for the discriminator and for the caveat that
the route producing those counts no longer completes on this machine.

And a caveat that must travel with any of these names: `Texture Read Limiter`
reports a peak of 8.99e10 where every sibling limiter is bounded near 101. That
column is **unread, not a large measurement**. A name being plaintext does not
mean its encoding is understood.

A cross-check that can actually fail: `agxps_counter_get_grc_enable_str`
appears to emit the counter-mux enable string, i.e. the programmable form of
the `Partition`/`Select`/`Flag` triple in `AGXMetalPerfCountersExternal.plist`.
If the registry's enable string for an ident agrees with the plist for the same
hash, both sources are being read correctly; disagreement means one is being
misread. `[?]`

`[V]` `tmc/*` is the only public binding of AGXPS in existence:
`"agxps_counter_compute_derived_counters"` returns 0 GitHub hits, and
`"agxps_initialize"` returns 6 of which 3 are ours and the rest are Apple
decompilation dumps. There is no external corroboration for our signatures, so
disassembly is the only available check.

`[D]` Variant numbering provenance: `GTAGXProfilingSupportHelper::Initialize`
takes a dictionary keyed `gpu_gen`, `num_cores`, `num_mgpus`, `gpu_var`, where
`gpu_var` is a single character lowercased and folded to a small integer. "G16
variant 6" is the letter suffix of G16X/G16G as an int.

Open work is tracked against `internal/agxps` and
`docs/research/agxps-signatures.yaml`. Two cautions carried over:

- The `agxps` C signatures in this repo were originally name-derived guesses,
  with roughly half wrong. A wrong signature produces a call that may appear to
  work while corrupting the stack. Verify against disassembly before use.
- `[?]` A third-party Rust reimplementation reports that `agxps_*` symbols are
  absent from the export trie, so `dlsym` fails and a UUID-pinned offset from
  `_dyld_get_image_header()` is required. If true, our current prober's
  resolution strategy needs auditing, and any offset-based approach must verify
  the binary UUID and refuse rather than proceed — an offset that silently
  moves after an OS update calls the wrong function.

### An in-process route that skips MTLReplayer entirely [?]

A third-party Python/PyObjC profiler drives `streamData` parsing directly,
with no CLI, no launch constraint, and no entitlement:

```
processor = GTShaderProfilerStreamDataProcessor.alloc()
    .initWithStreamData_llvmHelperPath_(streamData, GTLLVMHelper_path)
processor.processStreamData(); processor.waitUntilFinished()
processor.processAPSTimelineData(); processor.processAPSCostData()
mio = processor.result().mioData()   # GTMioCounterData
```

`GTLLVMHelper` is an out-of-line helper binary the processor shells out to. It
**is present in all three installs here** — `Xcode.app`, `Xcode-rc.app`,
`Xcode-rc-old.app` — under
`Contents/Developer/Platforms/MacOSX.platform/Developer/Library/GPUToolsPlatform/PlugIns/`. [V]

`[D]` The source appears to be written by someone who actually ran it: they
`dup2` `/dev/null` over fds 1 and 2 to suppress the helper's LLVM warnings, a
detail unlikely to be invented.

`[?]` The open question, and the one worth answering before investing here, is
whether `processAPSCostData` / `mioData()` surfaces **derived** counters or
only the timing and cost data `internal/counter/` already parses. If derived,
this bypasses the plist problem entirely; if not, it duplicates existing work.
See `docs/research/GTMIO_CAPABILITY_MATRIX.md` before probing — part of the
answer may already be recorded there.

## Part 4 — xctrace: dead as a counter oracle, alive as a timing oracle

Recording works with no TCC or entitlement wall. [V] The route needs no plist,
no private entitlement, no Xcode GUI, and no root. It is nonetheless not a
counter oracle on this hardware.

### The decisive negative [V]

The only `counterprofile` this device accepts is **0**, whose counter set is
**exactly one counter: `RT Unit Active`** — a raytracing utilization percentage
that reads 0.0 for all 13,254 samples of a compute workload. Profiles 1 through
6 are each rejected by the GPU service with `Selected counter profile is not
supported on target device` and yield 0 counter rows. Six separate records,
each verified by exporting `gpu-counter-info`.

So there is nothing to attribute, and per-encoder attribution is moot.

Even had the counters existed, the attribution answer would still be bad, and
the numbers say why: [V]

- our compute encoder ran **27,208 ns**; counter sampling is **12,125 ns**, so
  ~2 samples land inside the whole encoder
- samples carry `accelerator-id` and **no pid or encoder-id**
- the machine was not GPU-quiet even with nothing else running: WindowServer
  produced 3,914 of 4,257 GPU intervals in a 2 s window; of 41 Compute
  intervals, exactly 1 was ours

A GPU-wide sample during our encoder is dominated by compositor work.

### Third-party guidance did not survive contact [V]

The documented recipe came from an M1 Pro (G13X). Adjudicated here on G16:

| Claim | Here |
|---|---|
| `--instrument "Metal GPU Counters"` is load-bearing | **Refuted, inverted.** It selects profile 3, which this device rejects; `--template "Metal System Trace"` is what works. |
| ~31 WWDC20 counters | **Refuted.** One. |
| 10 ms sample interval | **Refuted.** Median 12.125 µs. |
| 135-250 MB XML per second | Not reproduced — because there is 1 counter, not 31. |
| `gpu-counter-info` / `gpu-counter-value` schemas | Confirmed. |
| `id=`/`ref=` backreference scheme | Confirmed, and **worse**: ids also appear on *nested* elements, so a parser registering ids only on direct row children raises `KeyError`. Register from every element in document order. |
| Counters GPU-wide, keyed by accelerator | Confirmed. |
| `counterprofile` knob exists and is honored | Confirmed; their *values* are meaningless here. |

The prior art was still worth having — it named the traps — but every
quantitative claim in it was generation-specific.

### The consolation prize is real [V]

`metal-gpu-intervals` gives per-encoder GPU intervals with the Metal label
intact:

```
start=2282242833 duration=27208 process=16359 channel-name=Compute
event-label="HighALU:ComplexMath 16359 1292636735"
cmdbuffer-id=5570826808 encoder-id=5570826809
```

That is an independent, non-MTLReplayer, non-GUI source of per-encoder start,
duration and command-buffer grouping, **filterable by pid**, from a scriptable
CLI in ~14 s. It supplies no counters, occupancy, limiters, or instruction
counts.

`[?]` Whether it is worth wiring into `TestParity` as a *timing* oracle is
undecided, and correlating its ids with gputrace's own encoder ids has not been
attempted. Also populated in the same trace:
`metal-command-buffer-completed` (333), `metal-gpu-execution-points` (8,514),
`metal-gpu-state-intervals` (7,912), `metal-driver-intervals` (2,481),
`gpu-performance-state-intervals` (200), `metal-object-label` (7,587).

`gpudebug`, a CLI trace reader Apple documents with a `performance` subtree, is
**absent on this machine** (`xcrun -f gpudebug` fails) and was independently
observed absent on macOS 26.5.2. [V]

## What is settled, and what is not

Settled:

- The G16 derived-counter dictionary is recoverable from the live AGXPS
  registry: 110 counters with names, groups and raw dependencies, corroborated
  31/31 against the kext's selector map. This is what the retired plist
  provided.
- Headless replay and profiling works, with a verified recipe. Problem 1 solved.
- The three-name contract was wrong; `tools-counters.plist` never existed.
- G16 ships no derived-counter plist, by Apple's design change at G15, and
  `--script` is unavailable here. Confirmed independently from two directions,
  with positive controls.
- Synthesizing a plist is rejected on silent-wrongness grounds, not on effort.
- G16 definitions exist in the AGXPS compiled registry and that registry
  accepts this GPU.

Not settled:

- Whether AGXPS can be bound safely and correctly from gputrace.
- Whether `xctrace` yields per-encoder-attributable counters. Probably not, but
  untested here.
- Whether the pipeline-statistics `Draw ID -> PipelineState ID` mapping is a
  legitimate join key.
- The hash-to-name mapping for raw counter identities.

`TestParity` therefore remains blocked, and the oracle captures it depended on
are gone (see the capture-corpus notes). The difference from a week ago is that
the blockage now has a known cause and two candidate routes rather than one
route that was never going to work.

## Method notes

Two corrections during this work are worth recording, because both were
instances of the failure mode this document keeps warning about.

A claim that MTLReplayer's `--list-devices` failed to enumerate devices was
wrong: `makeDataSource` runs before any device-list branch, so the real message
was `Failed to open capture archive: (null)`. The reading was one inference
past the evidence, in the direction that made the tool look more broken.

A probe was accused of quitting Xcode on the basis of a `SIGTERM ... sent by
Xcode` log line. Timestamps refuted it: Xcode had been force-quit through the
Dock two minutes before the probe's first exec. A plausible mechanism plus
evidence consistent with it is not causation.
