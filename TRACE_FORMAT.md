# GPU Trace Format Documentation

This document describes the findings from reverse engineering the Apple GPU trace format used by Xcode's GPU debugger.

## File Structure

A `.gputrace` directory contains:

- `capture` - Main binary trace file containing MTSP records
- `index` - xdic index mapping function calls to offsets
- `metadata` - Trace metadata (timestamps, device info, etc.)
- `device-resources-*` - Device resource state snapshots
- `MTLBuffer-*-*` - Metal buffer contents (symlinks for aliased buffers)
- `store0` - zlib-compressed file (typically all zeros - no timing data)
- Various shader files (hex UUIDs)

### ⚠️ Important: Timing Data Not Stored

**Critical Discovery:** The `.gputrace` files do NOT contain GPU execution timing or performance percentages.

- The `store0` file decompresses to all zeros (no pre-computed timing)
- MTSP records contain command structure, not execution measurements
- Xcode Instruments derives timing by **replaying the GPU workload** with performance counters enabled

See [INSTRUMENTS_TIMING_INVESTIGATION.md](./INSTRUMENTS_TIMING_INVESTIGATION.md) for complete details on how Instruments measures GPU timing.

## Capture File Format

### File Header
```
Magic: "MTSP" (Metal Trace Stream Protocol)
```

### Record Types

### Record Types

The capture file contains various record types identified by 4-byte magic numbers or strings.
Parsing logic now supports recursive discovery of nested records.

1. **CS** - Command Submission? (Container)
   - Often acts as a textual label container but ALSO contains nested records.
   - In `device-resources`, CS records map text strings (keys) to Addresses (values).
     - Example: "affine_qmv_..." -> 0x9bb8c81c0 (Function Address).
     - Example: "compute-pipeline-state" -> 0x9bb8cab600 (Pipeline Address).
     - Example: "library" -> 0x9bb8c81c0.

2. **Cuw** - Command Update/Write? (Container)
   - Identified as a container for kernels and commands.
   - Heavily nested structure.

3. **Ci** - Compute Info? (Container)
   - Size 256 bytes (common).
   - Contains:
     - `ICB` Address (Indirect Command Buffer?)
     - `Count` (Dispatch count or Threadgroups?)
   - Often nests `Cul` and `Cuw` records.

4. **Ctt** - Command details (Size 116 / 76)
   - Links Function objects to Pipeline States.
   - In `device-resources` (Size 76):
     - +0x04: Device Address
     - +0x0C: Function Address
     - +0x20: Pipeline State Address
   - In `capture` (Size 116):
     - Contains similar linking plus execution params.

5. **Ciulul** / **CiSulul** - Container (Variable size)
   - Appears in `device-resources`.
   - Contains arguments ("in", "out") and source file mappings.

6. **Cul** - Unknown label records (Size 56/68)

7. **Other Unknowns**
   - Numerous small records (22, 24, 25 bytes) nested in `Ct`/`Ci`.
   - Likely contain kernel arguments, grid sizes, or dispatch flags.

### Device Resources
The `device-resources` file is an MTSP stream that acts as a symbol table/linker.
- Maps Function Addresses to Names (`CS` record).
- Maps Pipeline Addresses to "compute-pipeline-state" label (`CS` record).
- Maps Function Addresses to Source Files (`CiSulul` record).


## Index File Format (xdic)

The `index` file maps function indices to capture file offsets:

```
Header (20 bytes):
  +0x00: "xdic" magic (4 bytes)
  +0x04: version (4 bytes)
  +0x08: entry_size (4 bytes) - typically 8192 (0x2000)
  +0x0C: entry_count (4 bytes) - number of function indices
  +0x10: entry_count_copy (4 bytes) - duplicate count

Entry Array (starting at 0x20):
  Each entry is 8 bytes (two uint32s):
    [function_index]: offset1, offset2

  0xffffffff indicates no mapping for that slot
```

In the test trace:
- 3,771 total entries
- 1,138 unique function indices with mappings

