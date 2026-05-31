# GPU Trace Record Formats

Based on analysis of `.gputrace` files and GPUDebugger.ideplugin.

## File Structure

GPU trace directories contain:
- `capture` - Main trace file with MTSP header and record stream (3.6MB typical)
- `index` - Index file with "xdic" magic for quick lookup (254KB)
- `metadata` - Binary plist with trace metadata (version, API, device info)
- `device-resources-0xADDRESS` - Device resource snapshots with MTSP format (1.6MB typical)
- `delta-device-resources-0xADDRESS` - Delta/diff files (420B typical)
- `MTLBuffer-XXXX-Y` - Metal buffer snapshots (Y=0 is data, Y=1,2 are symlinks)
- `store0` - zlib-compressed file (typically all zeros - no command timing data stored)
- Hex-named files (e.g., `FE52ED69B41ABB45`) - Metal shader libraries/pipeline states
- `.gpuprofiler_raw/` - (Optional) Profiler export data if profiling is enabled, including `streamData`, APSTimelineData timing, and hardware counter files

### ⚠️ Important: No Execution Timing in MTSP Records

**MTSP records contain command structure, NOT execution timing.**

The records document what GPU commands were submitted, but not how long they took to execute. For timing information:
- Xcode Instruments: Replays the workload with performance counters
- Profiler exports: May include `.gpuprofiler_raw/streamData` with APSTimelineData `ReplayerGPUTime`, command-buffer timestamps, and encoder/dispatch cumulative offsets
- This library: Uses streamData/APSTimelineData when present; otherwise kdebug events, signposts, or approximate fallbacks
- Shader metrics expose `TimingSource` and `TimingApprox` so consumers can distinguish real profiler timing from heuristic/synthetic estimates
- Direct IOReport channel timing is not treated as a supported timing source unless it can fail closed with fixture-backed duration semantics
- See [INSTRUMENTS_TIMING_INVESTIGATION.md](./INSTRUMENTS_TIMING_INVESTIGATION.md) for details

### Metadata File Format

Binary plist (bplist00) containing:
```text
{
  "(uuid)": "0C215EC0-4997-4066-881D-17747E3E22FE"
  "DYCaptureEngine.captured_frames_count": 1
  "DYCaptureSession.capture_version": 0
  "DYCaptureSession.graphics_api": 1          # 1 = Metal
  "DYCaptureSession.deviceId": 1
  "DYCaptureSession.nativePointerSize": 8     # 64-bit
  "DYCaptureSession.boundaryLess": false
  "DYCaptureSession.interpose_feature_version": 65538
  "DYCaptureSession.library_link_time_versions": {
    "Metal": 24264455 (0x01723187)
    "CoreFoundation": 269287935
    "Foundation": 269287935
    "System": 88866816
  }
  "DYCaptureSession.unusedBufferCount": 0
  "DYCaptureSession.unusedCommandQueueCount": 0
  "DYCaptureSession.unusedComputePipelineStateCount": 0
  # ... other unused resource counts
}
```

Parse with: `plutil -p metadata` or use plist library in code.

### Index File Format

- Magic: "xdic" (0x78646963)
- Binary index structure with uint32 offsets
- Pattern: pairs of offsets followed by 0xFFFFFFFF markers
- Used for random access into capture/resource files

## MTSP Header

File begins with MTSP magic:
```text
Offset  Content
0x0000  4D 54 53 50              "MTSP" magic
0x0004  00 04 00 00              Version (0x0400 = 4.0?)
0x0008  3C 00 00 00              Header size (60 bytes)
0x000C  00 C1 FF FF              Flags/type marker
0x0010  ... (zeros/padding)
```

## Record Types

Records appear after the MTSP header and are identified by type codes:

### 1. "Culul" - Command Buffer Record
```text
Offset  Size  Description
+0x00   4     Length (09 00 00 00 = 9 bytes)
+0x04   5     "Culul" marker
+0x09   3     Padding (00 00 00)
+0x0C   8     Command buffer address/pointer
+0x14   ...   Additional metadata
```

Found at: `enhanced_parser.go:92`
Pattern: `09 00 00 00 43 75 6c 75 6c 00 00 00`

Example from capture:
```text
00124bd0  09 00 00 00 43 75 6c 75  6c 00 00 00 00 40 20 c1
          ^^^^^^^^^^^  C  u  l  u   l
          length=9
```

