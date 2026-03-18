package shader

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ShaderSourceMapper helps map kernel names to Metal shader source files.
type ShaderSourceMapper struct {
	// Map from kernel name to source file path
	kernelToFile map[string]string
	// Map from kernel name to line number
	kernelToLine map[string]int
	// Search paths for .metal files
	searchPaths []string
}

// NewShaderSourceMapper creates a new source mapper with default search paths.
func NewShaderSourceMapper(searchPaths ...string) *ShaderSourceMapper {
	mapper := &ShaderSourceMapper{
		kernelToFile: make(map[string]string),
		kernelToLine: make(map[string]int),
		searchPaths:  searchPaths,
	}

	// Add default search paths if none provided
	if len(searchPaths) == 0 {
		if env := os.Getenv("GPUTRACE_SHADER_SEARCH_PATHS"); env != "" {
			mapper.searchPaths = append(mapper.searchPaths, filepath.SplitList(env)...)
		}
		// Common MLX locations
		mapper.searchPaths = append(mapper.searchPaths,
			"/opt/homebrew/Cellar/mlx-c/*/include/mlx/backend/metal",
			"./mlx/backend/metal",
			"../mlx/backend/metal",
		)
	}

	return mapper
}

// IndexShaderSources scans search paths and indexes kernel definitions.
func (m *ShaderSourceMapper) IndexShaderSources() error {
	for _, searchPath := range m.searchPaths {
		// Expand glob patterns
		matches, err := filepath.Glob(searchPath)
		if err != nil {
			continue
		}

		for _, path := range matches {
			if err := m.scanDirectory(path); err != nil {
				continue
			}
		}
	}

	return nil
}

// scanDirectory recursively scans for .metal files.
func (m *ShaderSourceMapper) scanDirectory(dir string) error {
	return filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}

		if !info.IsDir() && strings.HasSuffix(path, ".metal") {
			m.indexMetalFile(path)
		}

		return nil
	})
}

// indexMetalFile parses a .metal file and indexes kernel definitions.
func (m *ShaderSourceMapper) indexMetalFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// Regular expressions for Metal kernel definitions
	kernelRegex := regexp.MustCompile(`kernel\s+void\s+(\w+)\s*\(`)
	funcRegex := regexp.MustCompile(`^\s*(?:inline\s+)?(?:device\s+|constant\s+)?(?:void|float|int|half|uint)\s+(\w+)\s*\(`)

	scanner := bufio.NewScanner(f)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		// Check for kernel definitions
		if matches := kernelRegex.FindStringSubmatch(line); len(matches) > 1 {
			kernelName := matches[1]
			m.kernelToFile[kernelName] = path
			m.kernelToLine[kernelName] = lineNum
		}

		// Also check for regular function definitions (helper functions)
		if matches := funcRegex.FindStringSubmatch(line); len(matches) > 1 {
			funcName := matches[1]
			// Only add if not already mapped (kernels take precedence)
			if _, exists := m.kernelToFile[funcName]; !exists {
				m.kernelToFile[funcName] = path
				m.kernelToLine[funcName] = lineNum
			}
		}
	}

	return scanner.Err()
}

// GetSourceLocation returns the source file and line number for a kernel.
// Returns empty string and 0 if not found.
func (m *ShaderSourceMapper) GetSourceLocation(kernelName string) (file string, line int) {
	// Try exact match first
	if file, ok := m.kernelToFile[kernelName]; ok {
		return file, m.kernelToLine[kernelName]
	}

	// Try fuzzy matching - kernel names may have type suffixes
	// e.g., "rope_single_freqs_float16" might map to "rope_single_freqs"
	for knownKernel, file := range m.kernelToFile {
		if strings.Contains(kernelName, knownKernel) || strings.Contains(knownKernel, kernelName) {
			return file, m.kernelToLine[knownKernel]
		}
	}

	// Try removing common suffixes
	baseName := stripTypeSuffixes(kernelName)
	if file, ok := m.kernelToFile[baseName]; ok {
		return file, m.kernelToLine[baseName]
	}

	return "", 0
}

// stripTypeSuffixes removes common Metal type suffixes from kernel names.
func stripTypeSuffixes(name string) string {
	// Common type suffixes in MLX kernels
	suffixes := []string{
		"_float32", "_float16", "_float",
		"_int32", "_int64", "_int",
		"_uint32", "_uint64", "_uint",
		"_half", "_bfloat16",
	}

	for _, suffix := range suffixes {
		if strings.HasSuffix(name, suffix) {
			return strings.TrimSuffix(name, suffix)
		}
	}

	return name
}

// Stats returns statistics about indexed kernels.
func (m *ShaderSourceMapper) Stats() (files int, kernels int) {
	fileSet := make(map[string]bool)
	for _, file := range m.kernelToFile {
		fileSet[file] = true
	}
	return len(fileSet), len(m.kernelToFile)
}
