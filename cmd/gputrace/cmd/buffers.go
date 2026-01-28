package cmd

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace"
)

var (
	buffersSort          string
	buffersMinSize       string
	buffersFormat        string
	buffersBindings      bool
	buffersInspect       string // Buffer name to inspect (e.g., MTLBuffer-12-0)
	buffersInspectBytes  int    // Number of bytes to show in inspection
	buffersInspectFormat string // Format for inspection: hex, float32, int32, etc.
)

var buffersCmd = &cobra.Command{
	Use:   "buffers <trace.gputrace>",
	Short: "List buffers in a GPU trace",
	Long: `Display information about Metal buffers captured in a GPU trace.

This command shows:
  - Buffer IDs and addresses
  - Buffer sizes
  - Buffer usage (total/unique)
  - Aliasing information (symlinks)
  - Buffer bindings to encoders (with --verbose)

The output can be sorted by size, ID, or name, and filtered by minimum size.

Examples:
  gputrace buffers trace.gputrace
  gputrace buffers trace.gputrace --sort size
  gputrace buffers trace.gputrace --min-size 1MB
  gputrace buffers trace.gputrace --format json`,
	Args: cobra.ExactArgs(1),
	RunE: runBuffers,
}

func init() {
	rootCmd.AddCommand(buffersCmd)

	buffersCmd.Flags().StringVar(&buffersSort, "sort", "size", "Sort by: size, id, name")
	buffersCmd.Flags().StringVar(&buffersMinSize, "min-size", "", "Minimum buffer size (e.g., 1KB, 1MB, 1GB)")
	buffersCmd.Flags().StringVar(&buffersFormat, "format", "table", "Output format: table, json, csv")
	buffersCmd.Flags().BoolVar(&buffersBindings, "bindings", false, "Show which encoders use each buffer")
	buffersCmd.Flags().StringVar(&buffersInspect, "inspect", "", "Inspect buffer contents (e.g., MTLBuffer-12-0)")
	buffersCmd.Flags().IntVar(&buffersInspectBytes, "bytes", 256, "Number of bytes to show in inspection")
	buffersCmd.Flags().StringVar(&buffersInspectFormat, "inspect-format", "hex", "Inspection format: hex, float32, int32, uint32, float16")
}

func runBuffers(cmd *cobra.Command, args []string) error {
	tracePath := args[0]

	// Verify trace file exists
	if err := checkTraceFile(tracePath); err != nil {
		return err
	}

	// Open trace
	trace, err := gputrace.Open(tracePath)
	if err != nil {
		return fmt.Errorf("failed to open trace: %w", err)
	}

	// If --inspect is specified, handle buffer inspection
	if buffersInspect != "" {
		return inspectBuffer(tracePath, buffersInspect, buffersInspectBytes, buffersInspectFormat)
	}

	// Extract buffer information
	buffers, err := extractBufferInfo(tracePath, trace, buffersBindings)
	if err != nil {
		return fmt.Errorf("failed to extract buffer info: %w", err)
	}

	// Parse minimum size if specified
	minSize := uint64(0)
	if buffersMinSize != "" {
		parsed, err := parseSize(buffersMinSize)
		if err != nil {
			return fmt.Errorf("invalid min-size: %w", err)
		}
		minSize = parsed
	}

	// Filter by minimum size
	if minSize > 0 {
		filtered := make([]BufferInfo, 0, len(buffers))
		for _, buf := range buffers {
			if buf.Size >= minSize {
				filtered = append(filtered, buf)
			}
		}
		buffers = filtered
	}

	// Sort buffers
	sortBuffers(buffers, buffersSort)

	// Format and display
	switch buffersFormat {
	case "json":
		return formatBuffersJSON(buffers)
	case "csv":
		return formatBuffersCSV(buffers)
	default:
		return formatBuffersTable(buffers, trace)
	}
}

// BufferInfo contains information about a single buffer.
type BufferInfo struct {
	ID        string
	Filename  string
	Size      uint64
	IsSymlink bool
	Target    string // For symlinks, what they point to
	Aliases   []string
	Bindings  []BufferBindingInfo // Encoder bindings (populated with --bindings)
	Address   uint64              // Buffer memory address
}

// BufferBindingInfo contains information about a buffer binding to an encoder.
type BufferBindingInfo struct {
	EncoderLabel string
	Index        int
	Offset       uint64
}

