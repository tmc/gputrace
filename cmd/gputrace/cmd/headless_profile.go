//go:build darwin

package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

var (
	headlessProfileJSON            bool
	headlessProfileOutDir          string
	headlessProfileProcessName     string
	headlessProfileTraceName       string
	headlessProfileTemplate        string
	headlessProfileInstruments     []string
	headlessProfileTimeLimit       string
	headlessProfileAttach          string
	headlessProfileAttachLaunched  bool
	headlessProfileAttachWait      time.Duration
	headlessProfileAttachAfterFile string
	headlessProfileAllProcesses    bool
	headlessProfileTimeout         time.Duration
	headlessProfileMaxRows         int
	headlessProfileMinFreeGiB      float64
	headlessProfileMinMemFree      int
	headlessProfileAllowSignal     bool
	headlessProfileEncodeSD        bool
)

type headlessProfileOutput struct {
	Record  xctraceRecordOutputDoc `json:"record"`
	Profile *xctraceProfileOutput  `json:"profile,omitempty"`
	Launch  *headlessLaunchOutput  `json:"launch,omitempty"`
}

type headlessLaunchOutput struct {
	Cmd           []string `json:"cmd"`
	Attach        string   `json:"attach"`
	PID           int      `json:"pid,omitempty"`
	Started       bool     `json:"started"`
	Exited        bool     `json:"exited"`
	ExitCode      int      `json:"exit_code,omitempty"`
	Signal        string   `json:"signal,omitempty"`
	StdoutBytes   int      `json:"stdout_bytes"`
	StderrBytes   int      `json:"stderr_bytes"`
	StdoutPreview string   `json:"stdout_preview,omitempty"`
	StderrPreview string   `json:"stderr_preview,omitempty"`
}

var headlessProfileCmd = &cobra.Command{
	Use:          "headless-profile --out-dir out --process name [-- command args...]",
	Short:        "Record and export a guarded headless GPU profile",
	SilenceUsage: true,
	Long: `Record a headless xctrace Metal System Trace, export Metal GPU
intervals, and write target-attributed interval rows as JSON.

The command checks output-volume free space and memory pressure before the
record and export phases. Timing claims require real exported GPU interval
rows. Counter claims remain disabled unless non-empty counter samples are
parsed.

Pass --encode-streamdata to also encode .gpuprofiler_raw/streamData for
compatibility with existing gputrace streamData readers. That opt-in path uses
Xcode private GPUToolsReplay classes and is not a stable public Apple API.`,
	Args: cobra.ArbitraryArgs,
	RunE: runHeadlessProfile,
}

func init() {
	rootCmd.AddCommand(headlessProfileCmd)
	headlessProfileCmd.Flags().BoolVar(&headlessProfileJSON, "json", false, "Output in JSON format")
	headlessProfileCmd.Flags().StringVar(&headlessProfileOutDir, "out-dir", "", "Output directory on the profiling volume")
	headlessProfileCmd.Flags().StringVar(&headlessProfileProcessName, "process", "", "Process name substring required for timing rows")
	headlessProfileCmd.Flags().StringVar(&headlessProfileTraceName, "trace-name", "capture.trace", "Trace bundle name inside --out-dir")
	headlessProfileCmd.Flags().StringVar(&headlessProfileTemplate, "template", "Metal System Trace", "xctrace template name or path")
	headlessProfileCmd.Flags().StringArrayVar(&headlessProfileInstruments, "instrument", nil, "Additional xctrace instrument name; may be repeated")
	headlessProfileCmd.Flags().StringVar(&headlessProfileTimeLimit, "time-limit", "10s", "xctrace recording time limit")
	headlessProfileCmd.Flags().StringVar(&headlessProfileAttach, "attach", "", "Attach to process name or pid")
	headlessProfileCmd.Flags().BoolVar(&headlessProfileAttachLaunched, "attach-launched", false, "Launch command, wait for --process, then record by attaching to that process")
	headlessProfileCmd.Flags().DurationVar(&headlessProfileAttachWait, "attach-wait", 15*time.Second, "Maximum time to wait for --process before attach-launched recording")
	headlessProfileCmd.Flags().StringVar(&headlessProfileAttachAfterFile, "attach-after-file", "", "In attach-launched mode, wait for this file before recording")
	headlessProfileCmd.Flags().BoolVar(&headlessProfileAllProcesses, "all-processes", false, "Record all processes")
	headlessProfileCmd.Flags().DurationVar(&headlessProfileTimeout, "timeout", 120*time.Second, "Timeout for each xctrace/helper step")
	headlessProfileCmd.Flags().IntVar(&headlessProfileMaxRows, "max-rows", 20000, "Maximum target interval rows to keep")
	headlessProfileCmd.Flags().Float64Var(&headlessProfileMinFreeGiB, "min-out-dir-free-gib", 24, "Minimum free GiB required on the output volume")
	headlessProfileCmd.Flags().IntVar(&headlessProfileMinMemFree, "min-memory-free-percent", 10, "Minimum memory_pressure free percentage required")
	headlessProfileCmd.Flags().BoolVar(&headlessProfileAllowSignal, "allow-record-signal-export", false, "Try export when xctrace record reports a signal but produced a trace bundle")
	headlessProfileCmd.Flags().BoolVar(&headlessProfileEncodeSD, "encode-streamdata", false, "Opt in to native .gpuprofiler_raw/streamData encoding using Xcode private GPUToolsReplay classes")
}

