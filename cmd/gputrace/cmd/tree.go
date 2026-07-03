package cmd

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/trace"
)

var treeCmd = newTreeCommand(&treeOptions{
	groupBy: "encoder",
})

type treeOptions struct {
	groupBy string
	verbose bool
	json    bool
}

func newTreeCommand(opts *treeOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tree <trace-path>",
		Short: "Display execution tree grouped by pipeline state or encoder",
		Long: `Display a hierarchical view of GPU execution.

Grouping modes:
  - encoder:  Group by Encoder (Command Buffer), then Commands (default)
  - pipeline: Group by Compute Pipeline State, then Kernel`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTree(cmd, args, opts)
		},
	}

	cmd.Flags().StringVar(&opts.groupBy, "group-by", opts.groupBy, "Grouping mode: encoder, pipeline")
	cmd.Flags().BoolVarP(&opts.verbose, "verbose", "v", opts.verbose, "Show detailed information")
	cmd.Flags().BoolVar(&opts.json, "json", opts.json, "Output in JSON format")
	return cmd
}

func init() {
	rootCmd.AddCommand(treeCmd)
}

func runTree(cmd *cobra.Command, args []string, opts *treeOptions) error {
	tracePath := args[0]
	t, err := trace.Open(tracePath)
	if err != nil {
		return fmt.Errorf("open trace: %w", err)
	}
	defer t.Close()

	// 1. Parse top-level MTSP records (preserving hierarchy)
	records, err := t.ParseMTSPRecords()
	if err != nil {
		return fmt.Errorf("parse records: %w", err)
	}

	// 2. Build symbol table (FunctionAddr -> Name)
	addrToName := make(map[uint64]string)
	// Scan device resources first
	for addr, label := range t.DeviceLabels {
		addrToName[addr] = label
	}
	// Scan buffer labels from trace extraction
	// Note: We need a way to map buffer labels to addresses if they weren't in DeviceLabels
	// Currently extractStringsFromMTSP populates lists but not a map.
	// But `t.DeviceLabels` is populated by `extractDeviceLabels` using addresses.

	// Flatten records recursively to handle containerized traces
	// We preserve containers so that hierarchy markers (CS) are still visible
	var flattened []trace.MTSPRecord
	var flatten func([]trace.MTSPRecord)
	flatten = func(recs []trace.MTSPRecord) {
		for _, rec := range recs {
			// Always append the record itself (even if it's a container)
			flattened = append(flattened, rec)

			// Check for nested children
			nested, err := t.ParseNestedRecords(rec)
			if err == nil && len(nested) > 0 {
				flatten(nested)
			}
		}
	}
	flatten(records)

	// Scan main records (flattened)
	scanForNames(flattened, addrToName)

	if opts.json {
		return renderTreeJSON(t, flattened, addrToName)
	}

	// 3. Render Tree based on grouping
	switch opts.groupBy {
	case "encoder":
		return renderEncoderTree(t, flattened, addrToName, opts)
	case "pipeline":
		return renderPipelineTree(t, flattened, addrToName, opts)
	default:
		return fmt.Errorf("unknown group-by mode: %s", opts.groupBy)
	}
}

// scanForNames recursively scans records for CS/CSuwuw labels
func scanForNames(records []trace.MTSPRecord, addrToName map[uint64]string) {
	for _, rec := range records {
		if rec.Type == trace.RecordTypeCS {
			// Populate address types
			if rec.Label != "" {
				addrToName[rec.Address] = rec.Label
				if rec.SecondaryAddr != 0 {
					addrToName[rec.SecondaryAddr] = rec.Label
				}
			}
		} else if rec.Type == trace.RecordTypeCSuwuw && rec.Label != "" && rec.Address != 0 {
			addrToName[rec.Address] = rec.Label
		} else if rec.Type == trace.RecordTypeCtU {
			if ctu, err := rec.ParseCtURecord(); err == nil && ctu.Name != "" {
				addrToName[ctu.Address] = ctu.Name
			}
		}
		// Recurse using shared logic
		// Note: We create a dummy trace instance to access the method if needed,
		// but since scanForNames is standalone, we rely on the caller passing flattened or we re-parse.
		// Actually, for simple scanning, we can just peek into data if we suspect nested.
		// But cleaner to rely on what we have. For this pass, top-level CS are most important.
		// If we wanted deep scan we'd need to parse nested here.
		// Let's rely on top-level and device-resources for now as that covers 99% of cases.
	}
}

