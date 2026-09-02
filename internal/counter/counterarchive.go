package counter

import (
	"encoding/binary"
	"fmt"
	"sort"

	"github.com/tmc/apple/x/plist"
)

// Counter samples that attribute to an encoder.
//
// streamData carries three parallel blob streams: APSData, APSTimelineData and
// APSCounterData. The ShaderProfilerData blobs in APSTimelineData hold only
// machine-wide samples (GRC_ENCODER_ID 0xFFFFFFFF). The samples that name an
// encoder live in the last APSCounterData blob, a nested archive whose root
// dictionary holds "Derived Counter Sample Data" (the GPRWCNTR blobs, grouped
// per pass), "Encoder Infos" (the encoder ids belonging to this capture) and
// "Subdivided Dictionary" -> "passList" (the per-pass column names).
//
// A sample belongs to this capture iff its GRC_ENCODER_ID appears in Encoder
// Infos. That join needs no clock reconciliation, which matters because the
// counter timestamps and the command-buffer timestamps do not line up and no
// offset in the archive corrects them.

// EncoderSamples aggregates the counter samples attributed to one encoder id.
type EncoderSamples struct {
	EncoderID           uint64 `json:"encoder_id"`
	KickTraceID         uint64 `json:"kick_trace_id,omitempty"` // Set when every sample agrees
	Group               int    `json:"group"`                   // Encoder Infos group (one per pass)
	Ordinal             int    `json:"ordinal"`                 // Position within the group, i.e. encoder execution order
	BatchID             int    `json:"batch_id"`                // From the TraceId tables, by ordinal
	SampleIndex         int    `json:"sample_index"`            // From the TraceId tables, by ordinal
	BatchIDRecorded     bool   `json:"batch_id_recorded"`
	SampleIndexRecorded bool   `json:"sample_index_recorded"`
	SampleCount         int    `json:"sample_count"`
	EndSamples          int    `json:"end_samples"` // Records with GRC_SAMPLE_TYPE 5, one per pass
	GPUCycles           uint64 `json:"gpu_cycles"`  // Sum of GRC_GPU_CYCLES over the end records
	StartTicks          uint64 `json:"start_ticks"`
	EndTicks            uint64 `json:"end_ticks"`
	DurationNs          uint64 `json:"duration_ns,omitempty"`
}

// AttributedCounterSample is one GPRWCNTR record whose encoder ID appears in
// APSCounterData Encoder Infos. Counter values remain in recorded column order;
// the archive does not establish a safe join from a sample blob to passList.
type AttributedCounterSample struct {
	BlobOrdinal      int      `json:"blob_ordinal"`
	RecordOrdinal    int      `json:"record_ordinal"`
	EncoderGroup     int      `json:"encoder_group"`
	ExecutionOrdinal int      `json:"execution_ordinal"`
	Timestamp        uint64   `json:"timestamp"`
	GPUCycles        uint64   `json:"gpu_cycles"`
	SampleType       uint64   `json:"sample_type"`
	EncoderID        uint64   `json:"encoder_id"`
	KickTraceID      uint64   `json:"kick_trace_id"`
	KickSlotIdx      uint64   `json:"kick_slot_idx"`
	SourceID         uint64   `json:"source_id"`
	Counters         []uint64 `json:"counters,omitempty"`
}

// CounterArchive is the decoded per-encoder counter attribution.
type CounterArchive struct {
	Encoders           []EncoderSamples          `json:"encoders"` // Sorted by encoder id
	AttributedRecords  []AttributedCounterSample `json:"attributed_records,omitempty"`
	TotalSamples       int                       `json:"total_samples"`        // Every record decoded
	AttributedSamples  int                       `json:"attributed_samples"`   // Records naming an encoder of this capture
	MachineWideSamples int                       `json:"machine_wide_samples"` // Records with GRC_ENCODER_ID 0xFFFFFFFF
	KnownEncoderIDs    int                       `json:"known_encoder_ids"`    // Distinct ids in Encoder Infos
	PassColumns        [][]string                `json:"pass_columns,omitempty"`
	TraceIDs           *TraceIDTable             `json:"trace_ids,omitempty"`
	Blobs              int                       `json:"blobs"`
	StrideMismatches   int                       `json:"stride_mismatches"` // Blobs rejected because the stride did not divide
}

// AttributedFraction returns the share of decoded samples that name an encoder
// of this capture. It is small by design: the counter stream is machine-wide.
func (a *CounterArchive) AttributedFraction() float64 {
	if a.TotalSamples == 0 {
		return 0
	}
	return float64(a.AttributedSamples) / float64(a.TotalSamples)
}

