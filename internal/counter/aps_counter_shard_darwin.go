//go:build darwin

package counter

import "github.com/tmc/gputrace/internal/agxps"

// DecodeAPSCounterShard decodes the safely copyable shape of one
// Counters_f_*.raw file. It does not decode sample values; see
// [ErrAPSCounterValuesBinding].
func DecodeAPSCounterShard(data []byte, config APSGPUConfig) (*APSCounterShard, error) {
	shape, err := agxps.DecodeCounterProfileShape(data, agxps.CounterDecodeConfig{
		Generation: config.Generation, Variant: config.Variant, Revision: config.Revision,
		UarchBehaviour: config.CounterUarchBehaviour,
	})
	if err != nil {
		return nil, err
	}
	return assembleAPSCounterShard(shape.SystemTimestamps, shape.CounterIDs,
		shape.CounterValueNums, shape.CounterGroupIDs, shape.KickCount, shape.ParsedTokens, shape.ParsedBits)
}
