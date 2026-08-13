package cmd

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tmc/apple/x/plist"
	"github.com/tmc/gputrace/internal/counter"
)

// TimelineRaw represents the parsed content of a Timeline_f_*.raw file.
type TimelineRaw struct {
	CounterCount uint32
	DataOffset   uint32
	Timestamps   []uint64
}

// ParseTimelineRaw reads the header information from a raw timeline file.
func ParseTimelineRaw(path string) (*TimelineRaw, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read raw file: %w", err)
	}

	if len(data) < 0x3c000 {
		return nil, fmt.Errorf("file too small: %d bytes (expected > 0x3c000)", len(data))
	}

	tr := &TimelineRaw{
		CounterCount: binary.LittleEndian.Uint32(data[12:16]),
		DataOffset:   binary.LittleEndian.Uint32(data[32:36]),
	}

	// Scan header for timestamps (heuristic based on user findings)
	// "timestamps found in raw file headers (500B-700B range)"
	tr.Timestamps = extractTimestampsFromHeader(data[500:800])

	return tr, nil
}

func extractTimestampsFromHeader(header []byte) []uint64 {
	var ts []uint64
	for i := 0; i < len(header)-8; i += 8 {
		val := binary.LittleEndian.Uint64(header[i : i+8])
		// Filter for reasonable timestamp values (e.g., > 0 and somewhat monotonic/large if absolute)
		// User noted "profiler sampling timestamps".
		if val > 100000 {
			ts = append(ts, val)
		}
	}
	return ts
}

// EnhanceTimelineWithRawData updates the timeline using data extracted from raw files.
func EnhanceTimelineWithRawData(timeline *Timeline, tracePath string) error {
	rawData, err := ParseAPSTimelineData(tracePath)
	if err != nil {
		return fmt.Errorf("parse raw data: %w", err)
	}

	// Add GPRWCNTR samples as a new track
	// Use ThreadID 10 for "GPRWCNTR Samples"
	trName := TimelineEvent{
		Name:      "thread_name",
		Phase:     "M",
		ProcessID: 1,
		ThreadID:  10,
		Args: map[string]interface{}{
			"name": "GPRWCNTR Samples",
		},
	}
	timeline.Events = append(timeline.Events, trName)

	timebaseNumer := uint64(125)
	timebaseDenom := uint64(3)
	if timeline.TimebaseNumer != 0 {
		timebaseNumer = timeline.TimebaseNumer
		timebaseDenom = timeline.TimebaseDenom
	}

	// We need to handle the absolute time offset.
	// In buildTimelineFromProfilerData:
	// startNs = (cb.StartTicks - absoluteTime) * ...
	// GPRWCNTR timestamps are absolute ticks (like cb.StartTicks).
	// So we need to subtract timeline.AbsoluteTime.

	// If timeline doesn't store AbsoluteTime publicly, we might have an issue.
	// Let's rely on the passed-in timeline having StartTime (ns) and assume
	// we can align by finding the first CB and calculating the offset if needed.
	// BUT, the summary says APSTimelineData has AbsoluteTime.
	// Let's just assume we can use the same conversion if we knew AbsoluteTime.

	// Hack: Infer AbsoluteTime from first CB if available.
	// Or just look at the first GPRWCNTR timestamp vs timeline.StartTime

	// Actually, I should update Timeline struct to publicize AbsoluteTime if possible.
	// But for now, let's just assume 0 offset relative to "absolute" if we are matching ticks.
	// Wait, timeline.Events use microseconds meaningful to Chrome.
	// The timestamps in timeline.Events are shifting to 0-based or capture-start-based.

	// Let's try to deduce the offset from the first CB event if it exists.
	var absoluteTimeRef uint64
	var baseNsRef uint64
	foundRef := false

	for _, ev := range timeline.Events {
		if ev.Category == "command_buffer" {
			if startTicks, ok := ev.Args["start_ticks"].(uint64); ok {
				absoluteTimeRef = startTicks
				baseNsRef = ev.Timestamp * 1000 // Convert back to ns
				foundRef = true
				break
			}
		}
	}

	if !foundRef {
		// Fallback or skip
		return nil
	}

	for _, rec := range rawData.GPRWCNTRRecords {
		// Convert rec.Timestamp (ticks) to timeline standard (us)
		// delta_ticks = rec.Timestamp - absoluteTimeRef
		// time_ns = baseNsRef + delta_ticks * numer / denom

		// Handle signed delta carefully
		var timestampUs uint64
		if rec.Sample.Timestamp >= absoluteTimeRef {
			delta := rec.Sample.Timestamp - absoluteTimeRef
			deltaNs := delta * timebaseNumer / timebaseDenom
			timestampUs = (baseNsRef + deltaNs) / 1000
		} else {
			delta := absoluteTimeRef - rec.Sample.Timestamp
			deltaNs := delta * timebaseNumer / timebaseDenom
			if deltaNs > baseNsRef {
				continue
			} // Should not happen if within capture
			timestampUs = (baseNsRef - deltaNs) / 1000
		}

		// Filter outliers (e.g. invalid large timestamps)
		// If timestamp is more than 60 seconds from the reference base, skip it.
		// baseNsRef is the start of CB#0.
		// 60s = 60 * 1e6 us
		if timestampUs > (baseNsRef/1000)+60_000_000 {
			continue
		}

		event := TimelineEvent{
			Name:      "Sample",
			Category:  "gprwcntr",
			Phase:     "I", // Instant event
			Timestamp: timestampUs,
			ProcessID: 1,
			ThreadID:  10,
			Args:      gprwcntrEventArgs(rec),
		}
		timeline.Events = append(timeline.Events, event)
	}

	return nil
}