func runHeadlessProfile(cmd *cobra.Command, args []string) error {
	if headlessProfileOutDir == "" || headlessProfileProcessName == "" {
		return fmt.Errorf("--out-dir and --process are required")
	}
	targetModes := 0
	if len(args) > 0 {
		targetModes++
	}
	if headlessProfileAttach != "" {
		targetModes++
	}
	if headlessProfileAllProcesses {
		targetModes++
	}
	if headlessProfileAttachLaunched {
		if len(args) == 0 || headlessProfileAttach != "" || headlessProfileAllProcesses {
			return fmt.Errorf("--attach-launched requires a command after -- and cannot be combined with --attach or --all-processes")
		}
		targetModes = 1
	}
	if targetModes != 1 {
		return fmt.Errorf("choose exactly one target mode: command after --, --attach, --attach-launched, or --all-processes")
	}
	if filepath.Base(headlessProfileTraceName) != headlessProfileTraceName {
		return fmt.Errorf("--trace-name must be a file name, not a path")
	}
	if err := os.MkdirAll(headlessProfileOutDir, 0o755); err != nil {
		return err
	}

	tracePath := filepath.Join(headlessProfileOutDir, headlessProfileTraceName)
	xctraceRecordOutput = tracePath
	xctraceRecordTemplate = headlessProfileTemplate
	xctraceRecordInstruments = append([]string{}, headlessProfileInstruments...)
	xctraceRecordTimeLimit = headlessProfileTimeLimit
	xctraceRecordAttach = headlessProfileAttach
	xctraceRecordAllProcesses = headlessProfileAllProcesses
	xctraceRecordTimeout = headlessProfileTimeout
	xctraceRecordMinFreeGiB = headlessProfileMinFreeGiB
	xctraceRecordMinMemFree = headlessProfileMinMemFree
	if err := preflightXctraceRecordResources(tracePath); err != nil {
		return err
	}
	if err := os.RemoveAll(tracePath); err != nil {
		return err
	}
	out := headlessProfileOutput{}
	var record xctraceRecordOutputDoc
	if headlessProfileAttachLaunched {
		launch, attachRecord := runHeadlessAttachLaunched(args)
		out.Launch = launch
		record = attachRecord
	} else {
		record = runXctraceRecordCommand(buildXctraceRecordArgv(args))
	}
	out.Record = record
	recordSignalBlocked := record.Signal != "" && !headlessProfileAllowSignal
	if record.ExitCode != 0 || recordSignalBlocked || record.TimedOut || !record.Output.Present {
		return emitHeadlessProfileOutput(out, fmt.Errorf("xctrace record failed"))
	}

	profileDir := filepath.Join(headlessProfileOutDir, "profile")
	xctraceProfileTrace = tracePath
	xctraceProfileOutDir = profileDir
	xctraceProfileProcessName = headlessProfileProcessName
	xctraceProfileMaxRows = headlessProfileMaxRows
	xctraceProfileTimeout = headlessProfileTimeout
	xctraceProfileMinFreeGiB = headlessProfileMinFreeGiB
	xctraceProfileMinMemFree = headlessProfileMinMemFree
	xctraceProfileEncodeSD = headlessProfileEncodeSD
	profile, err := runXctraceProfileExport()
	out.Profile = profile
	if err != nil {
		return emitHeadlessProfileOutput(out, err)
	}
	if !profile.TimingClaimsAllowed {
		return emitHeadlessProfileOutput(out, fmt.Errorf("headless profile did not produce usable target interval rows"))
	}
	return emitHeadlessProfileOutput(out, nil)
}

