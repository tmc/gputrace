#!/usr/bin/env python3
"""
Analyze .gpuprofiler_raw counter files to find field offsets.
Target metrics from CSV Row 1:
- ALU Utilization: 0.98 (98%)
- Kernel Occupancy: 0.30 (30%)
- Buffer L1 Miss Rate: 25.15%
- Kernel Invocations: 1024.00
"""

import struct
import sys
from pathlib import Path

def find_records(data):
    """Find all 0x4E record markers."""
    records = []
    for i in range(len(data) - 4):
        if data[i:i+4] == b'\x4e\x00\x00\x00':
            records.append(i)
    return records

def analyze_record(data, offset, next_offset=None):
    """Analyze a single record for potential field values."""
    size = next_offset - offset if next_offset else len(data) - offset
    record = data[offset:offset+size]
    
    findings = []
    
    # Search for float32 values matching our targets
    targets = {
        0.98: "ALU Utilization",
        0.30: "Kernel Occupancy", 
        25.15: "Buffer L1 Miss Rate",
        1024.0: "Kernel Invocations",
    }
    
    # Check every 4-byte aligned position as float32
    for i in range(0, min(size - 4, 512), 4):
        try:
            val = struct.unpack('<f', record[i:i+4])[0]
            for target, name in targets.items():
                if abs(val - target) < 0.01:
                    findings.append((offset + i, name, val))
        except:
            pass
    
    return findings, size

def main():
    counter_file = Path("/tmp/fast-llm-mlx-test-perf.gputrace/fast-llm-mlx-test.gputrace.gpuprofiler_raw/Counters_f_0.raw")
    
    if not counter_file.exists():
        print(f"Error: {counter_file} not found")
        return 1
    
    data = counter_file.read_bytes()
    print(f"File size: {len(data):,} bytes")
    
    records = find_records(data)
    print(f"Found {len(records)} records with 0x4E marker\n")
    
    # Analyze first 20 records
    all_findings = []
    for i in range(min(20, len(records))):
        offset = records[i]
        next_offset = records[i+1] if i+1 < len(records) else None
        findings, size = analyze_record(data, offset, next_offset)
        
        if findings:
            print(f"Record {i} at offset 0x{offset:08x} (size: {size} bytes):")
            for field_offset, name, value in findings:
                print(f"  0x{field_offset:08x} (+{field_offset-offset:4d}): {name} = {value:.2f}")
            all_findings.extend(findings)
    
    if not all_findings:
        print("\nNo matching float32 values found in first 20 records.")
        print("This suggests values are either:")
        print("  1. Stored in different format (uint32, uint64)")
        print("  2. Aggregated across multiple records")
        print("  3. Encoded/scaled differently")
        print("\nTrying alternative searches...")
        
        # Search for integers that could be counts
        offset = records[0]
        next_offset = records[1] if len(records) > 1 else len(data)
        record = data[offset:next_offset]
        
        print(f"\nFirst record (0x{offset:08x}, {next_offset-offset} bytes):")
        print("Searching for uint32 values in reasonable ranges...")
        
        for i in range(0, min(next_offset - offset - 4, 256), 4):
            val_u32 = struct.unpack('<I', record[i:i+4])[0]
            if 1000 <= val_u32 <= 2000000:  # Reasonable count range
                print(f"  +{i:4d}: uint32 = {val_u32:,}")
    
    return 0

if __name__ == '__main__':
    sys.exit(main())
