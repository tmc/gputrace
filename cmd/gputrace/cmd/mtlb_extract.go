package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/mtlb"
)

var (
	extractOutput    string
	extractLibrary   string
	extractAll       bool
	extractOutputDir string
)

var mtlbExtractCmd = &cobra.Command{
	Use:   "extract [trace]",
	Short: "Extract MTLB to standalone .metallib file",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		tracePath := args[0]

		files, err := mtlb.FindMTLBFiles(tracePath)
		if err != nil {
			return err
		}

		if len(files) == 0 {
			fmt.Println("No MTLB files found in trace.")
			return nil
		}

		if extractAll {
			if extractOutputDir == "" {
				return fmt.Errorf("must specify --output-dir when using --all")
			}
			if err := os.MkdirAll(extractOutputDir, 0755); err != nil {
				return err
			}

			count := 0
			for _, f := range files {
				destPath := filepath.Join(extractOutputDir, f.Name+".metallib")
				if err := copyFile(f.Path, destPath); err != nil {
					fmt.Printf("Failed to extract %s: %v\n", f.Name, err)
				} else {
					fmt.Printf("Extracted %s -> %s\n", f.Name, destPath)
					count++
				}
			}
			fmt.Printf("Extracted %d libraries to %s/\n", count, extractOutputDir)
			return nil
		}

		// Single file extraction
		var targetFile mtlb.FoundMTLB
		if extractLibrary != "" {
			found := false
			for _, f := range files {
				if f.Name == extractLibrary {
					targetFile = f
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("library %s not found in trace", extractLibrary)
			}
		} else {
			// Default to the first one (usually the largest one in the example)
			// But maybe we should pick the largest one?
			// The example shows "Extracted F98EC4E82B8CACCA -> kernels.metallib"
			// Let's pick the largest one by default.
			maxSize := int64(-1)
			for _, f := range files {
				if f.Size > maxSize {
					maxSize = f.Size
					targetFile = f
				}
			}
		}

		if extractOutput == "" {
			extractOutput = targetFile.Name + ".metallib"
		}

		if err := copyFile(targetFile.Path, extractOutput); err != nil {
			return fmt.Errorf("failed to extract: %w", err)
		}

		fmt.Printf("Extracted %s -> %s (%s)\n", targetFile.Name, extractOutput, formatSize(targetFile.Size))

		return nil
	},
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() {
		if cErr := out.Close(); cErr != nil && err == nil {
			err = cErr
		}
	}()

	_, err = io.Copy(out, in)
	return err
}

func init() {
	mtlbCmd.AddCommand(mtlbExtractCmd)
	mtlbExtractCmd.Flags().StringVarP(&extractOutput, "output", "o", "", "Output filename (for single extraction)")
	mtlbExtractCmd.Flags().StringVar(&extractLibrary, "library", "", "Specific library name to extract")
	mtlbExtractCmd.Flags().BoolVar(&extractAll, "all", false, "Extract all libraries")
	mtlbExtractCmd.Flags().StringVar(&extractOutputDir, "output-dir", "", "Output directory for --all")
}
