package difftrace

import "sort"

// BuildPipelinePairs pairs pipelines by function name and threadgroup signature.
func BuildPipelinePairs(a, b *TraceData) []PipelinePair {
	left := groupPipelinePairSides(a)
	right := groupPipelinePairSides(b)

	pairs := []PipelinePair{}
	for key, as := range left {
		bs := right[key]
		if len(bs) == 0 {
			continue
		}
		sortPipelinePairSides(as)
		sortPipelinePairSides(bs)
		n := len(as)
		if len(bs) < n {
			n = len(bs)
		}
		for i := 0; i < n; i++ {
			pairs = append(pairs, newPipelinePair(key, as[i], bs[i]))
		}
	}

	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].AbsDeltaUs == pairs[j].AbsDeltaUs {
			if pairs[i].FunctionName == pairs[j].FunctionName {
				if pairs[i].ThreadgroupSig == pairs[j].ThreadgroupSig {
					return pairs[i].APipelineID < pairs[j].APipelineID
				}
				return pairs[i].ThreadgroupSig < pairs[j].ThreadgroupSig
			}
			return pairs[i].FunctionName < pairs[j].FunctionName
		}
		return pairs[i].AbsDeltaUs > pairs[j].AbsDeltaUs
	})
	if pairs == nil {
		return []PipelinePair{}
	}
	return pairs
}

type pipelinePairKey struct {
	functionName   string
	threadgroupSig string
}

type pipelinePairSide struct {
	pipelineID     int
	pipelineHash   string
	totalUs        int
	staticCounters StaticCounters
}

func groupPipelinePairSides(t *TraceData) map[pipelinePairKey][]pipelinePairSide {
	type agg struct {
		pipelinePairSide
	}
	by := map[pipelinePairKey]map[int]*agg{}
	for _, d := range t.Dispatches {
		name := safeFunctionName(d.FunctionName)
		key := pipelinePairKey{functionName: name, threadgroupSig: d.ThreadgroupSig}
		pipelines := by[key]
		if pipelines == nil {
			pipelines = map[int]*agg{}
			by[key] = pipelines
		}
		a := pipelines[d.PipelineID]
		if a == nil {
			info := t.Pipelines[d.PipelineID]
			hash := d.PipelineHash
			if hash == "" {
				hash = info.PipelineHash
			}
			a = &agg{pipelinePairSide: pipelinePairSide{
				pipelineID:     d.PipelineID,
				pipelineHash:   hash,
				staticCounters: info.StaticCounters,
			}}
			pipelines[d.PipelineID] = a
		}
		a.totalUs += d.DurationUs
	}

	out := map[pipelinePairKey][]pipelinePairSide{}
	for key, pipelines := range by {
		for _, side := range pipelines {
			out[key] = append(out[key], side.pipelinePairSide)
		}
	}
	return out
}

func sortPipelinePairSides(sides []pipelinePairSide) {
	sort.Slice(sides, func(i, j int) bool {
		if sides[i].pipelineID == sides[j].pipelineID {
			return sides[i].pipelineHash < sides[j].pipelineHash
		}
		return sides[i].pipelineID < sides[j].pipelineID
	})
}

func newPipelinePair(key pipelinePairKey, a, b pipelinePairSide) PipelinePair {
	delta := a.totalUs - b.totalUs
	return PipelinePair{
		FunctionName:   key.functionName,
		ThreadgroupSig: key.threadgroupSig,
		AUs:            a.totalUs,
		BUs:            b.totalUs,
		AbsDeltaUs:     absInt(delta),
		APipelineID:    a.pipelineID,
		BPipelineID:    b.pipelineID,
		APipelineHash:  a.pipelineHash,
		BPipelineHash:  b.pipelineHash,
		StaticCounterDelta: StaticCounters{
			Instructions: a.staticCounters.Instructions - b.staticCounters.Instructions,
			Registers:    a.staticCounters.Registers - b.staticCounters.Registers,
			Loads:        a.staticCounters.Loads - b.staticCounters.Loads,
			Stores:       a.staticCounters.Stores - b.staticCounters.Stores,
		},
	}
}