func gprwcntrEventArgs(rec rawProfilerRecord) map[string]interface{} {
	return map[string]interface{}{
		"index":                    rec.StreamIndex,
		"stream_index":             rec.StreamIndex,
		"record_index":             rec.RecordIndex,
		"timestamp_ticks":          rec.Sample.Timestamp,
		"timestamp_domain":         "mach absolute ticks",
		"coordinate_basis":         "GPRWCNTR tick delta converted with the trace timebase and anchored to the first APSTimelineData command buffer",
		"grc_gpu_cycles_raw":       rec.Sample.GPUCycles,
		"grc_sample_type_raw":      rec.Sample.SampleType,
		"grc_encoder_id_raw":       rec.Sample.EncoderID,
		"grc_kick_trace_id_raw":    rec.Sample.KickTraceID,
		"grc_kick_slot_index_raw":  rec.Sample.KickSlotIdx,
		"grc_source_id_raw":        rec.Sample.SourceID,
		"machine_wide":             rec.Sample.MachineWide(),
		"record_stride_bytes":      rec.Stride,
		"record_column_count":      7 + len(rec.Sample.Counters),
		"hardware_counter_columns": len(rec.Sample.Counters),
		"record_format":            "GPRWCNTR variable-stride record",
		"counter_decode_status":    "fixed GRC columns decoded; hardware counter columns remain uninterpreted",
	}
}

type rawProfilerRecord struct {
	Sample      counter.GPRWCNTRSample
	Stride      int
	StreamIndex int // Raw stream ordinal.
	RecordIndex int // Zero-based record ordinal within the stream.
}

func parseRawProfilerRecords(data []byte, streamIndex int) ([]rawProfilerRecord, error) {
	samples, stride, err := counter.ParseGPRWCNTR(data)
	if err != nil {
		return nil, err
	}
	records := make([]rawProfilerRecord, len(samples))
	for i, sample := range samples {
		records[i] = rawProfilerRecord{
			Sample: sample, Stride: stride, StreamIndex: streamIndex, RecordIndex: i,
		}
	}
	return records, nil
}

// SerialMapping maps a serial ID to a raw timeline filename.
type SerialMapping struct {
	Serial      uint64
	Filename    string
	SourceIndex int
}

// RawData holds extracted raw data from streamData.
type RawData struct {
	GPRWCNTRRecords []rawProfilerRecord
	Mappings        []SerialMapping
	CounterNames    map[string]string
}

