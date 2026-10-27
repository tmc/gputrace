package cmd

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/trace"
)

type blobOptions struct {
	out  string
	list bool
}

var blobCmd = newBlobCommand(&blobOptions{})

func newBlobCommand(opts *blobOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "blob <trace.gputrace> [name]",
		Short: "Read an entry of the bundle's index: a store0 blob or a bundle file",
		Long: `Looks a name up in the bundle's xdic index and prints its data. Names are the
16-hex-digit keys that "U" arguments carry (see 'gputrace records'), plus the
bundle's own file names. Blobs are inflated from store0.

Examples:
  gputrace blob trace.gputrace --list
  gputrace blob trace.gputrace 315F954AD9D508B0
  gputrace blob trace.gputrace 315F954AD9D508B0 --out desc.bin`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBlob(args, opts)
		},
	}
	cmd.Flags().StringVar(&opts.out, "out", "", "Write the raw bytes to this file instead of a hex dump")
	cmd.Flags().BoolVar(&opts.list, "list", false, "List every index entry")
	return cmd
}

func init() {
	rootCmd.AddCommand(blobCmd)
}

func runBlob(args []string, opts *blobOptions) error {
	dir := args[0]
	idx, err := trace.ReadStoreIndex(filepath.Join(dir, "index"))
	if err != nil {
		return err
	}
	if opts.list || len(args) == 1 {
		for _, e := range idx.Entries {
			kind := "blob"
			if e.Kind == trace.StoreKindFile {
				kind = "file"
			}
			fmt.Printf("%-4s %10d %s\n", kind, e.Size, e.Name)
		}
		return nil
	}
	data, err := trace.ReadStoreBlob(dir, idx, args[1])
	if err != nil {
		return err
	}
	if opts.out != "" {
		return os.WriteFile(opts.out, data, 0o644)
	}
	fmt.Print(hex.Dump(data))
	return nil
}
