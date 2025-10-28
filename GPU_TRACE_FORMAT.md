# GPU Trace Format Analysis

## Overview

Metal GPU traces (.gputrace directories) contain detailed execution information in a proprietary binary format called MTSP (Metal Trace Storage Protocol).

## Directory Structure

```
BenchmarkLlamaForward.gputrace/
├── capture                          # Main capture file (MTSP format, ~10 MB)
├── device-resources-0x8baccc000     # Device resources (MTSP format, ~1.2 MB)
├── 1195650BBF3A22DF                 # UUID file with pointer table
├── FB9F8843-5421-43A4-974D-F140FFE8D23B.plist  # Metadata plist
└── ... (200+ additional UUID files)
```

## File Formats

### capture File

**Header**: MTSP magic bytes + version info
- Magic: "MTSP" (4 bytes)
- Version: uint32 (typically 1024)
- Size: uint64
- Offset: uint64

**Contents**:
- Kernel names (null-terminated strings)
- Encoder labels
- Command buffer records
- Execution timing data (to be fully decoded)
- Total size: ~10 MB for 200 kernel dispatches

### device-resources Files

Contains MTLBuffer and texture information in MTSP format.

**Buffer Entry Pattern**:
```
Offset +0:  "CU<b>ulul" (9 bytes) - marker
Offset +9:  0x00 0x00 0x00 (3 bytes) - padding
Offset +12: uint64 - pointer/address
Offset +20: "MTLBuffer-XXXX-Y\0" - buffer name (null-terminated)
Offset +N:  0x00 0x00 0x00 0x00 0x00 (5 bytes) - padding
Offset +N+5: uint64 - buffer size in bytes
```

**Example**:
```
Offset 0x190: 43 55 3c 62 3e 75 6c 75 6c  |CU<b>ulul|
Offset 0x19c: 00 c0 cc ba 08 00 00 00     |........| (pointer)
Offset 0x1a0: 4d 54 4c 42 75 66 66 65 72  |MTLBuffer|
Offset 0x1a9: 2d 31 37 34 34 2d 30 00     |-1744-0.|
Offset 0x1b1: 00 00 00 00                 |....|
Offset 0x1b5: 30 00 00 00 00 00 00 00     |0.......| (size = 48 bytes)
```

### UUID Files

Appear to contain pointer tables and reference structures. Format not yet fully understood.

**Pattern observed**:
- 8-byte header with count
- Repeating 32-byte entries containing:
  - uint64 pointer values (in range 0x08bb20xxxxxx)
  - uint16 index values
  - Padding bytes

## Extracted Information

### From BenchmarkLlamaForward.gputrace:

**Kernels**: 200 unique GPU kernels identified
- Examples: AsTypeMultiplyQuantizedMatmul, RoPEScaledDotProductAttention, etc.
- Kernel names extracted from capture file string table

**Buffers**: 348 MTLBuffer allocations tracked
- Total memory: 747,152 bytes (0.71 MB)
- Size distribution:
  - <100 bytes: 155 buffers (likely parameter buffers)
  - 100B-1KB: 80 buffers
  - 1KB-1MB: 113 buffers
  - >=1MB: 0 buffers (in this trace)

**Command Buffers**: ~29,920 potential command buffer records
- Currently using "Culul" marker detection (high false positive rate)
- Need more precise parsing to extract actual command buffer structure

**Encoder Labels**: 0 found in this trace
- This benchmark doesn't use labeled encoders
- When present, labels appear as null-terminated strings

## Implementation

### Current Parser (`gputrace.go`)

Extracts:
- Metadata from .plist file
- Kernel names from capture file
- Encoder labels (when present)
- Buffer labels (basic)

### Enhanced Parser (`enhanced_parser.go`)

Adds:
- Detailed buffer extraction with sizes
- Command buffer detection (needs refinement)
- Texture binding identification
- Structured metadata (CommandBufferInfo, EncoderInfo, DispatchInfo)
- Comprehensive trace analysis report

### Analysis Tool (`cmd/analyze/main.go`)

Provides:
- Trace structure overview
- Kernel frequency analysis
- Buffer size distribution
- File size breakdown
- Timestamp pattern scanning (experimental)

## Timing Information

**Challenge**: Timing data is embedded in the binary format but extraction is incomplete.

**Observations**:
- Mach absolute time values (>1e15) appear in the data
- Timing likely associated with:
  - Command buffer submission/completion
  - Individual kernel dispatch start/end
  - Encoder creation/commit
- Pattern not yet fully decoded

**Current Status**:
- `ExtractTimingData()` returns empty results
- Need to identify timing field offsets in binary structures
- May require reverse engineering or Metal framework documentation

## Future Work

1. **Timing Extraction**:
   - Decode command buffer timestamp fields
   - Extract per-kernel execution timing
   - Parse encoder timing information

2. **Command Buffer Structure**:
   - Refine "Culul" marker detection
   - Extract command buffer labels
   - Identify encoder relationships

3. **Correlation**:
   - Link kernel names to specific dispatches
   - Associate buffers with kernel arguments
   - Map command buffers to encoders

4. **Validation**:
   - Compare with Xcode Instruments output
   - Verify buffer sizes against actual allocations
   - Test with various trace types (compute, render, mixed)

## References

- Metal Performance Shaders documentation
- MTLCaptureManager API
- Xcode Instruments .trace format (related but different)
- MTSP appears to be proprietary Apple format

## Example Usage

```go
trace, err := gputrace.Open("path/to/trace.gputrace")
if err != nil {
    log.Fatal(err)
}

// Get enhanced metadata
meta, err := trace.ExtractEnhancedMetadata()
if err != nil {
    log.Fatal(err)
}

fmt.Printf("Kernels: %d\n", meta.TotalKernels)
fmt.Printf("Buffers: %d\n", len(meta.BufferBindings))
fmt.Printf("Command Buffers: %d\n", len(meta.CommandBuffers))

// Generate full analysis
report := trace.AnalyzeTraceStructure()
fmt.Println(report)
```

## Testing

```bash
cd experiments/gputrace
go run ./cmd/analyze ~/path/to/trace.gputrace
```

Output includes:
- Trace metadata
- Kernel list and frequency
- Buffer bindings with sizes
- Command buffer addresses
- File size breakdown
