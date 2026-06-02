//go:build darwin

package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/tmc/gputrace/internal/counter"
)

const profileCommandCaptureLimit = 64 * 1024

type profileResourcePreflight struct {
	OutputDir         string  `json:"output_dir"`
	CheckedPath       string  `json:"checked_path,omitempty"`
	FreeGiB           float64 `json:"free_gib,omitempty"`
	MemoryFreePercent int     `json:"memory_free_percent,omitempty"`
	MinFreeGiB        float64 `json:"min_free_gib"`
	MinMemoryFree     int     `json:"min_memory_free_percent"`
	AllowLowResources bool    `json:"allow_low_resources"`
}

type profileCommandResult struct {
	Name          string   `json:"name"`
	Cmd           []string `json:"cmd"`
	OutputDir     string   `json:"output_dir,omitempty"`
	ElapsedMillis int64    `json:"elapsed_millis"`
	TimedOut      bool     `json:"timed_out"`
	ExitCode      int      `json:"exit_code,omitempty"`
	Signal        string   `json:"signal,omitempty"`
	StdoutBytes   int      `json:"stdout_bytes"`
	StderrBytes   int      `json:"stderr_bytes"`
	StdoutPath    string   `json:"stdout_path,omitempty"`
	StderrPath    string   `json:"stderr_path,omitempty"`
	StdoutPreview string   `json:"stdout_preview,omitempty"`
	StderrPreview string   `json:"stderr_preview,omitempty"`
	FileCount     int      `json:"file_count,omitempty"`
	ProfilerFiles []string `json:"profiler_files,omitempty"`
}

type streamDataProbeStats struct {
	ParseError                string `json:"parse_error,omitempty"`
	NumEncoders               int    `json:"num_encoders"`
	NumGPUCommands            int    `json:"num_gpu_commands"`
	NumPipelines              int    `json:"num_pipelines"`
	DispatchCount             int    `json:"dispatch_count"`
	EncoderTimingCount        int    `json:"encoder_timing_count"`
	DerivedCounterSampleCount int    `json:"derived_counter_sample_count"`
	TotalTimeUs               int    `json:"total_time_us"`
	TimingUsable              bool   `json:"timing_usable"`
	CounterUsable             bool   `json:"counter_usable"`
}

func collectResourcePreflight(outDir string, minFreeGiB float64, minMemFree int, allowLowResources bool) profileResourcePreflight {
	summary := profileResourcePreflight{
		OutputDir:         outDir,
		MinFreeGiB:        minFreeGiB,
		MinMemoryFree:     minMemFree,
		AllowLowResources: allowLowResources,
	}
	if freeBytes, checkedPath, err := availableBytesForPath(outDir); err == nil {
		summary.CheckedPath = checkedPath
		summary.FreeGiB = float64(freeBytes) / (1024 * 1024 * 1024)
	}
	if freePercent, err := currentMemoryFreePercent(); err == nil {
		summary.MemoryFreePercent = freePercent
	}
	return summary
}

func availableBytesForPath(path string) (uint64, string, error) {
	checkPath := path
	for {
		var stat syscall.Statfs_t
		if err := syscall.Statfs(checkPath, &stat); err == nil {
			return uint64(stat.Bavail) * uint64(stat.Bsize), checkPath, nil
		}
		parent := filepath.Dir(checkPath)
		if parent == checkPath {
			var stat syscall.Statfs_t
			if err := syscall.Statfs(parent, &stat); err != nil {
				return 0, parent, err
			}
			return uint64(stat.Bavail) * uint64(stat.Bsize), parent, nil
		}
		checkPath = parent
	}
}

func currentMemoryFreePercent() (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	stdout, stderr, err := runCommandCapture(ctx, exec.CommandContext(ctx, "memory_pressure", "-Q"))
	if err != nil {
		return 0, fmt.Errorf("%w: %s", err, previewOutput(stderr))
	}
	freePercent, ok := parseMemoryPressureFreePercent(string(stdout))
	if !ok {
		return 0, fmt.Errorf("could not parse memory_pressure output: %s", previewOutput(stdout))
	}
	return freePercent, nil
}