// ParseCounterArchive decodes per-encoder counter attribution from the
// APSCounterData blobs. It returns nil when no blob carries a counter archive.
func ParseCounterArchive(blobs [][]byte, timebaseNumer, timebaseDenom uint64, traceIDs *TraceIDTable) *CounterArchive {
	for i := len(blobs) - 1; i >= 0; i-- {
		if a := parseCounterArchiveBlob(blobs[i], timebaseNumer, timebaseDenom, traceIDs); a != nil {
			return a
		}
	}
	return nil
}

func parseCounterArchiveBlob(data []byte, timebaseNumer, timebaseDenom uint64, traceIDs *TraceIDTable) *CounterArchive {
	root, objects, ok := archiveRoot(data)
	if !ok {
		return nil
	}
	dict := keyedDict(root, objects)
	if dict == nil {
		return nil
	}
	sampleData, ok := dict["Derived Counter Sample Data"]
	if !ok {
		return nil
	}

	known := encoderInfoIDs(dict["Encoder Infos"], objects)
	place := encoderInfoPlacement(dict["Encoder Infos"], objects)
	archive := &CounterArchive{
		TraceIDs:        traceIDs,
		KnownEncoderIDs: len(known),
		PassColumns:     passColumnNames(dict["Subdivided Dictionary"], objects),
	}

	byEncoder := make(map[uint64]*EncoderSamples)
	for blobOrdinal, blob := range gprwcntrBlobs(sampleData, objects) {
		archive.Blobs++
		samples, _, err := ParseGPRWCNTR(blob)
		if err != nil {
			archive.StrideMismatches++
			continue
		}
		for recordOrdinal, s := range samples {
			archive.TotalSamples++
			if s.MachineWide() {
				archive.MachineWideSamples++
				continue
			}
			if _, ok := known[s.EncoderID]; !ok {
				continue
			}
			archive.AttributedSamples++
			placement := place[s.EncoderID]
			archive.AttributedRecords = append(archive.AttributedRecords, AttributedCounterSample{
				BlobOrdinal: blobOrdinal, RecordOrdinal: recordOrdinal,
				EncoderGroup: placement.group, ExecutionOrdinal: placement.ordinal,
				Timestamp: s.Timestamp, GPUCycles: s.GPUCycles,
				SampleType: s.SampleType, EncoderID: s.EncoderID,
				KickTraceID: s.KickTraceID, KickSlotIdx: s.KickSlotIdx,
				SourceID: s.SourceID, Counters: append([]uint64(nil), s.Counters...),
			})
			e := byEncoder[s.EncoderID]
			if e == nil {
				e = &EncoderSamples{
					EncoderID:   s.EncoderID,
					KickTraceID: s.KickTraceID,
					StartTicks:  s.Timestamp,
					EndTicks:    s.Timestamp,
				}
				if pl, ok := place[s.EncoderID]; ok {
					e.Group, e.Ordinal = pl.group, pl.ordinal
					if b, ok := traceIDs.BatchForOrdinal(pl.ordinal); ok {
						e.BatchID = b
						e.BatchIDRecorded = true
					}
					if idx, ok := traceIDs.SampleIndexForOrdinal(pl.ordinal); ok {
						e.SampleIndex = idx
						e.SampleIndexRecorded = true
					}
				}
				byEncoder[s.EncoderID] = e
			}
			e.SampleCount++
			if s.SampleType == GRCSampleTypeEncoderEnd {
				e.EndSamples++
				e.GPUCycles += s.GPUCycles
			}
			if s.KickTraceID != e.KickTraceID {
				e.KickTraceID = 0 // Not a single kick; do not claim one.
			}
			if s.Timestamp < e.StartTicks {
				e.StartTicks = s.Timestamp
			}
			if s.Timestamp > e.EndTicks {
				e.EndTicks = s.Timestamp
			}
		}
	}

	archive.Encoders = make([]EncoderSamples, 0, len(byEncoder))
	for _, e := range byEncoder {
		e.DurationNs = ticksToNs(e.StartTicks, e.EndTicks, timebaseNumer, timebaseDenom)
		archive.Encoders = append(archive.Encoders, *e)
	}
	sort.Slice(archive.Encoders, func(i, j int) bool {
		return archive.Encoders[i].EncoderID < archive.Encoders[j].EncoderID
	})
	return archive
}

// encoderInfoIDs returns the encoder ids of this capture. "Encoder Infos" is an
// array of NSData, each a packed list of uint32 ids.
func encoderInfoIDs(v any, objects []any) map[uint64]struct{} {
	ids := make(map[uint64]struct{})
	for _, item := range nsArray(v, objects) {
		data := nsData(item, objects)
		for off := 0; off+4 <= len(data); off += 4 {
			ids[uint64(binary.LittleEndian.Uint32(data[off:]))] = struct{}{}
		}
	}
	return ids
}