// ParseAPSCounterDataBlob parses Blob 0 content.
func ParseAPSCounterDataBlob(blobData []byte) (map[string]string, error) {
	var inner map[string]interface{}
	if _, err := plist.Unmarshal(blobData, &inner); err != nil {
		return nil, err
	}
	// Traverse to find "Limiter Counter List Map"
	// simplified: dump all strings that look like hashes
	counterMap := make(map[string]string)

	// Helper to walk
	var walk func(interface{})
	walk = func(v interface{}) {
		switch obj := v.(type) {
		case map[string]interface{}:
			// Check for explicit keys
			if listMap, ok := obj["Limiter Counter List Map"]; ok {
				walk(listMap)
			}
			if sampleCtrs, ok := obj["limiter sample counters"]; ok {
				walk(sampleCtrs)
			}
			// Also walk NS.objects if present
			if objs, ok := obj["NS.objects"].([]interface{}); ok {
				for _, o := range objs {
					walk(o)
				}
			}
			// If it's the root dict (with $objects), usage is different in recursive calls?
			// The blob is a plist.
			// Let's assume standard plist structure for Blob 0 as seen in debug output.
			if version, ok := obj["$version"]; ok {
				// Archive structure
				if objs, ok := obj["$objects"].([]interface{}); ok {
					for _, o := range objs {
						walk(o)
					}
				}
				_ = version
			}
		case string:
			if len(obj) == 64 && obj[0] == '_' {
				// It's a hash. Use it as name for now.
				counterMap[obj] = "Counter " + obj[0:8]
			}
		case []interface{}:
			for _, o := range obj {
				walk(o)
			}
		}
	}
	walk(inner)
	return counterMap, nil
}

