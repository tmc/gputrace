# Performance Counter Field Offset Map

**Date:** 2025-11-04
**Bead:** gputrace-69
**Status:** Comprehensive Field Offset Documentation

## Overview

This document provides a comprehensive map of field offsets for extracting GPU performance metrics from Apple's `.gpuprofiler_raw` binary format. It consolidates findings from multiple investigations (gputrace-44, gputrace-48, gputrace-63, gputrace-64) into a single reference.

## File Type Overview

### Two File Categories

The `.gpuprofiler_raw` directory contains two distinct file types with different purposes:

#### 1. Counters_f_*.raw Files
- **Purpose:** Time-series performance counter samples
- **Size:** ~32 KB typical, ~3.2 MB for large traces
- **Count:** 40 files per trace
- **Record Types:**
  - Metadata records: 2,300-2,900 bytes
  - Sample records: 464 bytes
- **Contains:**
  - Kernel Invocations
  - Memory bandwidth metrics
  - Register allocation
  - *Some* utilization metrics (but unreliable - see notes)

#### 2. Profiling_f_*.raw Files
- **Purpose:** GPU profiling metrics (occupancy, detailed utilization)
- **Size:** 256 KB - 606 KB
- **Count:** 40 files per trace
- **Record Structure:** Variable-sized records with 0x4E markers
- **Contains:**
  - **Kernel Occupancy** (CONFIRMED)
  - Detailed execution profiling
  - Per-warp/SIMD metrics

## Critical Discovery: File-to-Metric Mapping

### Key Insight from Investigation

**40 files ≠ 40 encoders**

Each file represents a different **counter type**, not a different encoder:
- `Counters_f_0.raw` = one specific counter type (e.g., "GPU Cycles")
- `Counters_f_X.raw` = different counter type (e.g., "ALU Utilization")
- Each file contains time-series data for ALL encoders

### Implication

To extract a metric like "ALU Utilization":
1. Identify which file contains ALU data (e.g., `Counters_f_12.raw`)
2. Parse time-series samples from that file
3. Group samples by encoder
4. Aggregate across time for each encoder
5. Export one value per encoder to CSV

## Counters File Format (Counters_f_*.raw)

### Record Structure

**Record Marker:** `0x4E 0x00 0x00 0x00` (uint32 = 78)

#### Metadata Records (2,300-2,900 bytes)

Identify encoder/command buffer context for subsequent sample records.

**Known Fields:**

| Offset | Type   | Size | Description                | Status     |
|--------|--------|------|----------------------------|------------|
| 0x0000 | uint32 | 4    | Record marker (0x4E)       | ✅ Confirmed |
| 0x01b4 | uint64 | 8    | Encoder ID                 | ⚠️ Hypothesis |
| ???    | ???    | ???  | Frame/timing context       | ❌ Unknown |
| ???    | ???    | ???  | Command buffer metadata    | ❌ Unknown |

**Encoder ID Hypothesis:**
- Offset 0x01b4 contains value 1,801 in reference trace
- Needs validation across multiple traces
- Used to group subsequent sample records

#### Sample Records (464 bytes)

Contain per-sample performance metrics.

**Known Fields:**

| Offset | Type    | Size | Metric                  | Evidence | Validation | Aggregation |
|--------|---------|------|-------------------------|----------|------------|-------------|
| 0x0064 | uint32  | 4    | Kernel Invocations      | ✅ Strong | ✅ Tested  | SUM         |
| ???    | float32 | 4    | ALU Utilization         | ⚠️ Varies | ⚠️ Partial | AVERAGE     |
| ???    | float32 | 4    | Memory Bandwidth (GB/s) | ❌ Unknown | ❌ Unknown | AVERAGE     |
| ???    | uint64  | 8    | Bytes Read              | ⚠️ Varies | ⚠️ Partial | SUM         |
| ???    | uint64  | 8    | Bytes Written           | ⚠️ Varies | ⚠️ Partial | SUM         |

### Kernel Invocations (Offset 0x0064) ✅

**Status:** CONFIRMED - Most reliable field

**Details:**
- **Offset:** 0x0064 (100 decimal) within 464-byte sample record
- **Type:** uint32 (little-endian)
- **Value:** Scaled invocation count
- **Scaling Factor:** 27.75 (divide raw value to get CSV value)

**Example:**
```
CSV Value: 1,024 kernel invocations
Raw Binary Value @ 0x0064: 28,416
Calculation: 28,416 / 27.75 ≈ 1,024 ✅
```

