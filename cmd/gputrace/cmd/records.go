package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/trace"
)

type recordsOptions struct {
	file   string
	funcID int
	sig    string
	limit  int
}

var recordsCmd = newRecordsCommand(&recordsOptions{file: "capture", limit: -1})

func newRecordsCommand(opts *recordsOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "records <trace.gputrace>",
		Short: "List every call in an MTSP stream with decoded arguments",
		Long: `Walks an MTSP file record by record (length-prefixed, no marker scanning) and
prints each call's function id, flags, signature and decoded arguments.
Works for render and compute captures alike.

Examples:
  gputrace records trace.gputrace
  gputrace records trace.gputrace --file device-resources --func -10236
  gputrace records trace.gputrace --sig CU --limit 20`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRecords(args[0], opts)
		},
	}
	cmd.Flags().StringVar(&opts.file, "file", opts.file, "MTSP file in the bundle; a prefix such as device-resources is enough")
	cmd.Flags().IntVar(&opts.funcID, "func", 0, "Only records with this function id")
	cmd.Flags().StringVar(&opts.sig, "sig", "", "Only records with this signature")
	cmd.Flags().IntVar(&opts.limit, "limit", opts.limit, "Stop after this many records")
	return cmd
}

func init() {
	rootCmd.AddCommand(recordsCmd)
}

// bundleFile resolves a file name or prefix (device-resources-0x...) inside a bundle.
func bundleFile(dir, name string) (string, error) {
	if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
		return filepath.Join(dir, name), nil
	}
	matches, _ := filepath.Glob(filepath.Join(dir, name+"*"))
	if len(matches) != 1 {
		return "", fmt.Errorf("%s: %d files match %q", dir, len(matches), name)
	}
	return matches[0], nil
}

func runRecords(dir string, opts *recordsOptions) error {
	path, err := bundleFile(dir, opts.file)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	recs, err := trace.WalkFramedRecords(data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: %v (showing %d records read before it)\n", err, len(recs))
	}
	shown := 0
	for _, r := range recs {
		if opts.funcID != 0 && int(r.FuncID) != opts.funcID || opts.sig != "" && r.Signature != opts.sig {
			continue
		}
		if opts.limit >= 0 && shown >= opts.limit {
			break
		}
		shown++
		args, rest, err := r.Args()
		parts := make([]string, len(args))
		for i, a := range args {
			parts[i] = a.String()
		}
		line := fmt.Sprintf("%#08x %7d flags=%#x %-20s %s", r.Offset, r.FuncID, r.Flags, r.Signature, strings.Join(parts, " "))
		if err != nil {
			line += " [" + err.Error() + "]"
		} else if n := len(rest); n > 0 {
			if ts, ok := r.Trailer(); ok {
				n -= 32
				line += fmt.Sprintf(" trailer=%d,%d,%d", ts[0], ts[1], ts[2])
			}
			if n > 0 {
				line += fmt.Sprintf(" ret=% x", rest[:n])
			}
		}
		fmt.Println(line)
	}
	return nil
}
