# GPU Performance Counter Field Offset Quick Reference

**Date:** 2025-11-07

## Quick Lookup Table

| Offset | Size | Type    | Metric                  | Formula/Notes                    | CSV Column |
|--------|------|---------|-------------------------|----------------------------------|------------|
| 0x0000 | 4    | uint32  | Record Marker           | `0x0000004e`; used for boundaries | -          |
| 0x0004 | 4    | uint32  | Record Payload Type     | Varies by record                 | -          |
| 0x0064 | 4    | uint32  | **Kernel Invocations**  | `value ÷ 27.75`                  | 70         |
| TBD    | 4    | float32 | **ALU Utilization**     | Range: 0.0-5.0%                  | 13         |
| TBD    | 4    | float32 | **Kernel Occupancy**    | Range: 0.0-2.0 (0-200%)          | 71         |
| TBD    | 8    | uint64  | **Buffer Read Bytes**   | Sum across samples               | 24         |
| TBD    | 8    | uint64  | **Buffer Write Bytes**  | Sum across samples               | 25         |

**TBD** = To Be Determined - discovered via range-based search

## Extraction Methods

### Method 1: Direct Offset (Known Position)

```go
// Kernel Invocations at 0x0064
func extractKernelInvocations(data []byte) int {
    raw := binary.LittleEndian.Uint32(data[0x0064:0x0068])
    return int(float64(raw) / 27.75)
}
```

### Method 2: Range-Based Search (Float32)

```go
// ALU Utilization: float32 in range 0.0-5.0
func findALUUtilization(data []byte) float64 {
    for offset := 0; offset < len(data)-4; offset += 4 {
        value := math.Float32frombits(binary.LittleEndian.Uint32(data[offset:offset+4]))
        if value >= 0.0 && value <= 5.0 && value != 0.0 {
            return float64(value)
        }
    }
    return 0.0
}
```

### Method 3: Range-Based Search (Uint64)

```go
// Buffer bytes: uint64 in reasonable range (1KB - 100KB)
func findBufferBytes(data []byte, minVal, maxVal uint64) uint64 {
    for offset := 0; offset < len(data)-8; offset += 4 {
        value := binary.LittleEndian.Uint64(data[offset:offset+8])
        if value >= minVal && value <= maxVal {
            return value
        }
    }
    return 0
}
```

## Common Patterns

### Pattern 1: Scaling Factors

Some metrics use non-linear scaling:

```
Kernel Invocations = Raw Value ÷ 27.75
```

**Hypothesis:** Related to SIMD width (32) or GPU thread dispatch units.

### Pattern 2: Range-Based Validation

When exact offset is unknown, validate by expected value range:

| Metric            | Type    | Range           | Typical Value |
|-------------------|---------|-----------------|---------------|
| ALU Utilization   | float32 | 0.0 - 5.0       | 0.01 - 3.10   |
| Kernel Occupancy  | float32 | 0.0 - 2.0       | 0.05 - 1.50   |
| Buffer Read Bytes | uint64  | 1K - 100K       | 10K - 50K     |

### Pattern 3: Multi-Sample Aggregation

Different metrics need different aggregation:

```go
// Deterministic (constant per encoder)
kernelInvocations := samples[0].extractAt(0x0064)  // First sample

// Timing (average across samples)
aluUtilization := average(samples, extractALU)     // Average

// Counters (sum across samples)
totalBytes := sum(samples, extractBytes)           // Sum
```

## CSV Column Reference

### Key Columns

| Column | Metric Name                     | Type    | Source                          |
|--------|---------------------------------|---------|---------------------------------|
| 0      | Index                           | int     | Encoder sequence number         |
| 1      | Encoder FunctionIndex           | int     | Function ID                     |
| 2      | CommandBuffer Label             | string  | CB debug label                  |
| 3      | Encoder Label                   | string  | Encoder debug label             |
| 4      | Debug Group                     | string  | Hierarchical debug group        |
| 13     | ALU Utilization                 | float   | Range search 0.0-5.0            |
| 24     | Buffer Device Memory Bytes Read | uint64  | Range search + sum              |
| 25     | Buffer Device Memory Bytes Written | uint64 | Range search + sum           |
| 70     | Kernel Invocations              | int     | Offset 0x0064 ÷ 27.75           |
| 71     | Kernel Occupancy                | float   | Range search 0.0-2.0, averaged  |
| 106    | Kernel ALU Performance          | uint64  | Instruction count               |

## Record Type Detection

```go
func detectRecordType(data []byte) string {
    if len(data) < 4 {
        return "invalid"
    }

    if binary.LittleEndian.Uint32(data[0:4]) != 0x4e {
        return "invalid"
    }

    switch len(data) {
    case 464:
        return "sample"      // Performance metrics
    default:
        if len(data) >= 2300 && len(data) <= 2900 {
            return "metadata" // Encoder context
        }
        return "unknown"
    }
}
```

## Validation Checklist

When implementing new metric extraction:

- [ ] Identify expected value range from CSV ground truth
- [ ] Determine data type (uint32, float32, uint64)
- [ ] Choose extraction method (direct offset vs range search)
- [ ] Determine aggregation strategy (first, average, sum)
- [ ] Validate against Xcode CSV export
- [ ] Document in this reference

## Common Pitfalls

### ❌ Wrong: Index-based access without bounds check
```go
value := binary.LittleEndian.Uint32(data[0x0064:])  // Panic if too short!
```

### ✅ Right: Bounds-checked access
```go
if len(data) >= 0x0068 {
    value := binary.LittleEndian.Uint32(data[0x0064:0x0068])
}
```

### ❌ Wrong: Aggregating deterministic metrics
```go
total := 0
for _, sample := range samples {
    total += sample.kernelInvocations  // Wrong! Same value every time
}
```

### ✅ Right: Use first sample for deterministic metrics
```go
kernelInvocations := samples[0].kernelInvocations  // Same for all samples
```

### ❌ Wrong: Assuming exact offsets for all metrics
```go
aluUtil := extractFloat32(data, 0x00XX)  // Might not be there!
```

### ✅ Right: Use range-based search for variable metrics
```go
aluUtil := findFloatInRange(data, 0.0, 5.0)  // Search entire record
```

---

**See Also:**
- [BINARY_FORMAT_REFERENCE.md](BINARY_FORMAT_REFERENCE.md) - Comprehensive documentation
- [PERFCOUNTER_FIELD_OFFSET_MAP.md](./PERFCOUNTER_FIELD_OFFSET_MAP.md) - Detailed field map

**Last Updated:** 2025-11-07