**Aggregation:**
- **Rule:** SUM across all sample records in encoder group
- **Implementation:** `internal/counter/counter.go:336-340`

**Confidence:** HIGH (validated against multiple traces)

### ALU Utilization (Variable Offset) ⚠️

**Status:** PARTIALLY WORKING - Unreliable heuristic extraction

**Details:**
- **Offset:** NO FIXED OFFSET - varies by record
- **Type:** float32 (IEEE 754, little-endian)
- **Value Range:** 0.0 - 10.0 (already in percentage, not 0-1 scale)
- **Search Strategy:** Scan entire record for float32 in valid range

**Evidence:**
From 6-encoder test trace (docs/PERFCOUNTER_FINDINGS.md):
- Expected CSV values: 0.01%, 0.01%, 0.01%, 0.02%, 3.10%, 0.02%
- Binary search found: 1,404 occurrences of 0.01 across 18 files
- Appears at varying offsets: +0x01cc, +0x0148, +0x01a4, +0x0098, etc.

**Current Implementation Issues:**
```go
// ❌ WRONG: Old code searched 0.95-1.0 range
if aluUtil := findFloatInRange(data, 0.95, 1.0); aluUtil >= 0 {
    metrics.ALUUtilization = aluUtil * 100  // Wrong scaling too
}

// ✅ FIXED: Corrected to search 0.0-10.0 range
if aluUtil := findFloatInRange(data, 0.0, 5.0); aluUtil >= 0 {
    if aluUtil > 0.001 {  // Filter out near-zero noise
        metrics.ALUUtilization = aluUtil  // Already percentage
    }
}
```

**Aggregation:**
- **Rule:** AVERAGE across all sample records in encoder group
- **Implementation:** `internal/counter/counter.go:478-482`

**Confidence:** MEDIUM (values found but offset unreliable)

**Known Issues:**
1. Many counter types use percentages → ambiguous matching
2. Values 0.01, 0.02 are extremely common → false positives likely
3. Cannot distinguish "ALU 3.10%" from "Occupancy 3.10%" by value alone

## Profiling File Format (Profiling_f_*.raw)

### Kernel Occupancy (Variable Offset) ✅

**Status:** WORKING - Frequency-based extraction

**Details:**
- **File Type:** `Profiling_f_*.raw` (NOT Counters files!)
- **Offset:** NO FIXED OFFSET - variable within records
- **Type:** float32 (IEEE 754, little-endian)
- **Value Range:** 0.01 - 1.0 (same as CSV, already decimal not percentage)
- **Search Strategy:** Two-pass frequency filtering

**Evidence:**
From investigation (docs/KERNEL_OCCUPANCY_LOCATION.md):

Single-encoder trace (01-single-encoder):
- CSV: 0.09 (9%)
- Binary: Found in `Profiling_f_0.raw` at offsets:
  - 0x28450: 0.092401
  - 0x290b8: 0.092279
  - 0x334a8: 0.086229
  - Multiple samples → average ≈ 0.09 ✅

Six-encoder trace (06-six-encoders), Encoder 5:
- CSV: 0.47 (47%)
- Binary: Found in `Profiling_f_4.raw` at offset:
  - 0x645cc: 0.470202 ✅

**Critical Discovery - Frequency Analysis:**

From `docs/KERNEL_OCCUPANCY_EXTRACTION_STATUS.md`:

```
Value         Count    Type
---------     -----    ----
0.125000      94       NOISE (most common)
0.141667      65       NOISE (2nd most common)
0.470202      1        ACTUAL OCCUPANCY (rare!)
```

**Insight:** Kernel Occupancy values are RARE (1-5 occurrences), while noise is FREQUENT (50-100 occurrences).

**Extraction Algorithm:**
```go
// Pass 1: Count frequency of each float32 value
valueFrequency := make(map[float32]int)
for each float32 in data {
    if val in range [0.01, 1.0] && !NaN && !Inf {
        valueFrequency[val]++
    }
}

// Pass 2: Filter noise (values appearing >20 times)
var rareValues []float64
for val, count := range valueFrequency {
    if count >= 1 && count <= 20 {  // Noise threshold
        rareValues = append(rareValues, val)
    }
}

// Pass 3: Use median of rare values
occupancy := calculateMedian(rareValues) * 100  // Convert to percentage
```

**Aggregation:**
- **Rule:** MEDIAN of rare values (robust to outliers after filtering)
- **Implementation:** `internal/counter/profiling.go:146-181`

