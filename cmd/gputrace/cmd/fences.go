package cmd

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/trace"
)

type fencesOptions struct {
	json bool
}

var fencesCmd = newFencesCommand(&fencesOptions{})

func newFencesCommand(opts *fencesOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:    "fences <trace.gputrace>",
		Short:  "List fence operations in the trace",
		Hidden: true,
		Long:   `Scans the trace for fence operations (e.g. waitForFence, updateFence) encoded as ICB executions.`,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runFences(cmd, args, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.json, "json", false, "Output in JSON format")
	return cmd
}

func init() {
	rootCmd.AddCommand(fencesCmd)
}

func runFences(cmd *cobra.Command, args []string, opts *fencesOptions) error {
	tracePath := args[0]
	t, err := trace.Open(tracePath)
	if err != nil {
		return fmt.Errorf("failed to open trace: %w", err)
	}

	// Scan capture file for Culul records
	capturePath := filepath.Join(tracePath, "capture")
	data, err := os.ReadFile(capturePath)
	if err != nil {
		data = t.CaptureData
	}

	type fenceOp struct {
		Offset  string `json:"offset"`
		Address string `json:"address"`
		Label   string `json:"label"`
		OpType  string `json:"op_type"`
		Field1  uint32 `json:"field1"`
		Field2  uint32 `json:"field2"`
	}

	cululMarker := []byte("Culul\x00\x00\x00")
	offset := 0
	var fences []fenceOp

	for {
		pos := bytes.Index(data[offset:], cululMarker)
		if pos == -1 {
			break
		}
		absPos := offset + pos

		if absPos+40 <= len(data) {
			addr := binary.LittleEndian.Uint64(data[absPos+8 : absPos+16])

			label := t.DeviceLabels[addr]
			if label == "" {
				if addr == 0x9df0ec000 {
					label = "fences (inferred)"
				} else {
					label = "unknown"
				}
			}

			field1 := binary.LittleEndian.Uint32(data[absPos+16 : absPos+20])
			field2 := binary.LittleEndian.Uint32(data[absPos+20 : absPos+24])

			isFence := label == "fences" || label == "fences (inferred)" || addr == 0x9df0ec000
			if isFence {
				opType := "Unknown"
				if field1 == 0x80000 {
					opType = "Update?"
				}
				if field1 == 0x800 {
					opType = "Wait?"
				}
				fences = append(fences, fenceOp{
					Offset:  fmt.Sprintf("0x%x", absPos),
					Address: fmt.Sprintf("0x%x", addr),
					Label:   label,
					OpType:  opType,
					Field1:  field1,
					Field2:  field2,
				})
			}
		}
		offset = absPos + 8
	}

	w := cmd.OutOrStdout()
	if opts.json {
		data, err := json.MarshalIndent(fences, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal json: %w", err)
		}
		_, err = fmt.Fprintln(w, string(data))
		return err
	}

	fmt.Fprintln(w, "Scanning for fence operations...")
	fmt.Fprintf(w, "%-10s %-18s %-30s %s\n", "Offset", "Address", "Label", "Details")
	fmt.Fprintln(w, "--------------------------------------------------------------------------------")
	for _, f := range fences {
		fmt.Fprintf(w, "%-10s %-18s %-30s %s (Fields: %x %x)\n",
			f.Offset, f.Address, f.Label, f.OpType, f.Field1, f.Field2)
	}
	fmt.Fprintf(w, "\nTotal fence operations found: %d\n", len(fences))
	return nil
}
