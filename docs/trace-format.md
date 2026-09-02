# Apple Metal GPU Trace Format

This document describes the structure and file formats found in Apple Metal GPU trace bundles (`.gputrace`).

## Trace Bundle Structure

A `.gputrace` bundle is a directory containing:

| File/Pattern | Purpose | Format |
|--------------|---------|--------|
| `capture` | Main trace command stream | MTSP Binary |
| `device-resources-*` | Device initialization commands | MTSP Binary |
| `MTLBuffer-*` | Buffer contents | Raw Binary |
| `MTLHeap-*` | Heap contents | Raw Binary |
| `*.gpuprofiler_raw/` | Performance counter data | Directory |
| `F98EC4E82B8CACCA` | Metal Library (MTLB) or metadata | Binary with MTSP-like chunks |

## MTSP Binary Format

The `capture` and `device-resources` files use a custom record-based format we call "MTSP".

### Record Structure

Records follow a generic structure but specific fields vary by type.

| Offset | Type | Description |
|--------|------|-------------|
| 0x00 | uint32 | Record Size (in bytes) |
| 0x04 | ... | Record Data (Type-specific) |

### Key Record Types

Record types are identified by ASCII markers within the record data (typically near the beginning).

#### CS (Command Submission / Encoder)
Identifies a Metal Encoder or a Kernel Function Name.

| Offset | Description |
|--------|-------------|
| +0x00 | Marker `CS\0\0` |
| +0x04 | Address (8 bytes) - Encoder ID or Function Address |
| +0x0C | Label (null-terminated string) |

**Notes:**
- In `device-resources`: Maps Function Address to Kernel Name (e.g., "vn_copybfloat...").
- In `capture`: Maps Encoder Address to Debug Label (e.g., "Multiply", "Squeeze").

#### Ct (Command / Pipeline Set)
Sets the active pipeline state for an encoder.

| Offset | Description |
|--------|-------------|
| +0x00 | Marker `Ct\0\0` |
| +0x04 | Encoder Address (8 bytes) |
| +0x0C | Pipeline State Address (8 bytes) |

#### Ctt (Pipeline Creation)
Maps a Pipeline State Address to a Function Address.

| Offset | Description |
|--------|-------------|
| +0x00 | Marker `Ctt\0` |
| +0x04 | Device Address (8 bytes) |
| +0x0C | Function Address (8 bytes) |
| +0x14 | Reserved |
| +0x20 | Pipeline State Address (8 bytes) |

**Mapping Logic:**
To resolve a kernel name for a dispatch:
1. Dispatch occurs in an active Encoder.
2. `Ct` record maps Encoder -> Pipeline State.
3. `Ctt` record maps Pipeline State -> Function Address.
4. `CS` record (in device-resources) maps Function Address -> Name.

*Fallback:* If `Ct` record is missing (common in some traces), the Encoder Label from the `CS` record in `capture` is used as a proxy for the kernel name.

#### Dispatch (ul@3)
Represents a compute dispatch.

| Offset | Description |
|--------|-------------|
| +0x00 | Marker `ul@3` |
| +0x11 | ThreadsX (8 bytes) |
| +0x19 | ThreadsY (8 bytes) |
| +0x21 | ThreadsZ (8 bytes) |
| +0x29 | ThreadsPerGroupX (8 bytes) |
| ... | ... |

## Performance Counters (.gpuprofiler_raw)

When enabled, traces include a `.gpuprofiler_raw` directory containing:

| File | Format | Description |
|------|--------|-------------|
| `streamData` | NSKeyedArchiver plist | Pipeline metadata, dispatch timing, encoder timing |
| `Counters_f_*.raw` | Binary | Marker-scanned GPU counter data; marker-gap lengths vary and do not establish sample semantics |
| `Profiling_f_*.raw` | Binary | Statistical profiling samples (Execution Cost) |
| `Timeline_f_*.raw` | Binary | Timeline visualization event data |

### Retraction: 464-byte sample records

