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
| `Counters_f_*.raw` | Binary | GPU counter samples (464-byte records) |
| `Profiling_f_*.raw` | Binary | Statistical profiling samples (Execution Cost) |
| `Timeline_f_*.raw` | Binary | Timeline visualization event data |

### streamData

The `streamData` file is the key metadata file containing:
- **pipelineStateInfoData**: Pipeline-to-function mapping (40 bytes/record)
- **gpuCommandInfoData**: Per-dispatch timing in microseconds (32 bytes/record)
- **encoderInfoData**: Per-encoder timing (40 bytes/record)
- **pipelinePerformanceStatistics**: Instruction counts, register usage

See [STREAMDATA_FORMAT.md](STREAMDATA_FORMAT.md) for detailed binary layouts.

### Three Timing Metrics

Xcode shows three distinct metrics:

1. **Dispatch Duration**: Wall clock time per dispatch (from gpuCommandInfoData)
2. **Kernel Duration**: Aggregated dispatch time per pipeline
3. **Execution Cost**: Statistical GPU sampling (from Profiling_f_*.raw, not yet parsed)

## Metal Libraries (MTLB)

Files with hex names (e.g., `F98EC4E82B8CACCA`) often contain Metal Library data.
They may contain embedded MTSP-like records defining functions.
