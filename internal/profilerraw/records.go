package profilerraw

import "bytes"

var recordMarker = []byte{0x4e, 0x00, 0x00, 0x00}

// Record is one marker-delimited record in a profiler counter file.
// Data aliases the input passed to Records.
type Record struct {
	Offset int64
	Data   []byte
}

// Records returns the marker-delimited records in data.
func Records(data []byte) []Record {
	var records []Record
	for start := bytes.Index(data, recordMarker); start >= 0; {
		nextOffset := bytes.Index(data[start+len(recordMarker):], recordMarker)
		end := len(data)
		if nextOffset >= 0 {
			end = start + len(recordMarker) + nextOffset
		}
		records = append(records, Record{
			Offset: int64(start),
			Data:   data[start:end],
		})
		if nextOffset < 0 {
			break
		}
		start = end
	}
	return records
}
