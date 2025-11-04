#!/usr/bin/env python3
"""Analyze record sizes and types in counter files."""

import struct
from pathlib import Path
from collections import Counter

def find_records(data):
    """Find all 0x4E record markers."""
    records = []
    for i in range(len(data) - 4):
        if data[i:i+4] == b'\x4e\x00\x00\x00':
            records.append(i)
    return records

def main():
    counter_file = Path("/tmp/fast-llm-mlx-test-perf.gputrace/fast-llm-mlx-test.gputrace.gpuprofiler_raw/Counters_f_0.raw")
    data = counter_file.read_bytes()
    
    records = find_records(data)
    print(f"Total records: {len(records)}\n")
    
    # Analyze record sizes
    sizes = []
    for i in range(len(records)):
        if i + 1 < len(records):
            size = records[i+1] - records[i]
        else:
            size = len(data) - records[i]
        sizes.append(size)
    
    size_counts = Counter(sizes)
    print("Record size distribution:")
    for size, count in sorted(size_counts.items())[:10]:
        print(f"  {size:5d} bytes: {count:4d} records ({count/len(records)*100:.1f}%)")
    
    # Check what follows the 0x4E marker
    print("\nBytes following 0x4E marker (first 10 records):")
    for i in range(min(10, len(records))):
        offset = records[i]
        # Read next 32 bytes after marker
        chunk = data[offset:offset+32]
        hex_str = ' '.join(f'{b:02x}' for b in chunk)
        print(f"  Record {i:3d} @ 0x{offset:08x}: {hex_str}")
        
        # Try to interpret as structured data
        if len(chunk) >= 8:
            val1 = struct.unpack('<I', chunk[4:8])[0]
            val2 = struct.unpack('<I', chunk[8:12])[0] if len(chunk) >= 12 else 0
            print(f"    -> [4:8]=uint32:{val1:,}  [8:12]=uint32:{val2:,}")

if __name__ == '__main__':
    main()