func parseMemoryPressureFreePercent(output string) (int, bool) {
	const marker = "System-wide memory free percentage:"
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, marker) {
			continue
		}
		value := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(strings.TrimPrefix(line, marker)), "%"))
		var percent int
		if _, err := fmt.Sscanf(value, "%d", &percent); err != nil {
			return 0, false
		}
		return percent, true
	}
	return 0, false
}

func summarizeEncodedStreamData(profilerDir string) *streamDataProbeStats {
	stats, err := counter.ParseStreamData(profilerDir)
	if err != nil {
		return &streamDataProbeStats{ParseError: err.Error()}
	}
	out := &streamDataProbeStats{
		NumEncoders:               stats.NumEncoders,
		NumGPUCommands:            stats.NumGPUCommands,
		NumPipelines:              stats.NumPipelines,
		DispatchCount:             len(stats.Dispatches),
		EncoderTimingCount:        len(stats.EncoderTimings),
		DerivedCounterSampleCount: stats.DerivedCounterSampleCount(),
		TotalTimeUs:               stats.TotalTimeUs,
	}
	out.TimingUsable = out.DispatchCount > 0 || out.EncoderTimingCount > 0 || out.TotalTimeUs > 0
	out.CounterUsable = out.DerivedCounterSampleCount > 0
	return out
}

func runExternalCommand(name string, argv []string, outputDir string, timeout time.Duration) profileCommandResult {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	start := time.Now()
	command := exec.CommandContext(ctx, argv[0], argv[1:]...)
	stdoutPath, stderrPath := commandLogPaths(outputDir, name)
	stdout, stderr, err := runCommandCaptureWithLogs(ctx, command, stdoutPath, stderrPath)
	result := profileCommandResult{
		Name:          name,
		Cmd:           append([]string{}, argv...),
		OutputDir:     outputDir,
		ElapsedMillis: time.Since(start).Milliseconds(),
		TimedOut:      ctx.Err() == context.DeadlineExceeded,
		StdoutBytes:   len(stdout),
		StderrBytes:   len(stderr),
		StdoutPath:    stdoutPath,
		StderrPath:    stderrPath,
		StdoutPreview: previewOutput(stdout),
		StderrPreview: previewOutput(stderr),
	}
	if outputDir != "" {
		result.FileCount, result.ProfilerFiles = summarizeOutputFiles(outputDir)
	}
	if err == nil {
		return result
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			if status.Signaled() {
				result.Signal = status.Signal().String()
			} else {
				result.ExitCode = status.ExitStatus()
			}
			return result
		}
		result.ExitCode = exitErr.ExitCode()
		return result
	}
	result.Signal = err.Error()
	return result
}

func commandLogPaths(outputDir, name string) (string, string) {
	if outputDir == "" {
		return "", ""
	}
	_ = os.MkdirAll(outputDir, 0o755)
	safeName := strings.NewReplacer("/", "_", " ", "_", ":", "_").Replace(name)
	return filepath.Join(outputDir, safeName+".stdout.txt"), filepath.Join(outputDir, safeName+".stderr.txt")
}

