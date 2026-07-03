package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace"
)

type dumpOptions struct {
	filter             string
	noIndent           bool
	noNumbers          bool
	buffersOnly        bool
	dispatchOnly       bool
	encodersOnly       bool
	json               bool
	full               bool
	commandBufferIndex int
}

var dumpCmd = newDumpCommand(&dumpOptions{commandBufferIndex: -1})

func newDumpCommand(opts *dumpOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dump <trace-path>",
		Short: "Dump all API calls from a GPU trace",
		Long: `Dumps all Metal API calls from a GPU trace in a format similar to Xcode Instruments.

The output shows:
- Initialization calls (buffer/library/pipeline creation)
- Command buffer execution with all encoder calls
- Buffer bindings and dispatch calls

Filtering options:
  --filter <pattern>    Only show API calls matching pattern
  --buffers-only        Show only buffer creation calls
  --dispatch-only       Show only dispatch calls
  --encoders-only       Show only encoder-related calls
  --command-buffer <n>  Show only calls from command buffer N

Formatting options:
  --no-indent          Disable indentation for nested calls
  --no-numbers         Don't number the API calls
  --json               Output in JSON format
  --full               Show expanded tree view with all call levels

Examples:
  gputrace dump trace.gputrace
  gputrace dump trace.gputrace --full
  gputrace dump trace.gputrace --filter "Buffer"
  gputrace dump trace.gputrace --dispatch-only
  gputrace dump trace.gputrace --command-buffer 0
`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDump(cmd, args, *opts)
		},
	}
	cmd.Flags().StringVar(&opts.filter, "filter", "", "Filter API calls by pattern")
	cmd.Flags().BoolVar(&opts.noIndent, "no-indent", false, "Disable indentation")
	cmd.Flags().BoolVar(&opts.noNumbers, "no-numbers", false, "Don't number API calls")
	cmd.Flags().BoolVar(&opts.buffersOnly, "buffers-only", false, "Show only buffer creation calls")
	cmd.Flags().BoolVar(&opts.dispatchOnly, "dispatch-only", false, "Show only dispatch calls")
	cmd.Flags().BoolVar(&opts.encodersOnly, "encoders-only", false, "Show only encoder-related calls")
	cmd.Flags().BoolVar(&opts.json, "json", false, "Output in JSON format")
	cmd.Flags().BoolVar(&opts.full, "full", false, "Show expanded tree view with all call levels")
	cmd.Flags().IntVar(&opts.commandBufferIndex, "command-buffer", -1, "Show only calls from specific command buffer")
	return cmd
}

func init() {
	rootCmd.AddCommand(dumpCmd)
}

func runDump(cmd *cobra.Command, args []string, opts dumpOptions) error {
	if err := validateDumpOptions(opts); err != nil {
		return err
	}

	tracePath := args[0]

	trace, err := gputrace.Open(tracePath)
	if err != nil {
		return fmt.Errorf("open trace: %w", err)
	}

	apiList, err := trace.ParseAPICallList()
	if err != nil {
		return fmt.Errorf("parse api calls: %w", err)
	}
	apiList = filterDumpAPICallList(apiList, opts)

	w := dumpOutput(cmd)
	if opts.json {
		if err := writeDumpJSON(w, apiList); err != nil {
			return fmt.Errorf("write json: %w", err)
		}
		return nil
	}

	if opts.full && !dumpHasContentFilters(opts) {
		err = formatDumpAPICallListFull(w, apiList, opts)
	} else {
		err = formatDumpAPICallList(w, apiList, opts)
	}
	if err != nil {
		return fmt.Errorf("format api calls: %w", err)
	}

	return nil
}

func validateDumpOptions(opts dumpOptions) error {
	if opts.commandBufferIndex < -1 {
		return fmt.Errorf("--command-buffer must be >= -1")
	}
	return nil
}

func dumpOutput(cmd *cobra.Command) io.Writer {
	if cmd == nil {
		return os.Stdout
	}
	return cmd.OutOrStdout()
}