### 2. "CtU<b>ulul" - Buffer Binding Record
```text
Offset  Size  Description
+0x00   4     Length (04 00 00 00 = 4 bytes type field)
+0x04   9     "CtU<b>ulul" marker
+0x0D   3     Padding
+0x10   8     Pointer/address
+0x18   N     Buffer name (e.g., "MTLBuffer-918-0\0")
+N      5     Padding (00 00 00 00 00)
+N+5    8     Buffer size (uint64 little-endian)
```

Found at: `enhanced_parser.go:140`
Pattern: `43 74 55 3c 62 3e 75 6c 75 6c` = "CtU<b>ulul"

Example from capture:
```text
000005d0  04 00 00 00 43 74 55 3c 62 3e 75 6c 75 6c 00 00
          ^^^^^^^^^^^ C  t  U  <  b  >  u  l  u  l
000005e0  00 d5 8e be 09 00 00 00 80 4a 40 c0 09 00 00 00
          ^^^^^^^^^^^^ pointer   ^^^^^^^^^^^^ ???
000005f0  4d 54 4c 42 75 66 66 65 72 2d 39 31 38 2d 30 00
          M  T  L  B  u  f  f  e  r  -  9  1  8  -  0  \0
00000600  00 00 00 00 00 00 00 00 00 00 c0 00 00 00 00 00
          ^^^^^^^^^^^^^^^^^^^^^^^^ padding   ^^^^^^^^^^
                                              size=0xc000 (49,152 bytes)
```

### 3. "CUUU" - Unknown Record Type
```text
Offset  Size  Description
+0x00   4     Length (04 00 00 00)
+0x04   4     "CUUU" marker
+0x08   ...   Unknown data
```

Example from capture:
```text
000004c0  04 00 00 00 43 55 55 55 00 00 00 00 00 d5 8e be
          ^^^^^^^^^^^ C  U  U  U
```

### 4. "CS" - Command Submission Records ⭐ **Key Discovery**

**CS records mark encoder boundaries and contain kernel names or pipeline state UUIDs.**

```text
Format:
[preceder: 4 bytes] [CS marker: 0x43 0x53 0x00 0x00] [address: 8 bytes] [identifier: null-terminated string]

Preceder values:
- 0x04000000 = Pipeline state UUID follows
- 0x09100000 = Kernel name follows
```

**Example 1 - Kernel Name**:
```text
00004fc0  00 00 00 00 00 00 00 00  00 00 00 00 09 10 00 00
                                                ^^^^^^^^^^^ preceder
00004fd0  43 53 00 00 00 5e c4 74  0a 00 00 00 76 73 5f 4d
          C  S        ^^^^^^^^^^^^ address     v  s  _  M
00004fe0  75 6c 74 69 70 6c 79 66  6c 6f 61 74 33 32 00 00
          u  l  t  i  p  l  y  f   l  o  a  t  3  2  \0

Kernel name: "vs_Multiplyfloat32"
```

**Example 2 - Pipeline UUID**:
```text
000004a0  00 00 00 00 00 00 00 00  00 00 00 00 04 00 00 00
                                                ^^^^^^^^^^^ preceder
000004b0  43 53 00 00 40 63 c4 74  0a 00 00 00 33 42 30 32
          C  S        ^^^^^^^^^^^^ address     3  B  0  2
000004c0  36 34 30 39 2d 37 38 39  44 2d 33 36 39 36 2d 42
          6  4  0  9  -  7  8  9   D  -  3  6  9  6  -  B
000004d0  45 32 41 2d 35 30 34 32  42 30 41 42 30 37 37 44
          E  2  A  -  5  0  4  2   B  0  A  B  0  7  7  D

UUID: "3B026409-789D-3696-BE2A-5042B0AB077D"
```

**Usage**:
- CS records appear at encoder submission boundaries
- Kernel names identify compute shader functions
- Pipeline UUIDs link to compiled pipeline states
- Address field provides correlation with other records
- Essential for matching encoders to actual GPU kernels

**API**:
```go
// Parse all CS records
records, _ := trace.ParseCSRecords()

// Get only kernel names
kernels, _ := trace.GetKernelNameCSRecords()

// Get only pipeline UUIDs
uuids, _ := trace.GetUUIDCSRecords()
```