func runHeadlessAttachLaunched(args []string) (*headlessLaunchOutput, xctraceRecordOutputDoc) {
	launch := &headlessLaunchOutput{
		Cmd:    append([]string{}, args...),
		Attach: headlessProfileProcessName,
	}
	command := exec.Command(args[0], args[1:]...)
	stdout := &limitedBuffer{limit: profileCommandCaptureLimit}
	stderr := &limitedBuffer{limit: profileCommandCaptureLimit}
	command.Stdout = stdout
	command.Stderr = stderr
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	waitDone := make(chan error, 1)
	if err := command.Start(); err != nil {
		launch.Signal = err.Error()
		launch.StdoutBytes = len(stdout.Bytes())
		launch.StderrBytes = len(stderr.Bytes())
		launch.StdoutPreview = previewOutput(stdout.Bytes())
		launch.StderrPreview = previewOutput(stderr.Bytes())
		return launch, xctraceRecordOutputDoc{
			Cmd:           []string{"xcrun", "xctrace", "record", "--attach", headlessProfileProcessName},
			Signal:        err.Error(),
			StderrPreview: err.Error(),
		}
	}
	launch.Started = true
	launch.PID = command.Process.Pid
	go func() {
		waitDone <- command.Wait()
	}()

	if _, err := waitForProcessMatch(headlessProfileProcessName, headlessProfileAttachWait); err != nil {
		terminateProcessGroup(command.Process.Pid)
		launch.Signal = err.Error()
		select {
		case waitErr := <-waitDone:
			launch.Exited = true
			if exitCode, ok := commandExitCode(waitErr); ok {
				launch.ExitCode = exitCode
			} else if waitErr != nil {
				launch.Signal = strings.TrimSpace(launch.Signal + "; " + waitErr.Error())
			}
		case <-time.After(2 * time.Second):
			killProcessGroup(command.Process.Pid)
			waitErr := <-waitDone
			launch.Exited = true
			if waitErr != nil {
				launch.Signal = strings.TrimSpace(launch.Signal + "; " + waitErr.Error())
			}
		}
		launch.StdoutBytes = len(stdout.Bytes())
		launch.StderrBytes = len(stderr.Bytes())
		launch.StdoutPreview = previewOutput(stdout.Bytes())
		launch.StderrPreview = previewOutput(stderr.Bytes())
		return launch, xctraceRecordOutputDoc{
			Cmd:           []string{"xcrun", "xctrace", "record", "--attach", headlessProfileProcessName},
			Signal:        launch.Signal,
			StdoutBytes:   launch.StdoutBytes,
			StderrBytes:   launch.StderrBytes,
			StdoutPreview: launch.StdoutPreview,
			StderrPreview: launch.StderrPreview,
		}
	}
	if headlessProfileAttachAfterFile != "" {
		if err := waitForFile(headlessProfileAttachAfterFile, headlessProfileAttachWait); err != nil {
			terminateProcessGroup(command.Process.Pid)
			launch.Signal = err.Error()
			select {
			case waitErr := <-waitDone:
				launch.Exited = true
				if exitCode, ok := commandExitCode(waitErr); ok {
					launch.ExitCode = exitCode
				} else if waitErr != nil {
					launch.Signal = strings.TrimSpace(launch.Signal + "; " + waitErr.Error())
				}
			case <-time.After(2 * time.Second):
				killProcessGroup(command.Process.Pid)
				waitErr := <-waitDone
				launch.Exited = true
				if waitErr != nil {
					launch.Signal = strings.TrimSpace(launch.Signal + "; " + waitErr.Error())
				}
			}
			launch.StdoutBytes = len(stdout.Bytes())
			launch.StderrBytes = len(stderr.Bytes())
			launch.StdoutPreview = previewOutput(stdout.Bytes())
			launch.StderrPreview = previewOutput(stderr.Bytes())
			return launch, xctraceRecordOutputDoc{
				Cmd:           []string{"xcrun", "xctrace", "record", "--attach", headlessProfileProcessName},
				Signal:        launch.Signal,
				StdoutBytes:   launch.StdoutBytes,
				StderrBytes:   launch.StderrBytes,
				StdoutPreview: launch.StdoutPreview,
				StderrPreview: launch.StderrPreview,
			}
		}
	}

	oldAttach := xctraceRecordAttach
	oldAll := xctraceRecordAllProcesses
	defer func() {
		xctraceRecordAttach = oldAttach
		xctraceRecordAllProcesses = oldAll
	}()
	xctraceRecordAttach = headlessProfileProcessName
	xctraceRecordAllProcesses = false
	record := runXctraceRecordCommand(buildXctraceRecordArgv(nil))

	select {
	case waitErr := <-waitDone:
		launch.Exited = true
		if exitCode, ok := commandExitCode(waitErr); ok {
			launch.ExitCode = exitCode
		} else if waitErr != nil {
			launch.Signal = waitErr.Error()
		}
	default:
		terminateProcessGroup(command.Process.Pid)
		select {
		case waitErr := <-waitDone:
			launch.Exited = true
			if exitCode, ok := commandExitCode(waitErr); ok {
				launch.ExitCode = exitCode
			} else if waitErr != nil {
				launch.Signal = waitErr.Error()
			}
		case <-time.After(2 * time.Second):
			killProcessGroup(command.Process.Pid)
			waitErr := <-waitDone
			launch.Exited = true
			if waitErr != nil {
				launch.Signal = waitErr.Error()
			}
		}
	}
	launch.StdoutBytes = len(stdout.Bytes())
	launch.StderrBytes = len(stderr.Bytes())
	launch.StdoutPreview = previewOutput(stdout.Bytes())
	launch.StderrPreview = previewOutput(stderr.Bytes())
	return launch, record
}

