package difftrace

const SchemaVersion = "gputrace.diff.v2"

// TraceData is parsed dispatch-level timing data for a single trace.
type TraceData struct {
	Path       string
	Label      string
	Dispatches []Dispatch
	Encoders   []EncoderInfo
	Warnings   []string
}

// Dispatch is one GPU dispatch entry from streamData.
type Dispatch struct {
	SourceIndex    int
	FunctionName   string
	FunctionKey    string
	KernelID       string
	PipelineHash   string
	ThreadgroupSig string
	PipelineID     int
	PipelineIndex  int
	EncoderIndex   int
	DurationUs     int
	CumulativeUs   int
}

// EncoderInfo summarizes timing for one encoder.
type EncoderInfo struct {
	Index         int
	Label         string
	DurationUs    int
	DispatchCount int
}

// AlignOptions controls dispatch matching.
type AlignOptions struct {
	OnlyEncoder         int
	OnlyFunction        string
	MinDeltaUs          int
	SequenceDPCellLimit int
}

// ReportOptions controls report shaping and render limits.
type ReportOptions struct {
	Limit      int
	MinDeltaUs int
}

// AlignmentResult is the result of matching two dispatch streams.
type AlignmentResult struct {
	TraceA     []Dispatch
	TraceB     []Dispatch
	Matches    []MatchPair
	UnmatchedA []Dispatch
	UnmatchedB []Dispatch
}

// MatchPair is one matched dispatch pair.
type MatchPair struct {
	SourceIndexA    int     `json:"source_index_a"`
	SourceIndexB    int     `json:"source_index_b"`
	FunctionName    string  `json:"function_name"`
	KernelID        string  `json:"kernel_id,omitempty"`
	EncoderIndex    int     `json:"encoder_index"`
	PipelineIDA     int     `json:"pipeline_id_a"`
	PipelineIDB     int     `json:"pipeline_id_b"`
	PipelineHashA   string  `json:"pipeline_hash_a,omitempty"`
	PipelineHashB   string  `json:"pipeline_hash_b,omitempty"`
	ThreadgroupSigA string  `json:"threadgroup_signature_a,omitempty"`
	ThreadgroupSigB string  `json:"threadgroup_signature_b,omitempty"`
	DurationAUs     int     `json:"trace_a_us"`
	DurationBUs     int     `json:"trace_b_us"`
	DeltaUs         int     `json:"delta_us"`
	MatchMethod     string  `json:"match_method"`
	Confidence      float64 `json:"confidence"`
}

// UnmatchedDispatch is a dispatch that exists in only one trace after alignment.
type UnmatchedDispatch struct {
	Trace          string `json:"trace"`
	SourceIndex    int    `json:"source_index"`
	FunctionName   string `json:"function_name"`
	KernelID       string `json:"kernel_id,omitempty"`
	EncoderIndex   int    `json:"encoder_index"`
	PipelineID     int    `json:"pipeline_id"`
	PipelineHash   string `json:"pipeline_hash,omitempty"`
	ThreadgroupSig string `json:"threadgroup_signature,omitempty"`
	DurationUs     int    `json:"duration_us"`
}

// FunctionDelta summarizes cost deltas by function.
type FunctionDelta struct {
	FunctionName           string `json:"function_name"`
	DispatchCountA         int    `json:"dispatch_count_a"`
	DispatchCountB         int    `json:"dispatch_count_b"`
	DispatchCountDelta     int    `json:"dispatch_count_delta"`
	MatchedPairs           int    `json:"matched_pairs"`
	TotalAUs               int    `json:"total_a_us"`
	TotalBUs               int    `json:"total_b_us"`
	TotalDeltaUs           int    `json:"total_delta_us"`
	FirstOccurrenceDeltaUs int    `json:"first_occurrence_delta_us"`
	MaxOccurrenceDeltaUs   int    `json:"max_occurrence_delta_us"`
}

// EncoderDelta summarizes cost deltas by encoder.
type EncoderDelta struct {
	EncoderIndex       int `json:"encoder_index"`
	DispatchCountA     int `json:"dispatch_count_a"`
	DispatchCountB     int `json:"dispatch_count_b"`
	DispatchCountDelta int `json:"dispatch_count_delta"`
	TotalAUs           int `json:"total_a_us"`
	TotalBUs           int `json:"total_b_us"`
	TotalDeltaUs       int `json:"total_delta_us"`
}

// EncoderReport is an encoder-scoped report with matched/unmatched split.
type EncoderReport struct {
	EncoderIndex     int         `json:"encoder_index"`
	DispatchCountA   int         `json:"dispatch_count_a"`
	DispatchCountB   int         `json:"dispatch_count_b"`
	MatchedCount     int         `json:"matched_count"`
	MatchedDeltaUs   int         `json:"matched_delta_us"`
	UnmatchedCountA  int         `json:"unmatched_count_a"`
	UnmatchedCountB  int         `json:"unmatched_count_b"`
	UnmatchedCount   int         `json:"unmatched_count"`
	UnmatchedDeltaUs int         `json:"unmatched_delta_us"`
	TopDispatches    []MatchPair `json:"top_dispatches"`
}

