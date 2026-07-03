package difftrace

// AnalyzeEncoderDivergence compares per-encoder wall-time vectors.
func AnalyzeEncoderDivergence(a, b []EncoderInfo, thresholdUs int) EncoderDivergence {
	if thresholdUs <= 0 {
		thresholdUs = 20
	}
	aw := encoderWallTimes(a)
	bw := encoderWallTimes(b)
	n := len(aw)
	if len(bw) > n {
		n = len(bw)
	}
	if len(aw) < n {
		aw = append(aw, make([]int, n-len(aw))...)
	}
	if len(bw) < n {
		bw = append(bw, make([]int, n-len(bw))...)
	}

	first := -1
	for i := 0; i < n; i++ {
		if absInt(aw[i]-bw[i]) > thresholdUs {
			first = i
			break
		}
	}

	return EncoderDivergence{
		AWallUs:                aw,
		BWallUs:                bw,
		FirstDivergentIndex:    first,
		ThresholdUs:            thresholdUs,
		TailSlopeAUsPerEncoder: tailSlope(aw, first),
		TailSlopeBUsPerEncoder: tailSlope(bw, first),
	}
}

func encoderWallTimes(encoders []EncoderInfo) []int {
	max := -1
	for _, enc := range encoders {
		if enc.Index > max {
			max = enc.Index
		}
	}
	if max < 0 {
		return []int{}
	}
	out := make([]int, max+1)
	for _, enc := range encoders {
		if enc.Index >= 0 {
			out[enc.Index] = enc.DurationUs
		}
	}
	return out
}

func tailSlope(v []int, start int) float64 {
	if start < 0 || start >= len(v)-1 {
		return 0
	}
	tail := v[start:]
	n := float64(len(tail))
	var sumX, sumY, sumXY, sumXX float64
	for i, y := range tail {
		x := float64(i)
		fy := float64(y)
		sumX += x
		sumY += fy
		sumXY += x * fy
		sumXX += x * x
	}
	den := n*sumXX - sumX*sumX
	if den == 0 {
		return 0
	}
	return (n*sumXY - sumX*sumY) / den
}
