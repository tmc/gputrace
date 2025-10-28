# Revised GPU Timing Extraction Plan

## Discovery: xctrace export DOES NOT work with .gputrace

After testing, we found:
- `xctrace export` only works with `.trace` files (Instruments recordings)
- `xctrace import` does not support `.gputrace` format
- `.gputrace` is a Metal-specific GPU capture format (created by MTLCaptureManager)
- `.trace` is Instruments' unified profiling format

**Result**: We need to parse .gputrace directly, not convert it.

## The Real Solution: Parse store0 Directly

### What We Know About store0

From examining `BenchmarkLlamaForward.gputrace/store0`:

1. **Format**: zlib-compressed binary data
2. **Size**: 30MB compressed → ~300MB decompressed (estimated)
3. **Header**: `78 5e` (zlib magic bytes)
4. **Contents**: GPU performance counter data, kernel execution times

### The store0 file IS the timing data

Looking at the files:
```
BenchmarkLlamaForward.gputrace/
├── capture (4.1 MB)         # Kernel names, structure ✅
├── store0 (30 MB)           # ⭐ TIMING DATA (compressed)
├── UUID files (203M total)  # Shader binaries, resources
└── device-resources (8B)    # Buffer metadata ✅
```

The `capture` file has the **structure** (what ran), and `store0` has the **timing** (when/how long).

## New Approach: Direct store0 Parsing

### Step 1: Decompress store0

```go
func decompressStore(storePath string) ([]byte, error) {
    compressed, err := os.ReadFile(storePath)
    if err != nil {
        return nil, err
    }

    reader, err := zlib.NewReader(bytes.NewReader(compressed))
    if err != nil {
        return nil, err
    }
    defer reader.Close()

    return io.ReadAll(reader)
}
```

### Step 2: Understand the Decompressed Format

The decompressed data likely contains:
- Performance counter records (timestamps, durations)
- Kernel dispatch records (which kernel, when it ran)
- GPU hardware counters (optional)

**Format hypothesis** (needs verification):
```
Header:
  - Magic bytes or version
  - Record count
  - Offset table

Records (repeated):
  - Record type (uint32)
  - Timestamp (uint64) - Mach absolute time
  - Duration (uint64) - nanoseconds
  - Kernel index (uint32) - into capture's kernel name table
  - Additional data (variable)
```

### Step 3: Match Timing with Kernel Names

We already extract kernel names from the capture file:

```go
trace, _ := gputrace.Open("trace.gputrace")
kernelNames := trace.KernelNames // 200 entries
```

The store0 records likely reference kernels by index into this array.

### Step 4: Create TimingData

```go
type TimingData struct {
    Label           string  // Kernel name
    StartTimestamp  uint64  // Mach absolute time
    EndTimestamp    uint64  // Mach absolute time
    DurationMs      float64 // Calculated
}

func (t *Trace) ExtractTimingFromStore() ([]TimingData, error) {
    // 1. Decompress store0
    storeData, err := t.decompressStore()

    // 2. Parse records
    records := parseStoreRecords(storeData)

    // 3. Match with kernel names
    var timings []TimingData
    for _, record := range records {
        if record.KernelIndex < len(t.KernelNames) {
            timings = append(timings, TimingData{
                Label:          t.KernelNames[record.KernelIndex],
                StartTimestamp: record.StartTime,
                EndTimestamp:   record.EndTime,
                DurationMs:     float64(record.EndTime-record.StartTime) / 1e6,
            })
        }
    }

    return timings, nil
}
```

## Implementation Plan (Revised)

### Phase 1: Decompress and Inspect (Day 1)

1. **Decompress store0**:
```bash
cd /Users/tmc/ml-explore/mlx-go/experiments/gputrace
```

Create `store_parser.go`:
```go
package gputrace

import (
    "bytes"
    "compress/zlib"
    "io"
    "os"
    "path/filepath"
)

func (t *Trace) DecompressStore() ([]byte, error) {
    storePath := filepath.Join(t.Path, "store0")
    compressed, err := os.ReadFile(storePath)
    if err != nil {
        return nil, err
    }

    reader, err := zlib.NewReader(bytes.NewReader(compressed))
    if err != nil {
        return nil, err
    }
    defer reader.Close()

    return io.ReadAll(reader)
}
```

