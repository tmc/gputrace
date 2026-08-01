//go:build darwin

package counter

// Manual probe: does streamData's command buffer timeline share a clock with
// the system timestamps inside Counters_f_*.raw?
//
// The counter series decode to samples stamped with an index into the APS
// system timestamp table, whose raw values are mach absolute ticks
// (agxps_aps_system_timestamp_to_nanoseconds computes ticks*1000/24). If
// APSTimelineData's command buffer ticks are the same clock, the counter series
// can be windowed per command buffer, and per encoder if encoder bounds exist
// in the same units.
//
// Runs only when GPUTRACE_PROBE_STREAMDATA names a .gpuprofiler_raw directory.

import (
	"fmt"
	"os"
	"testing"

	"github.com/tmc/apple/x/plist"
)

func TestStreamDataTimebaseProbe(t *testing.T) {
	dir := os.Getenv("GPUTRACE_PROBE_STREAMDATA")
	if dir == "" {
		t.Skip("set GPUTRACE_PROBE_STREAMDATA to a .gpuprofiler_raw directory")
	}
	stats, err := ParseStreamData(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("encoders=%d gpuCommands=%d pipelines=%d dispatches=%d totalEncoderUs=%d",
		stats.NumEncoders, stats.NumGPUCommands, stats.NumPipelines, len(stats.Dispatches), stats.TotalEncoderTimeUs)
	if stats.EffectiveGPUTimeUs != nil {
		t.Logf("effective GPU time = %d us", *stats.EffectiveGPUTimeUs)
	}
	tl := stats.Timeline
	if tl == nil {
		t.Fatalf("no APSTimelineData timeline")
	}
	t.Logf("timebase %d/%d absoluteTime=%d continuousTime=%d replayerGPUTime=%dns cbActive=%dns cbWall=%dns",
		tl.TimebaseNumer, tl.TimebaseDenom, tl.AbsoluteTime, tl.ContinuousTime,
		tl.ReplayerGPUTimeNs, tl.CommandBufferActiveNs, tl.CommandBufferWallNs)

	// No truncation: every command buffer, in order, with its span in ticks and
	// the wall gap to the previous one.
	var prevEnd uint64
	var activeTicks uint64
	for i, cb := range tl.CommandBufferTimestamps {
		gap := int64(0)
		if i > 0 {
			gap = int64(cb.StartTicks) - int64(prevEnd)
		}
		activeTicks += cb.EndTicks - cb.StartTicks
		t.Logf("  cb[%2d] start=%d end=%d span=%d ticks (%.3f ms) gapFromPrev=%d ticks",
			cb.Index, cb.StartTicks, cb.EndTicks, cb.EndTicks-cb.StartTicks,
			float64(cb.EndTicks-cb.StartTicks)*1000/24/1e6, gap)
		prevEnd = cb.EndTicks
	}
	if n := len(tl.CommandBufferTimestamps); n > 0 {
		first := tl.CommandBufferTimestamps[0]
		last := tl.CommandBufferTimestamps[n-1]
		t.Logf("  CB wall span %d ticks (%.3f ms), CB active %d ticks (%.3f ms)",
			last.EndTicks-first.StartTicks, float64(last.EndTicks-first.StartTicks)*1000/24/1e6,
			activeTicks, float64(activeTicks)*1000/24/1e6)
	}

	// Encoder timings carry a cumulative end offset in microseconds, which is
	// the oracle's join key. Print all of them so the key can be matched
	// against the oracle rows without guessing.
	t.Logf("encoder timings (%d):", len(stats.EncoderTimings))
	for _, e := range stats.EncoderTimings {
		t.Logf("  enc[%2d] seq=%d startTs=%d endOffsetUs=%d durationUs=%d",
			e.Index, e.SequenceID, e.StartTimestamp, e.EndOffsetMicros, e.DurationMicros)
	}

	t.Logf("encoder profiles (%d):", len(tl.EncoderProfiles))
	for _, p := range tl.EncoderProfiles {
		t.Logf("  prof[%2d] source=%s ring=%d samples=%d start=%d end=%d duration=%dns",
			p.Index, p.Source, p.RingBufferIndex, p.SampleCount, p.StartTicks, p.EndTicks, p.DurationNs)
	}
}

// logValue prints one archived value, recursing into nested NSDictionary and
// NSArray nodes up to depth. It does not cap the number of entries at a level:
// the fields being hunted here (AbsoluteTimeOffset, ContinuousTimeOffset,
// SystemTimePeriod) are buried in nested config dictionaries, and a helper that
// stopped early would hide exactly what it was written to find.
func logValue(t *testing.T, objects []any, key string, val any, indent string, depth int) {
	t.Helper()
	m, ok := val.(map[string]any)
	if !ok {
		if b, ok := val.([]byte); ok {
			t.Logf("%s%-32s %d bytes", indent, key, len(b))
			return
		}
		t.Logf("%s%-32s %v (%T)", indent, key, val, val)
		return
	}
	if d, ok := m["NS.data"].([]byte); ok {
		t.Logf("%s%-32s NS.data %d bytes", indent, key, len(d))
		return
	}
	keys, hasKeys := m["NS.keys"].([]any)
	vals, hasVals := m["NS.objects"].([]any)
	switch {
	case hasKeys && hasVals && len(keys) == len(vals):
		t.Logf("%s%-32s dict[%d]", indent, key, len(keys))
		if depth == 0 {
			return
		}
		for i := range keys {
			ku, ok := keys[i].(plist.UID)
			if !ok || int(ku) >= len(objects) {
				continue
			}
			sub, _ := objects[int(ku)].(string)
			vu, ok := vals[i].(plist.UID)
			if !ok || int(vu) >= len(objects) {
				t.Logf("%s  %-30s <no value>", indent, sub)
				continue
			}
			logValue(t, objects, sub, objects[int(vu)], indent+"  ", depth-1)
		}
	case hasVals:
		t.Logf("%s%-32s array[%d]", indent, key, len(vals))
		if depth == 0 {
			return
		}
		for i := range vals {
			vu, ok := vals[i].(plist.UID)
			if !ok || int(vu) >= len(objects) {
				continue
			}
			logValue(t, objects, fmt.Sprintf("[%d]", i), objects[int(vu)], indent+"  ", depth-1)
		}
	default:
		t.Logf("%s%-32s dict (opaque)", indent, key)
	}
}

// TestAPSTimelineKeysProbe dumps every key and value of every APSTimelineData
// blob, at full length. The parser reads only a handful of them, and the
// archive's own clock-alignment fields -- AbsoluteTimeOffset,
// ContinuousTimeOffset and SystemTimePeriod, all visible in the raw strings --
// are not among the ones it reads. They are the candidate sync point for
// reconciling APS system timestamps with command buffer ticks.
func TestAPSTimelineKeysProbe(t *testing.T) {
	dir := os.Getenv("GPUTRACE_PROBE_STREAMDATA")
	if dir == "" {
		t.Skip("set GPUTRACE_PROBE_STREAMDATA to a .gpuprofiler_raw directory")
	}
	stats, err := ParseStreamData(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d APSTimelineData blobs", len(stats.APSTimelineData))
	for i, blob := range stats.APSTimelineData {
		var archive map[string]any
		if _, err := plist.Unmarshal(blob, &archive); err != nil {
			t.Logf("blob[%d] %d bytes: not a plist (%v)", i, len(blob), err)
			continue
		}
		objects, _ := archive["$objects"].([]any)
		top, _ := archive["$top"].(map[string]any)
		rootUID, ok := top["root"].(plist.UID)
		if !ok || int(rootUID) >= len(objects) {
			t.Logf("blob[%d] %d bytes: no root", i, len(blob))
			continue
		}
		root, ok := objects[int(rootUID)].(map[string]any)
		if !ok {
			continue
		}
		keys, _ := root["NS.keys"].([]any)
		vals, _ := root["NS.objects"].([]any)
		t.Logf("blob[%d] %d bytes, %d keys", i, len(blob), len(keys))
		for j := range keys {
			ku, ok := keys[j].(plist.UID)
			if !ok || int(ku) >= len(objects) {
				continue
			}
			key, _ := objects[int(ku)].(string)
			vu, ok := vals[j].(plist.UID)
			if !ok || int(vu) >= len(objects) {
				t.Logf("    %-32s <no value>", key)
				continue
			}
			logValue(t, objects, key, objects[int(vu)], "    ", 3)
		}
	}
}
