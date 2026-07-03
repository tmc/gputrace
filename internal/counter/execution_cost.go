package counter

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// ExecutionCostMetrics contains execution cost data per pipeline.
type ExecutionCostMetrics struct {
	PipelineCosts      map[int]float64 // pipelineID -> cost percentage (0-100)
	TotalSamples       int             // Total USC samples found
	SamplesPerPipeline map[int]int     // pipelineID -> sample count
}

// ParseExecutionCost extracts execution cost percentages from Profiling_f_*.raw files.
//
// The Profiling files contain USC (Unified Shader Core) statistical sampling data.
// This function aggregates samples by pipeline ID to compute relative execution cost.
//
// Algorithm:
// 1. Scan all Profiling_f_*.raw files
// 2. Find pipeline IDs (uint32 values matching known pipeline IDs)
// 3. Count samples per pipeline
// 4. Compute cost as percentage of total samples
func ParseExecutionCost(profilerDir string, knownPipelineIDs []int) (*ExecutionCostMetrics, error) {
	// Find all Profiling_f_*.raw files
	files, err := filepath.Glob(filepath.Join(profilerDir, "Profiling_f_*.raw"))
	if err != nil {
		return nil, fmt.Errorf("find profiling files: %w", err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no profiling files found in %s", profilerDir)
	}

	// Create set of known pipeline IDs for fast lookup
	pipelineSet := make(map[uint32]bool)
	for _, pid := range knownPipelineIDs {
		pipelineSet[uint32(pid)] = true
	}

	// Aggregate samples across all files
	samplesPerPipeline := make(map[int]int)
	totalSamples := 0

	for _, file := range files {
		fileSamples, err := countPipelineSamples(file, pipelineSet)
		if err != nil {
			continue // Skip files that fail to parse
		}
		for pid, count := range fileSamples {
			samplesPerPipeline[pid] += count
			totalSamples += count
		}
	}

	if totalSamples == 0 {
		return nil, fmt.Errorf("no pipeline samples found")
	}

	// Compute execution cost percentages
	costs := make(map[int]float64)
	for pid, count := range samplesPerPipeline {
		costs[pid] = float64(count) / float64(totalSamples) * 100.0
	}

	return &ExecutionCostMetrics{
		PipelineCosts:      costs,
		TotalSamples:       totalSamples,
		SamplesPerPipeline: samplesPerPipeline,
	}, nil
}

// countPipelineSamples counts occurrences of each pipeline ID in a profiling file.
func countPipelineSamples(path string, knownPipelines map[uint32]bool) (map[int]int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}

	counts := make(map[int]int)

	// Scan for uint32 values that match known pipeline IDs
	// Pipeline IDs appear throughout the profiling data as part of sample records
	for i := 0; i < len(data)-4; i += 4 {
		val := binary.LittleEndian.Uint32(data[i : i+4])
		if knownPipelines[val] {
			counts[int(val)]++
		}
	}

	return counts, nil
}

// ExtractExecutionCostFromDir parses profiling data and returns execution cost.
// This is a convenience function that finds pipeline IDs from streamData first.
func ExtractExecutionCostFromDir(profilerDir string) (*ExecutionCostMetrics, error) {
	// First parse streamData to get known pipeline IDs
	stats, err := ParseStreamData(profilerDir, nil)
	if err != nil {
		return nil, fmt.Errorf("parse streamData for pipeline IDs: %w", err)
	}

	var pipelineIDs []int
	for _, p := range stats.Pipelines {
		pipelineIDs = append(pipelineIDs, p.PipelineID)
	}

	if len(pipelineIDs) == 0 {
		return nil, fmt.Errorf("no pipelines found in streamData")
	}

	return ParseExecutionCost(profilerDir, pipelineIDs)
}

// ExecutionCostByFunction aggregates execution cost by function name.
type ExecutionCostByFunction struct {
	FunctionName string
	CostPercent  float64
	PipelineIDs  []int
	SampleCount  int
}

// AggregateExecutionCostByFunction groups execution costs by function name.
func AggregateExecutionCostByFunction(
	costs *ExecutionCostMetrics,
	pipelines []PipelineStats,
) []ExecutionCostByFunction {
	// Build map from pipeline ID to function name
	pipelineToFunc := make(map[int]string)
	for _, p := range pipelines {
		pipelineToFunc[p.PipelineID] = p.FunctionName
	}

	// Aggregate costs by function
	funcCosts := make(map[string]*ExecutionCostByFunction)
	for pid, cost := range costs.PipelineCosts {
		funcName := pipelineToFunc[pid]
		if funcName == "" {
			funcName = fmt.Sprintf("(pipeline_%d)", pid)
		}

		if fc, ok := funcCosts[funcName]; ok {
			fc.CostPercent += cost
			fc.PipelineIDs = append(fc.PipelineIDs, pid)
			fc.SampleCount += costs.SamplesPerPipeline[pid]
		} else {
			funcCosts[funcName] = &ExecutionCostByFunction{
				FunctionName: funcName,
				CostPercent:  cost,
				PipelineIDs:  []int{pid},
				SampleCount:  costs.SamplesPerPipeline[pid],
			}
		}
	}

	// Convert to sorted slice
	var result []ExecutionCostByFunction
	for _, fc := range funcCosts {
		result = append(result, *fc)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].CostPercent > result[j].CostPercent
	})

	return result
}

// ProfilingExecutionCost represents per-pipeline execution cost from statistical profiling.
type ProfilingExecutionCost struct {
	PipelineID    int
	FunctionName  string
	CostPercent   float64 // 0-100
	SampleCount   int
	LimiterValues map[string]float64 // Optional: extracted limiter metrics
}