2. **Analyze decompressed data**:
```go
func TestStoreDecompression(t *testing.T) {
    trace, _ := Open("testdata/BenchmarkLlamaForward.gputrace")
    data, err := trace.DecompressStore()
    require.NoError(t, err)

    // Print header
    fmt.Printf("Decompressed size: %d bytes\n", len(data))
    fmt.Printf("First 512 bytes (hex):\n%s\n", hex.Dump(data[:512]))

    // Look for patterns
    // - Repeated structures
    // - Timestamp-like values (large uint64s)
    // - Kernel indices
}
```

3. **Identify record structure**:
   - Look for repeated byte patterns
   - Search for timestamp values (>1e15)
   - Find kernel index references (0-199 range)
   - Determine record size

### Phase 2: Parse Records (Day 2)

1. **Implement record parser**:
```go
type StoreRecord struct {
    Type        uint32
    KernelIndex uint32
    StartTime   uint64
    EndTime     uint64
    // Additional fields as discovered
}

func parseStoreRecords(data []byte) ([]StoreRecord, error) {
    // Based on Phase 1 findings
    // Parse binary records
}
```

2. **Test with known kernels**:
```go
func TestStoreRecordParsing(t *testing.T) {
    trace, _ := Open("testdata/trace.gputrace")
    store, _ := trace.DecompressStore()
    records := parseStoreRecords(store)

    // Should have ~200 records (one per kernel)
    assert.Len(t, records, 200)

    // Check first record matches first kernel
    assert.Equal(t, trace.KernelNames[records[0].KernelIndex], "SqueezeMultiply")
}
```

### Phase 3: Integrate with Timing Extraction (Day 3)

1. **Update ExtractTimingData()**:
```go
func (t *Trace) ExtractTimingData() ([]TimingData, error) {
    // Old approach: search for encoder labels (returns 0)
    // New approach: parse store0

    storeData, err := t.DecompressStore()
    if err != nil {
        return nil, err
    }

    records, err := parseStoreRecords(storeData)
    if err != nil {
        return nil, err
    }

    return convertToTimingData(records, t.KernelNames), nil
}
```

2. **Test end-to-end**:
```bash
cd /Users/tmc/ml-explore/mlx-go/experiments/mlxprof
go test -run TestMergeWithGPUTrace
# Should now show 200 GPU samples instead of 0
```

## Alternative: Use Google's instrumentsToPprof

Located at `/Users/tmc/go/src/github.com/google/instrumentsToPprof`

**If they've already solved this**, we can:
1. Check their gputrace parser implementation
2. See how they parse store0
3. Adapt their approach

Let me check their code:

```bash
cd /Users/tmc/go/src/github.com/google/instrumentsToPprof
grep -r "store" internal/parsers/gputrace/
grep -r "zlib" internal/parsers/gputrace/
```

## Success Criteria

✅ **Phase 1 Complete** when:
- store0 successfully decompressed
- Decompressed data structure identified
- Record format hypothesis formed

✅ **Phase 2 Complete** when:
- Records successfully parsed
- Kernel indices correctly matched
- Timestamps extracted

✅ **Phase 3 Complete** when:
- `ExtractTimingData()` returns 200 records
- GPU samples appear in unified profile
- `go tool pprof -sample_index=gpu -top` shows kernel names

## Timeline (Revised)

- **Day 1**: Decompress + analyze structure (4-6 hours)
- **Day 2**: Implement parser + testing (6-8 hours)
- **Day 3**: Integration + validation (4-6 hours)

**Total**: 2-3 days (down from 3-5 days with xctrace approach)

## Next Immediate Steps

1. Implement `DecompressStore()` function
2. Write test that decompresses and dumps first 1KB
3. Analyze the binary structure
4. Look for timestamp patterns
5. Identify record boundaries

## Why This Is Better Than xctrace

- ✅ No external tool dependency
- ✅ Works with .gputrace directly
- ✅ Faster (no subprocess overhead)
- ✅ More control over parsing
- ✅ Can be automated in tests
- ❌ Requires reverse engineering (but we're close!)

## The Key Insight

We don't need to convert `.gputrace` → `.trace` → XML.

We can parse `.gputrace` directly:
1. ✅ capture file → kernel names (DONE)
2. ✅ device-resources → buffer sizes (DONE)
3. ⏳ store0 → timing data (NEXT)

Let's do this! 🚀