// extractBufferInfo scans the trace directory for buffer files.
func extractBufferInfo(tracePath string, trace *gputrace.Trace, extractBindings bool) ([]BufferInfo, error) {
	entries, err := os.ReadDir(tracePath)
	if err != nil {
		return nil, err
	}

	// Map buffer IDs to their info
	bufferMap := make(map[string]*BufferInfo)
	symlinks := make(map[string][]string) // target -> symlinks

	// First pass: find base buffers and collect symlinks
	for _, entry := range entries {
		name := entry.Name()

		if !strings.HasPrefix(name, "MTLBuffer-") {
			continue
		}

		// Extract buffer ID (e.g., "12" from "MTLBuffer-12-0")
		parts := strings.TrimPrefix(name, "MTLBuffer-")
		idEnd := strings.Index(parts, "-")
		if idEnd == -1 {
			continue
		}
		bufferID := parts[:idEnd]

		// Check if it's a symlink
		fullPath := filepath.Join(tracePath, name)
		info, err := os.Lstat(fullPath)
		if err != nil {
			continue
		}

		if info.Mode()&os.ModeSymlink != 0 {
			// It's a symlink - read target
			target, err := os.Readlink(fullPath)
			if err != nil {
				continue
			}
			symlinks[target] = append(symlinks[target], name)
		} else if strings.HasSuffix(name, "-0") {
			// Base buffer file
			fileInfo, err := os.Stat(fullPath)
			if err != nil {
				continue
			}

			bufferMap[bufferID] = &BufferInfo{
				ID:        bufferID,
				Filename:  name,
				Size:      uint64(fileInfo.Size()),
				IsSymlink: false,
			}
		}
	}

	// Second pass: associate aliases with base buffers
	for target, aliases := range symlinks {
		// Extract buffer ID from target
		parts := strings.TrimPrefix(target, "MTLBuffer-")
		idEnd := strings.Index(parts, "-")
		if idEnd == -1 {
			continue
		}
		targetID := parts[:idEnd]

		if buf, ok := bufferMap[targetID]; ok {
			buf.Aliases = aliases
		}
	}

	// Third pass: extract buffer bindings if requested
	if extractBindings {
		if err := extractBufferBindings(trace, bufferMap); err != nil {
			return nil, fmt.Errorf("extract bindings: %w", err)
		}
	}

	// Convert map to slice
	buffers := make([]BufferInfo, 0, len(bufferMap))
	for _, buf := range bufferMap {
		buffers = append(buffers, *buf)
	}

	return buffers, nil
}

