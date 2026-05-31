# GPU Trace Generator

**Purpose:** Generate diverse GPU traces with known characteristics for binary format analysis.

## Overview

This tool creates Metal workloads with varying complexity to enable systematic analysis of Apple's `.gpuprofiler_raw` binary format.

The repository only checks in a small canonical fixture subset under `testdata/traces`.
Use this generator to recreate the broader analysis corpus locally when you need it.

## Scenarios

### Encoder Count Variations
- `01-single-encoder` - Single compute dispatch (baseline)
- `02-two-encoders` - Two sequential dispatches
- `03-three-encoders` - Three sequential dispatches
- `04-four-encoders` - Four sequential dispatches
- `06-six-encoders` - Six dispatches (matches LLM trace complexity)

### Known Invocation Counts
- `known-invocations-1000` - Exactly 1,000 thread invocations
- `known-invocations-10000` - Exactly 10,000 thread invocations

**Purpose:** Search for these exact values in binary to locate invocation count field

### ALU Utilization Variations
- `low-alu-simple-add` - Minimal ALU operations (~5-10% expected)
- `high-alu-complex-math` - Intensive mathematical operations (>50% expected)

**Purpose:** Identify ALU utilization field by comparing traces with known ALU characteristics

### Occupancy Variations
- `low-occupancy-high-registers` - High register pressure (128 floats per thread)
- `high-occupancy-low-registers` - Minimal register usage

**Purpose:** Identify occupancy field by comparing register pressure patterns

## Building

```bash
cd testdata/trace-generator
swift build -c release
```

## Running

### Single Scenario

```bash
# Run a specific scenario
.build/release/trace-generator 01-single-encoder

# Or run directly with swift
swift run trace-generator 01-single-encoder
```

### All Scenarios

```bash
.build/release/trace-generator all
```

### List Available Scenarios

```bash
.build/release/trace-generator list
```

## Capturing Traces

### Method 1: Xcode Instruments (Recommended)

```bash
# 1. Build the executable
swift build -c release

# 2. Open Instruments
open -a Instruments

# 3. In Instruments:
#    - Choose "GPU Counters" template
#    - Click "Choose Target"
#    - Select: .build/release/trace-generator
#    - Add arguments: 01-single-encoder
#    - Click Record
#    - Wait for completion
#    - File > Export > Save as .gputrace

# 4. Export CSV:
#    - File > Export > Counters
#    - Save as CSV

# 5. Organize local captures outside the checked-in fixture directories:
CAPTURE_DIR=../traces/local-$(date +%Y%m%d-%H%M%S)/01-single-encoder
mkdir -p "$CAPTURE_DIR"
mv ~/Downloads/trace-generator.gputrace "$CAPTURE_DIR/trace.gputrace"
mv ~/Downloads/counters.csv "$CAPTURE_DIR/counters.csv"
```

### Method 2: Command Line (Experimental)

```bash
# Capture with xctrace (if available)
CAPTURE_DIR=../traces/local-$(date +%Y%m%d-%H%M%S)
mkdir -p "$CAPTURE_DIR"
xcrun xctrace record \
    --template 'GPU Counters' \
    --launch .build/release/trace-generator \
    --launch-arg 01-single-encoder \
    --output "$CAPTURE_DIR/01-single-encoder.trace"

# Convert to .gputrace if needed
```

### Method 3: Automated Batch Capture

```bash
# Capture one scenario with programmatic Metal capture. By default this writes
# to ../traces/generated-YYYYMMDD-HHMMSS/ so checked-in fixtures are not replaced.
make run-capture SCENARIO=01-single-encoder

# Capture every scenario.
make capture-all RUNS=3

# Choose an explicit local output directory when needed.
make capture-all RUNS=1 CAPTURE_DIR=../traces/local-smoke
```

The automated targets set `MTL_CAPTURE_ENABLED=1` and pass an output `.gputrace`
path to the generator. They create `.gputrace` packages; use Instruments when
you also need to export GPU counter CSV files.

## Expected Output

### Single Encoder Example

```
GPU Trace Generator
==================

✓ Metal device: Apple M4 Max

============================================================
Scenario: 01-single-encoder
Description: Single encoder: 1 compute dispatch (1024 threads)
============================================================
✓ Dispatched: 16 threadgroups × 64 threads = 1024 threads
  Expected encoders in trace: 1

✓ All scenarios completed
```

### Known Invocations Example

```
============================================================
Scenario: known-invocations-1000
Description: Known invocations: exactly 1000 (10 threadgroups × 100 threads)
============================================================
✓ Dispatched: 10 threadgroups × 100 threads = 1000 threads
  Search for value 1000 in binary to find invocation count field
```

## Directory Structure

Checked-in fixtures use this layout:

