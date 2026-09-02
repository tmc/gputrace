# `derived-counter-raw-inputs.json` — provenance and known gap

The only machine-readable derived-counter → raw-hash binding that exists in this
project. Checked in 2026-08-01 from `~/tmp/gputrace-derived-counter-raw-inputs.json`
(written 2025-07-31 22:21), **byte-for-byte unmodified**.

    sha256  eb6f8dc74c68fc0c999c239ff4aadbbb30a303b68181a87baa62a6d1abb72b57
    bytes   1913

The file is kept verbatim rather than given an inline provenance key, so that
its digest stays comparable to the original and so this project is not in the
position of having edited a research artifact it did not produce. All caveats
live here instead.

## What it is

Produced by the investigation recorded in `COUNTER_NAME_MAPPING.md`, which
established `[V]` that raw counter hashes **cannot** be deobfuscated (the
`RawCountersMapping.csv` Apple never shipped), but that the *derived name → raw
hash* direction is readable out of GTShaderProfiler's instruction stream at
`0x54d9d0-0x54e0c0`, corroborated at `0x4f478c-0x4f4818`.

Five entries:

| derived counter | hashes |
|---|---|
| `Vertex Shader Launch Utilization` | 6 |
| `Fragment Shader Launch Utilization` | 6 |
| `Instruction Issue Utilization` | 7 |
| `Vertex Simdgroups Inflight Per Shader Core` | 2 |
| `Fragment Simdgroups Inflight Per Shader Core` | 3 |

## THIS IS NOT THE COMPLETE SET — the gap is compute-shaped

**Five entries is what one extraction run produced, not what exists.** There are
~150 derived counters. Do not read this file as an inventory.

`[V]` The omission is systematic, not random. Both vertex/fragment pairs present
are missing exactly their compute sibling:

| counter family | vertex | fragment | compute |
|---|---|---|---|
| Simdgroups Inflight Per Shader Core | present | present | **absent** |
| Shader Launch Utilization | present | present | **absent** |

Two complete v/f pairs with both compute members absent is a stage filter, not
coincidence. And `Compute Simdgroups Inflight Per Shader Core` is **not
undiscovered** — `COUNTER_NAME_MAPPING.md` carries it in prose at raw ordinals
483–485 as `33634F0D, FD6F91B4, 50E7E1AA`, 8-hex prefixes only.

So the fix is a re-run with the filter removed, not new reversing.

## Why this matters more than its size suggests

`COUNTER_LANES_DESIGN.md` §11.4 turns its entire delivery order on this file.
The consequence of the gap:

- The four vertex/fragment bindings here read ~zero on a compute capture — they
  are bindings for lanes a compute workload cannot exercise.
- `Instruction Issue Utilization` is compute-relevant but has no Xcode oracle
  column and no lane in the Counters rail, so it can prove machinery and not
  numbers.
- `Fragment Shader Launch Utilization` is the one entry both bound and checkable:
  oracle column 36 reads 15 nonzero of 23 encoders, max `0.79%`, mean `0.126%`.
  Small, but varying — enough for a binding test.

## Acceptance test for the re-run

Stated in advance so it can fail. Re-running the extraction without the stage
filter should yield:

| counter | predicted hashes | basis |
|---|---|---|
| `Compute Shader Launch Utilization` | 6 | both siblings are 6 |
| `Compute Simdgroups Inflight Per Shader Core` | 3 | Fragment is 3 (Vertex is 2) |

and the second must be `33634F0D…`, `FD6F91B4…`, `50E7E1AA…` at full 64-hex
width, per ordinals 483–485.

**If it returns three different hashes, the extraction is wrong somewhere else
and nothing downstream may trust this file until that is explained.** Writing
the prediction down first is deliberate: the alternative is reading whatever
comes back as confirmation, which is exactly how the falsified
`Counters_f_N → column N-4` mapping in `COUNTER_FILE_MAPPING.md` survived for
months.

## Related

- `COUNTER_NAME_MAPPING.md` — why deobfuscation is closed, and where these came
  from
- `COUNTER_LANES_DESIGN.md` — the design that consumes this, §11.4 especially
- `COUNTER_FILE_MAPPING.md` — the falsified mapping, kept as a worked example of
  a check that could not fail
