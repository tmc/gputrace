# Raw counter hashes cannot be named, and the question was malformed

`.gputrace` archives identify hardware counters by 64-hex obfuscated ids. The
goal was to recover plaintext names for them by reversing Xcode's frameworks.

**Result: 0 of the 141 raw counter ids in a trace can be named, and no amount of
further reversing will change that.** The mapping is not in any shipped binary.
It is loaded at runtime from a file Apple does not ship to this machine.

This document records why, because the negative is more useful than the many
plausible-looking wrong answers the question produced.

## The category error

The question assumed one population of counters with one name per id. There are
two, in a 1:N relation:

| Population | Count | Identified by |
|-----------|-------|---------------|
| **Raw** hardware counters | 578 in the binary, 141 in a trace | 64-hex obfuscated hash |
| **Derived** counters | 150 | plaintext name + description |

A derived counter consumes a *set* of raw hashes. So no ordinal join between the
two can exist, and every attempt to build one — matching 392 against 578, or
against the plist's 534 — was matching populations that do not correspond.

[V] The derived→raw direction is real and was read out of the instruction stream
at `0x54d9d0-0x54e0c0`, corroborated by eight consecutive slots at
`0x4f478c-0x4f4818`:

	Compute  Simdgroups Inflight Per Shader Core <- 33634F0D, FD6F91B4, 50E7E1AA  (raw ordinals 483-485)
	Fragment Simdgroups Inflight Per Shader Core <- 55DDF08E, C4B3D90E, E0822A12  (486-488)
	Vertex   Simdgroups Inflight Per Shader Core <- FB75B1EE, C8CAD3DF            (489-490)

## Why no binary carries the map

[V] `agxps_counter_deobfuscate_name` at `0x4adc4c` reads a global table at
`0xeeb020`. That table is filled by exactly one writer,
`agxps_load_counter_obfuscation_map` at `0x4ad6b8`, which loads
`RawCountersMapping.csv` from bundle `com.apple.gpusw.AGXProfilingSupport`.

Both strings are present in the arm64 slice of
`GTShaderProfiler.framework/Versions/A/GTShaderProfiler`, alongside
`@(#)PROGRAM:AGXProfilingSupportStatic PROJECT:AGXProfilingSupport-2.0`.

[V] Neither the bundle nor the file exists on this machine (`mdfind` returns
nothing for either). `load(NULL)` returns 0 at runtime, and with both
`GPUToolsReplay` and `GTShaderProfiler` dlopened, deobfuscation is the identity
function for 578 of 578 hashes. **Xcode has no map either** — it displays raw
hashes for raw counters, exactly as gputrace does.

## Routes ruled out, and how

- **Pointer tables / parallel arrays.** [V] A full-file qword scan (raw and
  chained-fixup-masked) for pointers into the name region gave 6 hits, all
  incidental; the same scan over `__DATA_CONST __const` for the 578 hash string
  addresses gave 2, both coincidental. Name and hash strings are referenced
  *only* by ADRP/ADD from `__text` — 2456 hash references covering all 578, 813
  name-region references. There is no table to walk.
- **Computed digest.** [V] SHA-256 over 74,531 candidate strings (both
  frameworks, AGX bundles, all 534 `GPUCounterGraph.plist` vendorCounters)
  against 11,310 candidate hashes: **0 hits**. It is a substitution table, not a
  digest.
- **Older builds.** [V] `Xcode-rc.app` and `Xcode-rc-old.app` carry the identical
  578 hashes and the same Resources.

## Two traps this question set, both of which caught us

**The `_68_57` strings are not extraction failures.** They look like
placeholders and were used as a defect metric — "173 of 392 rows are
placeholders" — driving a bucket table that was then optimised against. They are
real strings in the shipping binary: Apple's own `__FILE__"_line_col`
identifiers for unnamed derived counters, e.g.

	".../DerivedCounters/AGXPSLimiters.inl"_68_57
	".../DerivedCounters/AGXPSLimiters.inl"_69_58

[V] Read from the binary. Counting them as damage measured the wrong thing, and
every "improvement" in that count was noise.

**Descriptions that look synthesized can be genuine.** Several real descriptions
are Apple's own unfinished copy — `<blurb about the Threadgroup Load limiter`,
`<blurb about the MMU limiter`. A template-looking description is therefore not
by itself proof of fabrication.

## Ordinals: what is and is not established

[V] Verified by reading the bytes at the computed addresses (`__TEXT` is
identity-mapped in this slice, so VM address == file offset):

	0x00542f08  ADRP x1, 0xa97000 ; ADD x1, #0x823 -> 0xa97823 "Threadgroup Load Limiter"
	0x00542f10  ADRP x2, 0xa97000 ; ADD x2, #0x83c -> 0xa9783c "<blurb about the Threadgroup Load limiter"
	0x00545164  ADRP x1, 0xa98000 ; ADD x1, #0x5f7 -> 0xa985f7 "Compute Occupancy"
	0x0054516c  ADRP x2, 0xa98000 ; ADD x2, #0x609 -> 0xa98609 "The number of compute simdgroups running concurrently..."

Derived-counter ordinals are 49 Control Flow Limiter, 69 Compute Occupancy, 70
Compute Simdgroups Inflight Per Shader Core.

An earlier reconstruction of this project's placed Compute Occupancy at 21 and
Compute Simdgroups Inflight at 22, and those were treated as verified anchors
and used to reject other work. The **adjacency** was right and reproducible; the
absolute index was not. 21 is Threadgroup Load Limiter. No offset was shimmed to
reconcile them.

That reconstruction also assigned hash `B6B78FAB…01F2` to Compute Simdgroups
Inflight. [V] Wrong: `B6B78FAB` is raw ordinal 64, and all eight of its
references (`0x48025c`, `0x4f482c`, `0x506edc`, `0x5173e0`, `0x55f3e4`,
`0x575c74`, `0x58726c`, `0x5985c8`) sit inside per-generation counter *enable
lists*, never adjacent to that name. It replaced one wrong hash with another.

## What to do instead

Xcode does not deobfuscate; it *computes*. Named columns come from
`GPUCounterGraph.plist` (534 vendorCounters, shipped in the framework Resources,
present locally) evaluated over hashed raw inputs by
`agxps_counter_compute_derived_counters` at `0x558f6c`. That path needs no
deobfuscation and is fully available offline.

## Artifacts

- `~/tmp/gputrace-counter-hash-inventory.csv` — 141 rows, every name explicitly
  `UNKNOWN`. Not a mapping; an inventory of what is unnamed.
- `~/tmp/gputrace-derived-counter-raw-inputs.json` — the derived→raw hash sets.
