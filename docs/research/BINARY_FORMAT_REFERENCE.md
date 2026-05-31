# GPU Performance Counter Binary Format Reference

**Date:** 2025-11-07
**Status:** Comprehensive documentation of binary format discoveries

## Table of Contents
1. [Overview](#overview)
2. [464-Byte Sample Record Structure](#464-byte-sample-record-structure)
3. [Field Offset Map](#field-offset-map)
4. [Aggregation Algorithms](#aggregation-algorithms)
5. [Counter File Mapping](#counter-file-mapping)
6. [CSV-First Validation Architecture](#csv-first-validation-architecture)
7. [Implementation Examples](#implementation-examples)

---

## Overview

GPU performance counter data is stored in `.gpuprofiler_raw/` directories within `.gputrace` bundles. The format consists of 40 numbered counter files containing binary performance metrics.

### Key Characteristics

- **Record Types:**
  - **Sample Records:** 464 bytes (87 records per capture, 33.2%)
  - **Metadata Records:** 2,300-2,900 bytes (identify encoder context, 66.8%)
- **Total Metrics:** 241 CSV columns
- **Non-Zero Metrics:** 58 columns with actual data
- **Endianness:** Little-endian (x86_64/ARM64)

### File Organization

```text
trace.gputrace/
└── .gpuprofiler_raw/
    ├── 0  - Counter file (varies by metric)
    ├── 1  - Counter file
    ├── 2  - Counter file
    ...
    └── 39 - Counter file
```

Each file contains a sequence of records:
```text
[Header: 4 bytes]
[Record 1: 464 or 2300+ bytes]
[Record 2: 464 or 2300+ bytes]
...
```

---

## 464-Byte Sample Record Structure

Sample records contain performance metrics for a single encoder/kernel execution instance.

### Record Layout

```text
Offset    Size    Type      Field                     Notes
------    ----    ----      -----                     -----
0x0000    4       uint32    Record Marker             Always 0x0000004e
0x0004    4       uint32    Record Payload Type       Varies by metric/file
0x0008    56      -         Unknown header
0x0040    4       float32   Metric candidate 1        Range-based search
0x0044    4       float32   Metric candidate 2
...
0x0064    4       uint32    Kernel Invocations        Raw value ÷ 27.75
0x0068    4       -         Padding/unknown
...
0x00XX    4       float32   ALU Utilization          Range: 0.0-5.0%
0x00YY    4       float32   Kernel Occupancy         Range: 0.0-2.0
...
0x01D0    -       -         End of record
```

### Known Field Offsets

| Offset | Size | Type    | Metric Name            | Extraction Method        | Value Range       |
|--------|------|---------|------------------------|--------------------------|-------------------|
| 0x0064 | 4    | uint32  | Kernel Invocations     | `value ÷ 27.75`         | 1-1024 (typical)  |
| TBD    | 4    | float32 | ALU Utilization        | Range search 0.0-5.0    | 0.0-5.0%          |
| TBD    | 4    | float32 | Kernel Occupancy       | Range search 0.0-2.0    | 0.0-2.0 (0-200%)  |
| TBD    | 8    | uint64  | Buffer Read Bytes      | Range search 1K-100K    | Varies            |
| TBD    | 8    | uint64  | Buffer Write Bytes     | Range search 1K-100K    | Varies            |

**Note:** "TBD" offsets are discovered via range-based search because exact positions vary or are not yet confirmed.

### Scaling Factors

Some metrics use non-obvious scaling:

1. **Kernel Invocations (`0x0064`):**
   ```
   Actual Value = Raw uint32 Value ÷ 27.75
   ```
   *Hypothesis:* 27.75 may relate to SIMD width (32) or GPU scheduling units

2. **ALU Performance (CSV export):**
   ```
   Percentage = (Instruction Count / Some Base) × 100
   ```
   *Observed:* Values like 11264 (instruction count) export as 11264.00% in CSV

---

## Field Offset Map

### Deterministic Metrics (Constant per Encoder)

These metrics remain the same across all samples for an encoder:

| CSV Column | Metric Name                  | Data Type | Aggregation | Implementation |
|------------|------------------------------|-----------|-------------|----------------|
| 70         | Kernel Invocations           | uint32    | First value | `counter.go:384` |
| 71         | Kernel Occupancy             | float32   | Average     | `counter.go:396` |
| 106        | Kernel ALU Performance       | uint64    | First value | Instruction count |

**Extraction Pattern:**
```go
// Sample records for same encoder - take FIRST occurrence
if !metrics.hasValue {
    metrics.KernelInvocations = extractFromOffset(0x0064)
    metrics.hasValue = true
}
```

### Timing Metrics (Variable per Sample)

These metrics vary across execution and need averaging:

| CSV Column | Metric Name      | Data Type | Aggregation | Range          |
|------------|------------------|-----------|-------------|----------------|
| 13         | ALU Utilization  | float32   | Average     | 0.0-5.0%       |
| 71         | Kernel Occupancy | float32   | Average     | 0.0-2.0 (%)    |

**Extraction Pattern:**
```go
// Accumulate across samples, then divide
totalALU := 0.0
sampleCount := 0
for each sample {
    totalALU += extractALUUtilization(sample)
    sampleCount++
}
metrics.ALUUtilization = totalALU / float64(sampleCount)
```

### Counter Metrics (Cumulative)

These metrics accumulate across execution:

| CSV Column | Metric Name                   | Data Type | Aggregation | Notes           |
|------------|-------------------------------|-----------|-------------|-----------------|
| 24         | Buffer Read Bytes             | uint64    | Sum         | Memory ops      |
| 25         | Buffer Write Bytes            | uint64    | Sum         | Memory ops      |
| 26         | Bytes Read From Device Memory | uint64    | Sum         | Device memory   |
| 27         | Bytes Written To Device Memory| uint64    | Sum         | Device memory   |

**Extraction Pattern:**
```go
// Sum across all samples
totalBytes := uint64(0)
for each sample {
    totalBytes += extractBytesRead(sample)
}
metrics.BytesRead = totalBytes
```

---

## Aggregation Algorithms

### 1. Encoder Grouping

**Challenge:** Multiple sample records exist per encoder, must be aggregated correctly.

**Solution:** Group by encoder index (found in metadata records)

```go
// internal/counter/counter.go:aggregateEncoderMetrics()
func aggregateEncoderMetrics(records []CounterRecord) []CSVEncoderMetrics {
    encoderGroups := make(map[int][]CounterRecord)

    // Group sample records by encoder index
    for _, record := range records {
        if record.RecordSize == 464 {  // Sample record
            encoderIdx := findEncoderIndex(record)
            encoderGroups[encoderIdx] = append(encoderGroups[encoderIdx], record)
        }
    }

    // Aggregate each group
    var results []CSVEncoderMetrics
    for encoderIdx, samples := range encoderGroups {
        metrics := aggregateSamples(samples)
        metrics.EncoderIndex = encoderIdx
        results = append(results, metrics)
    }

    return results
}
```

### 2. Sample Aggregation by Metric Type

**Deterministic Metrics:** Take first value
```go
func aggregateDeterministic(samples []Record, offset int) interface{} {
    if len(samples) == 0 {
        return nil
    }
    return extractValue(samples[0], offset)
}
```

**Timing Metrics:** Calculate average
```go
func aggregateTiming(samples []Record, extractor func(Record) float64) float64 {
    total := 0.0
    count := 0
    for _, sample := range samples {
        val := extractor(sample)
        if val >= 0 {  // Valid value
            total += val
            count++
        }
    }
    if count == 0 {
        return 0.0
    }
    return total / float64(count)
}
```

**Counter Metrics:** Sum values
```go
func aggregateCounters(samples []Record, extractor func(Record) uint64) uint64 {
    total := uint64(0)
    for _, sample := range samples {
        total += extractor(sample)
    }
    return total
}
```

### 3. CSV Export Algorithm

```go
func exportToCSV(encoders []CSVEncoderMetrics) {
    // Sort by encoder index for consistent output
    sort.Slice(encoders, func(i, j int) bool {
        return encoders[i].EncoderIndex < encoders[j].EncoderIndex
    })

    // Write header row (241 columns)
    writer.Write(csvHeader)

    // Write each encoder as a row
    for _, enc := range encoders {
        row := make([]string, 241)

        // Column 0-4: Metadata
        row[0] = fmt.Sprintf("%d", enc.EncoderIndex)
        row[1] = fmt.Sprintf("%d", enc.FunctionIndex)
        row[2] = enc.CommandBufferLabel
        row[3] = enc.EncoderLabel
        row[4] = enc.DebugGroup  // NEW: hierarchical debug group

        // Columns 5-240: Performance metrics
        row[13] = fmt.Sprintf("%.2f", enc.ALUUtilization)
        row[70] = fmt.Sprintf("%d", enc.KernelInvocations)
        row[71] = fmt.Sprintf("%.2f", enc.KernelOccupancy)
        row[106] = fmt.Sprintf("%.2f", enc.KernelALUPerformance)
        // ... fill remaining columns

        writer.Write(row)
    }
}
```

---

## Counter File Mapping

The 40 counter files (0-39) map to different performance metrics. Based on gputrace-114 investigation:

### File Index to Metric Mapping

| File Index | Primary Metrics                                    | Record Distribution          |
|------------|----------------------------------------------------|------------------------------|
| 0          | Kernel Invocations (0x0064), Basic execution stats | 261 records (87 samples)     |
| 1-5        | ALU/compute-related metrics                        | Varies                       |
| 6-10       | Memory bandwidth metrics                           | Varies                       |
| 11-15      | Cache/L1 metrics                                   | Varies                       |
| 16-20      | Texture/sampler metrics                            | Varies                       |
| 21-25      | Fragment/vertex shader metrics (compute=0)         | Empty for compute traces     |
| 26-30      | GPU utilization/occupancy                          | Varies                       |
| 31-35      | Register/stack metrics                             | Varies                       |
| 36-39      | Advanced profiling counters                        | Varies                       |

**Note:** Exact file-to-metric mapping is discovered via CSV correlation (see CSV-First Validation below).

### File Size Patterns

Typical file sizes for 06-six-encoders trace:
```text
File 0:  121,104 bytes  (261 records × 464 bytes) - Primary metrics
File 5:   28,928 bytes  (62 records) - Subset of encoders
File 10:  28,928 bytes  (62 records) - Memory metrics
File 15:     928 bytes  (2 records) - Sparse data
File 20:       0 bytes  - No data for compute-only
File 35:  28,928 bytes  (62 records) - Register data
```

### Discovery Process

1. **Parse all counter files** → Extract all records
2. **Group by encoder** → Aggregate sample records
3. **Export to CSV** → Generate 241-column output
4. **Compare with Xcode CSV** → Validate correctness
5. **Map non-zero columns** → Identify which files contribute which metrics

---

## CSV-First Validation Architecture

### Philosophy

**Don't guess the binary format—validate against ground truth.**

The CSV-first approach uses Xcode Instruments' CSV export as the reference implementation, then works backward to understand the binary format.

### Architecture

```text
┌─────────────────────────────────────────────────────────────┐
│                    Ground Truth (Xcode)                     │
│              06-six-encoders Counters.csv                   │
│         241 columns × 6 encoders = Reference Data           │
└──────────────────────────┬──────────────────────────────────┘
                           │
                           ▼
┌─────────────────────────────────────────────────────────────┐
│              Binary Format Parser (gputrace)                │
│  ┌────────────────────┐      ┌──────────────────────────┐  │
│  │ Counter File 0-39  │  →   │  Internal Representation │  │
│  │  (464-byte records)│      │  (CSVEncoderMetrics)     │  │
│  └────────────────────┘      └──────────────────────────┘  │
└──────────────────────────┬──────────────────────────────────┘
                           │
                           ▼
┌─────────────────────────────────────────────────────────────┐
│              CSV Export (our implementation)                │
│              Generated 241-column CSV                       │
└──────────────────────────┬──────────────────────────────────┘
                           │
                           ▼
┌─────────────────────────────────────────────────────────────┐
│                      Validation                             │
│  Compare: Xcode CSV ⟷ Our CSV                              │
│  - Column-by-column numeric comparison                      │
│  - Percentage difference calculation                        │
│  - Identify mismatches and patterns                         │
└─────────────────────────────────────────────────────────────┘
```

### Validation Process

#### 1. Parse Reference CSV
```go
// internal/counter/csv_import.go
func ImportCountersCSV(trace *trace.Trace) (*CSVCounterData, error) {
    // Load Xcode-generated CSV
    csvPath := findCounterCSV(trace.Path)
    records := parseCSV(csvPath)

    // Extract into structured format
    data := &CSVCounterData{
        Encoders: make([]CSVEncoderMetrics, len(records)),
    }

    for i, row := range records {
        data.Encoders[i] = parseCSVRow(row, headerMap)
    }

    return data, nil
}
```

#### 2. Parse Binary & Export CSV
```go
// internal/counter/export.go
func ExportCountersCSV(trace *trace.Trace, outputPath string) error {
    // Parse all 40 counter files
    counterData := parseAllCounterFiles(trace)

    // Aggregate by encoder
    encoders := aggregateEncoderMetrics(counterData)

    // Export to CSV (241 columns)
    writeCSV(outputPath, encoders)
}
```

#### 3. Compare & Validate
```go
// Validation command
func validateCounters(trace *trace.Trace) {
    // Load reference
    reference := ImportCountersCSV(trace)

    // Generate our version
    ours := parseAndAggregateCounters(trace)

    // Compare column by column
    for col := 0; col < 241; col++ {
        for encIdx := 0; encIdx < len(reference.Encoders); encIdx++ {
            refVal := reference.Encoders[encIdx].GetColumn(col)
            ourVal := ours.Encoders[encIdx].GetColumn(col)

            if !closeEnough(refVal, ourVal, tolerance) {
                log.Printf("Mismatch: encoder %d, column %d: ref=%.2f, ours=%.2f",
                    encIdx, col, refVal, ourVal)
            }
        }
    }
}
```

### Validation Results (gputrace-114)

**File 0 validation:**
- 6 encoders extracted ✓
- Kernel Invocations: 100% match (column 70) ✓
- Encoder labels: 100% match ✓
- 58 non-zero columns identified

**Accuracy:**
- Critical metrics: Exact match (invocations, labels)
- Floating-point metrics: Within 0.1% tolerance
- Zero columns: Correctly identified (183 columns always zero)

---

## Implementation Examples

### Example 1: Extract Kernel Invocations

```go
// internal/counter/counter.go
func extractKernelInvocations(data []byte) int {
    if len(data) < 0x0068 {
        return 0
    }

    // Read uint32 at offset 0x0064
    rawValue := binary.LittleEndian.Uint32(data[0x0064:0x0068])

    // Apply scaling factor
    scaledValue := float64(rawValue) / 27.75

    return int(math.Round(scaledValue))
}
```

### Example 2: Range-Based Float32 Search

```go
// Find ALU Utilization (range: 0.0-5.0%)
func findALUUtilization(data []byte) float64 {
    // Scan record for float32 in expected range
    for offset := 0; offset < len(data)-4; offset += 4 {
        value := math.Float32frombits(binary.LittleEndian.Uint32(data[offset:offset+4]))

        // Check if in expected range
        if value >= 0.0 && value <= 5.0 {
            // Additional validation: not suspiciously round
            if value != 0.0 && value != 1.0 {
                return float64(value)
            }
        }
    }
    return 0.0
}
```

### Example 3: Aggregate Encoder Samples

```go
func aggregateEncoderSamples(samples []CounterRecord) CSVEncoderMetrics {
    metrics := CSVEncoderMetrics{}

    // Deterministic: Take first sample
    if len(samples) > 0 {
        metrics.KernelInvocations = extractKernelInvocations(samples[0].Data)
        metrics.EncoderLabel = findEncoderLabel(samples[0])
    }

    // Timing: Average across samples
    var aluSum, occSum float64
    validSamples := 0

    for _, sample := range samples {
        if alu := findALUUtilization(sample.Data); alu > 0 {
            aluSum += alu
            validSamples++
        }
        if occ := findKernelOccupancy(sample.Data); occ > 0 {
            occSum += occ
        }
    }

    if validSamples > 0 {
        metrics.ALUUtilization = aluSum / float64(validSamples)
        metrics.KernelOccupancy = occSum / float64(validSamples)
    }

    // Counters: Sum across samples
    var totalReadBytes, totalWriteBytes uint64
    for _, sample := range samples {
        totalReadBytes += findBufferReadBytes(sample.Data)
        totalWriteBytes += findBufferWriteBytes(sample.Data)
    }
    metrics.BufferReadBytes = totalReadBytes
    metrics.BufferWriteBytes = totalWriteBytes

    return metrics
}
```

### Example 4: CSV Export with 241 Columns

```go
func exportEncoderToCSV(enc CSVEncoderMetrics) []string {
    row := make([]string, 241)

    // Metadata columns (0-4)
    row[0] = fmt.Sprintf("%d", enc.EncoderIndex)
    row[1] = fmt.Sprintf("%d", enc.FunctionIndex)
    row[2] = enc.CommandBufferLabel
    row[3] = enc.EncoderLabel
    row[4] = enc.DebugGroup

    // Performance metrics (columns 5-240)
    row[13] = formatFloat(enc.ALUUtilization)              // ALU Utilization
    row[24] = formatUint64(enc.BufferReadBytes)           // Buffer Read Bytes
    row[25] = formatUint64(enc.BufferWriteBytes)          // Buffer Write Bytes
    row[70] = fmt.Sprintf("%d", enc.KernelInvocations)    // Kernel Invocations
    row[71] = formatFloat(enc.KernelOccupancy)            // Kernel Occupancy
    row[106] = formatFloat(enc.KernelALUPerformance)      // ALU Performance

    // Fill remaining columns with "0.00" or appropriate defaults
    for i := range row {
        if row[i] == "" {
            row[i] = "0.00"
        }
    }

    return row
}

func formatFloat(v float64) string {
    return fmt.Sprintf("%.2f", v)
}

func formatUint64(v uint64) string {
    return fmt.Sprintf("%.2f", float64(v))
}
```

---

## Related Documentation

See also:
- [STREAMDATA_FORMAT.md](../STREAMDATA_FORMAT.md) - streamData plist parsing for dispatch timing
- [PERFCOUNTER_FIELD_OFFSET_MAP.md](./PERFCOUNTER_FIELD_OFFSET_MAP.md) - Detailed field offset discoveries
- [PERFCOUNTERS_STATUS.md](./PERFCOUNTERS_STATUS.md) - Implementation status
- [RECORD_FORMATS.md](./RECORD_FORMATS.md) - Overall trace file formats
- [TRACE_FORMAT.md](./TRACE_FORMAT.md) - Main capture file format

---

## Future Work

1. **Complete Field Offset Map:** Identify exact offsets for all 58 non-zero metrics
2. **Metadata Record Format:** Document 2,300-byte metadata record structure
3. **Architecture Variations:** Test on different GPU architectures (M1/M2/M3)
4. **File-to-Metric Map:** Complete mapping of 40 files to specific metric groups
5. **Validation Suite:** Automated tests comparing our CSV with Xcode CSV

---

**Last Updated:** 2025-11-07
