package cmd

import (
	"encoding/binary"
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
	dumpRecordsAnalyze bool
)

var dumpRecordsCmd = &cobra.Command{
	Use:    "dump-records <trace-path>",
	Short:  "Dump raw MTSP records from a GPU trace",
	Hidden: true,
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
	dumpRecordsCmd.Flags().BoolVarP(&dumpRecordsSummary, "summary", "s", false, "Show summary only (no data dump)")
	dumpRecordsCmd.Flags().BoolVarP(&dumpRecordsAnalyze, "analyze", "a", false, "Show coverage analysis (byte counts)")
}

func runDumpRecords(cmd *cobra.Command, args []string) error {
	if err := validateDumpRecordsFlags(dumpRecordsOffset, dumpRecordsLimit); err != nil {
		return err
	}

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

	if dumpRecordsAnalyze {
		printCoverageAnalysis(records)
		return nil
	}

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

func validateDumpRecordsFlags(offset, limit int) error {
	if offset < 0 {
		return fmt.Errorf("--offset must be >= 0")
	}
	if limit < -1 {
		return fmt.Errorf("--limit must be >= -1")
	}
	return nil
}

func printCoverageAnalysis(records []trace.MTSPRecord) {
	totalBytes := 0
	totalRecords := len(records)
	typeBytes := make(map[string]int)
	typeCounts := make(map[string]int)

	for _, rec := range records {
		totalBytes += rec.Size
		typeBytes[rec.Type] += rec.Size
		typeCounts[rec.Type]++
	}

	fmt.Println("=== Extraction Coverage Analysis ===")
	fmt.Printf("Total Bytes: %d\n", totalBytes)
	fmt.Printf("Total Records: %d\n\n", totalRecords)

	const (
		TypeKnown   = "Known"
		TypePartial = "Partial"
		TypeUnknown = "Unknown"
	)

	// Classify record types
	classify := func(t string) string {
		switch t {
		case trace.RecordTypeCS, trace.RecordTypeCSuwuw:
			return TypeKnown
		case trace.RecordTypeCt, trace.RecordTypeCtt:
			return TypeKnown
		case "CtU<b\u003eulul": // Check for the specific name if it surfaces, or generic
			return TypeKnown
		case trace.RecordTypeCi, trace.RecordTypeCiulSl, trace.RecordTypeCiulul:
			return TypePartial
		case trace.RecordTypeCuw, trace.RecordTypeCul, trace.RecordTypeCulul:
			return TypePartial
		case trace.RecordTypeCU, trace.RecordTypeCut:
			return TypePartial
		case trace.RecordTypeUnknown:
			return TypeUnknown
		default:
			// Heuristic: if it starts with 'C' it is likely a command
			if len(t) > 0 && t[0] == 'C' {
				return TypePartial
			}
			return TypeUnknown
		}
	}

	// Aggregate by category
	catBytes := make(map[string]int)
	catCounts := make(map[string]int)

	for t, bytes := range typeBytes {
		cat := classify(t)
		catBytes[cat] += bytes
		catCounts[cat] += typeCounts[t]
	}

	fmt.Println(Colorize("Byte Coverage:", ColorBold))
	fmt.Printf("  Known:   %12d (%5.1f%%) - CS, Ct, Ctt, etc.\n", catBytes[TypeKnown], float64(catBytes[TypeKnown])/float64(totalBytes)*100)
	fmt.Printf("  Partial: %12d (%5.1f%%) - Ci, Cuw, Cul, etc.\n", catBytes[TypePartial], float64(catBytes[TypePartial])/float64(totalBytes)*100)
	fmt.Printf("  Unknown: %12d (%5.1f%%)\n", catBytes[TypeUnknown], float64(catBytes[TypeUnknown])/float64(totalBytes)*100)

	fmt.Println(Colorize("\nDetailed Breakdown:", ColorBold))
	fmt.Printf("  %-15s %10s %10s\n", "Type", "Count", "Bytes")
	fmt.Println("  " + "---------------------------------------")

	// Sort types not easily done in map loop, but okay for dev tool
	for t, b := range typeBytes {
		fmt.Printf("  %-15s %10d %10d\n", t, typeCounts[t], b)
	}
}

func printRecord(index int, rec trace.MTSPRecord, indent string) {
	fmt.Printf("%s[%d] Offset: %s (Type: %s, Size: %d)\n", indent, index, Colorize(fmt.Sprintf("0x%x", rec.Offset), ColorCyan), Colorize(rec.Type, ColorYellow), rec.Size)

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
			fmt.Printf("%s  %s: Flags=%s Pipeline=%s Func=%s Bindings=%d\n",
				indent, Colorize("Parsed Ct", ColorGreen),
				Colorize(fmt.Sprintf("0x%x", ct.CommandFlags), ColorCyan),
				Colorize(fmt.Sprintf("0x%x", ct.PipelineAddr), ColorCyan),
				Colorize(fmt.Sprintf("0x%x", ct.FunctionAddr), ColorCyan),
				ct.BindingCount)
		}
	case trace.RecordTypeCi:
		if ci, err := rec.ParseCiRecord(); err == nil {
			fmt.Printf("%s  %s: Flags=%s ICB=%s Count=%d\n",
				indent, Colorize("Parsed Ci", ColorGreen),
				Colorize(fmt.Sprintf("0x%x", ci.CommandFlags), ColorCyan),
				Colorize(fmt.Sprintf("0x%x", ci.ICBAddr), ColorCyan),
				ci.Count)
		}
	case trace.RecordTypeCtt:
		if ctt, err := rec.ParseCttRecord(); err == nil {
			fmt.Printf("%s  %s: Device=%s Func=%s Pipeline=%s Bindings=%d\n",
				indent, Colorize("Parsed Ctt", ColorGreen),
				Colorize(fmt.Sprintf("0x%x", ctt.DeviceAddr), ColorCyan),
				Colorize(fmt.Sprintf("0x%x", ctt.FunctionAddr), ColorCyan),
				Colorize(fmt.Sprintf("0x%x", ctt.PipelineAddr), ColorCyan),
				ctt.BindingCount)
			if len(ctt.BufferBindings) > 0 {
				fmt.Printf("%s    Bindings: %x\n", indent, ctt.BufferBindings)
			}
		}
	case trace.RecordTypeCS:
		// Already parsed by ParseMTSPFromData
		flags := uint32(0)
		if len(rec.Data) >= 8 {
			flags = binary.LittleEndian.Uint32(rec.Data[4:8])
		}
		fmt.Printf("%s  %s: Label=%q Addr=%s SecAddr=%s Flags=%s\n",
			indent, Colorize("Parsed CS", ColorGreen),
			rec.Label,
			Colorize(fmt.Sprintf("0x%x", rec.Address), ColorCyan),
			Colorize(fmt.Sprintf("0x%x", rec.SecondaryAddr), ColorCyan),
			Colorize(fmt.Sprintf("0x%x", flags), ColorCyan))
	case trace.RecordTypeCtU:
		if ctu, err := rec.ParseCtURecord(); err == nil {
			fmt.Printf("%s  Parsed CtU: Addr=0x%x Name=%q\n", indent, ctu.Address, ctu.Name)
		}
	case trace.RecordTypeCul:
		if cul, err := rec.ParseCulRecord(); err == nil {
			fmt.Printf("%s  Parsed Cul: Flags=0x%x Addr=0x%x Field1=0x%x\n",
				indent, cul.CommandFlags, cul.BufferAddr, cul.Field1)
		}
	case trace.RecordTypeCuw:
		if cuw, err := rec.ParseCuwRecord(); err == nil {
			fmt.Printf("%s  Parsed Cuw: Flags=0x%x Addr=0x%x Value=0x%x\n",
				indent, cuw.CommandFlags, cuw.BufferAddr, cuw.Field1)
		}
	case trace.RecordTypeCulul:
		if culul, err := rec.ParseCululRecord(); err == nil {
			fmt.Printf("%s  Parsed Culul: Flags=0x%x ICB=0x%x PayloadAddr=0x%x ArrayCount=%d\n",
				indent, culul.CommandFlags, culul.ICBAddr, culul.PayloadAddr, culul.ArrayCount)
		}
	case trace.RecordTypeCtulul:
		if ctulul, err := rec.ParseCtululRecord(); err == nil {
			fmt.Printf("%s  Parsed Ctulul: Pipeline=0x%x Count=%d Bindings=%x\n",
				indent, ctulul.PipelineAddr, ctulul.BindingCount, ctulul.BufferBindings)
		}
	case trace.RecordTypeC:
		if c, err := rec.ParseCRecord(); err == nil {
			fmt.Printf("%s  %s: Flags=%s Encoder=%s\n", indent, Colorize("Parsed C", ColorGreen),
				Colorize(fmt.Sprintf("0x%x", c.CommandFlags), ColorCyan),
				Colorize(fmt.Sprintf("0x%x", c.EncoderAddr), ColorCyan))
		}
	}

	// Always attempt nested parsing for any record type if size is sufficient
	if len(rec.Data) > 16 {
		// Use shared nested parsing logic
		var t trace.Trace // Helper instance
		if nested, err := t.ParseNestedRecords(rec); err == nil && len(nested) > 0 {
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