## Counting Metal API Calls

### Command Buffers

**Key Discovery**: Command buffers are counted by CUUU markers, NOT by:
- Culul markers (only 6 vs 70 command buffers)
- Function call counts
- Other record types

Each `[MTLCommandBuffer commit]` call generates one CUUU record with:
- Unique timestamp
- Unique UUID identifier
- Offset in the capture stream

### Compute Encoders

Compute command encoders are identified by **Cul records with specific characteristics**:
- Type field = 1 (at offset +0x0C)
- Size/count field = 0x74 (116 decimal, at offset +0x14)

Each `[MTLCommandBuffer computeCommandEncoder]` or `[MTLCommandBuffer computeCommandEncoderWithDescriptor:]` call generates a Cul record matching these criteria.

Test results:
- Trace 1: 42 compute encoders
- Trace 2: 38 compute encoders

### Dispatch Calls

Dispatch calls (kernel launches) are identified by the **"ul@3" marker pattern**.

Each `[MTLComputeCommandEncoder dispatchThreadgroups:...]` or `[MTLComputeCommandEncoder dispatchThreads:...]` call generates this marker.

Test results:
- Trace 1: 1646 dispatch calls
- Trace 2: 1578 dispatch calls

## Implementation

See `command_buffer.go` for the Go implementation:

```go
// Command buffers (CUUU markers)
type CommandBuffer struct {
    Index     int      // 0-based index
    Timestamp uint64   // Commit timestamp
    UUID      string   // Unique identifier
    Offset    int64    // File offset
}
func (t *Trace) ParseCommandBuffers() ([]*CommandBuffer, error)
func (t *Trace) CountCommandBuffers() (int, error)

// Compute encoders (Cul records with type=1, size=0x74)
type ComputeEncoder struct {
    Index   int      // 0-based index
    Address uint64   // Encoder address/ID
    Offset  int64    // File offset
}
func (t *Trace) ParseComputeEncoders() ([]*ComputeEncoder, error)
func (t *Trace) CountComputeEncoders() (int, error)

// Dispatch calls ("ul@3" markers)
type DispatchCall struct {
    Index  int      // 0-based index
    Offset int64    // File offset
}
func (t *Trace) ParseDispatchCalls() ([]*DispatchCall, error)
func (t *Trace) CountDispatchCalls() (int, error)
```

## Usage

```bash
# Count command buffers
gputrace stats trace.gputrace

# List all command buffers with details
go test -v -run TestParseCommandBuffers
```

## Timing Data in GPU Traces

**Important**: The `.gputrace` format does NOT contain pre-computed timing data or shader execution durations.

### What IS in the Trace

- Command buffer commit timestamps (CUUU records, +0x08)
- Command buffer UUIDs for identification
- Encoder structure and dispatch configurations
- Buffer bindings and resource state

### What is NOT in the Trace

- ❌ Per-shader execution time
- ❌ Shader cost percentages
- ❌ GPU cycle counts
- ❌ Performance counter values

### How to Get Timing Data

See [INSTRUMENTS_TIMING_INVESTIGATION.md](./INSTRUMENTS_TIMING_INVESTIGATION.md) for details on:

1. **Replay approach**: Reconstruct and re-execute commands with `MTLCounterSampleBuffer`
2. **kdebug approach**: Capture kernel debug events during original execution
3. **Signpost approach**: Use Metal AGX signposts for shader-level timing

The command buffer timestamps in CUUU records show when command buffers were submitted but not individual shader timing.

## References

Based on reverse engineering of:
- `/Applications/Xcode-beta.app/Contents/PlugIns/GPUDebugger.ideplugin`
- `/Applications/Xcode-beta.app/Contents/SharedFrameworks/GPUToolsCore.framework`

Key classes and symbols found:
- `GPUMTLTraceCommandBufferGroupItem`
- `GTHostMTLCommandBuffer`
- `DYCaptureSession`
- `kDYMessageGuestAppMTLCommandBuffersCaptured`