// ParseAPSTimelineData extracts GPRWCNTR records and file mappings from streamData.
func ParseAPSTimelineData(tracePath string) (*RawData, error) {
	streamDataPath := filepath.Join(tracePath, "streamData") // Assumes inside gpuprofiler_raw
	if _, err := os.Stat(streamDataPath); os.IsNotExist(err) {
		if profilerDir := findProfilerDir(tracePath); profilerDir != "" {
			streamDataPath = filepath.Join(profilerDir, "streamData")
		}
	}

	// Allow passing the direct directory or the .gputrace bundle
	matches, _ := filepath.Glob(filepath.Join(tracePath, "*.gpuprofiler_raw", "streamData"))
	if len(matches) > 0 {
		streamDataPath = matches[0]
	} else if _, err := os.Stat(filepath.Join(tracePath, "streamData")); err == nil {
		streamDataPath = filepath.Join(tracePath, "streamData")
	}

	data, err := os.ReadFile(streamDataPath)
	if err != nil {
		return nil, fmt.Errorf("read streamData: %w", err)
	}

	var archive map[string]interface{}
	if _, err := plist.Unmarshal(data, &archive); err != nil {
		return nil, fmt.Errorf("parse streamData plist: %w", err)
	}

	// Quick and dirty traversal to find APSTimelineData
	// In production this should be more robust
	obj, ok := archive["$objects"].([]interface{})
	if !ok || len(obj) < 2 {
		return nil, fmt.Errorf("invalid plist structure")
	}

	// Try to find APSTimelineData key in root object (usually at index 1)
	rootDict, ok := obj[1].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("root object not a dict")
	}

	atsUid, ok := rootDict["APSTimelineData"].(plist.UID)
	if !ok {
		return nil, fmt.Errorf("APSTimelineData not found in root")
	}

	atsObj_ := obj[int(atsUid)]
	atsObj, ok := atsObj_.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("APSTimelineData obj not a dict")
	}

	nsObjects, ok := atsObj["NS.objects"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("APSTimelineData NS.objects missing")
	}

	rawData := &RawData{
		GPRWCNTRRecords: make([]rawProfilerRecord, 0),
		Mappings:        make([]SerialMapping, 0),
		CounterNames:    make(map[string]string),
	}

	// Parse Blobs
	gprwcntrIndex := 0
	for i, blobRef := range nsObjects {
		uid, ok := blobRef.(plist.UID)
		if !ok {
			continue
		}

		blobDict, ok := obj[int(uid)].(map[string]interface{})
		if !ok {
			continue
		}

		blobData, ok := blobDict["NS.data"].([]byte)
		if !ok {
			continue
		}

		// Blob 0: APSCounterData
		if i == 0 {
			counters, err := ParseAPSCounterDataBlob(blobData)
			if err == nil {
				for k, v := range counters {
					rawData.CounterNames[k] = v
				}
			}
			continue
		}

		// Mappings (Blobs 13+)
		if i >= 13 {
			var inner map[string]interface{}
			if _, err := plist.Unmarshal(blobData, &inner); err == nil {
				if innerTop, ok := inner["$top"].(map[string]interface{}); ok {
					if rootUID, ok := innerTop["root"].(plist.UID); ok {
						if innerObjs, ok := inner["$objects"].([]interface{}); ok {
							rootObj := innerObjs[int(rootUID)].(map[string]interface{})

							// Resolve keys manually (simple approach)
							if keys, ok := rootObj["NS.keys"].([]interface{}); ok {
								var serial uint64
								var filename string
								var sourceIndex int

								vals := rootObj["NS.objects"].([]interface{})
								for kIdx, kRef := range keys {
									kUid := kRef.(plist.UID)
									keyStr := innerObjs[int(kUid)].(string)

									vUid := vals[kIdx].(plist.UID)

									switch keyStr {
									case "Serial":
										if v, ok := plistUint64(innerObjs[int(vUid)]); ok {
											serial = v
										}
									case "APSTraceDataFile":
										if v, ok := innerObjs[int(vUid)].(string); ok {
											filename = v
										}
									case "SourceIndex":
										if v, ok := plistUint64(innerObjs[int(vUid)]); ok {
											sourceIndex = int(v)
										}
									}
								}
								if filename != "" {
									rawData.Mappings = append(rawData.Mappings, SerialMapping{
										Serial: serial, Filename: filename, SourceIndex: sourceIndex,
									})
								}
							}
						}
					}
				}
			}
			continue
		}

		// GPRWCNTR (Blobs 1-11 approx)
		if len(blobData) > 50 {
			var inner map[string]interface{}
			if _, err := plist.Unmarshal(blobData, &inner); err == nil {
				// Check Source == "RDE_0"
				// Need to traverse inner objects to find "Source" key and its value
				if innerObjs, ok := inner["$objects"].([]interface{}); ok {
					isRDE0 := false
					// Find root object to locate Source key
					if top, ok := inner["$top"].(map[string]interface{}); ok {
						if rootUID, ok := top["root"].(plist.UID); ok && int(rootUID) < len(innerObjs) {
							if rootObj, ok := innerObjs[int(rootUID)].(map[string]interface{}); ok {
								if keys, ok := rootObj["NS.keys"].([]interface{}); ok {
									objs := rootObj["NS.objects"].([]interface{})
									for idx, k := range keys {
										if kUID, ok := k.(plist.UID); ok && int(kUID) < len(innerObjs) {
											if kStr, ok := innerObjs[int(kUID)].(string); ok && kStr == "Source" {
												if vUID, ok := objs[idx].(plist.UID); ok && int(vUID) < len(innerObjs) {
													if vStr, ok := innerObjs[int(vUID)].(string); ok && vStr == "RDE_0" {
														isRDE0 = true
													}
												}
											}
										}
									}
								}
							}
						}
					}

					// If not found via root traversal, try heuristic (scan for string "RDE_0")
					// But be careful not to match other things.
					// The root traversal is safer. If RDE_0 is not found, skip.
					// Actually, streamdata.go relies on this.
					if !isRDE0 {
						continue
					}

					for _, innerO := range innerObjs {
						if dataBlob, ok := innerO.([]byte); ok && len(dataBlob) > 8 && string(dataBlob[0:8]) == "GPRWCNTR" {
							recs, err := parseRawProfilerRecords(dataBlob, gprwcntrIndex)
							if err != nil {
								return nil, fmt.Errorf("parse GPRWCNTR stream %d: %w", gprwcntrIndex, err)
							}
							if len(recs) > 0 {
								rawData.GPRWCNTRRecords = append(rawData.GPRWCNTRRecords, recs...)
								gprwcntrIndex++ // Increment index only for RDE_0 profiles
							}
						}
					}
				}
			}
		}
	}

	return rawData, nil
}

func plistUint64(v any) (uint64, bool) {
	switch n := v.(type) {
	case uint64:
		return n, true
	case int64:
		if n < 0 {
			return 0, false
		}
		return uint64(n), true
	case int:
		if n < 0 {
			return 0, false
		}
		return uint64(n), true
	case uint32:
		return uint64(n), true
	case float64:
		if n < 0 {
			return 0, false
		}
		return uint64(n), true
	default:
		return 0, false
	}
}