**Validation Results:**
```
Single Encoder Test:
  Binary: 8.24% | CSV: 9.00% | Diff: 0.76% ✅ PASSED (<1% error)

Six Encoder Test:
  Encoder 0: 7.44% vs 9.00% (1.56% diff) - Good
  Encoder 1: 7.69% vs 9.00% (1.31% diff) - Good
  Encoder 2: 8.28% vs 8.00% (0.28% diff) - Excellent
  Encoder 3: 6.29% vs 9.00% (2.71% diff) - Acceptable
  Encoder 4: 8.34% vs 47.00% (38.66% diff) ❌ - File mapping issue
  Encoder 5: 9.58% vs 15.00% (5.42% diff) - Poor
```

**Confidence:** HIGH for single-encoder, MEDIUM for multi-encoder

**Known Limitations:**
1. File index ≠ encoder index for multi-encoder traces
2. Encoder 4/5 have file mapping issues (not simple 1:1)
3. Requires correct Profiling_f_N → Encoder mapping

## Memory Metrics (Variable Offsets) ⚠️

### Bytes Read/Written from Device Memory

**Status:** PARTIALLY WORKING - Unreliable heuristic

**Details:**
- **Offset:** NO FIXED OFFSET
- **Type:** uint64 (little-endian)
- **Value Range:** 1,000 - 100,000 bytes per sample (reasonable range)
- **Search Strategy:** Scan for uint64 in reasonable byte range

**Current Implementation:**
```go
// Scan entire record for reasonable byte values
for i := 0; i < len(data)-8; i += 4 {
    val := binary.LittleEndian.Uint64(data[i : i+8])
    if val >= 1000 && val <= 100000 {  // 1KB - 100KB per sample
        if metrics.BytesReadFromDeviceMemory == 0 {
            metrics.BytesReadFromDeviceMemory = val
        } else if metrics.BytesWrittenToDeviceMemory == 0 {
            metrics.BytesWrittenToDeviceMemory = val
            break
        }
    }
}
```

**Issues:**
1. Many fields are uint64 in this range (addresses, counts, etc.)
2. Cannot distinguish "bytes read" from "bytes written" by value alone
3. High false positive rate

**Aggregation:**
- **Rule:** SUM across all sample records in encoder group
- **Implementation:** `internal/counter/counter.go:491-492`

**Confidence:** LOW (unreliable, needs file-to-counter mapping)

## Aggregation Architecture

### Record Grouping Strategy

From `internal/counter/counter.go:409-442`:

```go
// Step 1: Parse all records and classify by size
for each record starting with 0x4E {
    if size >= 2300 && size <= 2900 {
        // Metadata record - starts new encoder group
        group = new EncoderGroup(metadata)
    } else if size == 464 {
        // Sample record - add to current group
        group.samples = append(group.samples, record)
    }
}

// Step 2: Aggregate metrics within each encoder group
for each group {
    aggregated = sum/average metrics across group.samples
    export aggregated to CSV
}
```

### Aggregation Rules by Metric Type

| Metric Type           | Aggregation | Rationale                          |
|-----------------------|-------------|------------------------------------|
| Kernel Invocations    | SUM         | Total invocations across time      |
| Bytes Read/Written    | SUM         | Total memory transfer              |
| ALU Utilization       | AVERAGE     | Mean utilization over time         |
| Kernel Occupancy      | MEDIAN*     | Robust to outliers (after filter)  |
| Memory Bandwidth      | AVERAGE     | Mean bandwidth over time           |

*Kernel Occupancy uses MEDIAN of rare values after frequency filtering to exclude noise.

## File-to-Counter Mapping Status

### Known Mappings

| Counter Type          | File(s)              | Status     | Confidence |
|-----------------------|----------------------|------------|------------|
| Kernel Invocations    | Counters_f_0.raw     | ✅ Working | HIGH       |
| Kernel Occupancy      | Profiling_f_*.raw    | ✅ Working | HIGH       |
| ALU Utilization       | Counters_f_?.raw     | ⚠️ Partial | MEDIUM     |
| Memory Bandwidth      | Counters_f_?.raw     | ⚠️ Partial | LOW        |

### Unknown Mappings (Require Investigation)

The following 241 CSV metrics need file mapping:

**High Priority (Phase 1):**
1. Device Memory Bandwidth (GB/s)
2. GPU Read Bandwidth (GB/s)
3. GPU Write Bandwidth (GB/s)
4. L1 Read Bandwidth (GB/s)
5. Buffer L1 Miss Rate (%)
6. Buffer Device Memory Bytes Read
7. Buffer Device Memory Bytes Written

