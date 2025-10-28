package gputrace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var (
	// ErrInvalidFormat is returned when the trace format is invalid
	ErrInvalidFormat = errors.New("invalid trace format")

	// ErrMissingFile is returned when a required file is missing
	ErrMissingFile = errors.New("required file missing")

	// ErrCorruptedData is returned when data appears corrupted
	ErrCorruptedData = errors.New("corrupted data")
)

// ValidationResult contains the results of trace validation.
type ValidationResult struct {
	Valid    bool
	Errors   []error
	Warnings []string
	Info     TraceInfo
}

// TraceInfo contains summary information about a trace.
type TraceInfo struct {
	Path              string
	TotalSize         int64
	CaptureSize       int
	DeviceResourceSize int
	NumKernels        int
	NumEncoders       int
	NumBuffers        int
	HasMetadata       bool
	HasCapture        bool
	HasDeviceResources bool
	HasStore0         bool
}

// Validate performs comprehensive validation of a trace bundle.
func Validate(path string) (*ValidationResult, error) {
	result := &ValidationResult{
		Valid:  true,
		Info: TraceInfo{
			Path: path,
		},
	}

	// Check if path exists and is a directory
	info, err := os.Stat(path)
	if err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, fmt.Errorf("stat path: %w", err))
		return result, nil
	}

	if !info.IsDir() {
		result.Valid = false
		result.Errors = append(result.Errors, fmt.Errorf("%w: not a directory", ErrInvalidFormat))
		return result, nil
	}

	// Check for required files
	result.checkRequiredFiles()

	// Validate file formats
	result.validateFileFormats(path)

	// Extract summary information
	result.extractSummaryInfo(path)

	return result, nil
}

// checkRequiredFiles checks for presence of required files.
func (r *ValidationResult) checkRequiredFiles() {
	requiredFiles := []string{"metadata", "capture"}

	for _, filename := range requiredFiles {
		filePath := filepath.Join(r.Info.Path, filename)
		if _, err := os.Stat(filePath); err != nil {
			r.Valid = false
			r.Errors = append(r.Errors, fmt.Errorf("%w: %s", ErrMissingFile, filename))

			switch filename {
			case "metadata":
				r.Info.HasMetadata = false
			case "capture":
				r.Info.HasCapture = false
			}
		} else {
			switch filename {
			case "metadata":
				r.Info.HasMetadata = true
			case "capture":
				r.Info.HasCapture = true
			}
		}
	}

	// Check for optional but common files
	optionalFiles := []string{"device-resources-*", "store0"}
	for _, pattern := range optionalFiles {
		matches, _ := filepath.Glob(filepath.Join(r.Info.Path, pattern))
		if len(matches) == 0 {
			r.Warnings = append(r.Warnings, fmt.Sprintf("missing optional file: %s", pattern))

			if pattern == "store0" {
				r.Info.HasStore0 = false
			} else if pattern == "device-resources-*" {
				r.Info.HasDeviceResources = false
			}
		} else {
			if pattern == "store0" {
				r.Info.HasStore0 = true
			} else if pattern == "device-resources-*" {
				r.Info.HasDeviceResources = true
			}
		}
	}
}

// validateFileFormats checks that files have correct format.
func (r *ValidationResult) validateFileFormats(path string) {
	// Validate capture file has MTSP magic
	capturePath := filepath.Join(path, "capture")
	if data, err := os.ReadFile(capturePath); err == nil {
		if len(data) < 4 {
			r.Valid = false
			r.Errors = append(r.Errors, fmt.Errorf("%w: capture file too small", ErrCorruptedData))
		} else if string(data[0:4]) != MagicMTSP {
			r.Valid = false
			r.Errors = append(r.Errors, fmt.Errorf("%w: capture file missing MTSP magic", ErrInvalidFormat))
		}
		r.Info.CaptureSize = len(data)
	}

	// Validate device resources files
	matches, _ := filepath.Glob(filepath.Join(path, "device-resources-*"))
	for _, devPath := range matches {
		if data, err := os.ReadFile(devPath); err == nil {
			if len(data) >= 4 && string(data[0:4]) != MagicMTSP {
				r.Warnings = append(r.Warnings, fmt.Sprintf("device resource file missing MTSP magic: %s", filepath.Base(devPath)))
			}
			r.Info.DeviceResourceSize += len(data)
		}
	}

	// Calculate total size
	r.Info.TotalSize = calculateDirSize(path)
}

