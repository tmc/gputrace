//go:build darwin

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/spf13/cobra"
)

var (
	xctraceProfileJSON        bool
	xctraceProfileTrace       string
	xctraceProfileOutDir      string
	xctraceProfileProcessName string
	xctraceProfileMaxRows     int
	xctraceProfileTimeout     time.Duration
	xctraceProfileMinFreeGiB  float64
	xctraceProfileMinMemFree  int
	xctraceProfileEncodeSD    bool
)

type xctraceProfileOutput struct {
	Trace                string                                `json:"trace"`
	OutputDir            string                                `json:"output_dir"`
	ProcessName          string                                `json:"process_name"`
	ResourcePreflight    profileResourcePreflight              `json:"resource_preflight"`
	TOC                  xctraceProfileTOCSummary              `json:"toc"`
	Tables               map[string]xctraceProfileTableSummary `json:"tables"`
	IntervalRows         xctraceProfileIntervalRows            `json:"interval_rows"`
	StreamData           xctraceStreamDataOutput               `json:"streamData"`
	StreamDataRequested  bool                                  `json:"streamData_requested"`
	TimingClaimsAllowed  bool                                  `json:"timing_claims_allowed"`
	CounterClaimsAllowed bool                                  `json:"counter_claims_allowed"`
	Reason               string                                `json:"reason,omitempty"`
}

type xctraceProfileTOCSummary struct {
	XML        string               `json:"xml"`
	Export     profileCommandResult `json:"export"`
	RunNumbers []int                `json:"run_numbers,omitempty"`
	Schemas    []string             `json:"schemas,omitempty"`
	Exportable bool                 `json:"exportable"`
}

type xctraceProfileTableSummary struct {
	Schema         string               `json:"schema"`
	XML            string               `json:"xml"`
	Export         profileCommandResult `json:"export"`
	RowCount       int                  `json:"row_count"`
	TargetRowCount int                  `json:"target_row_count,omitempty"`
}

type xctraceProfileIntervalRows struct {
	JSON         filePresence `json:"json"`
	RowsRead     int          `json:"rows_read"`
	RowsMatched  int          `json:"rows_matched"`
	TimingUsable bool         `json:"timing_usable"`
}

var xctraceProfileCmd = &cobra.Command{
	Use:          "xctrace-profile --trace trace.trace --process name --out-dir out",
	Short:        "Export headless xctrace Metal timing rows",
	SilenceUsage: true,
	Long: `Export headless xctrace Metal tables and write target-attributed
Metal GPU interval rows as JSON.

By default this uses public xctrace CLI output only. Pass --encode-streamdata to
also encode rows into .gpuprofiler_raw/streamData for compatibility with
gputrace's existing streamData readers. That opt-in path uses Xcode's local
GPUToolsReplay.framework classes and is not a stable public Apple API.

This command is fail-closed for counters: it reports counter table row counts,
but counter claims remain disabled until non-empty counter samples are parsed.`,
	Args: cobra.NoArgs,
	RunE: runXctraceProfile,
}

func init() {
	rootCmd.AddCommand(xctraceProfileCmd)
	xctraceProfileCmd.Flags().BoolVar(&xctraceProfileJSON, "json", false, "Output in JSON format")
	xctraceProfileCmd.Flags().StringVar(&xctraceProfileTrace, "trace", "", "Input .trace bundle")
	xctraceProfileCmd.Flags().StringVar(&xctraceProfileOutDir, "out-dir", "", "Output directory")
	xctraceProfileCmd.Flags().StringVar(&xctraceProfileProcessName, "process", "", "Process name substring required for timing rows")
	xctraceProfileCmd.Flags().IntVar(&xctraceProfileMaxRows, "max-rows", 20000, "Maximum target interval rows to keep")
	xctraceProfileCmd.Flags().DurationVar(&xctraceProfileTimeout, "timeout", 120*time.Second, "Timeout for each xctrace export/helper step")
	xctraceProfileCmd.Flags().Float64Var(&xctraceProfileMinFreeGiB, "min-out-dir-free-gib", 24, "Minimum free GiB required on the output volume")
	xctraceProfileCmd.Flags().IntVar(&xctraceProfileMinMemFree, "min-memory-free-percent", 10, "Minimum memory_pressure free percentage required")
	xctraceProfileCmd.Flags().BoolVar(&xctraceProfileEncodeSD, "encode-streamdata", false, "Opt in to native .gpuprofiler_raw/streamData encoding using Xcode private GPUToolsReplay classes")
}

