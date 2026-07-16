package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/fmtutil"
	"github.com/tmc/gputrace/internal/metallib"
)

var mtlbExtractCmd = newMTLBExtractCommand(&mtlbExtractOptions{})

type mtlbExtractOptions struct {
	output    string
	library   string
	all       bool
	outputDir string
}

func newMTLBExtractCommand(opts *mtlbExtractOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "extract <trace>",
		Short: "Extract MTLB to standalone .metallib file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMTLBExtract(cmd, args, opts)
		},
	}

	cmd.Flags().StringVarP(&opts.output, "output", "o", opts.output, "Output filename (for single extraction)")
	cmd.Flags().StringVar(&opts.library, "library", opts.library, "Specific library name to extract")
	cmd.Flags().BoolVar(&opts.all, "all", opts.all, "Extract all libraries")
	cmd.Flags().StringVar(&opts.outputDir, "output-dir", opts.outputDir, "Output directory for --all")
	return cmd
}

func runMTLBExtract(cmd *cobra.Command, args []string, opts *mtlbExtractOptions) error {
	tracePath := args[0]
	status := cmd.OutOrStdout()

	files, err := metallib.FindFiles(tracePath)
	if err != nil {
		return err
	}

	if len(files) == 0 {
		fmt.Fprintln(status, "No MTLB files found in trace.")
		return nil
	}

	if opts.all {
		if opts.outputDir == "" {
			return fmt.Errorf("must specify --output-dir when using --all")
		}
		if err := os.MkdirAll(opts.outputDir, 0755); err != nil {
			return err
		}

		count := 0
		for _, f := range files {
			destPath := filepath.Join(opts.outputDir, f.Name+".metallib")
			if err := copyFile(f.Path, destPath); err != nil {
				fmt.Fprintf(status, "Failed to extract %s: %v\n", f.Name, err)
			} else {
				fmt.Fprintf(status, "Extracted %s -> %s\n", f.Name, destPath)
				count++
			}
		}
		fmt.Fprintf(status, "Extracted %d libraries to %s/\n", count, opts.outputDir)
		return nil
	}

	// Single file extraction
	var targetFile metallib.FoundFile
	if opts.library != "" {
		found := false
		for _, f := range files {
			if f.Name == opts.library {
				targetFile = f
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("library %s not found in trace", opts.library)
		}
	} else {
		// Pick the largest library by default.
		maxSize := int64(-1)
		for _, f := range files {
			if f.Size > maxSize {
				maxSize = f.Size
				targetFile = f
			}
		}
	}

	output := opts.output
	if output == "" {
		output = targetFile.Name + ".metallib"
	}

	if err := copyFile(targetFile.Path, output); err != nil {
		return fmt.Errorf("failed to extract: %w", err)
	}

	fmt.Fprintf(mtlbExtractStatusWriter(output), "Extracted %s -> %s (%s)\n", targetFile.Name, output, fmtutil.FormatBytes(targetFile.Size, 1))

	return nil
}

func mtlbExtractStatusWriter(outputPath string) *os.File {
	if outputPathIsExplicitStdout(outputPath) {
		return os.Stderr
	}
	return os.Stdout
}

func copyFile(src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if outputPathIsExplicitStdout(dst) {
		_, err = io.Copy(os.Stdout, in)
		return err
	}

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
}
