# GPU Trace Format Differences: Performance vs Non-Performance Captures

**Date:** 2025-11-07
**Status:** Comprehensive analysis of trace format differences

## Executive Summary

GPU traces captured with and without performance profiling enabled have significant structural differences. This document details these differences to ensure complete parsing of both trace types.

**Key Finding:** Performance and non-performance traces are **mutually exclusive** in file content—perf traces contain hardware metrics, non-perf traces contain resource state tracking.

---

## Quick Comparison Table

| Feature | Perf Trace | Non-Perf Trace | Notes |
|---------|------------|----------------|-------|
| `.gpuprofiler_raw/` | ✅ Yes (6GB+) | ❌ No | Hardware performance counters |
| `thumbnails_encoder/` | ✅ Yes (empty) | ❌ No | Encoder preview images (unused) |
| `device-resources-*` | ❌ No | ✅ Yes (7.5KB) | Device memory state snapshots |
| `delta-device-resources-*` | ❌ No | ✅ Yes (56B) | Resource allocation deltas |
| `startup-*-platform` | ❌ No | ✅ Yes (76B, 8B) | Platform initialization data |
| `unused-device-resources-*` | ❌ No | ✅ Yes (6.2KB) | Unused resource tracking |
| `store0` size | Larger (23KB) | Smaller (18KB) | Different compression/content |
| `metadata` size | 1,135 bytes | 1,143 bytes | Minor structural differences |
| `index` content | Different | Different | Offset tables vary |
| Common files | Yes | Yes | `capture`, `unsorted-capture` identical |

---

## File Presence Analysis

### Files in Common (Identical Content)

These files are **byte-for-byte identical** between perf and non-perf traces:

| File | Size | Purpose |
|------|------|---------|
| `capture` | 16,924 bytes | Main trace command stream (MTSP format) |
| `unsorted-capture` | 16,792 bytes | Unsorted command data |

**Implication:** Core GPU command capture is identical; differences are in auxiliary data.

### Files ONLY in Performance Traces

#### 1. `.gpuprofiler_raw/` Directory

**Size:** ~6GB (40 counter files)
**Purpose:** Hardware performance counter data

```text
.gpuprofiler_raw/
├── 0  - Primary metrics (121KB, 261 records)
├── 1  - ALU/compute metrics
├── 2  - Memory bandwidth
...
└── 39 - Advanced counters
```

**Content:** Binary performance metrics (464-byte sample records, 2,300+ byte metadata records)

**Parsing:** Already implemented in `internal/counter/counter.go`

**CSV Export:** 241 columns of hardware metrics

**Documentation:** See [BINARY_FORMAT_REFERENCE.md](BINARY_FORMAT_REFERENCE.md)

#### 2. `thumbnails_encoder/` Directory

**Size:** Empty (0 files)
**Purpose:** Intended for encoder preview images in Xcode Instruments
**Status:** Unused in current captures

**Speculation:** May be populated when capturing with additional Xcode debugging features enabled.

### Files ONLY in Non-Performance Traces

These files provide resource state tracking not present in perf traces:

#### 1. `device-resources-0xADDRESS`

**Size:** 7.5KB
**Format:** MTSP (Metal Trace Storage Protocol)
**Purpose:** Device memory state snapshot

**Header:**
```text
Offset  Content
0x00    4D 54 53 50     "MTSP" magic
0x04    00 04 00 00     Version (4)
0x08    3C 00 00 00     Header size (60 bytes)
0x0C    10 D0 FF FF     Flags
```

**Content Structure:**
```text
MTSP Header (60 bytes)
├── Record 1: Device UUID
│   Magic: "Ciui" (0x43 69 75 69)
│   Address: 0x997088000
│   UUID: "1848552C307F2D6C"
│
├── Record 2: Resource Tree "root"
│   Magic: "CSuwuw"
│   Label: "root"
│
├── Record 3: Resource Tree "buffers"
│   Magic: "CSuwuw"
│   Label: "buffers"
│
└── [Additional resource records...]
```