func runXctraceProfile(cmd *cobra.Command, args []string) error {
	out, err := runXctraceProfileExport()
	if xctraceProfileJSON && out != nil {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if encodeErr := enc.Encode(out); encodeErr != nil {
			return encodeErr
		}
		return err
	}
	if err != nil {
		return err
	}
	if xctraceProfileJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	fmt.Printf("Timing claims allowed:  %v\n", out.TimingClaimsAllowed)
	fmt.Printf("Counter claims allowed: %v\n", out.CounterClaimsAllowed)
	fmt.Printf("Rows matched:           %d\n", out.IntervalRows.RowsMatched)
	fmt.Printf("Interval rows JSON:     %s (%d bytes)\n", out.IntervalRows.JSON.Path, out.IntervalRows.JSON.Bytes)
	fmt.Printf("streamData requested:   %v\n", out.StreamDataRequested)
	if out.StreamDataRequested {
		fmt.Printf("Rows encoded:           %d\n", out.StreamData.RowsEncoded)
		fmt.Printf("streamData:             %s (%d bytes)\n", out.StreamData.StreamData.Path, out.StreamData.StreamData.Bytes)
	}
	if out.ResourcePreflight.CheckedPath != "" || out.ResourcePreflight.MemoryFreePercent != 0 {
		fmt.Printf("Preflight:              output=%s free=%.1fGiB memory_free=%d%%\n",
			out.ResourcePreflight.OutputDir,
			out.ResourcePreflight.FreeGiB,
			out.ResourcePreflight.MemoryFreePercent,
		)
	}
	if !out.TimingClaimsAllowed {
		return fmt.Errorf("xctrace profile did not produce usable target interval rows")
	}
	return nil
}