// extractBufferBindings populates buffer binding information from the trace.
func extractBufferBindings(trace *gputrace.Trace, bufferMap map[string]*BufferInfo) error {
	// Build address->name mapping from capture file
	// Look for CtU<b>ulul records which have both buffer address and name
	addrToName := make(map[uint64]string)

	// Read capture file
	capturePath := filepath.Join(trace.Path, "capture")
	captureData, err := os.ReadFile(capturePath)
	if err != nil {
		return fmt.Errorf("read capture: %w", err)
	}

	// Parse CtU<b>ulul records from capture
	// Marker: CtU<b>ulul = 43 74 55 3c 62 3e 75 6c 75 6c
	marker := []byte{0x43, 0x74, 0x55, 0x3c, 0x62, 0x3e, 0x75, 0x6c, 0x75, 0x6c}
	offset := 0
	matchCount := 0
	for {
		pos := bytes.Index(captureData[offset:], marker)
		if pos == -1 {
			break
		}
		matchCount++
		absolutePos := offset + pos

		// Structure based on hexdump analysis:
		// +0x00: "CtU<b>ulul" (10 bytes)
		// +0x0a: padding (2 bytes of 0x00)
		// +0x0c: first address (8 bytes, little-endian)
		// +0x14: buffer address (8 bytes, little-endian)
		// +0x1c: buffer name "MTLBuffer-XX-Y" or "MTLHeap-X-Y"

		// Read buffer address at +0x14 (corrected offset)
		if absolutePos+0x24 <= len(captureData) {
			bufAddr := binary.LittleEndian.Uint64(captureData[absolutePos+0x14 : absolutePos+0x1c])

			// Read buffer name at +0x1c (corrected offset)
			nameStart := absolutePos + 0x1c
			if bytes.HasPrefix(captureData[nameStart:], []byte("MTLBuffer-")) ||
				bytes.HasPrefix(captureData[nameStart:], []byte("MTLHeap-")) {
				nameEnd := bytes.IndexByte(captureData[nameStart:], 0)
				if nameEnd > 0 && nameEnd < 100 {
					name := string(captureData[nameStart : nameStart+nameEnd])
					addrToName[bufAddr] = name
				}
			}
		}

		offset += pos + 10
	}

	// Build map from buffer name to BufferInfo
	// Map full names like "MTLBuffer-93-0" -> BufferInfo
	nameToInfo := make(map[string]*BufferInfo)
	for _, buf := range bufferMap {
		nameToInfo[buf.Filename] = buf
		// Also add entries for all possible suffixes (MTLBuffer-93-1, MTLBuffer-93-2, etc.)
		// Extract ID from filename
		if strings.HasPrefix(buf.Filename, "MTLBuffer-") {
			parts := strings.TrimPrefix(buf.Filename, "MTLBuffer-")
			idEnd := strings.Index(parts, "-")
			if idEnd > 0 {
				bufferID := parts[:idEnd]
				// Add mappings for common suffixes
				for i := 0; i < 10; i++ {
					name := fmt.Sprintf("MTLBuffer-%s-%d", bufferID, i)
					nameToInfo[name] = buf
				}
			}
		}
	}

	// Parse command buffers to get buffer bindings
	commandBuffers, err := trace.ParseCommandBuffers()
	if err != nil {
		return fmt.Errorf("parse command buffers: %w", err)
	}

	// Get encoder labels
	encoderLabels := trace.EncoderLabels
	if len(encoderLabels) == 0 {
		// Fall back to kernel names if encoder labels aren't available
		encoderLabels = trace.KernelNames
	}

	// Process each command buffer
	encoderIdx := 0
	for cbIdx, cb := range commandBuffers {
		// Determine region for this command buffer
		var cbEnd int64
		if cbIdx+1 < len(commandBuffers) {
			cbEnd = commandBuffers[cbIdx+1].Offset
		} else {
			cbEnd = int64(len(captureData))
		}

		cbData := captureData[cb.Offset:cbEnd]

		// Parse buffer bindings in this command buffer
		bindings, err := parseCommandBufferBindings(cbData)
		if err != nil {
			continue
		}

		// Count encoders in this command buffer (number of dispatch calls)
		dispatches, _ := trace.ParseDispatchInRegion(cbData, cb.Offset)
		numEncoders := len(dispatches)
		if numEncoders == 0 {
			numEncoders = 1
		}

		// Distribute bindings across encoders
		// Simple heuristic: divide evenly
		bindingsPerEncoder := len(bindings) / numEncoders
		if bindingsPerEncoder == 0 {
			bindingsPerEncoder = 1
		}

		bindingIdx := 0
		for encIdx := 0; encIdx < numEncoders && encoderIdx < len(encoderLabels); encIdx++ {
			label := encoderLabels[encoderIdx]
			encoderIdx++

			// Determine this encoder's bindings
			endBindingIdx := bindingIdx + bindingsPerEncoder
			if encIdx == numEncoders-1 {
				// Last encoder gets all remaining
				endBindingIdx = len(bindings)
			}

			// Associate bindings with this encoder
			for bindingIdx < endBindingIdx && bindingIdx < len(bindings) {
				binding := bindings[bindingIdx]

				// Resolve buffer address to name
				bufName, ok := addrToName[binding.BufferAddr]
				if !ok {
					bindingIdx++
					continue
				}

				// Find the BufferInfo for this buffer name
				bufInfo, ok := nameToInfo[bufName]
				if !ok {
					bindingIdx++
					continue
				}

				// Add binding info
				bufInfo.Bindings = append(bufInfo.Bindings, BufferBindingInfo{
					EncoderLabel: label,
					Index:        binding.Index,
					Offset:       0, // Not available in CommandBufferBinding
				})
				bufInfo.Address = binding.BufferAddr

				bindingIdx++
			}
		}
	}

	return nil
}