// extractSummaryInfo extracts summary information from the trace.
func (r *ValidationResult) extractSummaryInfo(path string) {
	// Try to open and parse the trace
	trace, err := Open(path)
	if err != nil {
		r.Warnings = append(r.Warnings, fmt.Sprintf("could not fully parse trace: %v", err))
		return
	}

	r.Info.NumKernels = len(trace.KernelNames)
	r.Info.NumEncoders = len(trace.EncoderLabels)

	// Count buffers from enhanced metadata
	if meta, err := trace.ExtractEnhancedMetadata(); err == nil {
		r.Info.NumBuffers = len(meta.BufferBindings)
	}
}

// String returns a formatted string representation of the validation result.
func (r *ValidationResult) String() string {
	s := "=== Trace Validation Report ===\n\n"

	// Overall status
	if r.Valid {
		s += "✅ Status: VALID\n"
	} else {
		s += "❌ Status: INVALID\n"
	}
	s += "\n"

	// Errors
	if len(r.Errors) > 0 {
		s += "Errors:\n"
		for _, err := range r.Errors {
			s += fmt.Sprintf("  ❌ %v\n", err)
		}
		s += "\n"
	}

	// Warnings
	if len(r.Warnings) > 0 {
		s += "Warnings:\n"
		for _, warn := range r.Warnings {
			s += fmt.Sprintf("  ⚠️  %s\n", warn)
		}
		s += "\n"
	}

	// Summary information
	s += "Trace Information:\n"
	s += fmt.Sprintf("  Path: %s\n", r.Info.Path)
	s += fmt.Sprintf("  Total Size: %.2f MB\n", float64(r.Info.TotalSize)/(1024*1024))
	s += fmt.Sprintf("  Capture Size: %.2f MB\n", float64(r.Info.CaptureSize)/(1024*1024))
	s += fmt.Sprintf("  Device Resources: %.2f MB\n", float64(r.Info.DeviceResourceSize)/(1024*1024))
	s += "\n"

	s += "Contents:\n"
	s += fmt.Sprintf("  Metadata: %v\n", statusSymbol(r.Info.HasMetadata))
	s += fmt.Sprintf("  Capture: %v\n", statusSymbol(r.Info.HasCapture))
	s += fmt.Sprintf("  Device Resources: %v\n", statusSymbol(r.Info.HasDeviceResources))
	s += fmt.Sprintf("  Store0: %v\n", statusSymbol(r.Info.HasStore0))
	s += "\n"

	if r.Valid {
		s += "Extracted Data:\n"
		s += fmt.Sprintf("  GPU Kernels: %d\n", r.Info.NumKernels)
		s += fmt.Sprintf("  Encoders: %d\n", r.Info.NumEncoders)
		s += fmt.Sprintf("  Buffers: %d\n", r.Info.NumBuffers)
	}

	return s
}

// Helper functions

func statusSymbol(present bool) string {
	if present {
		return "✅ Present"
	}
	return "❌ Missing"
}

func calculateDirSize(path string) int64 {
	var size int64
	filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size
}

// QuickValidate performs a quick validation check without full parsing.
func QuickValidate(path string) error {
	// Check directory exists
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: not a directory", ErrInvalidFormat)
	}

	// Check for required files
	requiredFiles := []string{"metadata", "capture"}
	for _, filename := range requiredFiles {
		filePath := filepath.Join(path, filename)
		if _, err := os.Stat(filePath); err != nil {
			return fmt.Errorf("%w: %s", ErrMissingFile, filename)
		}
	}

	// Check capture file has MTSP magic
	capturePath := filepath.Join(path, "capture")
	data, err := os.ReadFile(capturePath)
	if err != nil {
		return fmt.Errorf("read capture: %w", err)
	}
	if len(data) < 4 || string(data[0:4]) != MagicMTSP {
		return fmt.Errorf("%w: invalid capture format", ErrInvalidFormat)
	}

	return nil
}