func runXctraceProfileExport() (*xctraceProfileOutput, error) {
	if xctraceProfileTrace == "" || xctraceProfileOutDir == "" {
		return nil, fmt.Errorf("--trace and --out-dir are required")
	}
	if xctraceProfileProcessName == "" {
		return nil, fmt.Errorf("--process is required to avoid profiling system-wide GPU rows")
	}
	if err := preflightXctraceProfileResources(xctraceProfileOutDir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(xctraceProfileOutDir, 0o755); err != nil {
		return nil, err
	}

	toc := exportXctraceProfileTOC()
	tables := map[string]xctraceProfileTableSummary{}
	for _, schema := range xctraceProfileSchemas(toc) {
		tables[schema] = exportXctraceProfileTable(schema)
	}

	out := &xctraceProfileOutput{
		Trace:               xctraceProfileTrace,
		OutputDir:           xctraceProfileOutDir,
		ProcessName:         xctraceProfileProcessName,
		ResourcePreflight:   collectResourcePreflight(xctraceProfileOutDir, xctraceProfileMinFreeGiB, xctraceProfileMinMemFree, false),
		TOC:                 toc,
		Tables:              tables,
		StreamDataRequested: xctraceProfileEncodeSD,
		StreamData: xctraceStreamDataOutput{
			OutputDir: filepath.Join(xctraceProfileOutDir, "xctrace-profile.gpuprofiler_raw"),
			ResourcePreflight: collectResourcePreflight(
				xctraceProfileOutDir,
				xctraceProfileMinFreeGiB,
				xctraceProfileMinMemFree,
				false,
			),
			ProcessName:   xctraceProfileProcessName,
			CounterUsable: false,
		},
		TimingClaimsAllowed:  false,
		CounterClaimsAllowed: false,
		Reason:               "timing claims require exported xctrace Metal GPU interval rows for the target process; hardware counter claims remain disabled until non-empty counter samples are parsed",
	}
	intervalTable := tables["metal-gpu-intervals"].XML
	if !toc.Exportable {
		out.StreamData.InputXML = intervalTable
		out.Reason = fmt.Sprintf("xctrace TOC export failed; trace is not exportable by xctrace: %s", toc.Export.StderrPreview)
		return out, errors.New(out.Reason)
	}
	rows, rowsRead, err := parseXctraceGPUIntervalsXML(intervalTable, xctraceProfileProcessName, xctraceProfileMaxRows)
	if err != nil {
		out.StreamData.InputXML = intervalTable
		out.StreamData.RowsRead = rowsRead
		out.Reason = fmt.Sprintf("metal-gpu-intervals export/parse failed: %v", err)
		return out, err
	}
	if len(rows) == 0 {
		err := fmt.Errorf("no metal-gpu-intervals rows matched process %q (rows read: %d)", xctraceProfileProcessName, rowsRead)
		out.StreamData.InputXML = intervalTable
		out.StreamData.RowsRead = rowsRead
		out.Reason = err.Error()
		return out, err
	}
	intervalSummary := tables["metal-gpu-intervals"]
	intervalSummary.TargetRowCount = len(rows)
	tables["metal-gpu-intervals"] = intervalSummary

	intervalRowsPath := filepath.Join(xctraceProfileOutDir, "xctrace-interval-rows.json")
	if err := writeXctraceIntervalRowsJSON(intervalRowsPath, rows); err != nil {
		out.Reason = fmt.Sprintf("write interval rows JSON failed: %v", err)
		return out, err
	}
	out.IntervalRows = xctraceProfileIntervalRows{
		JSON:         presence(intervalRowsPath),
		RowsRead:     rowsRead,
		RowsMatched:  len(rows),
		TimingUsable: true,
	}
	out.TimingClaimsAllowed = true
	out.CounterClaimsAllowed = false
	out.Reason = "timing claims use exported xctrace Metal GPU interval rows for the target process; hardware counter claims remain disabled until non-empty counter samples are parsed"
	if !xctraceProfileEncodeSD {
		return out, nil
	}

	profilerDir := filepath.Join(xctraceProfileOutDir, "xctrace-profile.gpuprofiler_raw")
	if err := os.RemoveAll(profilerDir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(profilerDir, 0o755); err != nil {
		return nil, err
	}
	if err := preflightXctraceProfileResources(profilerDir); err != nil {
		return nil, err
	}
	helper := encodeXctraceRowsWithHelper(rows, profilerDir)
	if helper.Signal == "" && helper.ExitCode == 0 && !helper.TimedOut {
		_ = writeXctraceIntervalRowsJSON(filepath.Join(profilerDir, "xctrace-interval-rows.json"), rows)
	}
	streamStats := summarizeEncodedStreamData(profilerDir)
	streamData := xctraceStreamDataOutput{
		InputXML:  intervalTable,
		OutputDir: profilerDir,
		ResourcePreflight: collectResourcePreflight(
			profilerDir,
			xctraceProfileMinFreeGiB,
			xctraceProfileMinMemFree,
			false,
		),
		RowsRead:        rowsRead,
		RowsEncoded:     len(rows),
		ProcessName:     xctraceProfileProcessName,
		Helper:          helper,
		StreamData:      presence(filepath.Join(profilerDir, "streamData")),
		StreamDataStats: streamStats,
		CounterUsable:   false,
	}
	if streamStats != nil {
		streamData.TimingUsable = streamStats.TimingUsable
	}
	out.StreamData = streamData
	out.TimingClaimsAllowed = out.IntervalRows.TimingUsable && streamData.TimingUsable && streamData.StreamData.Present && streamData.RowsEncoded > 0
	out.CounterClaimsAllowed = false
	out.Reason = "timing claims use exported xctrace Metal GPU interval rows plus opt-in streamData encoding; streamData encoding uses Xcode private GPUToolsReplay classes; hardware counter claims remain disabled until non-empty counter samples are parsed"
	return out, nil
}

func xctraceProfileSchemas(toc xctraceProfileTOCSummary) []string {
	seen := map[string]bool{}
	out := []string{}
	add := func(schema string) {
		if schema == "" || seen[schema] {
			return
		}
		seen[schema] = true
		out = append(out, schema)
	}
	for _, schema := range []string{
		"metal-gpu-intervals",
		"metal-gpu-counter-profile",
		"gpu-counter-value",
		"metal-gpu-counter-intervals",
		"gpu-aps-stream",
		"metal-shader-profiler-shader-list",
		"graphics-compiler-spill-events",
	} {
		add(schema)
	}
	for _, schema := range toc.Schemas {
		if bytes.Contains([]byte(schema), []byte("metal")) || bytes.Contains([]byte(schema), []byte("gpu")) {
			add(schema)
		}
	}
	return out
}

func exportXctraceProfileTOC() xctraceProfileTOCSummary {
	xmlPath := filepath.Join(xctraceProfileOutDir, "xctrace_toc.xml")
	argv := []string{
		"xcrun", "xctrace", "export",
		"--input", xctraceProfileTrace,
		"--toc",
		"--output", xmlPath,
	}
	result := runXctraceProfileCommand("xctrace_export_toc", argv, filepath.Dir(xmlPath))
	summary := xctraceProfileTOCSummary{
		XML:        xmlPath,
		Export:     result,
		Exportable: result.ExitCode == 0 && result.Signal == "" && !result.TimedOut && presence(xmlPath).Present,
	}
	if !summary.Exportable {
		return summary
	}
	runs, schemas := parseXctraceTOC(xmlPath)
	summary.RunNumbers = runs
	summary.Schemas = schemas
	return summary
}

func parseXctraceTOC(path string) ([]int, []string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil
	}
	runRe := regexp.MustCompile(`<run[^>]*\bnumber="([0-9]+)"`)
	schemaRe := regexp.MustCompile(`<table[^>]*\bschema="([^"]+)"`)
	runSet := map[int]bool{}
	for _, match := range runRe.FindAllSubmatch(data, -1) {
		var number int
		if _, err := fmt.Sscanf(string(match[1]), "%d", &number); err == nil {
			runSet[number] = true
		}
	}
	schemaSet := map[string]bool{}
	for _, match := range schemaRe.FindAllSubmatch(data, -1) {
		schemaSet[string(match[1])] = true
	}
	runs := make([]int, 0, len(runSet))
	for run := range runSet {
		runs = append(runs, run)
	}
	sort.Ints(runs)
	schemas := make([]string, 0, len(schemaSet))
	for schema := range schemaSet {
		schemas = append(schemas, schema)
	}
	sort.Strings(schemas)
	return runs, schemas
}

func preflightXctraceProfileResources(output string) error {
	if xctraceProfileMinFreeGiB > 0 {
		freeBytes, checkedPath, err := availableBytesForPath(output)
		if err != nil {
			return fmt.Errorf("resource preflight failed for output %s: %w", output, err)
		}
		freeGiB := float64(freeBytes) / (1024 * 1024 * 1024)
		if freeGiB < xctraceProfileMinFreeGiB {
			return fmt.Errorf("refusing to export xctrace profile: output volume at %s has %.1f GiB free, below %.1f GiB threshold", checkedPath, freeGiB, xctraceProfileMinFreeGiB)
		}
	}
	if xctraceProfileMinMemFree > 0 {
		freePercent, err := currentMemoryFreePercent()
		if err != nil {
			return fmt.Errorf("resource preflight failed reading memory pressure: %w", err)
		}
		if freePercent < xctraceProfileMinMemFree {
			return fmt.Errorf("refusing to export xctrace profile: memory_pressure free percentage is %d%%, below %d%% threshold", freePercent, xctraceProfileMinMemFree)
		}
	}
	return nil
}

func exportXctraceProfileTable(schema string) xctraceProfileTableSummary {
	xmlPath := filepath.Join(xctraceProfileOutDir, "xctrace_"+schema+".xml")
	argv := []string{
		"xcrun", "xctrace", "export",
		"--input", xctraceProfileTrace,
		"--xpath", fmt.Sprintf("/trace-toc/run[@number=\"1\"]/data/table[@schema=\"%s\"]", schema),
		"--output", xmlPath,
	}
	if err := preflightXctraceProfileResources(filepath.Dir(xmlPath)); err != nil {
		return xctraceProfileTableSummary{
			Schema: schema,
			XML:    xmlPath,
			Export: profileCommandResult{
				Name:          "xctrace_export_" + schema,
				Cmd:           argv,
				OutputDir:     filepath.Dir(xmlPath),
				Signal:        err.Error(),
				StderrPreview: err.Error(),
			},
		}
	}
	result := runXctraceProfileCommand("xctrace_export_"+schema, argv, filepath.Dir(xmlPath))
	rowCount := 0
	if count, readErr := countFileToken(xmlPath, []byte("<row>")); readErr == nil {
		rowCount = count
	}
	return xctraceProfileTableSummary{
		Schema:   schema,
		XML:      xmlPath,
		Export:   result,
		RowCount: rowCount,
	}
}

func runXctraceProfileCommand(name string, argv []string, outputDir string) profileCommandResult {
	if err := preflightXctraceProfileResources(outputDir); err != nil {
		return profileCommandResult{
			Name:          name,
			Cmd:           argv,
			OutputDir:     outputDir,
			Signal:        err.Error(),
			StderrPreview: err.Error(),
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), xctraceProfileTimeout)
	defer cancel()
	start := time.Now()
	command := exec.CommandContext(ctx, argv[0], argv[1:]...)
	stdout, stderr, err := runCommandCapture(ctx, command)
	result := profileCommandResult{
		Name:          name,
		Cmd:           argv,
		OutputDir:     outputDir,
		ElapsedMillis: time.Since(start).Milliseconds(),
		TimedOut:      ctx.Err() != nil,
		StdoutBytes:   len(stdout),
		StderrBytes:   len(stderr),
		StdoutPreview: previewOutput(stdout),
		StderrPreview: previewOutput(stderr),
	}
	if err != nil {
		if exitCode, ok := commandExitCode(err); ok {
			result.ExitCode = exitCode
		} else {
			result.Signal = err.Error()
		}
	}
	return result
}

func countFileToken(path string, token []byte) (int, error) {
	if len(token) == 0 {
		return 0, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	buf := make([]byte, 1024*1024)
	carry := []byte{}
	count := 0
	for {
		n, readErr := file.Read(buf)
		if n > 0 {
			chunk := append(carry, buf[:n]...)
			count += bytes.Count(chunk, token)
			carryLen := len(token) - 1
			if len(chunk) < carryLen {
				carryLen = len(chunk)
			}
			carry = append(carry[:0], chunk[len(chunk)-carryLen:]...)
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return count, readErr
		}
	}
	return count, nil
}