// parseCommandBufferBindings parses buffer bindings from command buffer data.
func parseCommandBufferBindings(data []byte) ([]CommandBufferBinding, error) {
	var bindings []CommandBufferBinding

	// Pattern: "Ctulul" followed by encoder address and buffer address
	// Structure:
	// +0x00: "Ctulul\x00\x00" (8 bytes)
	// +0x08: encoder address (8 bytes)
	// +0x10: buffer address (8 bytes)
	// +0x18: offset (4 bytes)
	// +0x1c: index (4 bytes)
	marker := []byte("Ctulul")

	offset := 0
	matchCount := 0
	for {
		pos := bytes.Index(data[offset:], marker)
		if pos == -1 {
			break
		}
		matchCount++

		absolutePos := offset + pos

		// Read buffer address at +0x10 and index at +0x1c
		if absolutePos+0x20 <= len(data) {
			bufAddr := binary.LittleEndian.Uint64(data[absolutePos+0x10 : absolutePos+0x18])
			index := binary.LittleEndian.Uint32(data[absolutePos+0x1c : absolutePos+0x20])

			bindings = append(bindings, CommandBufferBinding{
				BufferAddr: bufAddr,
				Index:      int(index),
				Offset:     int64(absolutePos),
			})
		}

		offset += pos + 6
	}

	return bindings, nil
}

// CommandBufferBinding represents a buffer binding within a command buffer.
type CommandBufferBinding struct {
	BufferAddr uint64
	Index      int
	Offset     int64
}

// sortBuffers sorts the buffer list by the specified field.
func sortBuffers(buffers []BufferInfo, sortBy string) {
	switch sortBy {
	case "size":
		sort.Slice(buffers, func(i, j int) bool {
			return buffers[i].Size > buffers[j].Size // Descending
		})
	case "id":
		sort.Slice(buffers, func(i, j int) bool {
			return buffers[i].ID < buffers[j].ID
		})
	case "name":
		sort.Slice(buffers, func(i, j int) bool {
			return buffers[i].Filename < buffers[j].Filename
		})
	}
}

// formatBuffersTable formats buffers as a human-readable table.
func formatBuffersTable(buffers []BufferInfo, trace *gputrace.Trace) error {
	// Calculate totals
	var totalSize uint64
	totalAliases := 0
	for _, buf := range buffers {
		totalSize += buf.Size
		totalAliases += len(buf.Aliases)
	}

	// Print summary line
	fmt.Printf("%d %s, %s", len(buffers), Pluralize(len(buffers), "buffer", "buffers"), FormatBytes(totalSize))
	if totalAliases > 0 {
		fmt.Printf(", %d %s", totalAliases, Pluralize(totalAliases, "alias", "aliases"))
	}
	fmt.Println()
	fmt.Println()

	// Print table header
	fmt.Println(Colorize("Buffers", ColorBold))
	fmt.Println(TableSeparator(80))
	fmt.Printf("%-8s %-25s %12s %s\n", "ID", "Filename", "Size", "Aliases")
	fmt.Println(TableSeparator(80))

	// Print each buffer
	for _, buf := range buffers {
		aliasInfo := ""
		if len(buf.Aliases) > 0 {
			if len(buf.Aliases) == 1 {
				aliasInfo = buf.Aliases[0]
			} else {
				aliasInfo = fmt.Sprintf("%d aliases", len(buf.Aliases))
			}
		}

		fmt.Printf("%-8s %-25s %12s %s\n",
			buf.ID,
			buf.Filename,
			FormatBytes(buf.Size),
			aliasInfo,
		)

		// Show all aliases if more than 1
		if len(buf.Aliases) > 1 {
			for _, alias := range buf.Aliases {
				fmt.Printf("%-8s   → %s\n", "", alias)
			}
		}

		// Show buffer bindings if present
		if len(buf.Bindings) > 0 {
			fmt.Printf("%-8s   Used by:\n", "")
			for _, binding := range buf.Bindings {
				fmt.Printf("%-8s     - %s (index %d", "", binding.EncoderLabel, binding.Index)
				if binding.Offset > 0 {
					fmt.Printf(", offset %d", binding.Offset)
				}
				fmt.Printf(")\n")
			}
		}
	}

	return nil
}

// formatBuffersJSON formats buffers as JSON.
func formatBuffersJSON(buffers []BufferInfo) error {
	fmt.Println("[")
	for i, buf := range buffers {
		comma := ","
		if i == len(buffers)-1 {
			comma = ""
		}
		fmt.Printf("  {\"id\": \"%s\", \"filename\": \"%s\", \"size\": %d, \"aliases\": %d}%s\n",
			buf.ID, buf.Filename, buf.Size, len(buf.Aliases), comma)
	}
	fmt.Println("]")
	return nil
}

