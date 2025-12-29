package trace

import (
	"fmt"
)

// ParseNestedRecords attempts to parse the data of the current record as a sequence
// of nested MTSP records. This is used for container records like CS and Ci.
// It skips the first 16 bytes (standard MTSP header/padding for containers)
// and attempts to parse the rest.
func (t *Trace) ParseNestedRecords(rec MTSPRecord) ([]MTSPRecord, error) {
	// Standard container header size heuristic
	const containerHeaderSize = 16

	if len(rec.Data) <= containerHeaderSize {
		return nil, nil // Not enough data to be a container
	}

	// Attempt to parse the payload
	nestedData := rec.Data[containerHeaderSize:]
	nestedRecords, err := t.ParseMTSPFromData(nestedData)
	if err != nil {
		return nil, fmt.Errorf("failed to parse nested data: %w", err)
	}

	// Heuristic: If we found valid records, return them.
	// If ParseMTSPFromData returns empty or error, it wasn't a container.
	if len(nestedRecords) > 0 {
		return nestedRecords, nil
	}

	return nil, nil
}
