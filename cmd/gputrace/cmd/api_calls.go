package cmd

import (
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
}

var apiCallsCmd = newAPICallsCommand(&apiCallsOptions{})

func newAPICallsCommand(opts *apiCallsOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "api-calls <trace.gputrace>",
		Short: "Display API call sequences from a GPU trace",
		Long: `Display the sequence of Metal API calls captured in a GPU trace.

Shows the full API call sequence including:
- Command buffer creation
- Encoder creation and configuration
- Compute pipeline state setup
- Buffer bindings
- Dispatch calls
- Encoder completion

Each call is numbered and indented to show the command buffer hierarchy.

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

	if opts.json {
		apiList, err := trace.ParseAPICallList()
		if err != nil {
			return fmt.Errorf("parse API calls: %w", err)
		}
		return writeAPICallsJSON(cmd.OutOrStdout(), apiList)
	}

	if opts.kernelFilter != "" {
		// Use filtered output
		if err := formatAPICallsFiltered(cmd.OutOrStdout(), trace, opts.kernelFilter); err != nil {
			return fmt.Errorf("failed to format API calls: %w", err)
		}
	} else {
		// Use FormatAPICallList which prints to stdout
		if err := trace.FormatAPICallList(cmd.OutOrStdout()); err != nil {
			return fmt.Errorf("failed to format API calls: %w", err)
		}
	}

	return nil
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