// encoderPlacement is an encoder id's position in Encoder Infos: which group
// (pass) it belongs to and its ordinal within that group.
type encoderPlacement struct{ group, ordinal int }

// encoderInfoPlacement maps each encoder id to its position. Ids come in
// (start, end) pairs, so a group of 23 encoders holds 46 ids; both ids of a
// pair get the same ordinal.
func encoderInfoPlacement(v any, objects []any) map[uint64]encoderPlacement {
	place := make(map[uint64]encoderPlacement)
	for group, item := range nsArray(v, objects) {
		data := nsData(item, objects)
		for off := 0; off+4 <= len(data); off += 4 {
			id := uint64(binary.LittleEndian.Uint32(data[off:]))
			place[id] = encoderPlacement{group: group, ordinal: off / 8}
		}
	}
	return place
}

// passColumnNames returns the column-name list of each pass, which is what
// gives a GPRWCNTR record its width.
func passColumnNames(subdivided any, objects []any) [][]string {
	dict := keyedDict(deref(objects, subdivided), objects)
	if dict == nil {
		return nil
	}
	var out [][]string
	for _, pass := range nsArray(dict["passList"], objects) {
		for _, list := range nsArray(pass, objects) {
			var names []string
			for _, n := range nsArray(list, objects) {
				if s, ok := deref(objects, n).(string); ok {
					names = append(names, s)
				}
			}
			if len(names) > 0 {
				out = append(out, names)
			}
		}
	}
	return out
}

// gprwcntrBlobs walks the nested arrays of "Derived Counter Sample Data" and
// returns every GPRWCNTR blob it finds.
func gprwcntrBlobs(v any, objects []any) [][]byte {
	var out [][]byte
	var walk func(any, int)
	walk = func(node any, depth int) {
		if depth > 4 {
			return
		}
		if data := nsData(node, objects); len(data) >= len(GPRWCNTRMagic) &&
			string(data[:len(GPRWCNTRMagic)]) == GPRWCNTRMagic {
			out = append(out, data)
			return
		}
		for _, child := range nsArray(node, objects) {
			walk(child, depth+1)
		}
	}
	walk(v, 0)
	return out
}

// archiveRoot unmarshals an NSKeyedArchiver blob and returns its root object.
func archiveRoot(data []byte) (root any, objects []any, ok bool) {
	root, objects, _, ok = archiveRootIndexed(data)
	return root, objects, ok
}

func archiveRootIndexed(data []byte) (root any, objects []any, rootIndex uint64, ok bool) {
	var archive map[string]any
	if _, err := plist.Unmarshal(data, &archive); err != nil {
		return nil, nil, 0, false
	}
	objects, ok = archive["$objects"].([]any)
	if !ok {
		return nil, nil, 0, false
	}
	top, ok := archive["$top"].(map[string]any)
	if !ok {
		return nil, nil, 0, false
	}
	uid, ok := top["root"].(plist.UID)
	if !ok || int(uid) >= len(objects) {
		return nil, nil, 0, false
	}
	return objects[int(uid)], objects, uint64(uid), true
}

// keyedDict resolves an NSDictionary (NS.keys + NS.objects) to a Go map keyed
// by its string keys. Values are left as raw objects for the caller to deref.
func keyedDict(v any, objects []any) map[string]any {
	m, ok := deref(objects, v).(map[string]any)
	if !ok {
		return nil
	}
	keys, ok1 := m["NS.keys"].([]any)
	vals, ok2 := m["NS.objects"].([]any)
	if !ok1 || !ok2 || len(keys) != len(vals) {
		return nil
	}
	out := make(map[string]any, len(keys))
	for i := range keys {
		if k, ok := deref(objects, keys[i]).(string); ok {
			out[k] = vals[i]
		}
	}
	return out
}

// nsArray resolves an NSArray to its element objects.
func nsArray(v any, objects []any) []any {
	m, ok := deref(objects, v).(map[string]any)
	if !ok {
		return nil
	}
	items, _ := m["NS.objects"].([]any)
	return items
}

// nsData resolves an NSData, which plist decoding may hand back either as raw
// bytes or wrapped in a dictionary.
func nsData(v any, objects []any) []byte {
	switch t := deref(objects, v).(type) {
	case []byte:
		return t
	case map[string]any:
		if d, ok := t["NS.data"].([]byte); ok {
			return d
		}
	}
	return nil
}

// String renders a one-line summary, so callers reporting counter attribution
// state the machine-wide share rather than hiding it.
func (a *CounterArchive) String() string {
	return fmt.Sprintf("%d encoders, %d/%d samples attributed (%.2f%%), %d machine-wide",
		len(a.Encoders), a.AttributedSamples, a.TotalSamples,
		100*a.AttributedFraction(), a.MachineWideSamples)
}
