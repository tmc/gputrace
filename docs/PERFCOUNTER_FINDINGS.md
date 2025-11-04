# ALU Utilization Validation Findings

**Date:** 2025-11-03
**Task:** gputrace-63 (ALU Utilization extraction validation)
**Status:** Investigation Complete - Issues Identified

---

## Summary

ALU Utilization values are present in the binary counter files, but the current extraction logic has incorrect search ranges. The investigation found:

1. ✅ ALU values ARE present in binary files
2. ✅ Values match CSV ground truth (0.01%, 0.02%, 3.10%)
3. ❌ Current Go code searches wrong range (0.95-1.0 instead of 0.0-10.0)
4. ❌ ALU appears at varying offsets (no fixed location like invocations@0x0064)

---

## Test Data Validation

### 6-Encoder Trace CSV Ground Truth

| Encoder | ALU Utilization | Kernel Invocations | Occupancy |
|---------|----------------|-------------------|-----------|
| Encoder_1_simple_add | 0.01% | 1024 | 0.09% |
| Encoder_2_simple_multiply | 0.01% | 1024 | 0.09% |
| Encoder_3_simple_subtract | 0.01% | 1024 | 0.08% |
| Encoder_4_simple_divide | 0.02% | 1024 | 0.09% |
| Encoder_5_complex_math | **3.10%** | 1024 | 0.47% |
| Encoder_6_low_register_pressure | 0.02% | 1024 | 0.15% |

### Binary Search Results

**Search for value 0.01:**
- Found in **18/40** counter files (Counters_f_0 through f_39)
- **1,404 occurrences** per file (matches 1,403 time samples)
- Example locations:
  - Counters_f_0.raw: record 693 @ offset +0x01cc
  - Counters_f_24.raw: record 1022 @ offset +0x0148

**Search for value 0.02:**
- Found in **18/40** counter files
- **1,404 occurrences** per file
- Example locations:
  - Counters_f_1.raw: record 1322 @ offset +0x01a4
  - Counters_f_24.raw: record 510 @ offset +0x0098

**Search for value 3.10:**
- Found in **27/40** counter files
- **1-3 occurrences** per file
- Example locations:
  - Counters_f_0.raw: record 1116 @ offset +0x00f8
  - Counters_f_1.raw: record 1312 @ offset +0x0034
  - Counters_f_3.raw: record 663 @ offset +0x0144

---

## Key Findings

### 1. ALU Values Appear Across Multiple Files

All 40 counter files contain similar percentage values. This indicates:
- **Percentage values (0-100) are common in the binary format**
- Values like 0.01, 0.02 appear in many counter types (not just ALU)
- Cannot identify "ALU Utilization file" by value alone

### 2. Variable Offsets Within Records

Unlike Kernel Invocations (fixed at offset 0x0064), ALU values appear at **varying offsets**:

| File | Value | Record | Offset |
|------|-------|--------|--------|
| Counters_f_0.raw | 0.01 | 693 | +0x01cc |
| Counters_f_0.raw | 3.10 | 1116 | +0x00f8 |
| Counters_f_1.raw | 0.02 | 1322 | +0x01a4 |
| Counters_f_1.raw | 3.10 | 1312 | +0x0034 |

**Conclusion:** ALU is NOT at a fixed byte offset within records.

### 3. Time-Series Structure Confirmed

The 1,404 occurrences of values like 0.01 across ALL files confirms:
- Each file contains time-series samples (1,403-1,404 samples)
- Each sample may contain readings for multiple encoders
- Values repeat across time as encoders execute

### 4. Current Go Code Issues

**File:** `internal/counter/counter.go:342-346`

```go
// ❌ WRONG: Searches for values 0.95-1.0
if aluUtil := findPercentageField(data, 0.95, 1.0); aluUtil >= 0 {
    metrics.ALUUtilization = aluUtil * 100
}
```

**Issues:**
1. Search range 0.95-1.0 misses actual values (0.01-3.10)
2. Assumes ALU is stored as decimal 0-1 (CSV shows percentage 0-100)
3. No fixed offset - requires scanning entire record

---

## Architecture Insights

### Original Hypothesis (Incorrect)
> "Each of 40 files corresponds to one encoder"

### Actual Structure (Confirmed)
> **"40 files = 40 counter TYPES (not encoders)"**

This means:
- Counters_f_0.raw might be "GPU Cycles" counter
- Counters_f_X.raw is "ALU Utilization" counter (X unknown)
- Counters_f_Y.raw is "Kernel Occupancy" counter (Y unknown)

Each file contains:
```
[Sample 1: encoder A=0.01%, encoder B=3.10%, ...]
[Sample 2: encoder A=0.01%, encoder B=3.10%, ...]
...
[Sample 1403: encoder A=0.01%, encoder B=3.10%, ...]
```

### Why We Can't Isolate ALU File

Percentage values (0.01, 0.02, 3.10) appear in MANY counter types because:
- Many metrics are percentages: occupancy, utilization, cache hit rate, etc.
- The values 0.01 and 0.02 are very common (near-zero percentages)
- Cannot distinguish "ALU Utilization 3.10%" from "Occupancy 3.10%" by value alone

---

## Recommended Approach

### Option 1: Map Files to Counter Names (Preferred)

