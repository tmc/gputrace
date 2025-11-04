#!/usr/bin/env python3
"""Deep analysis of 464-byte records."""

import struct
from pathlib import Path
from collections import defaultdict

def find_records(data):
    records = []
    for i in range(len(data) - 4):
        if data[i:i+4] == b'\x4e\x00\x00\x00':
            records.append(i)
    return records

def analyze_464_records(data, records):
    """Focus on the most common 464-byte records."""
    records_464 = []
    for i in range(len(records)):
        if i + 1 < len(records):
            size = records[i+1] - records[i]
            if size == 464:
                records_464.append(records[i])
    
    print(f"Found {len(records_464)} records of size 464 bytes\n")
    
    # Sample first few records
    print("Examining first 5 records:\n")
    for idx, offset in enumerate(records_464[:5]):
        record = data[offset:offset+464]
        print(f"Record {idx} @ 0x{offset:08x}:")
        
        # Check for non-zero values
        nonzero_positions = []
        for i in range(0, 464, 4):
            val_u32 = struct.unpack('<I', record[i:i+4])[0]
            val_u64 = struct.unpack('<Q', record[i:i+8])[0] if i < 456 else 0
            val_f32 = struct.unpack('<f', record[i:i+4])[0]
            
            if val_u32 != 0:
                nonzero_positions.append((i, val_u32, val_u64, val_f32))
        
        # Show interesting non-zero values
        for pos, u32, u64, f32 in nonzero_positions[:10]:
            print(f"  +{pos:3d}: u32={u32:12,} u64={u64:20,} f32={f32:.6f}")
        print()
    
    # Look for patterns across all 464-byte records
    print("\nLooking for consistent field patterns...")
    field_stats = defaultdict(lambda: {'nonzero_count': 0, 'values': []})
    
    for offset in records_464[:100]:  # Sample first 100
        record = data[offset:offset+464]
        for i in range(0, 464, 8):
            val = struct.unpack('<Q', record[i:i+8])[0] if i < 456 else 0
            if val != 0:
                field_stats[i]['nonzero_count'] += 1
                if len(field_stats[i]['values']) < 5:
                    field_stats[i]['values'].append(val)
    
    print("\nFields that are frequently non-zero (in first 100 records):")
    for offset in sorted(field_stats.keys()):
        stats = field_stats[offset]
        if stats['nonzero_count'] > 10:
            print(f"  Offset +{offset:3d}: non-zero in {stats['nonzero_count']:3d}/100 records")
            print(f"    Sample values: {stats['values'][:3]}")

def main():
    counter_file = Path("/tmp/fast-llm-mlx-test-perf.gputrace/fast-llm-mlx-test.gputrace.gpuprofiler_raw/Counters_f_0.raw")
    data = counter_file.read_bytes()
    records = find_records(data)
    analyze_464_records(data, records)

if __name__ == '__main__':
    main()