**Purpose:**
- Tracks Metal buffer allocations
- Resource hierarchy (root, buffers, textures)
- Memory state at capture time

**Parsing Status:** ❌ Not yet implemented

**Priority:** P1 - Needed for complete resource tracking

#### 2. `delta-device-resources-0xADDRESS`

**Size:** 56 bytes
**Format:** MTSP
**Purpose:** Resource allocation changes/deltas

**Content:** Minimal MTSP record indicating no resource changes during capture

```text
00000000  4d 54 53 50 00 04 00 00  30 00 00 00 08 d0 ff ff
00000010  00 00 00 00 00 00 00 00  00 00 00 00 00 00 00 00
00000020  00 00 00 00 00 00 00 00  08 00 00 00 43 44 00 00
00000030  00 80 08 97 09 00 00 00
```

**Record Type:** "CD" (0x43 44) - likely "Change Delta"

**Parsing Status:** ❌ Not yet implemented

**Priority:** P2 - Useful for dynamic resource tracking

#### 3. `startup-0-platform`

**Size:** 76 bytes
**Format:** MTSP
**Purpose:** Platform initialization data (device info)

**Content:**
```text
MTSP Header
└── Record: Device UUID
    Magic: "CU" (0x43 55)
    Address: 0x997088000
    UUID: "1848552C307F2D6C"
```

**Parsing Status:** ❌ Not yet implemented

**Priority:** P3 - Informational, not critical

#### 4. `startup-1-platform`

**Size:** 8 bytes
**Format:** MTSP (header only)
**Purpose:** Secondary platform initialization

**Content:** Empty MTSP header (no records)

**Parsing Status:** ❌ Not yet implemented

**Priority:** P3 - Likely unused

#### 5. `unused-device-resources-0xADDRESS`

**Size:** 6.2KB
**Format:** MTSP
**Purpose:** Tracks resources allocated but never used

**Content:** MTSP records identifying unused Metal objects:
- Unused buffers
- Unused textures
- Unused pipeline states
- Unused samplers

**Use Case:** Performance optimization - identifies waste

**Parsing Status:** ❌ Not yet implemented

**Priority:** P2 - Useful for optimization insights

---

## Binary Format Differences in Common Files

### 1. `index` File Differences

**Size:** 13,531 bytes (both traces)
**Format:** xdic (X-DIgital Capture index)

**Difference Count:** 167 bytes differ

**Analysis:**
```bash
$ cmp -l perf/index nonperf/index | wc -l
167
```

**Byte Positions Changed:** Near end of file (offset ~12,745+)

**Cause:** Index entries point to different offsets in `store0` and resource files

**Implication:** Index file is **trace-specific** and cannot be reused between perf/non-perf

**Parsing:** Already implemented in `internal/trace/trace.go:ParseXDICIndex()`

### 2. `metadata` File Differences

**Sizes:**
- Perf: 1,135 bytes
- Non-perf: 1,143 bytes (+8 bytes)

**Format:** Binary plist (bplist00)

**Difference Count:** 167 bytes differ

**Content Comparison:**
```bash
$ plutil -p perf/metadata > perf.txt
$ plutil -p nonperf/metadata > nonperf.txt
$ diff perf.txt nonperf.txt
```

**Result:** Structural content is **identical**

**Differences:**
- UUID timestamps
- Capture timestamps
- Minor binary plist encoding variations

**Implication:** Metadata is semantically identical, binary differences are non-functional

**Parsing:** Already implemented in `internal/trace/trace.go:parseMetadata()`

### 3. `store0` File Differences

**Sizes:**
- Perf: 23,607 bytes
- Non-perf: 18,390 bytes (-5,217 bytes, 22% smaller)

**Format:** zlib-compressed data

**Content:**
```text
Header: 78 5E ED    (zlib magic)
Data: Compressed trace data
```

**Difference Analysis:**

Non-perf trace is **significantly smaller** because:
1. No performance counter references
2. Simpler resource tracking
3. Fewer internal structures

