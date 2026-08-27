# Timeline counter names do not join the counter graph catalog

The runtime `GTMioTimelineCounters` dictionary and `GPUCounterGraph.plist` use
different namespaces. Two of thirty names join on a real graph edge. The other
twenty-eight do not join anything, and no transformation is offered here to make
them.

This is a precise negative. It is the answer, not a placeholder for one.

## The two namespaces

[V] The 413-draw recapture archive's timeline dictionary has 30 keys. Thirteen
are 64-hex-digit strings; the rest are readable but unprefixed:

```text
100299043F027ADADB62685130C7FBE549E29F08B58C365844FF8EC25BAEEAB0  (and 12 more)
AGenInstructions  ALU F16 Instructions  ALU F32 Instructions  ALU Total Instructions
ALUF16Issued  ALUF16Percent  ALUF32Issued  ALUF32Percent  ALUICPercent
ALUInstructions  ALUInt32AndCondIssued  ALUIntAndComplexIssued  ALUSCIBPercent
CFInstructions  CFIssued  GT Active Core Count  Instructions Executed
```

Recorded in `internal/agxps/deobfuscation_manual_test.go` so later work does not
need the 9.7G archive to reproduce the list.

[V] Zero of the 30 are top-level `counters` keys, `name` fields, or
`toolsCounter` values in either installed catalog. The test's
`timeline counter names in catalog=[]` is correct.

## What does join, and on what edge

[V] Two names are exact `vendorCounters` values, which is the plist's own edge
from a raw vendor counter to the node computed from it:

| runtime name | catalog node | node's vendorCounters |
|---|---|---|
| `CFIssued` | `Control Flow Instructions Issued` | `["CFIssued"]` |
| `GT Active Core Count` | `Active Cores` | `["GT Active Core Count"]` |

Exact string equality against a field the plist populates. No normalization.

## What deliberately was not joined

[V] `ALU Instructions` is **not** a catalog key, in either 26.3 or 26.5. The
keys are stage-prefixed display names -- `FS ALU Instructions`,
`Kernel ALU Instructions`, `VS ALU Instructions` -- whose `vendorCounters` are
`FSALUInstructions`, `CSALUInstructions`, `VSALUInstructions`.

So the runtime's bare `ALUInstructions` is neither a catalog key nor a
`vendorCounters` value. Reaching it requires deciding it means the compute-stage
one and dropping the `CS` prefix, and nothing in the plist says that. The
catalog's own answer points the other way: `Kernel ALU Instructions` is the
display name for `CSALUInstructions`, and [V] the runtime returns nil for
`Kernel ALU Instructions`, so the two namespaces do not overlap even where they
describe the same quantity.

[V] `ALU Total Instructions` does not occur anywhere in either plist.

## The mechanism that would resolve the hashes, and why it cannot here

[V] The framework exports a counter-name obfuscation map:

```text
agxps_load_counter_obfuscation_map(const char *csvPath) -> bool
agxps_counter_deobfuscate_name(const char *name) -> const char *
agxps_counter_obfuscated_name(const char *name) -> const char *
```

All four symbols are present in the 26.3 binary. If a map were loaded, this
would be the crosswalk, established by the framework rather than invented.

[V] It is unavailable in a shipped install. `TestProbeCounterObfuscationMap`
reports `loaded=false names=30 hex_names=13 resolved=0`:

```sh
GPUTRACE_ABI_PROBE=1 go test -run TestProbeCounterObfuscationMap -v ./internal/agxps/
```

[V] The binary names four map files -- `AGXCounterMapping.csv`,
`AGXRawCounterMapping.csv`, `RawCountersMapping.csv`, `remapping.csv` -- and
none of them exists in either installed Xcode. `GPUTRACE_COUNTER_OBFUSCATION_CSV`
loads one if it is ever obtained; the probe reports what it resolved and round
trips each result back through `agxps_counter_obfuscated_name`.

**The trap in this API:** both accessors return their argument unchanged when
the map is missing. A caller that does not compare against its input reads that
as a successful resolution. The probe treats an unchanged name as unresolved for
exactly this reason, and requires the round trip before believing a change.

[?] `XRGPUAPSCounterContainer` carries both `rawCounterNames` and
`deobfuscatedRawCounterNames`, which would be the same crosswalk from the other
side. They are C++ struct fields in a type encoding, not Objective-C ivars with
accessors, so reading them means struct-offset work that has not been done.
Whether those fields are populated when no map is loaded is unknown.

## Consequences

[D] Anything keyed by counter name across these two sources joins nothing for 28
of 30 counters, and silently. A name-keyed lookup that returns no rows looks the
same as one whose input was empty.

[D] Do not close this gap with a normalization. Stripping spaces would map
`ALUInstructions` onto a key that does not exist, and adding a `CS` prefix would
assert a stage the runtime never stated. Either the obfuscation map turns up, or
the container's deobfuscated names get read, or the crosswalk stays unresolved.
