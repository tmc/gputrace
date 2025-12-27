package cmd

import (
	"encoding/hex"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/trace"
)

var (
	dumpRecordsType    string
	dumpRecordsLimit   int
	dumpRecordsOffset  int
	dumpRecordsSummary bool
)

var dumpRecordsCmd = &cobra.Command{
	Use:   "dump-records [trace-path]",
	Short: "Dump raw MTSP records from a GPU trace",
	Long: `Dumps raw MTSP records from a GPU trace file for low-level analysis.

This command is useful for reverse-engineering the trace format and identifying
unknown fields in record types like Ct, Ci, Cuw, and Cul.

Examples:
  gputrace dump-records trace.gputrace
  gputrace dump-records trace.gputrace --type Ct
  gputrace dump-records trace.gputrace --limit 10

  # Dump a specific sidecar file (e.g. MTSP device-resources or MTLB library)
  gputrace dump-records trace.gputrace/5179640D...
`,
	Args: cobra.ExactArgs(1),
	RunE: runDumpRecords,
}

func init() {
	rootCmd.AddCommand(dumpRecordsCmd)

	dumpRecordsCmd.Flags().StringVar(&dumpRecordsType, "type", "", "Filter by record type (e.g., Ct, Ci, Cul)")
	dumpRecordsCmd.Flags().IntVar(&dumpRecordsLimit, "limit", -1, "Limit number of records to show")
	dumpRecordsCmd.Flags().IntVar(&dumpRecordsOffset, "offset", 0, "Start showing records from this index")
	dumpRecordsCmd.Flags().BoolVar(&dumpRecordsSummary, "summary", false, "Show summary only (no data dump)")
}

func runDumpRecords(cmd *cobra.Command, args []string) error {
	tracePath := args[0]

	var t *trace.Trace
	fi, err := os.Stat(tracePath)
	if err != nil {
		return fmt.Errorf("stat trace: %w", err)
	}

	if fi.IsDir() {
		t, err = trace.Open(tracePath)
		if err != nil {
			return fmt.Errorf("open trace: %w", err)
		}
	} else {
		// Single file mode - create dummy trace with file content
		data, err := os.ReadFile(tracePath)
		if err != nil {
			return fmt.Errorf("read file: %w", err)
		}
		t = &trace.Trace{
			CaptureData: data,
		}
	}

	records, err := t.ParseMTSPRecords()
	if err != nil {
		return fmt.Errorf("parse records: %w", err)
	}

	fmt.Printf("Parsed %d records from %s\n\n", len(records), tracePath)

	count := 0
	for i, rec := range records {
		if i < dumpRecordsOffset {
			continue
		}

		if dumpRecordsType != "" && rec.Type != dumpRecordsType {
			continue
		}

		if dumpRecordsLimit >= 0 && count >= dumpRecordsLimit {
			break
		}

		printRecord(i, rec, "")
		count++
	}

	return nil
}

func printRecord(index int, rec trace.MTSPRecord, indent string) {
	fmt.Printf("%s[%d] Offset: 0x%x (Type: %s, Size: %d)\n", indent, index, rec.Offset, rec.Type, rec.Size)

	if !dumpRecordsSummary {
		// Hex dump of data
		if len(rec.Data) > 256 {
			fmt.Println(hex.Dump(rec.Data[:256]))
			fmt.Printf("%s... (%d bytes more)\n", indent, len(rec.Data)-256)
		} else {
			fmt.Println(hex.Dump(rec.Data))
		}
	}

	// Print parsed fields if available
	switch rec.Type {
	case trace.RecordTypeCt:
		if ct, err := rec.ParseCtRecord(); err == nil {
			fmt.Printf("%s  Parsed Ct: Flags=0x%x Pipeline=0x%x Func=0x%x Bindings=%d\n",
				indent, ct.CommandFlags, ct.PipelineAddr, ct.FunctionAddr, ct.BindingCount)
		}
	case trace.RecordTypeCi:
		if ci, err := rec.ParseCiRecord(); err == nil {
			fmt.Printf("%s  Parsed Ci: Flags=0x%x ICB=0x%x Count=%d\n",
				indent, ci.CommandFlags, ci.ICBAddr, ci.Count)
		}
	case trace.RecordTypeCtt:
		if ctt, err := rec.ParseCttRecord(); err == nil {
			fmt.Printf("%s  Parsed Ctt: Device=0x%x Func=0x%x Pipeline=0x%x\n",
				indent, ctt.DeviceAddr, ctt.FunctionAddr, ctt.PipelineAddr)
		}
	case trace.RecordTypeCS:
		// Already parsed by ParseMTSPFromData
		fmt.Printf("%s  Parsed CS: Label=%q Addr=0x%x\n", indent, rec.Label, rec.Address)
	}

	// Always attempt nested parsing for any record type if size is sufficient
	if len(rec.Data) > 16 {
		t := &trace.Trace{}
		// Try parsing skipping potential header (16 bytes)
		// We use a heuristic: if we find multiple valid records, it's likely a container
		if nested, err := t.ParseMTSPFromData(rec.Data[16:]); err == nil && len(nested) > 0 {
			fmt.Printf("%s  Possible Container (%s) with %d nested records:\n", indent, rec.Type, len(nested))
			for j, nrec := range nested {
				printRecord(j, nrec, indent+"    ")
			}
		}
	}
	if indent == "" {
		fmt.Println()
	}
}
