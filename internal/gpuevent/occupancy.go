package gpuevent

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Occupancy inputs per GB100-class SM (Blackwell consumer/datacenter
// defaults). These bound the *theoretical* occupancy calculation; they are
// not measured. Provenance: [H] heuristic — device constants, real limits
// vary by part.
const (
	maxThreadsPerSM   = 1536 // Blackwell GB10: 1536 threads/SM (12 warps... conservative 48*32)
	maxBlocksPerSM    = 24
	maxRegsPerThread  = 255
	sharedMemPerSM    = 228 * 1024 // max shared memory per SM, bytes
	warpSize          = 32
	regFilePerSM      = 64 * 1024 // 64K 32-bit registers per SM
)

// TheoreticalOccupancy estimates the fraction of an SM's maximum warp
// slots this kernel's launch geometry can fill, from the three classic
// limiters: threads/blocks per SM, register file, and shared memory.
//
// Provenance [H]: computed from recorded launch attributes (registers,
// block size, shared_mem) against architectural constants above. It is a
// ceiling on achieved occupancy, not a measurement; treat <50% as "worth
// investigating", not "wasted".
func TheoreticalOccupancy(e Event) (float64, string) { // returns pct, limiter
	if e.Registers <= 0 || e.Block == "" {
		return 0, ""
	}
	blockDim := parseBlockX(e.Block)
	if blockDim <= 0 {
		return 0, ""
	}

	blocksByThreads := maxThreadsPerSM / blockDim

	blocksByRegs := math.MaxInt
	if e.Registers > 0 {
		rpb := blockDim * roundUpRegs(e.Registers)
		blocksByRegs = regFilePerSM / rpb
	}

	blocksByShared := maxBlocksPerSM
	if e.SharedMem > 0 {
		blocksByShared = sharedMemPerSM / e.SharedMem
	}

	blocks := min3(blocksByThreads, blocksByRegs, blocksByShared)
	if blocks > maxBlocksPerSM {
		blocks = maxBlocksPerSM
	}
	if blocks == 0 {
		return 0, "block-size" // blockDim > maxThreadsPerSM: launch would fail
	}
	activeWarps := blocks * blockDim / warpSize
	maxWarps := maxThreadsPerSM / warpSize
	pct := 100 * float64(activeWarps) / float64(maxWarps)

	limiter := "threads"
	if blocks == blocksByRegs && blocksByRegs <= blocksByThreads && blocksByRegs <= blocksByShared {
		limiter = "registers"
	} else if blocks == blocksByShared && blocksByShared < blocksByThreads {
		limiter = "shared-memory"
	}
	return pct, limiter
}

func roundUpRegs(r int) int {
	// Register allocation granularity is typically 8 registers/thread.
	return ((r + 7) / 8) * 8
}

func parseBlockX(block string) int {
	x, _, _ := strings.Cut(block, "x")
	n, err := strconv.Atoi(x)
	if err != nil {
		return 0
	}
	return n
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}

// OccupancySummary summarizes occupancy across one kernel's launches.
type OccupancySummary struct {
	MeanPct  float64 `json:"mean_pct"`
	Limiter  string  `json:"limiter"`
	Samples  int     `json:"samples"`
}

// SummarizeOccupancy computes mean theoretical occupancy for each kernel
// that carries the needed attributes.
func SummarizeOccupancy(events []Event) map[string]OccupancySummary {
	sums := map[string]*struct {
		total float64
		n     int
		limiter string
	}{}
	for _, e := range events {
		if e.Kind != KindKernel {
			continue
		}
		pct, limiter := TheoreticalOccupancy(e)
		if limiter == "" || limiter == "block-size" {
			continue
		}
		key := e.Name
		if key == "" {
			key = e.RawSymbol
		}
		s := sums[key]
		if s == nil {
			s = &struct {
				total   float64
				n       int
				limiter string
			}{}
			sums[key] = s
		}
		s.total += pct
		s.n++
		s.limiter = limiter
	}
	out := make(map[string]OccupancySummary, len(sums))
	for k, s := range sums {
		out[k] = OccupancySummary{MeanPct: s.total / float64(s.n), Limiter: s.limiter, Samples: s.n}
	}
	return out
}

// PageableTransferStats quantifies memcpy time spent moving pageable host
// memory, the cheapest large win in most pipelines (pin the buffers).
type PageableTransferStats struct {
	CopyCount    int    `json:"copy_count"`
	PageableNS   uint64 `json:"pageable_ns"`
	TotalCopyNS  uint64 `json:"total_copy_ns"`
	PageablePct  float64 `json:"pageable_pct"`
	ExampleBytes uint64 `json:"example_bytes"`
}

// AnalyzePageableTransfers sums memcpy durations touching pageable memory.
// Provenance [V]: src_kind/dst_kind are recorded by the shim from CUPTI's
// enum directly.
func AnalyzePageableTransfers(events []Event) PageableTransferStats {
	var st PageableTransferStats
	for _, e := range events {
		if e.Kind != KindMemcpy {
			continue
		}
		st.CopyCount++
		d := e.DurationNS()
		st.TotalCopyNS += d
		if e.SrcKind == "pageable" || e.DstKind == "pageable" {
			st.PageableNS += d
			st.ExampleBytes = e.Bytes
		}
	}
	if st.TotalCopyNS > 0 {
		st.PageablePct = 100 * float64(st.PageableNS) / float64(st.TotalCopyNS)
	}
	return st
}

var _ = fmt.Sprintf // keep fmt for future evidence strings
