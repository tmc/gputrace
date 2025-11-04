# Performance Counter Binary Format Analysis

**Bead:** gputrace-44 (Phase 1 Core Metrics Extraction)
**Date:** 2025-11-03
**Test Trace:** `/tmp/fast-llm-mlx-test-perf.gputrace`
**Reference CSV:** `/tmp/fast-llm-mlx-test Counters.csv`

## Overview

This document analyzes the binary format of `Counters_f_*.raw` files in `.gpuprofiler_raw` directories to enable extraction of GPU performance metrics.

## File Structure

### Directory Layout
```
trace.gputrace/
└── fast-llm-mlx-test.gputrace.gpuprofiler_raw/
    ├── Counters_f_0.raw   (3.2 MB)
    ├── Counters_f_1.raw   (3.2 MB)
    ├── ...
    └── Counters_f_90.raw  (3.2 MB)
```

### Record Structure

**Record Marker:** `0x4E 0x00 0x00 0x00` (uint32 = 78)

**Record Size:** 464 bytes (0x1D0) based on distance between markers

**Record Layout:**
```
Offset  Size  Description
------  ----  -----------
0x000   4     Record marker (0x4E000000)
0x004   460   Performance counter data
```

### Example Records

**First Record (Offset 0x2610):**
```
00002610  4e 00 00 00 00 00 00 00  00 00 00 00 00 00 00 00  |N...............|
00002620  00 00 00 00 00 00 00 00  00 00 dc 02 00 00 00 00  |................|
...
000027e0  4e 00 00 00 ...  (next record)
```

Distance: 0x27e0 - 0x2610 = 0x1D0 (464 bytes)

## CSV Correlation

### CSV Format
- **Columns:** 246 total
  - Column 1: Index
  - Column 2: Encoder FunctionIndex
  - Column 3: CommandBuffer Label
  - Column 4: Encoder Label
  - Column 5: Empty
  - Columns 6-246: 241 performance metrics

### Sample Data Row 1
```
Index: 1
Encoder FunctionIndex: 77
CommandBuffer: "Command Buffer 2 0xa74c48380"
Encoder: "Compute Encoder 0 0xa74c3c960"

Key Metrics:
- ALU Utilization: 0.98 (98%)
- Buffer Device Memory Bytes Read: 0.00
- Buffer Device Memory Bytes Written: 0.00
- Buffer L1 Miss Rate: 25.15%
- Device Memory Bandwidth: 16.44 GB/s
- GPU Read Bandwidth: 16.33 GB/s
- GPU Write Bandwidth: 0.11 GB/s
- Kernel Invocations: 1237392
- Kernel Occupancy: 0.30 (30%)
- L1 Read Bandwidth: 31.76 GB/s
```

### Sample Data Row 2
```
Index: 2
Encoder FunctionIndex: 115

Key Metrics:
- ALU Utilization: 0.10 (10%)
- Buffer Device Memory Bytes Read: 133120.00
- Buffer Device Memory Bytes Written: 0.00
- Buffer L1 Miss Rate: 50.76%
- Kernel Invocations: 87040
- Kernel Occupancy: 1.33 (133%??)  # Seems like percentage * 100
```

## Challenge: Field Mapping

### Problem
- 241 metrics → 464 bytes = ~1.93 bytes per metric
- Many metrics are floats (4 bytes) or uint64 (8 bytes)
- Suggests compression, bit packing, or sparse encoding

### Possible Encodings
1. **Float32 (4 bytes)**: Percentages, bandwidths
2. **Uint32 (4 bytes)**: Counts (invocations, bytes)
3. **Uint64 (8 bytes)**: Large counts, addresses
4. **Uint16 (2 bytes)**: Small counts, compressed values
5. **Uint8 (1 byte)**: Flags, small values

### Example Calculations

**Row 1: Kernel Invocations = 1237392**
- Hex: 0x12E310
- Binary search in record for: `10 E3 12 00` (little-endian uint32)