// formatBuffersCSV formats buffers as CSV.
func formatBuffersCSV(buffers []BufferInfo) error {
	fmt.Println("ID,Filename,Size,Aliases")
	for _, buf := range buffers {
		fmt.Printf("%s,%s,%d,%d\n", buf.ID, buf.Filename, buf.Size, len(buf.Aliases))
	}
	return nil
}

// parseSize parses a size string like "1KB", "1MB", "1GB".
func parseSize(s string) (uint64, error) {
	s = strings.ToUpper(strings.TrimSpace(s))

	multiplier := uint64(1)
	if strings.HasSuffix(s, "KB") {
		multiplier = 1024
		s = strings.TrimSuffix(s, "KB")
	} else if strings.HasSuffix(s, "MB") {
		multiplier = 1024 * 1024
		s = strings.TrimSuffix(s, "MB")
	} else if strings.HasSuffix(s, "GB") {
		multiplier = 1024 * 1024 * 1024
		s = strings.TrimSuffix(s, "GB")
	}

	var value uint64
	_, err := fmt.Sscanf(s, "%d", &value)
	if err != nil {
		return 0, fmt.Errorf("invalid size format: %s", s)
	}

	return value * multiplier, nil
}

// formatBytes formats a byte count as a human-readable string.
func formatBytes(bytes uint64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)

	if bytes >= GB {
		return fmt.Sprintf("%.2f GB", float64(bytes)/GB)
	} else if bytes >= MB {
		return fmt.Sprintf("%.2f MB", float64(bytes)/MB)
	} else if bytes >= KB {
		return fmt.Sprintf("%.2f KB", float64(bytes)/KB)
	}
	return fmt.Sprintf("%d B", bytes)
}

// inspectBuffer reads and displays buffer contents in the specified format.
func inspectBuffer(tracePath, bufferName string, numBytes int, format string) error {
	// Construct buffer file path
	bufferPath := filepath.Join(tracePath, bufferName)

	// Check if file exists
	info, err := os.Lstat(bufferPath)
	if err != nil {
		return fmt.Errorf("buffer not found: %s", bufferName)
	}

	// If it's a symlink, resolve it
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(bufferPath)
		if err != nil {
			return fmt.Errorf("failed to read symlink: %w", err)
		}
		fmt.Printf("Note: %s is a symlink to %s\n\n", bufferName, target)
		bufferPath = filepath.Join(tracePath, target)
	}

	// Open buffer file
	f, err := os.Open(bufferPath)
	if err != nil {
		return fmt.Errorf("failed to open buffer: %w", err)
	}
	defer f.Close()

	// Get file size
	stat, err := f.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat buffer: %w", err)
	}
	fileSize := stat.Size()

	// Adjust numBytes if it exceeds file size
	if int64(numBytes) > fileSize {
		numBytes = int(fileSize)
	}

	// Read buffer data
	data := make([]byte, numBytes)
	n, err := f.Read(data)
	if err != nil {
		return fmt.Errorf("failed to read buffer: %w", err)
	}
	data = data[:n]

	// Display header
	fmt.Printf("Buffer: %s\n", bufferName)
	fmt.Printf("Size: %s (%d bytes)\n", formatBytes(uint64(fileSize)), fileSize)
	fmt.Printf("Showing: %d bytes in %s format\n\n", n, format)

	// Format and display data
	switch format {
	case "hex":
		return formatHexDump(data)
	case "float32":
		return formatFloat32(data)
	case "int32":
		return formatInt32(data)
	case "uint32":
		return formatUint32(data)
	case "float16":
		return formatFloat16(data)
	default:
		return fmt.Errorf("unknown format: %s (supported: hex, float32, int32, uint32, float16)", format)
	}
}

// formatHexDump displays data in hexdump format with ASCII representation.
func formatHexDump(data []byte) error {
	const bytesPerLine = 16

	for offset := 0; offset < len(data); offset += bytesPerLine {
		// Print offset
		fmt.Printf("%08x  ", offset)

		// Print hex bytes
		end := offset + bytesPerLine
		if end > len(data) {
			end = len(data)
		}

		// First 8 bytes
		for i := offset; i < offset+8 && i < end; i++ {
			fmt.Printf("%02x ", data[i])
		}

		// Separator
		if end > offset+8 {
			fmt.Printf(" ")
			// Second 8 bytes
			for i := offset + 8; i < end; i++ {
				fmt.Printf("%02x ", data[i])
			}
		}

		// Padding for incomplete lines
		remaining := bytesPerLine - (end - offset)
		for i := 0; i < remaining; i++ {
			fmt.Printf("   ")
		}
		if end <= offset+8 {
			fmt.Printf(" ")
		}

		// Print ASCII representation
		fmt.Printf(" |")
		for i := offset; i < end; i++ {
			if data[i] >= 32 && data[i] <= 126 {
				fmt.Printf("%c", data[i])
			} else {
				fmt.Printf(".")
			}
		}
		fmt.Printf("|\n")
	}

	return nil
}

