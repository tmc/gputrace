package counter

import "errors"

// ErrAPSCounterValuesBinding reports that GTShaderProfiler provides no safe
// accessor for copying the samples of one decoded counter series.
//
// [V] Xcode 26.4's get_counter_values accessors copy framework-owned
// std::vector<uint64_t> data pointers, not sample values. The complete exported
// counter accessor surface has no indexed-sample or bulk sample-copy function.
// Reproduce with:
//
//	nm -gU GTShaderProfiler | grep agxps_aps_profile_data_get_counter
//
// and disassemble agxps_aps_profile_data_get_counter_values at 0x4edce4.
var ErrAPSCounterValuesBinding = errors.New("counter: GTShaderProfiler has no safe counter sample copy binding")

// APSGPUConfig identifies the GPU used to decode an APS counter shard.
// Values must come from the capture or device; the decoder does not guess them.
type APSGPUConfig struct {
	Generation uint32
	Variant    uint32
	Revision   uint32

	// CounterUarchBehaviour is passed through to the APS descriptor. [?] Its
	// selection rule is not established, so callers must provide it explicitly.
	CounterUarchBehaviour int32
}

// APSCounterSeriesShape describes one decoded series without exposing its
// values. It is intentionally not a metric row: owner, unit, and value
// semantics remain unestablished.
type APSCounterSeriesShape struct {
	// CounterID is copied from get_counter_names. [V] The accessor copies one
	// uint64 per series; the semantic name and unit are not established here.
	CounterID uint64

	// GroupID is copied from get_counter_group_id. [V] Disassembly shows that
	// this accessor writes one byte per series.
	GroupID uint8

	// SampleCount is copied from get_counter_values_num. [V] It is the length
	// of the framework-owned uint64 sample vector.
	SampleCount uint64
}

// APSCounterShard is the safely copied shape of one Counters_f_*.raw decode.
// It has no range endpoints: [?] timestamp ordering has not been established
// across captures. SystemTimestamps preserves the complete decoded order, and
// TimestampDescents makes a violated monotonicity assumption observable.
type APSCounterShard struct {
	SystemTimestamps  []uint64
	TimestampDescents int
	Series            []APSCounterSeriesShape
	KickCount         uint64
	ParsedTokens      uint64
	ParsedBits        uint64
}

// CounterValues returns the explicit binding gap instead of an empty value
// slice. Empty values would be indistinguishable from a successfully decoded
// all-zero or zero-length series.
func (s *APSCounterShard) CounterValues(series int) ([]uint64, error) {
	if s == nil {
		return nil, errors.New("counter: nil APS counter shard")
	}
	if series < 0 || series >= len(s.Series) {
		return nil, errors.New("counter: APS counter series index out of range")
	}
	return nil, ErrAPSCounterValuesBinding
}

func assembleAPSCounterShard(timestamps, counterIDs, sampleCounts []uint64, groupIDs []uint8, kicks, tokens, bits uint64) (*APSCounterShard, error) {
	if len(counterIDs) != len(sampleCounts) || len(counterIDs) != len(groupIDs) {
		return nil, errors.New("counter: inconsistent APS counter series arrays")
	}
	shard := &APSCounterShard{
		SystemTimestamps: append([]uint64(nil), timestamps...),
		Series:           make([]APSCounterSeriesShape, len(counterIDs)),
		KickCount:        kicks,
		ParsedTokens:     tokens,
		ParsedBits:       bits,
	}
	for i := 1; i < len(timestamps); i++ {
		if timestamps[i] < timestamps[i-1] {
			shard.TimestampDescents++
		}
	}
	for i := range counterIDs {
		shard.Series[i] = APSCounterSeriesShape{
			CounterID: counterIDs[i], GroupID: groupIDs[i], SampleCount: sampleCounts[i],
		}
	}
	return shard, nil
}