See `cs_records.go` for implementation details.

### 5. "C" (0x43) Records - Encoder/Dispatch Records
```text
Offset  Size  Description
+0x00   4     Length (varies: 0x0E, 0x20, etc.)
+0x04   4     Type = 43 00 00 00 ("C")
+0x08   8     Pointer/address
+0x10   4     Count or size
+0x14   ...   Variable data (often pointers/addresses)
```

Example from capture:
```text
00000060  0e 02 00 00 43 00 00 00 c0 e1 c1 c1 09 00 00 00
          ^^^^^^^^^^^ C              ^^^^^^^^^^^^ address
          length=0x020e (526 bytes)
```

## Type Markers Summary

| Marker | Hex | Purpose | Size Pattern |
|--------|-----|---------|--------------|
| MTSP | 4D 74 53 50 | File header | Fixed |
| Culul | 43 75 6c 75 6c | Command buffer | ~116 bytes |
| CtU<b>ulul | 43 74 55 3c 62 3e 75 6c 75 6c | Buffer binding | Variable |
| CUUU | 43 55 55 55 | Command buffer UUID | Variable |
| **CS** | **43 53 00 00** | **Command submission (kernel/pipeline)** | **Variable** |
| C | 43 00 00 00 | Encoder/dispatch | Variable |

## Command Buffer Counting

To count command buffers, search for "Culul" (0x43 0x75 0x6c 0x75 0x6c) markers in the capture file.

From the test trace (`/tmp/llm-tool_1762199057.gputrace/capture`):
```bash
strings capture | grep -c Culul
# or
hexdump -C capture | grep -c Culul
```

## Notes

1. All multi-byte integers appear to be little-endian
2. Pointers are 64-bit (8 bytes)
3. Many records are preceded by a length field (4 bytes)
4. Type markers often followed by 00 00 00 padding
5. Buffer versions (-0, -1, -2) use symlinks for deduplication
6. The 0xC1FFFF marker appears frequently, possibly indicating record boundaries

## Real-World Example

From `/tmp/llm-tool_1762199057.gputrace`:

```text
File Statistics:
  Total files: 2,496
  Total size: ~2GB

Metadata:
  UUID: 0C215EC0-4997-4066-881D-17747E3E22FE
  Graphics API: Metal (1)
  Device ID: 1
  Pointer Size: 8 bytes (64-bit)

MTSP Records: 5,789 total
  Culul (command buffers): 6
  Ci (indirect command buffers): 6
  Ct (command/dispatch ops): 4,624
  Cul (buffer bindings): 1,140
  Cuw (command write): 13

Memory Usage:
  Total Buffer Size: 1.83 GiB
  Unique Buffers: 1,026

Kernels:
  Unique Kernel Names: 45
  Including: rope_float16, argmax_float32, affine_dequantize, etc.
```

## Command Line Tools

### View trace statistics
```bash
# Basic stats
go run ./cmd/gputrace stats trace.gputrace

# Verbose output with all kernels and buffers
go run ./cmd/gputrace stats trace.gputrace -v

# Count command buffers
Use a small local helper program to inspect command buffer counts when validating these notes.

# Parse metadata
plutil -p trace.gputrace/metadata
```

### Inspect raw data
```bash
# Check file types
file trace.gputrace/*

# View MTSP header
hexdump -C trace.gputrace/capture | head -20

# Count Culul (command buffer) records
strings trace.gputrace/capture | grep -c "Culul"

# Find buffer bindings
strings trace.gputrace/device-resources-* | grep MTLBuffer
```

## Code References

- `internal/trace/trace.go` - Main trace opening and metadata parsing
- `internal/trace/mtsp.go` - MTSP record type definitions and parsing
- `internal/trace/mtsp_parsing.go` - Enhanced metadata extraction
- `internal/analysis/stats.go` - Statistics computation
- `internal/trace/kdebug.go` - Kernel debug trace parsing
- `internal/trace/cs.go` - CS record parsing
- `internal/counter/streamdata.go` - `.gpuprofiler_raw/streamData` and APSTimelineData timing parsing
- `internal/shader/metrics.go` - Shader timing source labels and approximation flags
- `cmd/gputrace/cmd/stats.go` - Command-line statistics tool
- GPUDebugger.ideplugin - Apple's Xcode plugin with GTTimeline.framework for trace processing