**Decompression Test:**
```go
// internal/trace/trace.go
func decompressStore0(data []byte) ([]byte, error) {
    r, err := zlib.NewReader(bytes.NewReader(data))
    if err != nil {
        return nil, err
    }
    defer r.Close()
    return io.ReadAll(r)
}
```

**Parsing Status:** ⚠️ Partially implemented (decompression works, content interpretation unclear)

**Current Status:** Decompressed content structure not yet documented

**Priority:** P2 - Currently treat as opaque data, works for existing use cases

---

## MTSP Record Format Reference

Many non-perf files use MTSP (Metal Trace Storage Protocol) format:

### MTSP Header (60 bytes)

```text
Offset  Size  Type    Field
0x00    4     char[4] Magic: "MTSP" (0x4D 54 53 50)
0x04    4     uint32  Version (typically 4)
0x08    4     uint32  Header size (60)
0x0C    4     uint32  Flags
0x10    40    -       Reserved/padding
```

### MTSP Record Structure

```text
Offset  Size  Type    Field
0x00    4     uint32  Record size (including header)
0x04    4     uint32  Record type/flags
0x08    8     -       Reserved
0x10    8     uint64  Object address (if applicable)
0x18    4     uint32  Data length
0x1C    ?     -       Record-specific data
```

### Common MTSP Record Types

| Magic | Hex        | Purpose |
|-------|------------|---------|
| "CU"  | 0x43 55    | Device UUID |
| "Ciui"| 0x43 69 75 69 | Device info |
| "CSuwuw" | 0x43 53 75 77 75 77 | Resource tree node |
| "CD"  | 0x43 44    | Change delta |

---

## Parsing Implementation Status

### ✅ Fully Implemented

| Component | Location | Notes |
|-----------|----------|-------|
| `.gpuprofiler_raw/` | `internal/counter/` | Complete with CSV export |
| `capture` | `internal/trace/trace.go` | MTSP command stream |
| `index` | `internal/trace/trace.go:ParseXDICIndex()` | Index parsing |
| `metadata` | `internal/trace/trace.go:parseMetadata()` | Binary plist |

### ⚠️ Partially Implemented

| Component | Status | Priority |
|-----------|--------|----------|
| `store0` | Decompression only | P2 |
| `thumbnails_encoder/` | Empty directory (no action needed) | P3 |

### ❌ Not Yet Implemented

| Component | Priority | Complexity | Value |
|-----------|----------|------------|-------|
| `device-resources-*` | P1 | Medium | High - resource state |
| `unused-device-resources-*` | P2 | Low | Medium - optimization |
| `delta-device-resources-*` | P2 | Low | Medium - dynamic tracking |
| `startup-*-platform` | P3 | Low | Low - mostly informational |

---

## Recommended Parsing Strategy

### Phase 1: Critical (P1) — implemented

`device-resources-*` parsing landed in `internal/trace/trace.go`
(`loadDeviceResources`), which also feeds kernel-name resolution in
`internal/shader/mapper.go`. The sketch below is the original design note; the
shipped types differ.

```go
type DeviceResources struct {
    DeviceAddress uint64
    DeviceUUID    string
    Resources     []ResourceNode
}

type ResourceNode struct {
    Type     string // "buffer", "texture", "pipeline"
    Label    string
    Size     uint64
    Children []ResourceNode
}

func ParseDeviceResources(path string) (*DeviceResources, error) {
    data, err := os.ReadFile(path)
    if err != nil {
        return nil, err
    }

    // Parse MTSP header
    if !bytes.HasPrefix(data, []byte("MTSP")) {
        return nil, fmt.Errorf("invalid MTSP header")
    }

    // Parse records
    return parseDeviceResourceRecords(data[60:])
}
```

**Integration:**
```go
// Add to Trace struct
type Trace struct {
    // ... existing fields
    DeviceResources *DeviceResources
}
```

### Phase 2: Optimization (P2)