**Row 1: ALU Utilization = 0.98**
- As float32: `0x3F7AE148` = 0.98046875
- Binary search for: `48 E1 7A 3F`

## Current Implementation Status

### ✅ Completed
- Record boundary detection (0x4E markers)
- Record counting and iteration
- File parsing infrastructure
- Integration with perfcounters.go

### ⏳ In Progress
- Field offset identification
- Metric value extraction
- CSV correlation analysis

### ❌ Pending
- Complete field mapping (241 metrics)
- Data type determination per field
- Multi-architecture support (M1/M2/M3/M4)
- Validation against Xcode output

## Pragmatic Implementation Strategy

### Phase 1: Core Metrics (5-10 fields)

Target the most valuable metrics for ML/GPU optimization:

1. **ALU Utilization** (%)
   - CSV Column: 13 (0-indexed: 12)
   - Expected: Float (0.0-1.0 or 0-100)
   - Row 1 Value: 0.98

2. **Kernel Occupancy** (%)
   - CSV Column: 107
   - Expected: Float
   - Row 1 Value: 0.30

3. **Kernel Invocations** (count)
   - CSV Column: 106
   - Expected: Uint32 or Uint64
   - Row 1 Value: 1237392 (0x12E310)

4. **Buffer Device Memory Bytes Read**
   - CSV Column: 22
   - Expected: Uint64 or Float
   - Row 1 Value: 0.00
   - Row 2 Value: 133120.00

5. **Buffer Device Memory Bytes Written**
   - CSV Column: 23
   - Expected: Uint64 or Float
   - Row 1 Value: 0.00

6. **Buffer L1 Miss Rate** (%)
   - CSV Column: 24
   - Expected: Float
   - Row 1 Value: 25.15

7. **Device Memory Bandwidth** (GB/s)
   - CSV Column: 52
   - Expected: Float
   - Row 1 Value: 16.44

8. **GPU Read Bandwidth** (GB/s)
   - CSV Column: 88
   - Expected: Float
   - Row 1 Value: 16.33

9. **GPU Write Bandwidth** (GB/s)
   - CSV Column: 89
   - Expected: Float
   - Row 1 Value: 0.11

10. **L1 Read Bandwidth** (GB/s)
    - CSV Column: 117
    - Expected: Float
    - Row 1 Value: 31.76

### Methodology

1. **Hexdump Search**
   ```bash
   # Search for Kernel Invocations (1237392 = 0x12E310)
   hexdump -C Counters_f_0.raw | grep "10 e3 12"

   # Search for ALU Utilization (0.98 as float = 0x3F7AE148)
   hexdump -C Counters_f_0.raw | grep "48 e1 7a 3f"
   ```

2. **Pattern Analysis**
   - Compare multiple records
   - Look for consistent field offsets
   - Validate with known values from CSV

3. **Incremental Extraction**
   - Start with 1 field (Kernel Invocations - easy to identify)
   - Validate against CSV
   - Add fields one at a time
   - Build confidence in offset stability

### Phase 2: Extended Metrics (15-20 fields)

Add shader-stage-specific metrics:
- FS/VS/Compute specific counters
- Memory hierarchy metrics (L1, LLC)
- Instruction counts (Float, Half, Integer)

### Phase 3: Complete Format (All 241 fields)

Full format specification with:
- Complete field map
- Architecture-specific variations
- Validation test suite

## Tools and Scripts

### Hexdump Analysis Script
```bash
#!/bin/bash
# analyze_counters.sh - Correlate CSV with binary

CSV="$1"
RAW="$2"

# Extract value from CSV (e.g., Kernel Invocations from row 1)
VALUE=$(awk -F',' 'NR==2 {print $107}' "$CSV")
echo "Looking for Kernel Invocations: $VALUE"

# Convert to hex and search
HEX=$(printf "%08x" "$VALUE" | sed 's/\(..\)\(..\)\(..\)\(..\)/\4 \3 \2 \1/')
echo "Hex pattern (little-endian): $HEX"

hexdump -C "$RAW" | grep -i "$HEX"
```