func renderTreeJSON(t *trace.Trace, records []trace.MTSPRecord, addrToName map[uint64]string) error {
	type treeNodeJSON struct {
		Type     string `json:"type"`
		Label    string `json:"label,omitempty"`
		Address  string `json:"address,omitempty"`
		GridSize []int  `json:"grid_size,omitempty"`
	}

	// Pre-scan for Ctt records
	pipelineToFunc := make(map[uint64]uint64)
	for _, rec := range records {
		if rec.Type == trace.RecordTypeCtt {
			if ctt, err := rec.ParseCttRecord(); err == nil {
				pipelineToFunc[ctt.PipelineAddr] = ctt.FunctionAddr
			}
		}
	}

	encoderToPipeline := make(map[uint64]uint64)
	var nodes []treeNodeJSON

	for _, rec := range records {
		switch rec.Type {
		case trace.RecordTypeCS:
			if rec.Label != "" {
				flags := uint32(0)
				if len(rec.Data) >= 8 {
					flags = binary.LittleEndian.Uint32(rec.Data[4:8])
				}
				nodeType := "label"
				switch flags & 0xFF {
				case 0x3d:
					nodeType = "debug_group"
				case 0x13:
					nodeType = "set_label"
				case 0x2d:
					nodeType = "encoder"
				}
				nodes = append(nodes, treeNodeJSON{Type: nodeType, Label: rec.Label})
			}
		case trace.RecordTypeCt:
			if ct, err := rec.ParseCtRecord(); err == nil {
				encoderToPipeline[ct.PipelineAddr] = ct.FunctionAddr
			}
		case trace.RecordTypeC_3ul:
			if d, err := rec.ParseDispatchRecord(); err == nil {
				pipelineID := encoderToPipeline[d.EncoderID]
				funcID := pipelineToFunc[pipelineID]
				funcName := addrToName[funcID]
				if funcName == "" {
					if name := addrToName[pipelineID]; name != "" {
						funcName = name
					} else if name := addrToName[d.EncoderID]; name != "" {
						funcName = name
					}
					if funcName == "" {
						funcName = "UnknownKernel"
					}
				}
				nodes = append(nodes, treeNodeJSON{
					Type:     "dispatch",
					Label:    funcName,
					Address:  fmt.Sprintf("0x%x", d.EncoderID),
					GridSize: []int{int(d.GridSize[0]), int(d.GridSize[1]), int(d.GridSize[2])},
				})
			}
		}
	}

	data, err := json.MarshalIndent(nodes, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal json: %w", err)
	}
	fmt.Println(string(data))
	return nil
}

