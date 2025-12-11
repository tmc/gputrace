package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var (
	clearBuffersDryRun bool
	clearBuffersYes    bool
)

var clearBuffersCmd = &cobra.Command{
	Use:   "clear-buffers <trace.gputrace>",
	Short: "Zero out MTLBuffer files to reduce trace size",
	Long: `Zero out all MTLBuffer-* files in a GPU trace directory.

This is useful for reducing trace size when buffer contents are not needed,
such as when sharing traces or storing them for later analysis of structure
without the actual data.

The command will:
  - Find all MTLBuffer-* files (skipping symlinks)
  - Zero out their contents while preserving file size
  - Report total space that could be saved

Examples:
  gputrace clear-buffers trace.gputrace              # Zero all buffers (prompts for confirmation)
  gputrace clear-buffers trace.gputrace -y           # Zero all buffers without prompting
  gputrace clear-buffers trace.gputrace --dry-run    # Show what would be done`,
	Args: cobra.ExactArgs(1),
	RunE: runClearBuffers,
}

func init() {
	rootCmd.AddCommand(clearBuffersCmd)
	clearBuffersCmd.Flags().BoolVar(&clearBuffersDryRun, "dry-run", false, "Show what would be done without making changes")
	clearBuffersCmd.Flags().BoolVarP(&clearBuffersYes, "yes", "y", false, "Skip confirmation prompt")
}

func runClearBuffers(cmd *cobra.Command, args []string) error {
	tracePath := args[0]

	// Verify trace directory exists
	info, err := os.Stat(tracePath)
	if err != nil {
		return fmt.Errorf("cannot access trace: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("trace path must be a directory: %s", tracePath)
	}

	// Find all MTLBuffer files
	entries, err := os.ReadDir(tracePath)
	if err != nil {
		return fmt.Errorf("read trace directory: %w", err)
	}

	// First pass: count files and total size
	var totalSize int64
	var fileCount int
	var skippedSymlinks int
	var bufferFiles []string

	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "MTLBuffer-") {
			continue
		}

		fullPath := filepath.Join(tracePath, entry.Name())

		// Check if it's a symlink
		fileInfo, err := os.Lstat(fullPath)
		if err != nil {
			continue
		}

		if fileInfo.Mode()&os.ModeSymlink != 0 {
			skippedSymlinks++
			continue
		}

		size := fileInfo.Size()
		totalSize += size
		fileCount++
		bufferFiles = append(bufferFiles, fullPath)
	}

	if fileCount == 0 {
		fmt.Println("No MTLBuffer files found")
		return nil
	}

	// Show summary and prompt for confirmation
	fmt.Printf("Found %d buffer files (%s total)\n", fileCount, formatByteSize(totalSize))
	if skippedSymlinks > 0 {
		fmt.Printf("Will skip %d symlinks\n", skippedSymlinks)
	}

	if clearBuffersDryRun {
		fmt.Println("\nDry run: no changes made")
		return nil
	}

	// Prompt for confirmation unless -y flag is set
	if !clearBuffersYes {
		fmt.Print("\nZero out all buffer files? [y/N]: ")
		reader := bufio.NewReader(os.Stdin)
		response, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("read response: %w", err)
		}
		response = strings.TrimSpace(strings.ToLower(response))
		if response != "y" && response != "yes" {
			fmt.Println("Cancelled")
			return nil
		}
	}

	// Zero out the files
	for _, path := range bufferFiles {
		fileInfo, _ := os.Stat(path)
		if err := zeroFile(path, fileInfo.Size()); err != nil {
			return fmt.Errorf("zero %s: %w", filepath.Base(path), err)
		}
	}

	fmt.Printf("Zeroed %d buffer files (%s)\n", fileCount, formatByteSize(totalSize))
	return nil
}

// zeroFile overwrites a file with zeros while preserving its size
func zeroFile(path string, size int64) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	// Write zeros in chunks
	const chunkSize = 64 * 1024 // 64KB chunks
	zeros := make([]byte, chunkSize)

	remaining := size
	for remaining > 0 {
		writeSize := remaining
		if writeSize > chunkSize {
			writeSize = chunkSize
		}
		n, err := f.Write(zeros[:writeSize])
		if err != nil {
			return err
		}
		remaining -= int64(n)
	}

	return nil
}

// formatByteSize formats bytes as human-readable size
func formatByteSize(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)

	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.2f GB", float64(bytes)/GB)
	case bytes >= MB:
		return fmt.Sprintf("%.2f MB", float64(bytes)/MB)
	case bytes >= KB:
		return fmt.Sprintf("%.2f KB", float64(bytes)/KB)
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}