func writeDumpJSON(w io.Writer, apiList *gputrace.APICallList) error {
	data, err := json.MarshalIndent(apiList, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	if _, err := io.WriteString(w, "\n"); err != nil {
		return err
	}
	return nil
}

func filterDumpAPICallList(apiList *gputrace.APICallList, opts dumpOptions) *gputrace.APICallList {
	filtered := &gputrace.APICallList{}

	if opts.commandBufferIndex < 0 {
		for _, call := range apiList.InitCalls {
			if dumpInitCallMatches(call, opts) {
				filtered.InitCalls = append(filtered.InitCalls, call)
			}
		}
	}

	for _, cb := range apiList.CommandBuffers {
		if opts.commandBufferIndex >= 0 && cb.Index != opts.commandBufferIndex {
			continue
		}

		filteredCB := cb
		filteredCB.Calls = nil
		for _, call := range cb.Calls {
			if dumpFormattedCallMatches(call, opts) {
				filteredCB.Calls = append(filteredCB.Calls, call)
			}
		}

		if dumpHasContentFilters(opts) {
			if len(filteredCB.Calls) == 0 && !dumpCommandBufferMatches(cb, opts) {
				continue
			}
		} else {
			filteredCB.Calls = append([]gputrace.FormattedAPICall(nil), cb.Calls...)
		}
		filtered.CommandBuffers = append(filtered.CommandBuffers, filteredCB)
	}

	return filtered
}

func dumpInitCallMatches(call gputrace.InitCall, opts dumpOptions) bool {
	if !dumpInitCallMatchesCategory(call, opts) {
		return false
	}
	return dumpMatchesFilter(opts.filter, call.Type, call.Label, call.Info, fmt.Sprintf("0x%x", call.Address))
}

func dumpFormattedCallMatches(call gputrace.FormattedAPICall, opts dumpOptions) bool {
	if !dumpFormattedCallMatchesCategory(call, opts) {
		return false
	}
	return dumpMatchesFilter(opts.filter, call.Type, call.Label, call.Details, fmt.Sprintf("0x%x", call.Address))
}

func dumpCommandBufferMatches(cb gputrace.CommandBufferCalls, opts dumpOptions) bool {
	return dumpCommandBufferHeaderMatches(cb, opts) || dumpCommandBufferLabelMatches(cb, opts)
}

func dumpCommandBufferHeaderMatches(cb gputrace.CommandBufferCalls, opts dumpOptions) bool {
	if dumpHasCategoryFilters(opts) {
		return false
	}
	return dumpMatchesFilter(opts.filter,
		"commandBuffer",
		cb.Label,
		fmt.Sprintf("0x%x", cb.Address),
		fmt.Sprintf("0x%x", cb.QueueAddress),
	)
}

func dumpCommandBufferLabelMatches(cb gputrace.CommandBufferCalls, opts dumpOptions) bool {
	if cb.Label == "" || dumpHasCategoryFilters(opts) {
		return false
	}
	return dumpMatchesFilter(opts.filter, "setLabel", cb.Label)
}

func dumpInitCallMatchesCategory(call gputrace.InitCall, opts dumpOptions) bool {
	if !dumpHasCategoryFilters(opts) {
		return true
	}
	return opts.buffersOnly && call.Type == "newBuffer"
}

func dumpFormattedCallMatchesCategory(call gputrace.FormattedAPICall, opts dumpOptions) bool {
	if !dumpHasCategoryFilters(opts) {
		return true
	}
	return opts.dispatchOnly && call.Type == "dispatch" ||
		opts.encodersOnly && dumpIsEncoderCall(call)
}

func dumpIsEncoderCall(call gputrace.FormattedAPICall) bool {
	return call.Indented ||
		call.Type == "encoder" ||
		call.Type == "endEncoding"
}

func dumpHasContentFilters(opts dumpOptions) bool {
	return opts.filter != "" || dumpHasCategoryFilters(opts)
}

func dumpHasCategoryFilters(opts dumpOptions) bool {
	return opts.buffersOnly || opts.dispatchOnly || opts.encodersOnly
}

func dumpMatchesFilter(filter string, fields ...string) bool {
	if filter == "" {
		return true
	}
	filter = strings.ToLower(filter)
	for _, field := range fields {
		if strings.Contains(strings.ToLower(field), filter) {
			return true
		}
	}
	return false
}

func formatDumpAPICallList(w io.Writer, apiList *gputrace.APICallList, opts dumpOptions) error {
	p := dumpPrinter{w: w, opts: opts}

	for _, call := range apiList.InitCalls {
		if dumpSkipInitCall(call) {
			continue
		}
		p.writeInitCall(p.next, call)
		p.next++
	}

	for _, cb := range apiList.CommandBuffers {
		if dumpShouldPrintCommandBufferHeader(cb, opts) {
			p.writeCommandBufferHeader(p.next, cb)
			p.next++
		}
		if dumpShouldPrintCommandBufferLabel(cb, opts) {
			p.writeCommandBufferLabel(p.next, cb)
			p.next++
		}
		for _, call := range cb.Calls {
			p.writeFormattedCall(p.next, call, call.Indented)
			p.next++
		}
	}

	return p.err
}

func formatDumpAPICallListFull(w io.Writer, apiList *gputrace.APICallList, opts dumpOptions) error {
	p := dumpPrinter{w: w, opts: opts}

	for _, call := range apiList.InitCalls {
		if dumpSkipInitCall(call) {
			continue
		}
		p.writeInitCall(p.next, call)
		p.next++
	}

	for _, cb := range apiList.CommandBuffers {
		cbStartNum := p.next

		p.writeCommandBufferHeader(p.next, cb)
		p.next++

		if cb.Label != "" {
			p.writeCommandBufferLabel(p.next, cb)
			p.next++
		}

		cbCallStart := p.next
		for _, call := range cb.Calls {
			p.writeFormattedCall(p.next, call, call.Indented)
			p.next++
		}

		p.writeBlankLine()

		p.writeCommandBufferHeader(cbStartNum, cb)
		if cb.Label != "" {
			p.writeCommandBufferLabel(cbStartNum+1, cb)
		}

		firstEncoderEnd := len(cb.Calls)
		encoderCount := 0
		for i, call := range cb.Calls {
			if call.Type == "encoder" {
				encoderCount++
			}
			if call.Type == "endEncoding" {
				encoderCount--
				if encoderCount == 0 {
					firstEncoderEnd = i + 1
					break
				}
			}
		}

		for i := 0; i < firstEncoderEnd; i++ {
			p.writeFormattedCall(cbCallStart+i, cb.Calls[i], false)
		}

		p.writeBlankLine()

		encoders := dumpGroupEncoderCalls(cb.Calls)
		for _, encoder := range encoders {
			for _, callIdx := range encoder.calls {
				p.writeFormattedCall(cbCallStart+callIdx, cb.Calls[callIdx], false)
			}
		}

		if len(encoders) > 0 {
			p.writeBlankLine()
			lastEncoder := encoders[len(encoders)-1]
			for _, callIdx := range lastEncoder.calls {
				p.writeFormattedCall(cbCallStart+callIdx, cb.Calls[callIdx], false)
			}
		}

		for i, call := range cb.Calls {
			if !call.Indented {
				p.writeFormattedCall(cbCallStart+i, call, false)
			}
		}
	}

	return p.err
}

type dumpEncoderCalls struct {
	calls []int
}

func dumpGroupEncoderCalls(calls []gputrace.FormattedAPICall) []dumpEncoderCalls {
	var encoders []dumpEncoderCalls
	currentEncoder := -1
	for i, call := range calls {
		if call.Type == "encoder" {
			encoders = append(encoders, dumpEncoderCalls{calls: []int{i}})
			currentEncoder = len(encoders) - 1
			continue
		}
		if currentEncoder >= 0 && call.Indented {
			encoders[currentEncoder].calls = append(encoders[currentEncoder].calls, i)
		}
	}
	return encoders
}

func dumpShouldPrintCommandBufferHeader(cb gputrace.CommandBufferCalls, opts dumpOptions) bool {
	if !dumpHasContentFilters(opts) {
		return true
	}
	return dumpCommandBufferHeaderMatches(cb, opts)
}

func dumpShouldPrintCommandBufferLabel(cb gputrace.CommandBufferCalls, opts dumpOptions) bool {
	if !dumpHasContentFilters(opts) {
		return cb.Label != ""
	}
	return dumpCommandBufferLabelMatches(cb, opts)
}

func dumpSkipInitCall(call gputrace.InitCall) bool {
	return call.Type == "bufferHeapOffset" || call.Type == "newSharedEvent"
}

type dumpPrinter struct {
	w    io.Writer
	opts dumpOptions
	next int
	err  error
}

func (p *dumpPrinter) writeInitCall(num int, call gputrace.InitCall) {
	if call.Type == "setLabel" || call.Type == "requestResidency" || call.Type == "addResidencySet" {
		p.writeLine(num, false, call.Info)
		return
	}

	prefix := fmt.Sprintf("0x%x", call.Address)
	if call.Label != "" {
		prefix = call.Label
	}
	p.writeLine(num, false, fmt.Sprintf("%s = %s", prefix, call.Info))
}

func (p *dumpPrinter) writeCommandBufferHeader(num int, cb gputrace.CommandBufferCalls) {
	prefix := fmt.Sprintf("0x%x", cb.Address)
	if cb.Label != "" {
		prefix = cb.Label
	}
	p.writeLine(num, false, fmt.Sprintf("%s = [0x%x commandBuffer]", prefix, cb.QueueAddress))
}

func (p *dumpPrinter) writeCommandBufferLabel(num int, cb gputrace.CommandBufferCalls) {
	p.writeLine(num, false, fmt.Sprintf("[setLabel:\"%s\"]", cb.Label))
}

func (p *dumpPrinter) writeFormattedCall(num int, call gputrace.FormattedAPICall, indented bool) {
	if call.Address != 0 {
		prefix := fmt.Sprintf("0x%x", call.Address)
		if call.Label != "" {
			prefix = call.Label
		}
		p.writeLine(num, indented, fmt.Sprintf("%s = [%s]", prefix, call.Details))
		return
	}
	p.writeLine(num, indented, fmt.Sprintf("[%s]", call.Details))
}

func (p *dumpPrinter) writeBlankLine() {
	if p.err != nil {
		return
	}
	_, p.err = io.WriteString(p.w, "\n")
}

func (p *dumpPrinter) writeLine(num int, indented bool, text string) {
	if p.err != nil {
		return
	}

	if indented && !p.opts.noIndent {
		if _, p.err = io.WriteString(p.w, "\t"); p.err != nil {
			return
		}
	}
	if !p.opts.noNumbers {
		if _, p.err = fmt.Fprintf(p.w, "#%d ", num); p.err != nil {
			return
		}
	}
	_, p.err = fmt.Fprintln(p.w, text)
}