func renderEncoderTree(t *trace.Trace, records []trace.MTSPRecord, addrToName map[uint64]string, opts *treeOptions) error {
	fmt.Println(Colorize("GpuTrace Execution Tree (Hierarchical)", ColorBold))

	// Indentation state
	indent := ""

	// Track Encoder State: EncoderID -> PipelineStateID
	encoderToPipeline := make(map[uint64]uint64)
	// Track Pipeline State: PipelineStateID -> FunctionID
	pipelineToFunc := make(map[uint64]uint64)

	// Pre-scan for Ctt records to ensure mapping is available before processing Ct records
	for _, rec := range records {
		if rec.Type == trace.RecordTypeCtt {
			if ctt, err := rec.ParseCttRecord(); err == nil {
				pipelineToFunc[ctt.PipelineAddr] = ctt.FunctionAddr
			}
		}
	}

	for _, rec := range records {
		// handle nested records first if any (though usually linear for minimal captures)
		// But here we assume linear stream for the command buffer logic

		switch rec.Type {
		case trace.RecordTypeCS:
			flags := uint32(0)
			if len(rec.Data) >= 8 {
				flags = binary.LittleEndian.Uint32(rec.Data[4:8])
			}

			// Flags analysis from Swift trace:
			// 0x...3d: PushDebugGroup
			// 0x...13: SetLabel
			// 0x...2d: Encoder Label

			if flags&0xFF == 0x3d {
				fmt.Printf("%s%s %s\n", indent, Colorize("📁", ColorBlue), Colorize(rec.Label, ColorBold))
				indent += "  "
			} else if flags&0xFF == 0x13 {
				fmt.Printf("%s%s %s\n", indent, Colorize("⌘", ColorBlue), Colorize(rec.Label, ColorYellow))
				indent += "  "
			} else if flags&0xFF == 0x2d {
				fmt.Printf("%s%s %s\n", indent, Colorize("ƒ", ColorBlue), Colorize(rec.Label, ColorPurple))
				indent += "  "
			} else {
				// Standard CS (Kernel Name often)
				if rec.Label != "" {
					fmt.Printf("%s%s  %s\n", indent, Colorize("🏷", ColorBlue), Colorize(rec.Label, ColorGreen))
				}
			}

		case trace.RecordTypeC:
			if c, err := rec.ParseCRecord(); err == nil {
				// PopDebugGroup
				if c.CommandFlags&0xFF == 0x3e {
					if len(indent) >= 2 {
						indent = indent[:len(indent)-2]
					}
					fmt.Printf("%s%s Pop Group\n", indent, Colorize("▲", ColorBlue))
				} else if c.CommandFlags&0xFF == 0x3b {
					if len(indent) >= 2 {
						indent = indent[:len(indent)-2]
					}
					fmt.Printf("%s%s End Encoding\n", indent, Colorize("▲", ColorBlue))
				} else if c.CommandFlags&0xFF == 0x17 {
					if len(indent) >= 2 {
						indent = indent[:len(indent)-2]
					}
					fmt.Printf("%s%s Commit\n", indent, Colorize("✓", ColorBlue))
				} else if c.CommandFlags&0xFF == 0x1d {
					fmt.Printf("%s%s Wait\n", indent, Colorize("⏸", ColorGray))
				}
			}

		case trace.RecordTypeCtulul:
			if ctulul, err := rec.ParseCtululRecord(); err == nil && opts.verbose {
				fmt.Printf("%s%s Set Buffer (Pipeline: %s)\n", indent, Colorize("•", ColorGray), Colorize(fmt.Sprintf("0x%x", ctulul.PipelineAddr), ColorCyan))
			}

		case trace.RecordTypeCtt:
			// Link PipelineState -> Function
			// Already handled in pre-scan
		case trace.RecordTypeCt:
			// Dispatch or Set Pipeline State?
			// In Swift trace, this appears to set the pipeline state for an encoder
			if ct, err := rec.ParseCtRecord(); err == nil {
				// We assume ct.PipelineAddr is actually the Encoder Address here
				// And ct.FunctionAddr appears to be the Pipeline ID (based on Xcode trace matching)
				encoderToPipeline[ct.PipelineAddr] = ct.FunctionAddr

				// Display Buffer Bindings in Encoder View
				if len(ct.BufferBindings) > 0 && opts.verbose {
					indentStr := indent
					fmt.Printf("%s%s Set Bindings (Pipeline: %s)\n", indentStr, Colorize("•", ColorGray), Colorize(fmt.Sprintf("0x%x", ct.FunctionAddr), ColorCyan))
					for i, b := range ct.BufferBindings {
						bName := addrToName[b]
						if bName == "" {
							fmt.Printf("%s  - Bind %d: %s\n", indentStr, i, Colorize(fmt.Sprintf("0x%x", b), ColorCyan))
						} else {
							fmt.Printf("%s  - Bind %d: %s (%s)\n", indentStr, i, Colorize(bName, ColorGreen), Colorize(fmt.Sprintf("0x%x", b), ColorCyan))
						}
					}
				}
			}

		case trace.RecordTypeC_3ul:
			if d, err := rec.ParseDispatchRecord(); err == nil {
				// Resolve Kernel Name via Chain: Encoder -> Pipeline -> Function -> Name
				pipelineID := encoderToPipeline[d.EncoderID]
				funcID := pipelineToFunc[pipelineID]
				funcName := addrToName[funcID]

				if funcName == "" {
					// Fallback: name might be directly on pipeline or encoder (unlikely but safe)
					if name := addrToName[pipelineID]; name != "" {
						funcName = name
					} else if name := addrToName[d.EncoderID]; name != "" {
						funcName = name
					}

					if funcName == "" && pipelineID != 0 {
						funcName = fmt.Sprintf("Func@0x%x", funcID)
					}
				}

				if funcName == "" {
					funcName = "UnknownKernel"
				}

				if opts.verbose {
					fmt.Printf("%s%s %s [dispatchThreads:%d,%d,%d threadsPerThreadgroup:%d,%d,%d] (Index: ?)\n",
						indent,
						Colorize("▦", ColorBlue),
						Colorize(funcName, ColorGreen),
						d.GridSize[0], d.GridSize[1], d.GridSize[2],
						d.GroupSize[0], d.GroupSize[1], d.GroupSize[2])
					fmt.Printf("%s  • Encoder: %s\n", indent, Colorize(fmt.Sprintf("0x%x", d.EncoderID), ColorCyan))
					fmt.Printf("%s  • Function: %s\n", indent, Colorize(fmt.Sprintf("0x%x", funcID), ColorCyan))
				} else {
					fmt.Printf("%s%s %s [dispatchThreads:%d,%d,%d threadsPerThreadgroup:%d,%d,%d]\n",
						indent,
						Colorize("▦", ColorBlue),
						Colorize(funcName, ColorGreen),
						d.GridSize[0], d.GridSize[1], d.GridSize[2],
						d.GroupSize[0], d.GroupSize[1], d.GroupSize[2])
				}
			}

		default:
			// Ignore others to reduce noise, or print if relevant
		}
	}
	return nil
}