func waitForProcessMatch(name string, timeout time.Duration) (int, error) {
	deadline := time.Now().Add(timeout)
	for {
		pid, err := firstPgrepMatch("-x", name)
		if err == nil && pid > 0 {
			return pid, nil
		}
		pid, err = firstPgrepMatch("-f", name)
		if err == nil && pid > 0 {
			return pid, nil
		}
		if time.Now().After(deadline) {
			return 0, fmt.Errorf("process %q did not appear within %s", name, timeout)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func waitForFile(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if info, err := os.Stat(path); err == nil && !info.IsDir() && info.Size() > 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("file %q did not appear within %s", path, timeout)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func firstPgrepMatch(mode, name string) (int, error) {
	out, err := exec.Command("pgrep", mode, name).Output()
	if err != nil {
		return 0, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, line := range lines {
		var pid int
		if _, err := fmt.Sscanf(strings.TrimSpace(line), "%d", &pid); err == nil && pid > 0 {
			return pid, nil
		}
	}
	return 0, fmt.Errorf("no pgrep match for %q", name)
}

func emitHeadlessProfileOutput(out headlessProfileOutput, runErr error) error {
	if headlessProfileJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			return err
		}
		return runErr
	}
	fmt.Printf("trace:      %s (%d bytes)\n", out.Record.Output.Path, out.Record.Output.Bytes)
	if out.Profile != nil {
		fmt.Printf("rows:       %s (%d rows)\n", out.Profile.IntervalRows.JSON.Path, out.Profile.IntervalRows.RowsMatched)
		if out.Profile.StreamDataRequested {
			fmt.Printf("streamData: %s (%d bytes)\n", out.Profile.StreamData.StreamData.Path, out.Profile.StreamData.StreamData.Bytes)
		}
		fmt.Printf("timing:     %v\n", out.Profile.TimingClaimsAllowed)
		fmt.Printf("counters:   %v\n", out.Profile.CounterClaimsAllowed)
	}
	return runErr
}