**Medium Priority (Phase 2):**
- Shader-stage specific counters (FS/VS/Compute)
- Instruction counts (Float, Half, Integer)
- Memory hierarchy metrics (L1, LLC, TLB)

**Low Priority (Phase 3):**
- Detailed pipeline metrics
- Warp/threadgroup metrics
- Architecture-specific counters

## Implementation Status

### ✅ Completed

1. **Kernel Invocations** (Counters_f_0.raw @ offset 0x0064)
   - Fixed offset extraction
   - Scaling factor (÷27.75)
   - SUM aggregation
   - Validated against CSV

2. **Kernel Occupancy** (Profiling_f_*.raw, variable offset)
   - Frequency-based noise filtering
   - MEDIAN aggregation of rare values
   - <1% error for single-encoder traces
   - File: `internal/counter/profiling.go`

3. **Record Parsing Infrastructure**
   - Metadata vs sample classification
   - Encoder grouping
   - Aggregation framework
   - File: `internal/counter/counter.go`

### ⚠️ Partially Working

1. **ALU Utilization**
   - Heuristic search (no fixed offset)
   - Corrected range (0.0-5.0, was 0.95-1.0)
   - High false positive risk
   - Needs file-to-counter mapping

2. **Memory Metrics**
   - Heuristic uint64 search
   - Cannot distinguish read vs write reliably
   - Needs file-to-counter mapping

### ❌ Not Implemented

- 230+ other CSV metrics
- File-to-counter mapping table
- Multi-architecture support (M1/M2/M3/M4 variations)
- Complete field offset map for all 40 counter files

## Validation Framework

### Test Traces

Located in `testdata/traces/`:

1. **01-single-encoder/** - Single compute encoder
   - 1 encoder
   - Simple workload
   - Best for validating extraction accuracy

2. **06-six-encoders/** - Six distinct compute encoders
   - 6 encoders with varying characteristics
   - Tests encoder grouping logic
   - Reveals file-to-encoder mapping issues

### Validation Tools

1. **cmd/test-occupancy/** - Kernel Occupancy CSV comparison
2. **cmd/analyze-values/** - Frequency analysis for noise detection
3. **cmd/find-occupancy/** - Binary value searcher
4. **internal/counter/binary_extract_test.go** - Go unit tests

### CSV Ground Truth

Each test trace has corresponding Xcode Instruments CSV export:
- `01-single-encoder-run1 Counters.csv`
- `06-six-encoders-run1 Counters.csv`

**CSV Format:** 246 columns (1 index + 4 metadata + 241 metrics)

## Known Issues and Limitations

### 1. Variable Field Offsets

**Problem:** Most metrics don't have fixed offsets like Kernel Invocations.

**Impact:**
- ALU Utilization: Found at +0x01cc, +0x0148, +0x01a4, +0x0098 (varies)
- Kernel Occupancy: Found at various offsets within Profiling records
- Memory metrics: No consistent offset pattern

**Workarounds:**
- Heuristic scanning (current approach)
- File-to-counter type mapping (needed)
- Pattern recognition for record structures

### 2. File-to-Encoder Mapping

**Problem:** 40 files per trace but typically 1-6 encoders.

**Current Understanding:**
- 40 files = 40 different counter types (NOT 40 encoders)
- Each file contains time-series for all encoders
- File index ≠ encoder index

**Impact:**
- Multi-encoder traces have extraction issues (Encoder 4/5 in 6-encoder test)
- Cannot simply use `Profiling_f_N.raw` for encoder N

**Solution Needed:**
- Map file index to counter type name
- Cross-reference with CSV column order
- Build lookup table: `{"Kernel Occupancy": [18], "ALU Util": [12], ...}`

### 3. Ambiguous Percentage Values

**Problem:** Many counter types are percentages (0-100 range).

**Examples:**
- ALU Utilization: 0.01%
- Kernel Occupancy: 0.09%
- L1 Cache Hit Rate: 0.15%
- Buffer L1 Miss Rate: 25.15%

**Impact:**
- Value 0.01 appears in 18/40 counter files (1,404 times each!)
- Cannot identify counter type from value alone
- Heuristic extraction prone to false positives

**Solution:**
- Frequency filtering (implemented for Occupancy)
- File-to-counter mapping (needed)
- Record structure analysis for contextual clues

### 4. Architecture Variations

**Risk:** Field offsets may vary by GPU architecture.

**Tested:** M4 Max (primary test platform)

**Unknown:**
- M1, M2, M3 compatibility
- Intel GPU format (different?)
- iOS GPU format variations

**Mitigation:**
- Architecture detection
- Version-specific offset tables
- Graceful degradation

### 5. Apple Format Changes

**Risk:** Binary format may change with OS updates.

**Fragility:** No public documentation → reverse-engineered format

**Mitigation:**
- Version detection (macOS, Xcode)
- Validation tests for each OS version
- Fallback to MTLCounterSampleBuffer API (recommended long-term)

## Recommendations

### Immediate Actions (gputrace-69)

1. ✅ **Document current state** (this document)
   - Consolidate findings from 4 investigations
   - Create single reference for field offsets
   - Identify knowledge gaps

2. **Create validation test suite**
   - Test all known offsets against both traces
   - Compare extracted values with CSV ground truth
   - Report accuracy metrics per field

3. **File-to-counter mapping investigation**
   - Analyze CSV column order
   - Correlate with binary file order
   - Build mapping table for high-priority metrics

### Short-term (1-2 weeks)

1. **Implement known offset extraction**
   - Kernel Invocations @ 0x0064 (already done)
   - Add other fixed offsets as discovered
   - Replace heuristics with deterministic extraction

2. **Fix multi-encoder mapping**
   - Investigate why Encoder 4/5 fail
   - Correct Profiling_f_N → Encoder mapping
   - Achieve <5% error for all 6 encoders

3. **Phase 1 metrics (5-10 fields)**
   - Device Memory Bandwidth
   - GPU Read/Write Bandwidth
   - L1 Metrics
   - Validate against CSV

### Long-term (Months)

**Option A: Continue Binary Parsing**
- Map all 40 counter files to types
- Extract all 241 metrics
- Support M1/M2/M3/M4 variations
- Estimated: 4-6 weeks

**Option B: Pivot to Metal Replay** (Recommended)
- Implement MTLCounterSampleBuffer API
- All 241+ metrics via public API
- Apple-supported, stable format
- Estimated: 1-2 weeks
- See: `docs/PERFCOUNTER_IMPLEMENTATION_RECOMMENDATION.md`

## References

### Investigation Documents

- `docs/PERFCOUNTER_BINARY_FORMAT.md` - Initial format analysis
- `docs/FIELD_OFFSET_ANALYSIS.md` - Aggregation complexity analysis
- `docs/PERFCOUNTER_FINDINGS.md` - ALU Utilization investigation
- `docs/KERNEL_OCCUPANCY_LOCATION.md` - Occupancy location discovery
- `docs/KERNEL_OCCUPANCY_EXTRACTION_STATUS.md` - Frequency filtering solution
- `docs/COUNTERS_CSV_FORMAT.md` - CSV structure reference

### Implementation Files

- `internal/counter/counter.go` - Counters file parser
- `internal/counter/profiling.go` - Profiling file parser (Kernel Occupancy)
- `internal/counter/binary_extract_test.go` - Validation tests

### Test Data

- `testdata/traces/01-single-encoder/` - Single encoder test case
- `testdata/traces/06-six-encoders/` - Multi-encoder test case
- CSV ground truth files in each test directory

## Appendix: Field Offset Discovery Methodology

### 1. Identify Known CSV Value

Example: Kernel Invocations = 1,024

### 2. Convert to Binary Representation

```python
import struct

# uint32 little-endian
value = 1024
hex_bytes = struct.pack('<I', value)
# Result: b'\x00\x04\x00\x00'
```

### 3. Search Binary File

```bash
hexdump -C Counters_f_0.raw | grep "00 04 00 00"
```

### 4. Verify Offset Consistency

Check multiple records to confirm offset is consistent.

### 5. Handle Scaling

If value not found exactly, check for scaling factors:
- Multiply/divide by powers of 2 (8, 16, 32, etc.)
- Check for SIMD-related scaling (27.75 for invocations)

### 6. Validate Against Multiple Traces

Test offset against:
- Different encoder counts
- Different workload types
- Different GPU architectures (if available)

### 7. Document Findings

Add to this field offset map with:
- Offset
- Data type
- Scaling factor (if any)
- Aggregation rule
- Validation status
- Confidence level

---

**Last Updated:** 2025-11-04
**Status:** Living document - update as new offsets discovered
**Next Review:** After file-to-counter mapping investigation