// formatFloat32 displays data as float32 values.
func formatFloat32(data []byte) error {
	const valuesPerLine = 8

	if len(data)%4 != 0 {
		fmt.Printf("Warning: data size (%d) is not a multiple of 4, last %d bytes will be ignored\n\n",
			len(data), len(data)%4)
	}

	count := 0
	for offset := 0; offset+4 <= len(data); offset += 4 {
		if count%valuesPerLine == 0 {
			if count > 0 {
				fmt.Println()
			}
			fmt.Printf("[%04d] ", count)
		}

		val := math.Float32frombits(binary.LittleEndian.Uint32(data[offset : offset+4]))
		fmt.Printf("%12.6f ", val)
		count++
	}
	fmt.Println()

	return nil
}

// formatInt32 displays data as int32 values.
func formatInt32(data []byte) error {
	const valuesPerLine = 8

	if len(data)%4 != 0 {
		fmt.Printf("Warning: data size (%d) is not a multiple of 4, last %d bytes will be ignored\n\n",
			len(data), len(data)%4)
	}

	count := 0
	for offset := 0; offset+4 <= len(data); offset += 4 {
		if count%valuesPerLine == 0 {
			if count > 0 {
				fmt.Println()
			}
			fmt.Printf("[%04d] ", count)
		}

		val := int32(binary.LittleEndian.Uint32(data[offset : offset+4]))
		fmt.Printf("%12d ", val)
		count++
	}
	fmt.Println()

	return nil
}

// formatUint32 displays data as uint32 values.
func formatUint32(data []byte) error {
	const valuesPerLine = 8

	if len(data)%4 != 0 {
		fmt.Printf("Warning: data size (%d) is not a multiple of 4, last %d bytes will be ignored\n\n",
			len(data), len(data)%4)
	}

	count := 0
	for offset := 0; offset+4 <= len(data); offset += 4 {
		if count%valuesPerLine == 0 {
			if count > 0 {
				fmt.Println()
			}
			fmt.Printf("[%04d] ", count)
		}

		val := binary.LittleEndian.Uint32(data[offset : offset+4])
		fmt.Printf("%12d ", val)
		count++
	}
	fmt.Println()

	return nil
}

// formatFloat16 displays data as float16 values.
// Note: Go doesn't have native float16, so we convert to float32 for display.
func formatFloat16(data []byte) error {
	const valuesPerLine = 8

	if len(data)%2 != 0 {
		fmt.Printf("Warning: data size (%d) is not a multiple of 2, last byte will be ignored\n\n",
			len(data))
	}

	count := 0
	for offset := 0; offset+2 <= len(data); offset += 2 {
		if count%valuesPerLine == 0 {
			if count > 0 {
				fmt.Println()
			}
			fmt.Printf("[%04d] ", count)
		}

		// Read 16-bit value
		bits := binary.LittleEndian.Uint16(data[offset : offset+2])

		// Convert float16 to float32
		// IEEE 754 half precision: 1 sign bit, 5 exponent bits, 10 mantissa bits
		sign := uint32((bits >> 15) & 0x1)
		exponent := uint32((bits >> 10) & 0x1F)
		mantissa := uint32(bits & 0x3FF)

		var f32bits uint32
		if exponent == 0 {
			if mantissa == 0 {
				// Zero
				f32bits = sign << 31
			} else {
				// Denormalized number
				// Not implementing full denormal conversion for simplicity
				f32bits = sign << 31
			}
		} else if exponent == 31 {
			// Infinity or NaN
			f32bits = (sign << 31) | 0x7F800000 | (mantissa << 13)
		} else {
			// Normalized number
			f32bits = (sign << 31) | ((exponent + 112) << 23) | (mantissa << 13)
		}

		val := math.Float32frombits(f32bits)
		fmt.Printf("%12.6f ", val)
		count++
	}
	fmt.Println()

	return nil
}
