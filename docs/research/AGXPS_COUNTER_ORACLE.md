# AGXPS Counter Oracle

[V] The checked-in [agxps-counter-oracle.json](./agxps-counter-oracle.json)
cross-references three independently derived counter-name surfaces. A shipped
G14 JavaScript file supplies a fourth, positive-control surface for recognizing
vendor-counter values that are demonstrably function names. [D] The result is
a provenance artifact, not an evaluator and not a formula catalog.

## Join contract

[V] The top-level record key is the exact display name. The graph plist key is
retained separately because 40 of 455 graph entries have a key that differs
from the entry's `name` field.

[V] A registry name is cross-referenced against three graph fields by exact
string equality: the graph dictionary key, the graph `name`, and every
`vendorCounters[]` value. Each `gpu_counter_graph_exact_matches` entry records
the field that matched. There is no case folding, normalization, suffix
stripping, edit-distance match, or other fuzzy fallback.

[V] The plist's `vendorCounters` field is heterogeneous. Of its 534 distinct
values, 229 exactly name functions in Apple's shipped G14 derived-counter
JavaScript and 305 do not. Only the exact G14 function matches carry a positive
`matches_g14_derived_js_function` marker; the artifact does not promote other
vendor values into a symbolic-identifier field.

[V] Registry raw dependencies are keyed only by their exact obfuscated names.
The registry's numeric raw identifiers are sorted into the separate
`raw_dependency_idents_unjoined` array. The schema provides no field that can
pair a numeric identifier with an obfuscated name.

[D] This separation is intentional: evaluator and registry raw-counter numeric
identifier spaces differ. A consumer may join them only by exact obfuscated raw
name, never by numeric identifier, row position, column position, or list
position.

[V] Every sourced field carries `field_provenance`. Source disagreements are
retained as values from both sources; no source silently overrides another.
Xcode header repetition is stored as an occurrence count by export filename,
not as a column index.

## Inputs

| Source | SHA-256 | Role |
|---|---|---|
| `/Users/tmc/tmp/claude-tmp/agxps-verify/derived-dictionary.tsv` | `9bd19dab3f8b2de91da42cbae6233138727a8cc6bdfe038ab2ade8650639dc3b` | [V] Live AGXPS registry names, registry identifiers, raw dependency names, unjoined raw identifiers, groups, normalized flag, and doc string. |
| `/Applications/Xcode.app/Contents/PlugIns/GPUDebugger.ideplugin/Contents/Frameworks/GTShaderProfiler.framework/Versions/A/Resources/GPUCounterGraph.plist` | `6d9dc92cfd8f2ba71698da4e6185791838ed00d0a59579aea43dbdd5828fd9be` | [V] Shipped display names, graph keys, vendor-counter values, units, descriptions, visibility, and graph groups. |
| `/System/Library/Extensions/AGXMetalG14X.bundle/Contents/Resources/AGXMetalStatisticsExternalG14S-derived.js` | `de6cd0340a4fbf4c57bc846518fde0b4eb73a1b6d7aae7c577631d30a0c395e6` | [V] Positive control for vendor-counter values that exactly name G14 JavaScript functions. |
| `/Users/tmc/tmp/gputrace-xcode-oracle-20260731/` | per-file hashes in the JSON artifact | [V] Xcode counter-tab header presence by export. Values are not copied into the artifact. |

[V] The selected `GPUCounterGraph.plist` is pinned by full path and hash. The
GPUDebugger top-level resource copy hashes to
`24109227a6dcdad76aacbfc5d214c64443a9f9ae0f947dfc132e1eaf973212c0`;
the Instruments GPUPlugin copy hashes to
`385845c97f31d88ca40a42b0c9e572cd652038edc1b71081a832e3d5501370de`.
Neither is byte-identical to the selected GTShaderProfiler-framework copy.

## Coverage

| Measure | Count |
|---|---:|
| [V] Registry entries | 110 |
| [V] Distinct registry raw dependency names | 31 |
| [V] Distinct unjoined registry raw identifiers | 101 |
| [V] Graph entries | 455 |
| [V] Unique graph display names | 449 |
| [V] Distinct graph vendor-counter values, including empty | 534 |
| [V] Distinct non-empty graph vendor-counter values | 533 |
| [V] Distinct vendor-counter values matching G14 JavaScript functions | 229 |
| [V] Graph display-name records with no non-empty vendor-counter value | 6 |
| [V] Xcode exports | 12 |
| [V] Standard `xcode-*` exports | 10 |
| [V] Named header fields in the standard exports | 274 |
| [V] Named header fields in all exports | 325 |
| [V] Unique Xcode counter display names | 205 |
| [V] Union display names | 533 |
| [V] Registry names exactly matching graph `name` | 27 |
| [V] Registry names exactly matching graph dictionary key | 37 |
| [V] Registry names exactly matching a graph `vendorCounters[]` value | 87 |
| [V] Registry names matching any of those graph fields | 94 |
| [V] Registry vendor-counter matches also naming a G14 JavaScript function | 2 |
| [V] Graph entries matched by a registry name | 94 |
| [V] Registry name and Xcode header intersection | 24 |
| [V] Graph name and Xcode header intersection | 204 |
| [V] Registry name, graph name, and Xcode header intersection | 24 |

[V] Exact cross-referencing resolves 94 of 110 registry names and 94 of 455
graph entries. `coverage.unmatched` lists the remaining 16 registry names and
361 graph keys in full. It separately retains display-name-only coverage: 422
graph names lack an equal registry name, 83 registry names lack an equal graph
name, 245 graph names lack Xcode headers, 1 Xcode header lacks a graph name, 86
registry names lack Xcode headers, and 181 Xcode headers lack registry names.

[V] The 16 unresolved registry counters are not spelling variants. They are
`Instruction Dispatch Limiter`, `Instruction Dispatch Utilization`,
`Instruction Issue Limiter`, `Instruction Issue Utilization`, the eight
`L1 * Bytes Occupancy` counters, `Leaf Test Occupancy`, `Ray T Leaf Test`,
`Raytracing Active`, and `Raytracing Node Test`. Similar names such as
`L1 GPR Occupancy` and `Raytracing Active GT` are separate registry rows and
are not substituted.

[V] The artifact records 40 graph-key/display-name disagreements, 21
graph-description/registry-doc disagreements, and 27 graph-group/registry-group
disagreements across 66 display-name records.

## Boundary

[?] A vendor-counter value that does not match the G14 JavaScript function set
may still be symbolic on another generation, but this artifact has no source
that proves it. It therefore remains a vendor-counter value only.

[?] This artifact does not establish counter formulas, units for registry-only
entries, evaluator ownership, value validity, or a capture-matched mapping from
raw values to exported metrics. Those require a source or runtime oracle beyond
these name surfaces.
