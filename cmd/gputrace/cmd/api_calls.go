package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace"
)

type apiCallsOptions struct {
	kernelFilter string
	json         bool
	limit        int
	all          bool
}

var apiCallsCmd = newAPICallsCommand(&apiCallsOptions{limit: defaultHumanLimit})

func newAPICallsCommand(opts *apiCallsOptions) *cobra.Command {
	if opts.limit == 0 {
		opts.limit = defaultHumanLimit
	}
	cmd := &cobra.Command{
		Use:   "api-calls <trace.gputrace>",
		Short: "Display API call sequences from a GPU trace",
		Long: `Display the decoded subset of Metal API calls captured in a GPU trace.

Shows decoded API calls including:
- Command buffer creation
- Encoder creation and configuration
- Compute pipeline state setup
- Buffer bindings
- Dispatch calls
- Encoder completion

Each call is numbered and indented to show the decoded command buffer hierarchy.
Human output reports how many trace dispatches are represented; a low count
means the decoded API list is incomplete, not that the trace did no GPU work.

Examples:
  # Show all API calls
  gputrace api-calls trace.gputrace

  # Show first 100 API calls
  gputrace api-calls trace.gputrace | head -100

  # Search for specific API calls
  gputrace api-calls trace.gputrace | grep setBuffer

  # Filter by kernel name
  gputrace api-calls trace.gputrace --kernel g3_copy`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAPICalls(cmd, args, opts)
		},
	}
	cmd.Flags().StringVarP(&opts.kernelFilter, "kernel", "k", "", "Filter output to show only calls related to kernels matching this pattern (case-insensitive)")
	cmd.Flags().BoolVar(&opts.json, "json", false, "Output in JSON format")
	cmd.Flags().IntVar(&opts.limit, "limit", opts.limit, "Maximum calls in human output")
	cmd.Flags().BoolVar(&opts.all, "all", opts.all, "Show all calls in human output")
	return cmd
}

func init() {
	rootCmd.AddCommand(apiCallsCmd)
}

func runAPICalls(cmd *cobra.Command, args []string, opts *apiCallsOptions) error {
	tracePath := args[0]
	if err := checkTraceFile(tracePath); err != nil {
		return err
	}

	trace, err := gputrace.Open(tracePath)
	if err != nil {
		return fmt.Errorf("failed to open trace: %w", err)
	}

	apiList, err := trace.ParseAPICallList()
	if err != nil {
		return fmt.Errorf("parse API calls: %w", err)
	}
	if opts.json {
		return writeAPICallsJSON(cmd.OutOrStdout(), apiList)
	}
	limit, err := resolveHumanLimit(opts.limit, opts.all)
	if err != nil {
		return err
	}

	var rendered bytes.Buffer
	if opts.kernelFilter != "" {
		// Use filtered output
		if err := formatAPICallsFiltered(&rendered, trace, opts.kernelFilter); err != nil {
			return fmt.Errorf("failed to format API calls: %w", err)
		}
	} else {
		if err := trace.FormatAPICallList(&rendered); err != nil {
			return fmt.Errorf("failed to format API calls: %w", err)
		}
	}

	decodedDispatches := 0
	for _, cb := range apiList.CommandBuffers {
		for _, call := range cb.Calls {
			if call.Type == "dispatch" {
				decodedDispatches++
			}
		}
	}
	traceDispatches, _ := trace.CountDispatchCalls()
	fmt.Fprintf(cmd.OutOrStdout(), "Decoded API subset: %d of %d trace dispatches represented\n\n",
		decodedDispatches, traceDispatches)
	return writeLimitedLines(cmd.OutOrStdout(), rendered.String(), limit, "calls")
}

func writeAPICallsJSON(w io.Writer, apiList *gputrace.APICallList) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(apiList); err != nil {
		return fmt.Errorf("marshal json: %w", err)
	}
	return nil
}

// formatAPICallsFiltered formats API calls, filtering by kernel name.
func formatAPICallsFiltered(w io.Writer, trace *gputrace.Trace, kernelFilter string) error {
	apiList, err := trace.ParseAPICallList()
	if err != nil {
		return fmt.Errorf("parse API calls: %w", err)
	}

	filterLower := strings.ToLower(kernelFilter)

	// Format init calls (always show)
	displayCallNum := 0
	for _, call := range apiList.InitCalls {
		if call.Type == "bufferHeapOffset" || call.Type == "newSharedEvent" {
			continue
		}

		if call.Type == "setLabel" || call.Type == "requestResidency" || call.Type == "addResidencySet" {
			fmt.Fprintf(w, "#%d %s\n", displayCallNum, call.Info)
		} else {
			prefix := fmt.Sprintf("0x%x", call.Address)
			if call.Label != "" {
				prefix = call.Label
			}
			fmt.Fprintf(w, "#%d %s = %s\n", displayCallNum, prefix, call.Info)
		}
		displayCallNum++
	}

	// Format command buffers, filtering by kernel match
	for _, cb := range apiList.CommandBuffers {
		// Check if this CB contains a matching kernel
		hasMatchingKernel := false
		for _, call := range cb.Calls {
			if call.Type == "setPipelineState" && strings.Contains(strings.ToLower(call.Details), filterLower) {
				hasMatchingKernel = true
				break
			}
			if call.Type == "encoder" && strings.Contains(strings.ToLower(call.Label), filterLower) {
				hasMatchingKernel = true
				break
			}
		}

		if !hasMatchingKernel {
			continue
		}

		// Show command buffer header
		cbPrefix := fmt.Sprintf("0x%x", cb.Address)
		if cb.Label != "" {
			cbPrefix = cb.Label
		}
		fmt.Fprintf(w, "#%d %s = [0x%x commandBuffer]\n", displayCallNum, cbPrefix, cb.QueueAddress)
		displayCallNum++

		if cb.Label != "" {
			fmt.Fprintf(w, "#%d [setLabel:\"%s\"]\n", displayCallNum, cb.Label)
			displayCallNum++
		}

		// Show all calls in this CB (since it has a matching kernel)
		for _, call := range cb.Calls {
			indent := ""
			if call.Indented {
				indent = "\t"
			}

			if call.Address != 0 {
				callPrefix := fmt.Sprintf("0x%x", call.Address)
				if call.Label != "" {
					callPrefix = call.Label
				}
				fmt.Fprintf(w, "%s#%d %s = [%s]\n", indent, displayCallNum, callPrefix, call.Details)
			} else {
				fmt.Fprintf(w, "%s#%d [%s]\n", indent, displayCallNum, call.Details)
			}
			displayCallNum++
		}
	}

	return nil
}