// PipelineDelta summarizes cost deltas by pipeline ID.
type PipelineDelta struct {
	PipelineID         int    `json:"pipeline_id"`
	FunctionName       string `json:"function_name,omitempty"`
	DispatchCountA     int    `json:"dispatch_count_a"`
	DispatchCountB     int    `json:"dispatch_count_b"`
	DispatchCountDelta int    `json:"dispatch_count_delta"`
	TotalAUs           int    `json:"total_a_us"`
	TotalBUs           int    `json:"total_b_us"`
	TotalDeltaUs       int    `json:"total_delta_us"`
}

// UnnamedDispatchDelta summarizes deltas for unnamed dispatches grouped by pipeline.
type UnnamedDispatchDelta struct {
	KernelID           string `json:"kernel_id"`
	PipelineID         int    `json:"pipeline_id"`
	PipelineHash       string `json:"pipeline_hash,omitempty"`
	ThreadgroupSig     string `json:"threadgroup_signature,omitempty"`
	DispatchCountA     int    `json:"dispatch_count_a"`
	DispatchCountB     int    `json:"dispatch_count_b"`
	DispatchCountDelta int    `json:"dispatch_count_delta"`
	TotalAUs           int    `json:"total_a_us"`
	TotalBUs           int    `json:"total_b_us"`
	TotalDeltaUs       int    `json:"total_delta_us"`
	TopOutlierDeltaUs  int    `json:"top_outlier_delta_us"`
	TopOutlierSourceA  int    `json:"top_outlier_source_index_a"`
	TopOutlierSourceB  int    `json:"top_outlier_source_index_b"`
}

// SpikeWindow summarizes contiguous outlier windows.
type SpikeWindow struct {
	EncoderIndex      int `json:"encoder_index"`
	StartSourceIndexA int `json:"start_source_index_a"`
	EndSourceIndexA   int `json:"end_source_index_a"`
	StartSourceIndexB int `json:"start_source_index_b"`
	EndSourceIndexB   int `json:"end_source_index_b"`
	MatchCount        int `json:"match_count"`
	TotalDeltaUs      int `json:"total_delta_us"`
	MaxAbsDeltaUs     int `json:"max_abs_delta_us"`
}

// Summary is top-level diagnostics.
type Summary struct {
	TraceALabel        string `json:"trace_a_label"`
	TraceBLabel        string `json:"trace_b_label"`
	DispatchCountA     int    `json:"dispatch_count_a"`
	DispatchCountB     int    `json:"dispatch_count_b"`
	DispatchCountDelta int    `json:"dispatch_count_delta"`
	TotalGPUTimeAUs    int    `json:"total_gpu_time_a_us"`
	TotalGPUTimeBUs    int    `json:"total_gpu_time_b_us"`
	TotalDeltaUs       int    `json:"total_delta_us"`
	MatchedDeltaUs     int    `json:"matched_delta_us"`
	UnmatchedDeltaUs   int    `json:"unmatched_delta_us"`
	LikelyCause        string `json:"likely_cause"`
}

// EncoderDivergence summarizes the first material encoder timing split.
type EncoderDivergence struct {
	AWallUs                []int   `json:"a_wall_us"`
	BWallUs                []int   `json:"b_wall_us"`
	FirstDivergentIndex    int     `json:"first_divergent_index"`
	ThresholdUs            int     `json:"threshold_us"`
	TailSlopeAUsPerEncoder float64 `json:"tail_slope_a_us_per_encoder"`
	TailSlopeBUsPerEncoder float64 `json:"tail_slope_b_us_per_encoder"`
}

// Report is the complete diff result with a stable JSON schema.
type Report struct {
	SchemaVersion         string                 `json:"schema_version"`
	TraceAPath            string                 `json:"trace_a_path"`
	TraceBPath            string                 `json:"trace_b_path"`
	Summary               Summary                `json:"summary"`
	TopFunctionDeltas     []FunctionDelta        `json:"top_function_deltas"`
	TopDispatchOutliers   []MatchPair            `json:"top_dispatch_outliers"`
	EncoderDeltas         []EncoderDelta         `json:"encoder_deltas"`
	EncoderReports        []EncoderReport        `json:"encoder_reports"`
	PipelineDeltas        []PipelineDelta        `json:"pipeline_deltas"`
	UnnamedDispatchDeltas []UnnamedDispatchDelta `json:"unnamed_dispatch_deltas"`
	TimelineSpikeWindows  []SpikeWindow          `json:"timeline_spike_windows"`
	OccurrenceMatches     []OccurrenceMatch      `json:"occurrence_matches"`
	MatchedPairs          []MatchPair            `json:"matched_pairs"`
	Unmatched             []UnmatchedDispatch    `json:"unmatched"`
	EncoderDivergence     *EncoderDivergence     `json:"encoder_divergence,omitempty"`
	Warnings              []string               `json:"warnings,omitempty"`
}

func safeFunctionName(name string) string {
	if name == "" {
		return "(unnamed)"
	}
	return name
}

func kernelIdentity(functionName, pipelineHash, threadgroupSig string) string {
	name := normalizeName(functionName)
	if threadgroupSig == "" {
		threadgroupSig = "unknown"
	}
	if pipelineHash == "" {
		pipelineHash = "none"
	}
	if name != "" {
		return "fn:" + name
	}
	return "unnamed:ph=" + pipelineHash + ":tg=" + threadgroupSig
}