[V] The original 464-byte claim came from one capture: 87 of 262 gaps between
the byte marker `4e 00 00 00` had length 464, and one file size, 121,104 bytes,
was divisible by 464. Commits `5cf5616` and `c8ebbe8` promoted those arithmetic
observations to a record and sample classification without an authenticated
framing or semantic decode.

[V] Commit `c3c972c` withdrew the classification after scanning the first five
counter files in
`qwen25-05b-staticmask-warm-tokens2-4-rep1-perfdata3.gputrace`: among roughly
30,000 marker-delimited gaps it found no 464-byte gap, with 1742, 612, 671, and
8192 among the common lengths. That original temporary capture is no longer
present, so the exact scan cannot be rerun from the current checkout.

[V] A scan using the current `internal/profilerraw.Records` marker algorithm on
the disposable
`counter-oracle-source-20260809-1020.gpuprofiler_raw` fixture found 8,783 gaps
across 40 counter files, including 3,020 gaps of length 464 and 437 distinct
lengths.

[D] The occurrence and frequency of a 464-byte marker gap are capture-dependent.
Neither its presence nor its absence proves that the gap is one GPU hardware
sample, and marker scanning can split on the same byte sequence inside a
payload. Consumers must not infer record or sample semantics from length alone.
An authenticated framing definition or capture-matched semantic decode would
falsify this boundary.

### streamData

The `streamData` file is the key metadata file containing:
- **pipelineStateInfoData**: Pipeline-to-function mapping (40 bytes/record)
- **gpuCommandInfoData**: Per-dispatch durations or cumulative offsets (32 bytes/record)
- **encoderInfoData**: Per-encoder timing offsets (40 bytes/record)
- **pipelinePerformanceStatistics**: Instruction counts, register usage
- **APSTimelineData**: ReplayerGPUTime, command-buffer active/wall timing, and GPRWCNTR encoder profile blobs

See [STREAMDATA_FORMAT.md](./STREAMDATA_FORMAT.md) for detailed binary layouts.

### Shader Table Metrics

Xcode's shader table combines timing and sampling metrics:

1. **Dispatch Duration**: streamData dispatch duration or cumulative offset delta (from gpuCommandInfoData)
2. **Kernel Duration**: Aggregated dispatch time per pipeline
3. **Execution Cost**: Statistical GPU sampling percentage (from Profiling_f_*.raw)

### Execution Cost per encoder

Xcode's Execution Cost column is keyed per encoder, not per pipeline, and is
absent from the Counters.csv export. It is rebuilt from `APSCounterData`:

- Each pass reads the hardware counters twice per encoder, `GRC_SAMPLE_TYPE` 4
  at the start and 5 at the end. The end record's `GRC_GPU_CYCLES` is the
  cycles spent between them. [D] derived: every attributed sample in the
  reference archive is one of these two types, they pair by encoder id within a
  blob, and the begin records' counter columns are uniformly zero.
- Encoders are identified by **ordinal**, not by id. The capture is replayed
  once per Encoder Infos group, so an encoder gets a fresh `GRC_ENCODER_ID` in
  each group and only its position is stable. [D]
- Cost is the encoder's share of summed `GRC_GPU_CYCLES`.

Measured against Xcode's own export of the same capture
(`testdata/xcode-oracle/compute-kernel-encoders.txt`, 23 encoders): max
residual **0.911 pp**, rms **0.278 pp**. The figure is close but not Xcode's
number, and no aggregation tried reproduced it exactly - see
`internal/counter/encodercost.go` for the variants ruled out. [D]

Sample **counts** are not a cost proxy: the counter reads are scheduled, so 20
of 23 encoders have exactly 304 samples and 3 have exactly 112 regardless of
cost. [V]

Not attributed: cost per individual `dispatchThreads` command inside an
encoder, which Xcode also shows. The counter archive reads counters at encoder
boundaries only, so nothing finer is archived there.

Timeline and summary views use APSTimelineData when available for Effective GPU Time and
command-buffer active/wall spans. Non-profiled traces may use approximate extracted or
synthetic timing and should be treated as visualization data.

## Metal Libraries (MTLB)

Files with hex names (e.g., `F98EC4E82B8CACCA`) often contain Metal Library data.
They may contain embedded MTSP-like records defining functions.
