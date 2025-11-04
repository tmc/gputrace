# Binary Counter Format Analysis

**Bead:** gputrace-44
**Date:** 2025-11-03
**Status:** In Progress - Field Offset Discovery

## Overview

Analysis of `.gpuprofiler_raw/Counters_f_*.raw` binary format to extract performance counter metrics.

## File Structure

### Reference Trace
- **File:** `/tmp/fast-llm-mlx-test-perf.gputrace/fast-llm-mlx-test.gputrace.gpuprofiler_raw/Counters_f_0.raw`
- **Size:** 3,387,392 bytes (3.2 MB)
- **Records:** 1,598 total with 0x4E marker

### Record Types by Size

| Size (bytes) | Count | Percentage | Type |
|--------------|-------|------------|------|
| 464 | 568 | 35.5% | Sample records (primary) |
| 523 | 179 | 11.2% | Extended samples |
| 516 | 45 | 2.8% | Extended samples |
| 2,898 | 1 | 0.1% | Metadata record (first) |
| Other | 805 | 50.4% | Various sizes |

**Key Finding:** 464-byte records are the most common and likely contain the core performance counter samples.

## 464-Byte Record Structure

### Consistent Fields (Metadata)

These fields have identical values across all sampled records (first 100):

| Offset | Type | Value | Interpretation |
|--------|------|-------|----------------|
| +0 | uint32 | 78 (0x4E) | Record marker |
| +24 | uint32 | 47,972,352 | Context/session ID? |
| +72 | uint32 | 1,400,897,536 | Large constant |
| +96 | uint64 | 122,045,790,683,136 | Very large constant |
| +100 | uint32 | 28,416 | Small constant |
| +148 | uint32 | 124,780,544 | Medium constant |
| +160 | uint32 | 8,257,536 | Medium constant |
| +200 | uint64 | 25,635,586,048 | Large constant |
| +256 | uint64 | 12,040 | Small constant |
| +264 | uint64 | 93,952,409,600 | Large constant |
| +304 | uint64 | 1 | Counter/flag |
| +352 | uint64 | 19 | Small constant |
| +376 | uint64 | 3,678,208 | Medium constant |
| +416 | uint64 | 3 | Counter/flag |

**Note:** "Constant" fields that never change are likely:
- Pipeline state addresses
- Shader configuration
- Execution context identifiers
- Hardware configuration

### Varying Fields (Counter Data)

These fields change between records and likely contain actual performance counter samples:

| Offset | Type | Variation | Likely Content |
|--------|------|-----------|----------------|
| +240 | uint64 | High variance | Timestamp or cycle counter |
| +248 | uint64 | High variance | Timestamp or cycle counter |

**Additional varying fields need identification through deeper analysis.**

## CSV Correlation Targets

From `/tmp/fast-llm-mlx-test Counters.csv` Row 1:

| Metric | CSV Value | Column | Format |
|--------|-----------|--------|--------|
| ALU Utilization | 0.98 (98%) | 14 | float32 0.0-1.0 |
| Kernel Occupancy | 0.30 (30%) | 109 | float32 0.0-1.0 |
| Buffer L1 Miss Rate | 25.15% | 25 | float32 percentage |
| Kernel Invocations | 1024.00 | 108 | float32/uint32 |

**Critical Finding:** These values are NOT stored directly in individual records. They are **aggregated** from ~5,244 binary records into 10 CSV rows.

## Aggregation Strategy

### Required Aggregation Types

1. **Summation** (Kernel Invocations, Bytes Read/Written)
   - Sum counter values across all records for an encoder

2. **Averaging** (ALU Utilization, Kernel Occupancy)
   - Average percentage/utilization values across samples

3. **Rate Calculation** (Cache Miss Rates)
   - Calculate: (misses / total_accesses) × 100

4. **Bandwidth Calculation** (Memory Bandwidth)
   - Calculate: total_bytes / total_time

### Encoder Grouping

Records must be grouped by:
- Command Buffer (identified by address/label)
- Encoder (identified by address/type)
- Shader/Pipeline State

**Unknown:** How to identify which encoder a 464-byte record belongs to.

## Implementation Status

### ✅ Completed
- Record boundary detection (0x4E markers)
- Record size distribution analysis
- Identified dominant 464-byte record type
- Found consistent metadata fields
- Found varying counter data fields
- Established aggregation requirements

### ❌ Pending
- **Field offset identification** for specific metrics
- **Encoder identification** within records
- **Counter type identification** (which field = which metric)
- **Aggregation implementation** per metric type
- **Validation** against CSV reference

## Next Steps

### Phase 1A: Field Offset Discovery (1-2 days)

1. **Identify timestamp/cycle fields**
   - Offsets +240, +248 look promising
   - Verify monotonic increase within encoder

2. **Find encoder identification fields**
   - Check for encoder addresses in metadata fields
   - Correlate with known encoder addresses from trace

3. **Locate core counter fields**
   - Search for cumulative counters (invocations, bytes)
   - Search for percentage/ratio fields (utilization, occupancy)

### Phase 1B: Aggregation Logic (1-2 days)

1. **Group records by encoder**
   - Implement encoder identification
   - Create per-encoder buckets

2. **Implement metric aggregation**
   - Sum: Kernel Invocations, bytes transferred
   - Average: ALU Utilization, Kernel Occupancy
   - Calculate: Cache miss rates, bandwidth

3. **Generate CSV output**
   - Match Xcode column order (246 columns)
   - Validate against reference CSV

### Phase 1C: Validation (0.5-1 day)

1. **Unit tests** for field extraction
2. **Integration tests** for aggregation
3. **Validation tests** against reference CSV
4. **Document** field offsets and meanings

## Tools Developed

- `analyze_counters.py` - Initial float32 search (unsuccessful - values are aggregated)
- `analyze_record_types.py` - Record size distribution analysis
- `deep_analysis.py` - 464-byte record structure analysis

## References

- `docs/REFERENCE_TRACE.md` - Reference trace documentation
- `docs/COUNTERS_CSV_FORMAT.md` - Target CSV format
- `/tmp/fast-llm-mlx-test Counters.csv` - Validation target

## Estimated Effort Remaining

- **Field discovery:** 1-2 days
- **Aggregation implementation:** 1-2 days
- **Validation:** 0.5-1 day
- **Total:** 2.5-5 days

**Status:** Foundation established, ready for detailed field-by-field reverse engineering.
