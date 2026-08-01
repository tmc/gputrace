package counter

import "sort"

// TraceId tables.
//
// The last blob of each of the three streams (APSData, APSTimelineData,
// APSCounterData) is a metadata dictionary holding "TraceId to BatchId",
// "TraceId to SampleIndex" and "TraceId to Coalesced BatchId". Each is itself
// a nested archive: a dictionary keyed by trace id.
//
//	TraceId to BatchId           id -> batch index
//	TraceId to SampleIndex       id -> [0, 4, 2, sampleIndex]
//	TraceId to Coalesced BatchId id -> [[batchIndex, sampleIndex]]
//
// All three are keyed by the same 23 ids in one archive, one per encoder, and
// their sample indices reproduce the third field of "Encoder Sample Index
// Data" exactly. [V]
//
// The ids do NOT join to GRC_ENCODER_ID or GRC_KICK_TRACE_ID by equality:
// measured overlap is 0 of 69 trace ids against 736 Encoder Infos ids, and 0
// against every encoder and kick id in the counter samples. Each stream owns a
// disjoint id range. [V]
//
// What does connect them is position. Ids are allocated in runs of 23 - one
// run per stream metadata table, sixteen runs in Encoder Infos - stepping by
// 52 within a run. So the k-th id of a run is the k-th encoder, and the batch
// index the tables give for the k-th trace id is the batch of the k-th encoder
// of every run. [D] derived: within all 16 Encoder Infos groups the samples'
// start timestamps ascend with k (16/16), and the groups themselves ascend in
// time, which a wrong ordering would break.

// TraceIDInfo is one row of the TraceId tables.
type TraceIDInfo struct {
	TraceID     uint64 `json:"trace_id"`
	BatchID     int    `json:"batch_id"`
	SampleIndex int    `json:"sample_index"`
}

// TraceIDTable is the decoded TraceId tables of one stream, ordered by trace id.
type TraceIDTable struct {
	Rows []TraceIDInfo `json:"rows"`
}

// BatchForOrdinal returns the batch id of the ordinal-th encoder, and whether
// the table covers that ordinal. Ordinals index the ascending trace ids, which
// is the encoder execution order.
func (t *TraceIDTable) BatchForOrdinal(ordinal int) (int, bool) {
	if t == nil || ordinal < 0 || ordinal >= len(t.Rows) {
		return 0, false
	}
	return t.Rows[ordinal].BatchID, true
}

// SampleIndexForOrdinal returns the sample index of the ordinal-th encoder.
func (t *TraceIDTable) SampleIndexForOrdinal(ordinal int) (int, bool) {
	if t == nil || ordinal < 0 || ordinal >= len(t.Rows) {
		return 0, false
	}
	return t.Rows[ordinal].SampleIndex, true
}

// ParseTraceIDTable finds and decodes the TraceId tables in a stream's blobs.
// It returns nil when no blob carries them.
func ParseTraceIDTable(blobs [][]byte) *TraceIDTable {
	for i := len(blobs) - 1; i >= 0; i-- {
		root, objects, ok := archiveRoot(blobs[i])
		if !ok {
			continue
		}
		dict := keyedDict(root, objects)
		if dict == nil {
			continue
		}
		batch := nestedKeyedDict(dict["TraceId to BatchId"], objects)
		if len(batch) == 0 {
			continue
		}
		sample := nestedKeyedDict(dict["TraceId to SampleIndex"], objects)

		t := &TraceIDTable{Rows: make([]TraceIDInfo, 0, len(batch))}
		for id, v := range batch {
			row := TraceIDInfo{TraceID: id, BatchID: int(plistUint64(deref(v.objects, v.value)))}
			// The sample index is the last element of the id's entry.
			if s, ok := sample[id]; ok {
				if items := nsArray(s.value, s.objects); len(items) > 0 {
					row.SampleIndex = int(plistUint64(deref(s.objects, items[len(items)-1])))
				}
			}
			t.Rows = append(t.Rows, row)
		}
		sort.Slice(t.Rows, func(a, b int) bool { return t.Rows[a].TraceID < t.Rows[b].TraceID })
		return t
	}
	return nil
}

// nestedValue is a value inside a nested archive, carried with the object
// table needed to resolve it.
type nestedValue struct {
	value   any
	objects []any
}

// nestedKeyedDict decodes a value that is itself an archived dictionary keyed
// by integer trace ids.
func nestedKeyedDict(v any, objects []any) map[uint64]nestedValue {
	data := nsData(v, objects)
	if len(data) == 0 {
		return nil
	}
	root, inner, ok := archiveRoot(data)
	if !ok {
		return nil
	}
	m, ok := root.(map[string]any)
	if !ok {
		return nil
	}
	keys, ok1 := m["NS.keys"].([]any)
	vals, ok2 := m["NS.objects"].([]any)
	if !ok1 || !ok2 || len(keys) != len(vals) {
		return nil
	}
	out := make(map[uint64]nestedValue, len(keys))
	for i := range keys {
		id := plistUint64(deref(inner, keys[i]))
		if id == 0 {
			continue
		}
		out[id] = nestedValue{value: vals[i], objects: inner}
	}
	return out
}