```
testdata/
├── trace-generator/           # This tool
│   ├── Package.swift
│   ├── Sources/
│   │   └── main.swift
│   └── .build/
└── traces/                    # Captured traces
    ├── 01-single-encoder/
    │   └── 01-single-encoder-run1.gputrace
    ├── 02-two-encoders/
    ├── 03-three-encoders/
    ├── 04-four-encoders/
    ├── 06-six-encoders/
    ├── known-invocations-1000/
    ├── known-invocations-10000/
    ├── low-alu-simple-add/
    ├── high-alu-complex-math/
    ├── low-occupancy-high-registers/
    └── high-occupancy-low-registers/
```

Captures created with `make run-capture` or `make capture-all` are grouped
under `testdata/traces/generated-YYYYMMDD-HHMMSS/` by default. Manual
Instruments captures should use `testdata/traces/local-*` or another local
directory unless intentionally refreshing checked-in fixtures.

## Analysis Workflow

### Step 1: Baseline Comparison

```bash
# Capture profiled local traces with Instruments first; the checked-in fixtures
# are structural and do not include profiler streamData.
.build/release/trace-generator 01-single-encoder
.build/release/trace-generator 06-six-encoders
# [Capture, profile, and export each scenario with Instruments]

LOCAL_SINGLE_ENCODER_PERFDATA=../traces/local-profiled/01-single-encoder-perfdata.gputrace
LOCAL_SIX_ENCODER_PERFDATA=../traces/local-profiled/06-six-encoders-perfdata.gputrace

# Compare local profiled captures
cd ../..
gputrace diff "$LOCAL_SIX_ENCODER_PERFDATA" "$LOCAL_SINGLE_ENCODER_PERFDATA" --explain
```

**Key Question:** Does 1 encoder produce fewer counter files than 6 encoders?

### Step 2: Encoder Scaling

```bash
# Capture 1, 2, 3, 4, 6 encoder traces
# Compare file counts:
#   1 encoder → X files
#   2 encoders → Y files
#   3 encoders → Z files
#   ...
# Find correlation pattern
```

### Step 3: Value Search

```bash
# Search for known values in binaries
python3 << 'EOF'
import struct
import glob

# Search for 1000 (known invocations) in a locally generated profiler capture.
target = 1000
trace_dir = "/path/to/known-invocations-1000.gputrace.gpuprofiler_raw"

for file in glob.glob(f"{trace_dir}/Counters_f_*.raw"):
    with open(file, 'rb') as f:
        data = f.read()

    # Search as uint32
    for offset in range(0, len(data) - 4, 4):
        val = struct.unpack('<I', data[offset:offset+4])[0]
        if val == target:
            print(f"Found {target} at offset {offset:#06x} in {file}")
EOF
```

### Step 4: Cross-Trace Validation

```python
# Compare same offsets across multiple traces
# Find which offsets are:
#   - Constant (configuration)
#   - Variable (actual counters)
#   - Correlated with known values
```

## Metadata Template

For each trace, create `metadata.json`:

```json
{
  "scenario": "01-single-encoder",
  "date_captured": "2025-11-03",
  "gpu_model": "Apple M4 Max",
  "gpu_arch": "AGX G16",
  "os_version": "macOS 15.1",
  "xcode_version": "16.0",
  "expected_encoders": 1,
  "expected_invocations": 1024,
  "expected_alu": "5-10%",
  "expected_occupancy": "high",
  "counter_files": null,
  "csv_encoder_rows": null,
  "notes": "Baseline single encoder test"
}
```

Fill in `counter_files` and `csv_encoder_rows` after capture.

## Success Criteria

✅ **Baseline established:**
- Understand if encoder count correlates with file count

✅ **Value locations identified:**
- Find offsets for: Invocations, ALU%, Occupancy%

✅ **Pattern validated:**
- Consistent across multiple traces

✅ **Decision made:**
- Continue full binary parsing investigation OR
- Defer as P3 research project

## Quick Start

```bash
# 1. Build
cd testdata/trace-generator
swift build -c release

# 2. Test run
.build/release/trace-generator 01-single-encoder

# 3. Capture with programmatic Metal capture
make run-capture SCENARIO=01-single-encoder

#    Or capture with Instruments (manual)
#    GPU Counters template
#    Select executable: .build/release/trace-generator
#    Arguments: 01-single-encoder

# 4. Export and analyze
#    Compare with the retained six-encoder fixture or a local LLM capture

# 5. Decide on next steps based on results
```

## Troubleshooting

### Build Errors

```bash
# Clean and rebuild
swift package clean
swift build -c release
```

### Metal Not Available

```
❌ Metal is not supported on this device
```

**Solution:** Must run on macOS with Apple Silicon (M1/M2/M3/M4)

### Shader Compilation Errors

Check Metal shader syntax in `main.swift`. All shaders are inline in source.

## Next Steps

1. **Immediate:** Capture baseline `01-single-encoder` trace
2. **Compare:** With the retained six-encoder fixture or a local LLM capture
3. **Decision:** Continue based on file count correlation
4. **If promising:** Capture remaining scenarios
5. **Analyze:** Compare checked-in and local captures to find field offsets

---

**Status:** Ready to use
**Requirements:** macOS with Apple Silicon, Xcode/Swift toolchain
**Time:** ~5 minutes per trace capture (manual Instruments workflow)