func runCommandCaptureWithLogs(ctx context.Context, command *exec.Cmd, stdoutPath, stderrPath string) ([]byte, []byte, error) {
	if stdoutPath == "" && stderrPath == "" {
		return runCommandCapture(ctx, command)
	}
	stdout := &limitedBuffer{limit: profileCommandCaptureLimit}
	stderr := &limitedBuffer{limit: profileCommandCaptureLimit}
	var stdoutFile *os.File
	var stderrFile *os.File
	var err error
	if stdoutPath != "" {
		stdoutFile, err = os.Create(stdoutPath)
		if err != nil {
			return stdout.Bytes(), stderr.Bytes(), err
		}
		defer stdoutFile.Close()
		command.Stdout = io.MultiWriter(stdoutFile, stdout)
	} else {
		command.Stdout = stdout
	}
	if stderrPath != "" {
		stderrFile, err = os.Create(stderrPath)
		if err != nil {
			return stdout.Bytes(), stderr.Bytes(), err
		}
		defer stderrFile.Close()
		command.Stderr = io.MultiWriter(stderrFile, stderr)
	} else {
		command.Stderr = stderr
	}
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		return stdout.Bytes(), stderr.Bytes(), err
	}
	waitDone := make(chan error, 1)
	go func() {
		waitDone <- command.Wait()
	}()
	select {
	case err := <-waitDone:
		return stdout.Bytes(), stderr.Bytes(), err
	case <-ctx.Done():
		terminateProcessGroup(command.Process.Pid)
		select {
		case err := <-waitDone:
			return stdout.Bytes(), stderr.Bytes(), errors.Join(ctx.Err(), err)
		case <-time.After(2 * time.Second):
			killProcessGroup(command.Process.Pid)
			err := <-waitDone
			return stdout.Bytes(), stderr.Bytes(), errors.Join(ctx.Err(), err)
		}
	}
}

func runCommandCapture(ctx context.Context, command *exec.Cmd) ([]byte, []byte, error) {
	stdout := &limitedBuffer{limit: profileCommandCaptureLimit}
	stderr := &limitedBuffer{limit: profileCommandCaptureLimit}
	command.Stdout = stdout
	command.Stderr = stderr
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		return stdout.Bytes(), stderr.Bytes(), err
	}
	waitDone := make(chan error, 1)
	go func() {
		waitDone <- command.Wait()
	}()
	select {
	case err := <-waitDone:
		return stdout.Bytes(), stderr.Bytes(), err
	case <-ctx.Done():
		terminateProcessGroup(command.Process.Pid)
		select {
		case err := <-waitDone:
			return stdout.Bytes(), stderr.Bytes(), errors.Join(ctx.Err(), err)
		case <-time.After(2 * time.Second):
			killProcessGroup(command.Process.Pid)
			err := <-waitDone
			return stdout.Bytes(), stderr.Bytes(), errors.Join(ctx.Err(), err)
		}
	}
}

func previewOutput(output []byte) string {
	const limit = 1200
	output = bytes.TrimSpace(output)
	if len(output) <= limit {
		return string(output)
	}
	const side = limit / 2
	return string(output[:side]) + "...<truncated>..." + string(output[len(output)-side:])
}

type limitedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.limit <= 0 {
		return len(p), nil
	}
	remaining := b.limit - b.buf.Len()
	if remaining <= 0 {
		b.truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		_, _ = b.buf.Write(p[:remaining])
		b.truncated = true
		return len(p), nil
	}
	_, _ = b.buf.Write(p)
	return len(p), nil
}

func (b *limitedBuffer) Bytes() []byte {
	data := b.buf.Bytes()
	if !b.truncated {
		return data
	}
	suffix := []byte("\n...<output truncated>...\n")
	out := make([]byte, 0, len(data)+len(suffix))
	out = append(out, data...)
	out = append(out, suffix...)
	return out
}

var _ io.Writer = (*limitedBuffer)(nil)

func terminateProcessGroup(pid int) {
	if pid <= 0 {
		return
	}
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	_ = syscall.Kill(pid, syscall.SIGTERM)
}

func killProcessGroup(pid int) {
	if pid <= 0 {
		return
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	_ = syscall.Kill(pid, syscall.SIGKILL)
}

func summarizeOutputFiles(root string) (int, []string) {
	files := []string{}
	profilerFiles := []string{}
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		files = append(files, path)
		name := filepath.Base(path)
		if filepath.Ext(filepath.Dir(path)) == ".gpuprofiler_raw" ||
			name == "streamData" ||
			matchAny(name, "Counters_f_*", "Profiling_f_*", "Timeline_f_*", "kdebug*") {
			profilerFiles = append(profilerFiles, path)
		}
		return nil
	})
	return len(files), profilerFiles
}

func matchAny(name string, patterns ...string) bool {
	for _, pattern := range patterns {
		if ok, _ := filepath.Match(pattern, name); ok {
			return true
		}
	}
	return false
}
