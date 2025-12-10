package cmd

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/trace"
)

var fencesCmd = &cobra.Command{
	Use:   "fences <trace.gputrace>",
	Short: "List fence operations in the trace",
	Long:  `Scans the trace for fence operations (e.g. waitForFence, updateFence) encoded as ICB executions.`,
	Args:  cobra.ExactArgs(1),
	RunE:  runFences,
}

func init() {
	rootCmd.AddCommand(fencesCmd)
}

func runFences(cmd *cobra.Command, args []string) error {
	tracePath := args[0]
	t, err := trace.Open(tracePath)
	if err != nil {
		return fmt.Errorf("failed to open trace: %w", err)
	}

	fmt.Println("Scanning for fence operations...")

	// Scan capture file for Culul records
	capturePath := filepath.Join(tracePath, "capture")
	data, err := os.ReadFile(capturePath)
	if err != nil {
        // Fallback
        data = t.CaptureData
	}

	cululMarker := []byte("Culul\x00\x00\x00")
	offset := 0
	count := 0

	fmt.Printf("%-10s %-18s %-30s %s\n", "Offset", "Address", "Label", "Details")
	fmt.Println("--------------------------------------------------------------------------------")

	for {
		pos := bytes.Index(data[offset:], cululMarker)
		if pos == -1 { break }
		absPos := offset + pos

		// Parse Culul
		// Marker at +0 (relative to absPos found by index)
		// Address at +8 (relative to marker start)
		if absPos+40 <= len(data) {
			addr := binary.LittleEndian.Uint64(data[absPos+8 : absPos+16])

            // Look up label
            label := t.DeviceLabels[addr]
            if label == "" {
                // Try searching for partial match in all labels? No, too slow.
                // Fallback: check if it matches known fence address
                if addr == 0x9df0ec000 {
                    label = "fences (inferred)"
                } else {
                    label = "unknown"
                }
            }

            // Extract fields
			field1 := binary.LittleEndian.Uint32(data[absPos+16 : absPos+20])
			field2 := binary.LittleEndian.Uint32(data[absPos+20 : absPos+24])

            // Heuristic for Fence Op
            // If label contains "fence" or address is known fence
            isFence := false
            if label == "fences" || label == "fences (inferred)" || addr == 0x9df0ec000 {
                isFence = true
            }

            if isFence {
                opType := "Unknown"
                if field1 == 0x80000 { opType = "Update?" } // Heuristic
                if field1 == 0x800 { opType = "Wait?" }     // Heuristic

                fmt.Printf("0x%-8x 0x%-16x %-30s %s (Fields: %x %x)\n",
                    absPos, addr, label, opType, field1, field2)
                count++
            }
		}
		offset = absPos + 8
	}

	fmt.Printf("\nTotal fence operations found: %d\n", count)
	return nil
}