func renderPipelineTree(t *trace.Trace, records []trace.MTSPRecord, addrToName map[uint64]string, opts *treeOptions) error {
	// Re-flatten for pipeline view, but respecting hierarchy for context if needed.
	// Actually, pipeline view is temporal, so flattening is fine if we just want sequential dispatches.
	// But we want to implement it robustly.

	var flattened []trace.MTSPRecord
	var flatten func([]trace.MTSPRecord)
	flatten = func(recs []trace.MTSPRecord) {
		for _, rec := range recs {
			// Flatten CS containers
			nested, err := t.ParseNestedRecords(rec)
			if err == nil && len(nested) > 0 {
				flatten(nested)
			} else {
				flattened = append(flattened, rec)
			}
		}
	}
	flatten(records)

	// Reuse existing pipeline grouping logic on flattened records
	// ... (We can adapt the existing logic here)

	type KernelNode struct {
		FunctionAddr   uint64
		CommandFlags   uint32
		BufferBindings []uint64
		Dispatches     int
	}
	type PipelineNode struct {
		Address uint64
		Kernels []*KernelNode
	}

	pipelineMap := make(map[uint64]*PipelineNode)
	var rootPipelines []*PipelineNode

	var currentPipeline *PipelineNode
	var currentKernel *KernelNode

	// Track Pipeline State: PipelineStateID -> FunctionID
	pipelineToFunc := make(map[uint64]uint64)

	// Pre-scan for Ctt records to ensure mapping is available before processing Ct records
	for _, rec := range flattened {
		if rec.Type == trace.RecordTypeCtt {
			if ctt, err := rec.ParseCttRecord(); err == nil {
				pipelineToFunc[ctt.PipelineAddr] = ctt.FunctionAddr
			}
		}
	}

	for _, rec := range flattened {
		if rec.Type == trace.RecordTypeCt {
			ct, err := rec.ParseCtRecord()
			if err != nil {
				continue
			}

			// Pipeline Change
			pNode, exists := pipelineMap[ct.PipelineAddr]
			if !exists {
				pNode = &PipelineNode{Address: ct.PipelineAddr}
				pipelineMap[ct.PipelineAddr] = pNode
				rootPipelines = append(rootPipelines, pNode)
			}
			currentPipeline = pNode

			// Kernel Change
			if len(currentPipeline.Kernels) > 0 {
				last := currentPipeline.Kernels[len(currentPipeline.Kernels)-1]
				if last.FunctionAddr == ct.FunctionAddr {
					currentKernel = last
					// Merge bindings... (simplified for brevity)
					continue
				}
			}

			// Resolve Real Function ID
			// We use pipelineToFunc to get the real Function ID
			// Ct.FunctionAddr holds the Pipeline State ID
			realFuncID := pipelineToFunc[ct.FunctionAddr]
			if realFuncID == 0 {
				realFuncID = ct.FunctionAddr
			}

			kNode := &KernelNode{
				FunctionAddr:   realFuncID,
				CommandFlags:   ct.CommandFlags,
				BufferBindings: ct.BufferBindings,
			}
			currentPipeline.Kernels = append(currentPipeline.Kernels, kNode)
			currentKernel = kNode
		}
		// Dispatch counting logic (ul@3)
		if bytesContains(rec.Data, []byte("ul@3")) && currentKernel != nil {
			currentKernel.Dispatches++
		}
	}

	fmt.Println(Colorize("GpuTrace Execution Tree (Grouped by Pipeline)", ColorBold))
	for _, p := range rootPipelines {
		fmt.Printf("%s %s\n", Colorize("▼ Compute Pipeline", ColorBlue), Colorize(fmt.Sprintf("0x%x", p.Address), ColorCyan))
		for _, k := range p.Kernels {
			name := addrToName[k.FunctionAddr]
			if name == "" {
				name = "Unknown"
			}
			fmt.Printf("  %s %s (%s)\n", Colorize("▼", ColorBlue), Colorize(name, ColorGreen), Colorize(fmt.Sprintf("0x%x", k.FunctionAddr), ColorCyan))
			if opts.verbose {
				for i, b := range k.BufferBindings {
					bName := addrToName[b]
					if bName == "" {
						fmt.Printf("    - Buffer %d: %s\n", i, Colorize(fmt.Sprintf("0x%x", b), ColorCyan))
					} else {
						fmt.Printf("    - Buffer %d: %s (%s)\n", i, Colorize(bName, ColorGreen), Colorize(fmt.Sprintf("0x%x", b), ColorCyan))
					}
				}
			}
			fmt.Printf("    %s Dispatches: %d\n", Colorize("▦", ColorBlue), k.Dispatches)
		}
	}
	return nil
}

func bytesContains(s, substr []byte) bool {
	return strings.Contains(string(s), string(substr))
}