### Record Extractor
```python
#!/usr/bin/env python3
# extract_records.py - Extract all records from .raw file

import sys
import struct

def extract_records(filename):
    with open(filename, 'rb') as f:
        data = f.read()

    records = []
    marker = b'\x4e\x00\x00\x00'
    pos = 0

    while True:
        idx = data.find(marker, pos)
        if idx == -1:
            break

        # Record is 464 bytes
        if idx + 464 <= len(data):
            record = data[idx:idx+464]
            records.append((idx, record))

        pos = idx + 4

    return records

if __name__ == '__main__':
    records = extract_records(sys.argv[1])
    print(f"Found {len(records)} records")

    # Dump first record for analysis
    if records:
        offset, data = records[0]
        print(f"\nFirst record at offset 0x{offset:08x}:")
        for i in range(0, len(data), 16):
            hex_str = ' '.join(f'{b:02x}' for b in data[i:i+16])
            ascii_str = ''.join(chr(b) if 32 <= b < 127 else '.' for b in data[i:i+16])
            print(f"  {i:04x}  {hex_str:<48}  {ascii_str}")
```

## Next Steps

1. **Run hexdump analysis** on known values
2. **Identify offsets** for Phase 1 metrics (5-10 fields)
3. **Update perfcounters.go** with field extraction
4. **Add tests** comparing extracted values with CSV
5. **Document** field offsets and data types
6. **Iterate** to Phase 2 and Phase 3

## References

- [COUNTERS_CSV_FORMAT.md](./COUNTERS_CSV_FORMAT.md) - CSV structure analysis
- [GPU_PROFILING_APIS_DISCOVERED.md](../GPU_PROFILING_APIS_DISCOVERED.md) - APS/AGX APIs
- perfcounters.go:194-215 - parseCounterRecord() function

## Status

**Current State:** Binary format analysis in progress

**Blocker:** Need to identify field offsets through hexdump correlation

**Next Action:** Run analysis scripts to find Kernel Invocations field offset

**Estimated Time:** 2-3 days for Phase 1 (5-10 metrics)

## Critical Discovery: Aggregation Required

**Date:** 2025-11-03 (continued analysis)

### Finding

The relationship between binary records and CSV rows is **NOT 1:1**:

- **Binary Data**: 1,598 records in Counters_f_0.raw alone (40 files total)
- **CSV Output**: 10 data rows
- **Implication**: Instruments **aggregates** thousands of per-sample records into summary statistics

### Record Structure Variation

Distances between 0x4E markers vary:
```
Position   Distance   Notes
0xf85      2898      Large gap (header/metadata?)
0x1ad7     464       Standard record
0x1ca7     2409      Large gap
0x2610     464       Standard record
0x27e0     464       Standard record
```

**Hypothesis**: Two record types:
1. **Metadata records** (~2400-2900 bytes) - Frame/encoder context
2. **Sample records** (464 bytes) - Individual counter samples

### Aggregation Logic

To match Xcode's CSV output, we need to:

1. **Group records** by encoder/command buffer
2. **Aggregate samples** within each group:
   - Sum counts (Kernel Invocations)
   - Average rates (ALU Utilization, Occupancy)
   - Sum bandwidth (Memory Bandwidth)
3. **Export** one CSV row per encoder

### Why Values Weren't Found

The hexdump search failed because:
- Binary contains **raw samples** (e.g., ALU util per sample: 0.01, 0.02, ...)
- CSV contains **aggregated values** (e.g., average ALU util: 0.98)
- Need to sum/average hundreds of samples to get CSV values

### Implementation Complexity Increase

**Original estimate**: 2-3 days for field extraction
**Revised estimate**: 5-7 days including:
- Record type identification
- Grouping logic by encoder
- Aggregation functions per metric type
- Validation against CSV

### Next Steps

1. Identify metadata vs sample records
2. Find encoder ID field in records
3. Implement grouping by encoder
4. Implement aggregation for each metric type
5. Validate sums/averages match CSV

This significantly increases the complexity of Phase 1 implementation.