1. **Parse `unused-device-resources-*`** for optimization insights
2. **Parse `delta-device-resources-*`** for dynamic tracking
3. **Analyze `store0` decompressed content** for additional data

### Phase 3: Completeness (P3)

1. Parse `startup-*-platform` files
2. Document `thumbnails_encoder/` usage (if ever populated)

---

## Validation Test Cases

### Test 1: Parse Both Trace Types

```go
func TestParseBothTraceTypes(t *testing.T) {
    perfTrace, err := gputrace.Open("testdata/06-six-encoders-run1-perf.gputrace")
    require.NoError(t, err)

    nonperfTrace, err := gputrace.Open("testdata/06-six-encoders-run1.gputrace")
    require.NoError(t, err)

    // Perf trace should have counter data
    assert.NotNil(t, perfTrace.CounterData)
    assert.Nil(t, nonperfTrace.CounterData)

    // Non-perf trace should have device resources
    assert.NotNil(t, nonperfTrace.DeviceResources)
    assert.Nil(t, perfTrace.DeviceResources)
}
```

### Test 2: Validate Common Files

```go
func TestCommonFilesIdentical(t *testing.T) {
    perf := "testdata/06-six-encoders-run1-perf.gputrace"
    nonperf := "testdata/06-six-encoders-run1.gputrace"

    // Capture files should be identical
    perfCapture, _ := os.ReadFile(filepath.Join(perf, "capture"))
    nonperfCapture, _ := os.ReadFile(filepath.Join(nonperf, "capture"))
    assert.Equal(t, perfCapture, nonperfCapture)
}
```

### Test 3: Feature Detection

```go
func TestTraceFeatureDetection(t *testing.T) {
    trace, _ := gputrace.Open("testdata/trace.gputrace")

    if trace.HasPerfCounters() {
        // Use performance metrics
        metrics := trace.ExtractShaderMetrics()
        assert.Greater(t, len(metrics.Shaders), 0)
    }

    if trace.HasDeviceResources() {
        // Use resource tracking
        resources := trace.DeviceResources
        assert.NotEmpty(t, resources.Resources)
    }
}
```

---

## API Design Recommendations

### Unified Trace Interface

```go
type Trace struct {
    Path      string
    Metadata  *Metadata

    // Common to both
    CaptureData []byte
    CommandBuffers []*CommandBuffer

    // Perf-specific (nil if non-perf)
    CounterData *PerfCounterData

    // Non-perf-specific (nil if perf)
    DeviceResources *DeviceResources
    UnusedResources *UnusedResources
}

// Feature detection
func (t *Trace) HasPerfCounters() bool {
    return t.CounterData != nil
}

func (t *Trace) HasDeviceResources() bool {
    return t.DeviceResources != nil
}

// Graceful degradation
func (t *Trace) ExtractShaderMetrics() *ShaderMetricsReport {
    if t.HasPerfCounters() {
        return extractWithRealMetrics(t)
    } else {
        return extractWithEstimates(t)
    }
}
```

---

## Future Work

1. **Complete Resource Parsing:**
   - Implement `device-resources-*` parser
   - Add resource tree visualization
   - Integrate with buffer tracking

2. **Store0 Investigation:**
   - Document decompressed content structure
   - Identify timing/state data (if any)
   - Add to API if useful

3. **Optimization Features:**
   - Parse `unused-device-resources-*`
   - Report unused allocations in `gputrace insights`
   - Suggest optimizations

4. **Format Documentation:**
   - Document MTSP record types completely
   - Create MTSP parsing library
   - Add examples for each record type

---

## Related Documentation

See also:
- [BINARY_FORMAT_REFERENCE.md](BINARY_FORMAT_REFERENCE.md) - Performance counter binary format
- [RECORD_FORMATS.md](./RECORD_FORMATS.md) - Main trace file formats
- [TRACE_FORMAT.md](./TRACE_FORMAT.md) - Capture file format

---

**Last Updated:** 2025-11-07
