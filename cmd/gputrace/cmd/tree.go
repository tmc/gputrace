package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/trace"
)

var treeCmd = &cobra.Command{
	Use:   "tree [trace-path]",
	Short: "Display execution tree grouped by pipeline state",
	Long:  `Display a hierarchical view of GPU execution, grouped by Compute Pipeline State, then by Kernel.`,
	Args:  cobra.ExactArgs(1),
	RunE:  runTree,
}

func init() {
	rootCmd.AddCommand(treeCmd)
}

func runTree(cmd *cobra.Command, args []string) error {
	tracePath := args[0]
	t, err := trace.Open(tracePath)
	if err != nil {
		return fmt.Errorf("open trace: %w", err)
	}
	defer t.Close()

	// 1. Parse all MTSP records to rebuild linear execution history
	rawRecords, err := t.ParseMTSPRecords()
	if err != nil {
		return fmt.Errorf("parse records: %w", err)
	}

	// Flatten nested records
	var records []trace.MTSPRecord
	var flatten func([]trace.MTSPRecord) error
	flatten = func(recs []trace.MTSPRecord) error {
		for _, rec := range recs {
			if rec.Type == trace.RecordTypeUnknown && len(rec.Data) > 64 {
				// Special check: If record contains dispatch marker, do NOT flatten it,
				// to avoid splitting the marker across nested record boundaries.
				if bytesContains(rec.Data, []byte("ul@3")) {
					records = append(records, rec)
					continue
				}

				// Try parsing as container (skip 16 bytes header)
				if len(rec.Data) > 16 {
					nested, err := t.ParseMTSPFromData(rec.Data[16:])
					if err == nil && len(nested) > 0 {
						// Heuristic: If we found multiple records, assume valid container
						if len(nested) > 0 {
							if err := flatten(nested); err != nil {
								return err
							}
							continue
						}
					}
				}
			}
			records = append(records, rec)
		}
		return nil
	}
	if err := flatten(rawRecords); err != nil {
		return fmt.Errorf("flatten records: %w", err)
	}

	// 2. Build symbol table (FunctionAddr -> Name)
	// Scan device resources for CS records which define function names
	addrToName := make(map[uint64]string)

	for _, data := range t.DeviceResources {
		resRecords, err := t.ParseMTSPFromData(data)
		if err != nil {
			continue
		}
		for _, rec := range resRecords {
			if (rec.Type == trace.RecordTypeCS || rec.Type == trace.RecordTypeCSuwuw) && rec.Label != "" && rec.Address != 0 {
				addrToName[rec.Address] = rec.Label
			}
		}
	}
	// Also scan main records for CS records (just in case definitions are inline)
	for _, rec := range records {
		if (rec.Type == trace.RecordTypeCS || rec.Type == trace.RecordTypeCSuwuw) && rec.Label != "" && rec.Address != 0 {
			addrToName[rec.Address] = rec.Label
		}
	}

	// 3. Reconstruct Execution Tree
	type Dispatch struct {
		ID     int
		Offset int
	}

	type KernelNode struct {
		FunctionAddr uint64
		// Name resolved later
		CommandFlags   uint32
		BufferBindings []uint64 // Unique buffer addresses used
		Dispatches     []Dispatch
	}

	type PipelineNode struct {
		Address uint64
		Kernels []*KernelNode // Ordered list of kernel invocations
	}

	pipelineMap := make(map[uint64]*PipelineNode)
	var rootPipelines []*PipelineNode // For deterministic ordering (e.g. by first use)

	var currentPipeline *PipelineNode
	var currentKernel *KernelNode // "Active" kernel node under current pipeline

	// Scan records
	// fmt.Printf("DEBUG: Scanning %d records\n", len(records))
	for _, rec := range records {
		if rec.Type == trace.RecordTypeCt {
			// ctCount++
			ct, err := rec.ParseCtRecord()
			if err != nil {
				// fmt.Printf("DEBUG: Failed to parse Ct: %v\n", err)
				continue
			}

			// Pipeline Change?
			pNode, exists := pipelineMap[ct.PipelineAddr]
			if !exists {
				pNode = &PipelineNode{
					Address: ct.PipelineAddr,
					Kernels: []*KernelNode{},
				}
				pipelineMap[ct.PipelineAddr] = pNode
				rootPipelines = append(rootPipelines, pNode)
			}
			currentPipeline = pNode

			// Function (Kernel) Change?
			// Check if we can reuse the current kernel node
			if len(currentPipeline.Kernels) > 0 {
				last := currentPipeline.Kernels[len(currentPipeline.Kernels)-1]
				if last.FunctionAddr == ct.FunctionAddr {
					currentKernel = last
					// Add new bindings if any
					for _, b := range ct.BufferBindings {
						found := false
						for _, existing := range currentKernel.BufferBindings {
							if existing == b {
								found = true
								break
							}
						}
						if !found {
							currentKernel.BufferBindings = append(currentKernel.BufferBindings, b)
						}
					}
					continue
				}
			}

			kNode := &KernelNode{
				FunctionAddr:   ct.FunctionAddr,
				CommandFlags:   ct.CommandFlags,
				BufferBindings: ct.BufferBindings, // Copy initial bindings
				Dispatches:     []Dispatch{},
			}
			currentPipeline.Kernels = append(currentPipeline.Kernels, kNode)
			currentKernel = kNode
		}

		// Check for Dispatch (ul@3 marker)
		if bytesContains(rec.Data, []byte("ul@3")) {
			// Found a dispatch!
			if currentKernel != nil {
				currentKernel.Dispatches = append(currentKernel.Dispatches, Dispatch{
					ID:     len(currentKernel.Dispatches), // Local ID
					Offset: rec.Offset,
				})
			} else {
				fmt.Printf("WARNING: Found ul@3 at offset %d but currentKernel is nil!\n", rec.Offset)
			}
		}
	}

	// 4. Resolve Names using MTLB Heuristic if needed
	// Collect unique function addresses in order of appearance
	uniqueFuncs := make([]uint64, 0)
	seenFuncs := make(map[uint64]bool)
	for _, p := range rootPipelines {
		for _, k := range p.Kernels {
			if !seenFuncs[k.FunctionAddr] {
				seenFuncs[k.FunctionAddr] = true
				uniqueFuncs = append(uniqueFuncs, k.FunctionAddr)
			}
		}
	}

	// Use addrToName first, then fallback to KernelNames (MTLB)
	finalNames := make(map[uint64]string)
	mtlbIndex := 0
	for _, addr := range uniqueFuncs {
		if name, ok := addrToName[addr]; ok {
			finalNames[addr] = name
		} else {
			// Fallback to MTLB list if available
			if mtlbIndex < len(t.KernelNames) {
				finalNames[addr] = t.KernelNames[mtlbIndex] // Heuristic: One-to-one mapping
				mtlbIndex++
			} else {
				finalNames[addr] = fmt.Sprintf("Unknown_0x%x", addr)
			}
		}
	}

	// 5. Print Tree
	fmt.Println("GpuTrace Execution Tree")
	for _, p := range rootPipelines {
		fmt.Printf("▼ Compute Pipeline 0x%x\n", p.Address)
		for _, k := range p.Kernels {
			name := finalNames[k.FunctionAddr]
			fmt.Printf("  ▼ %s (Flags: 0x%x)\n", name, k.CommandFlags)
			for i, b := range k.BufferBindings {
				bName := addrToName[b]
				if bName != "" {
					fmt.Printf("    - Buffer %d: %s (0x%x)\n", i, bName, b)
				} else {
					fmt.Printf("    - Buffer %d: 0x%x\n", i, b)
				}
			}
			fmt.Printf("    ▶ DispatchThreadgroups (%d calls)\n", len(k.Dispatches))
		}
	}

	return nil
}

func bytesContains(s, substr []byte) bool {
	return strings.Contains(string(s), string(substr))
}
