// Package gputrace provides parsing and analysis for .gputrace GPU trace files from Metal.
//
// A .gputrace file is a directory bundle containing multiple files that represent
// Metal GPU capture data. This package provides utilities to parse trace metadata,
// extract kernel names, labels, and timing information.
//
// The main entry point is the Open function which returns a Trace:
//
//	trace, err := gputrace.Open("path/to/trace.gputrace")
//	if err != nil {
//		log.Fatal(err)
//	}
//
// The Trace struct provides access to all parsed data and analysis capabilities.
//
// For command-line usage, see cmd/gputrace which provides various subcommands
// for analyzing traces, exporting to different formats, and generating insights.
package gputrace

import (
	"github.com/tmc/mlx-go/experiments/gputrace/internal/shader"
	"github.com/tmc/mlx-go/experiments/gputrace/internal/timing"
	"github.com/tmc/mlx-go/experiments/gputrace/internal/trace"
)

// Re-export main types from internal packages
type (
	Trace               = trace.Trace
	Metadata            = trace.Metadata
	RecordType          = trace.RecordType
	EncoderTiming       = trace.EncoderTiming
	ShaderSourceMapper  = shader.ShaderSourceMapper
	ShaderMetrics       = shader.ShaderMetrics
	ShaderMetricsReport = shader.ShaderMetricsReport
)

// Re-export constants
const (
	RecordTypeCommand      = trace.RecordTypeCommand
	RecordTypeString       = trace.RecordTypeString
	RecordTypeFunction     = trace.RecordTypeFunction
	RecordTypeInteger      = trace.RecordTypeInteger
	RecordTypeUnsignedLong = trace.RecordTypeUnsignedLong
)

// Re-export errors
var (
	ErrInvalidTrace    = trace.ErrInvalidTrace
	ErrInvalidMagic    = trace.ErrInvalidMagic
	ErrMissingMetadata = trace.ErrMissingMetadata
)

// Re-export magic constants
const (
	MagicMTSP   = trace.MagicMTSP
	MagicXDIC   = trace.MagicXDIC
	MagicBPList = trace.MagicBPList
)

// Re-export functions
var (
	ExtractTimingData       = timing.ExtractTimingData
	GenerateSyntheticTiming = timing.GenerateSyntheticTiming
	ExtractShaderMetrics    = shader.ExtractShaderMetrics
	NewShaderSourceMapper   = shader.NewShaderSourceMapper
)

// Open opens and parses a .gputrace bundle.
func Open(path string) (*Trace, error) {
	return trace.Open(path)
}
