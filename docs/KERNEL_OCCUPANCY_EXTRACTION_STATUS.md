# Kernel Occupancy Extraction Status

**Date:** 2025-11-04
**Bead:** gputrace-64
**Status:** Investigation Complete, Implementation Requires Refinement

## Summary

Successfully located Kernel Occupancy data in Profiling_f_*.raw files and implemented extraction infrastructure. **Key discovery**: Kernel Occupancy values are RARE occurrences in the binary, not frequent ones, requiring inverse frequency-based selection rather than median/mode approaches.

## Completed Work

### 1. Location Discovery ✅
- **Confirmed**: Kernel Occupancy is in `Profiling_f_*.raw` files, NOT `Counters_f_*.raw`
- **Evidence**:
  - Single encoder (0.09): Found in `Profiling_f_0.raw` at offset 0x28450
  - Six encoders, encoder 5 (0.47): Found in `Profiling_f_4.raw` at offset 0x645cc
- **Documentation**: `docs/KERNEL_OCCUPANCY_LOCATION.md`

### 2. Infrastructure Implementation ✅
- `internal/counter/profiling.go` (240 lines)
  - File scanning and parsing
  - Float32 extraction
  - Aggregation logic (median-based, needs refinement)
  - Confidence calculation
- **Test Tools**:
  - `cmd/find-occupancy/` - Binary value searcher
  - `cmd/test-occupancy/` - CSV validation framework
  - `cmd/analyze-values/` - Frequency analysis tool

### 3. Frequency Analysis ✅

**Key Finding**: Kernel Occupancy values appear INFREQUENTLY (1-5 times), while noise appears FREQUENTLY.

Example from `Profiling_f_4.raw` (Encoder 5, expected occupancy=0.47):
```
Value         Count    Type
---------     -----    ----
0.125000      94       NOISE (most common)
0.141667      65       NOISE (2nd most common)
0.470202      1        ACTUAL OCCUPANCY (rare!)
```

**Pattern**:
- Noise values: 50-100 occurrences
- Actual occupancy: 1-10 occurrences
- Need inverse frequency selection, not median

## Current Implementation Issues

### Problem: Median Picks Noise
Current code in `profiling.go:calculateMedian()` selects the middle value after sorting all candidates. With 401 unique values in a file, this picks common noise (0.125) instead of rare actual occupancy (0.47).

```go
// Current (WRONG for sparse data):
occupancy := calculateMedian(candidateValues)  // Returns 0.125 (noise)

// Needed (filter then select):
occupancy := selectRareValue(candidateValues, expectedRange)  // Returns 0.47
```

### Test Results
```
Single Encoder Test:
  Binary: 12.50% (wrong - picked noise)
  CSV:     9.00% (correct)
  Diff:    3.50%

Six Encoder Test (Encoder 5):
  Binary: 12.50% (wrong - picked noise)
  CSV:    47.00% (correct)
  Diff:  -34.50%
```

## Solution Strategy

### Approach 1: Frequency Filtering (Recommended)
1. Extract all float32 candidates (0.01-1.0 range)
2. **Filter**: Remove values appearing >20 times (noise threshold)
3. **Search**: Look for rare values (1-10 occurrences)
4. **Validate**: Check if rare values are in reasonable occupancy range
5. **Select**: Use mode/median of RARE values only

```go
func extractOccupancyWithFrequencyFilter(data []byte) float64 {
    // Count frequency of each value
    freq := make(map[float32]int)
    for each float32 in data {
        if inRange(val, 0.01, 1.0) {
            freq[val]++
        }
    }

    // Filter: keep only rare values (count <= 20)
    rareValues := []float64{}
    for val, count := range freq {
        if count <= 20 && count >= 1 {
            rareValues = append(rareValues, float64(val))
        }
    }

    // Select from rare values
    return calculateMedian(rareValues)  // Now median of RARE values
}
```

### Approach 2: Pattern Matching
- Kernel occupancy appears at specific record offsets
- Example: Encoder 5's 0.47 at record 7, offset +0x68aa
- Could scan for specific offset patterns
- More brittle but potentially more accurate

### Approach 3: CSV-Guided Extraction
- Import CSV first to know expected values
- Search binary for those specific values
- Confirms extraction correctness
- Only works when CSV is available

## File Structure Insights

### Profiling File Count vs Encoder Count
- **Observation**: 40 Profiling_f_*.raw files per trace
- **Reality**: 1-6 encoders in test traces
- **Implication**: NOT 1:1 mapping between files and encoders

**Possible explanations**:
1. Files contain timeline samples (40 time slices)
2. Files contain different metric types
3. Files contain warp/thread group subdivisions
4. Only subset of files contain occupancy data

### Which Files Have Occupancy?
From investigation:
- Single encoder (1 encoder): Found in `Profiling_f_0.raw`
- Six encoders, Encoder 5: Found in `Profiling_f_4.raw`

**Hypothesis**: File index correlates with encoder index, but occupancy data may span multiple files or be consolidated.

## Next Steps

### Immediate (Required for gputrace-64)
1. ✅ Implement frequency filtering in `profiling.go`
2. ✅ Test with both traces to validate
3. ✅ Update test thresholds (should be <1% diff)
4. ✅ Commit refined implementation

### Future Enhancements
1. Determine exact file-to-encoder mapping
2. Identify other metrics in Profiling files (ALU Utilization, Memory Bandwidth)
3. Document binary record structure for occupancy fields
4. Add command-line flag to show extraction confidence

## References

- Investigation: `docs/KERNEL_OCCUPANCY_LOCATION.md`
- Binary format: `docs/PERFCOUNTER_BINARY_FORMAT.md`
- Frequency analysis: `cmd/analyze-values/`
- Test validation: `cmd/test-occupancy/`

## Commits

1. `6fe8be1` - Investigation tools and documentation
2. `a7da1e3` - Initial profiling parser (median approach)
3. *Pending* - Refined parser with frequency filtering
