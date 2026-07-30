# Counter File to Metric Name Mapping

**Date:** 2025-11-06
**Status:** Complete

## Summary

Successfully reverse-engineered the mapping from Counters_f_*.raw files to performance counter metric names by analyzing the Xcode CSV export column order.

## Mapping Formula

```text
Counters_f_N.raw → CSV counter column (N - 4)
Counters_f_N.raw → Absolute CSV column (N + 1)
```

## Key Findings

1. **Files 0-3**: Do not map to counter columns (likely contain metadata or internal data)
2. **Files 4-39**: Map directly to the first 36 counter columns in the CSV
3. **Verified Example**: `Counters_f_12.raw → "ALU Utilization"` ✓

## Complete Mapping Table

| File Index | CSV Column | Counter Name |
|------------|------------|--------------|
| 4 | 5 | 1D Texture Array Sampler Calls |
| 5 | 6 | 1D Texture Sampler Calls |
| 6 | 7 | 2D MSAA Texture Sampler Calls |
| 7 | 8 | 2D Texture Array Sampler Calls |
| 8 | 9 | 2D Texture Sampler Calls |
| 9 | 10 | 2X MSAA Resolved Pixels Stored |
| 10 | 11 | 3D Texture Sampler Calls |
| 11 | 12 | 4X MSAA Resolved Pixels Stored |
| **12** | **13** | **ALU Utilization** |
| 13 | 14 | Anisotropic Sampler Calls |
| 14 | 15 | Attachment Pixels Stored |
| 15 | 16 | Average Anisotropic Level |
| 16 | 17 | Average Pixel Overdraw |
| 17 | 18 | Average Samples Per Pixel |
| 18 | 19 | Average Sparse Texture Tile Size |
| 19 | 20 | Back Face Clipped Primitives |
| 20 | 21 | Block Compressed Texture Samples |
| 21 | 22 | Buffer Device Memory Bytes Read |
| 22 | 23 | Buffer Device Memory Bytes Written |
| 23 | 24 | Buffer L1 Miss Rate |
| 24 | 25 | Buffer L1 Read Accesses |
| 25 | 26 | Buffer L1 Read Bandwidth |
| 26 | 27 | Buffer L1 Write Accesses |
| 27 | 28 | Buffer L1 Write Bandwidth |
| 28 | 29 | Bytes Read From Device Memory |
| 29 | 30 | Bytes Written To Device Memory |
| 30 | 31 | Clip Unit Limiter |
| 31 | 32 | Compression Ratio of Texture Memory Read |
| 32 | 33 | Compression Ratio of Texture Memory Written |
| 33 | 34 | Compute Shader Launch Limiter |
| 34 | 35 | Compute Shader Launch Utilization |
| 35 | 36 | Control Flow Limiter |
| 36 | 37 | Control Flow Utilization |
| 37 | 38 | Cube Array Texture Sampler Calls |
| 38 | 39 | Cube Texture Sampler Calls |
| 39 | 40 | Cull Unit Limiter |

## Important Metrics

The following metrics are particularly important for performance analysis:

- **File 12**: ALU Utilization (percentage, 0-100)
- **File 23**: Buffer L1 Miss Rate (percentage, 0-100)
- **File 28**: Bytes Read From Device Memory
- **File 29**: Bytes Written To Device Memory
- **File 33**: Compute Shader Launch Limiter
- **File 34**: Compute Shader Launch Utilization
- **File 35**: Control Flow Limiter
- **File 36**: Control Flow Utilization

## Implementation

The mapping is implemented in `internal/counter/file_mapping.go`:

- `CounterFileToName`: Map from file index to counter name
- `CounterNameToFile`: Reverse map from counter name to file index
- `AllCounterNames`: Complete list of all 241 counter names

## Usage

```go
import "github.com/tmc/gputrace/internal/counter"

// Get counter name from file index
name := counter.CounterFileToName[12]  // "ALU Utilization"

// Get file index from counter name
fileIdx := counter.CounterNameToFile["ALU Utilization"]  // 12

// Iterate all counter names
for i, name := range counter.AllCounterNames {
    fmt.Printf("Counter %d: %s\n", i, name)
}
```

## Notes

- The CSV export contains 241 total counter columns
- Only 40 Counters_f_*.raw files exist (files 0-39)
- Files 4-39 map to the first 36 counter columns
- The remaining 205 counter columns (indices 36-240) do not have corresponding raw files
- These unmapped counters may be computed/derived metrics or may use different storage

## Related Documentation

- [PERFCOUNTERS_REFERENCE.md](./PERFCOUNTERS_REFERENCE.md) - Performance counter parsing status
- `internal/counter/counter.go` - Counter parsing implementation