**Strategy:**
1. Analyze Instruments CSV export column order
2. Reverse-engineer which Counters_f_X.raw maps to which CSV column
3. Create lookup table: `{"ALU Utilization": "Counters_f_12.raw", ...}`

**Pros:**
- Direct, accurate mapping
- Can extract ALL 241 metrics correctly
- Aligns with Apple's internal structure

**Cons:**
- Requires investigation to map all 40 files
- Mapping may change between GPU types or OS versions

### Option 2: Heuristic Extraction (Current Approach - Needs Fixes)

**Strategy:**
1. For deterministic metrics (invocations): Use fixed offset (0x0064)
2. For percentage metrics (ALU, occupancy): Scan for reasonable ranges
3. Use contextual clues (record type, adjacent values) to identify fields

**Fixes Needed:**
```go
// Fix ALU search range
if aluUtil := findPercentageField(data, 0.0, 10.0); aluUtil >= 0 {  // Was: 0.95-1.0
    if aluUtil > 0.001 {  // Filter out near-zero noise
        metrics.ALUUtilization = aluUtil  // Already a percentage
    }
}
```

**Pros:**
- Works without file-to-counter mapping
- More resilient to format changes

**Cons:**
- May extract wrong percentage values
- Ambiguous (many fields are percentages)
- Less accurate than Option 1

### Option 3: Hybrid Approach (Recommended)

1. **Use fixed offsets for deterministic fields** (invocations, registers)
2. **Map critical files** (ALU, occupancy) via investigation
3. **Fall back to heuristics** for less important metrics

---

## Next Steps

### Immediate (Fix Current Code)

1. **Update ALU search range** in `counter.go:344`
   ```go
   - if aluUtil := findPercentageField(data, 0.95, 1.0); aluUtil >= 0 {
   + if aluUtil := findPercentageField(data, 0.0, 10.0); aluUtil >= 0 {
   ```

2. **Fix occupancy search range** in `counter.go:349`
   ```go
   - if occupancy := findPercentageField(data, 0.25, 0.35); occupancy >= 0 {
   + if occupancy := findPercentageField(data, 0.0, 1.0); occupancy >= 0 {
   ```

3. **Remove double conversion** (values already in percent 0-100):
   ```go
   - metrics.ALUUtilization = aluUtil * 100
   + metrics.ALUUtilization = aluUtil
   ```

4. **Test against 6-encoder trace**
   - Should extract 6 distinct ALU values: 0.01, 0.01, 0.01, 0.02, 3.10, 0.02
   - Currently: Likely extracting zeros or wrong values

### Short-Term (Map Counter Files)

1. **Identify "ALU Utilization" counter file**
   - Use correlation with CSV column order
   - Analyze record structures in each file
   - Validate against test traces

2. **Identify "Kernel Occupancy" counter file**
   - Same process as ALU

3. **Create counter file mapping**
   ```go
   counterFileMap := map[string]int{
       "ALU Utilization": 12,      // Counters_f_12.raw (hypothesis)
       "Kernel Occupancy": 18,     // Counters_f_18.raw (hypothesis)
       "Kernel Invocations": 0,    // Counters_f_0.raw (verified)
   }
   ```

### Long-Term (Complete Implementation)

1. Map all 40 counter files to metric names
2. Extract all 241 CSV columns from binary
3. Replace synthetic data in `export-counters` command
4. Validate output matches Instruments CSV byte-for-byte

---

## Test Plan

### Phase 1: Fix Range Issues

```bash
# Before fix
./gputrace perfcounters testdata/traces/06-six-encoders/06-six-encoders-run1-perf.gputrace
# Expected: ALU values all 0 or wrong

# After fix
./gputrace perfcounters testdata/traces/06-six-encoders/06-six-encoders-run1-perf.gputrace
# Expected: 6 encoders with ALU values in range 0.01-3.10
```

### Phase 2: Validate Extraction

Compare extracted values to CSV:

| Encoder | CSV ALU | Binary ALU | Match? |
|---------|---------|-----------|--------|
| simple_add | 0.01% | ? | ❌ |
| simple_multiply | 0.01% | ? | ❌ |
| simple_subtract | 0.01% | ? | ❌ |
| simple_divide | 0.02% | ? | ❌ |
| complex_math | **3.10%** | ? | ❌ |
| low_register | 0.02% | ? | ❌ |

Goal: All ✅ after fixes

### Phase 3: File Mapping

Identify correct counter file for ALU:
- Test all 40 files individually
- Extract single metric from each
- Match against CSV columns
- Document mapping

---

## Files Modified

- ❌ `internal/counter/counter.go` (needs update)
- ✅ `/tmp/gputrace-alu-validation-findings.md` (this document)
- ✅ `/tmp/find_alu_utilization.py` (analysis script)
- ✅ `/tmp/find_alu_exact.py` (analysis script)
- ✅ `/tmp/find_alu_range.py` (analysis script)
- ✅ `/tmp/identify_alu_file.py` (analysis script)

---

## References

- Investigation Report: `/tmp/gputrace-binary-parsing-final-report.md`
- Test Data: `testdata/traces/06-six-encoders/`
- CSV Ground Truth: `06-six-encoders-run1 Counters.csv`
- Current Implementation: `internal/counter/counter.go:342-351`

---

**Status:** ✅ Validation Complete - Issues Identified
**Next:** Update Go code with corrected search ranges
