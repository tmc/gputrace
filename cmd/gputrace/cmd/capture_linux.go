//go:build linux

package cmd

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/buildinfo"
	"github.com/tmc/gputrace/internal/cupticapture"
	"github.com/tmc/gputrace/internal/gpudoctor"
	"github.com/tmc/gputrace/internal/gpuevent"
	"github.com/tmc/gputrace/internal/nvidia"
)

// captureLinuxOptions mirrors the darwin captureOptions subset meaningful on
// Linux. It is registered as additional behavior of the same `capture`
// command so the CLI surface stays identical across platforms.
type captureLinuxOptions struct {
	samples        bool
	api            bool
	nvtx           bool
	sampleInterval string
}

var captureLinuxOpts = captureLinuxOptions{sampleInterval: "25ms"}

// runCaptureLinux executes the workload with the CUPTI shim preloaded and
// writes a .gpucapture bundle.
func runCaptureLinux(cmd *cobra.Command, opts *captureOptions, args []string) error {
	out := opts.output
	if out == "" {
		return fmt.Errorf("capture: -o is required (e.g. -o run.gpucapture)")
	}
	if !strings.HasSuffix(out, ".gpucapture") {
		out += ".gpucapture"
	}
	if abs, err := filepath.Abs(out); err == nil {
		out = abs // the target may run with a different working directory
	}
	if fi, err := os.Stat(out); err == nil && fi.IsDir() && len(args) > 0 {
		return fmt.Errorf("capture: output bundle %s already exists", out)
	}

	eventsPath := filepath.Join(out, cupticapture.EventsFileName)
	meta := cupticapture.Meta{
		Command:         args,
		Dir:             opts.dir,
		GPUTRACEVersion: buildinfo.EffectiveVersion(),
	}
	if devices, err := nvidia.Devices(); err == nil && len(devices) > 0 {
		d := devices[0]
		meta.GPUName = d.Name
		meta.GPUUUID = d.UUID
		meta.DriverVersion = d.DriverVer
	}
	if err := cupticapture.CreateBundle(out, meta); err != nil {
		return fmt.Errorf("capture: create bundle: %w", err)
	}

	preloadEnv, err := cupticapture.PreloadEnv(cupticapture.Options{
		OutputPath: eventsPath,
		APIRecords: captureLinuxOpts.api,
		NVTX:       captureLinuxOpts.nvtx,
	})
	if err != nil {
		return err
	}

	// App-events sidecar: any process in the target tree may append
	// span/instant JSONL records here; they are merged into the bundle's
	// events stream when the target exits. See docs/ENVIRONMENT.md.
	sidecarPath := filepath.Join(out, "app_events.jsonl")
	preloadEnv = append(preloadEnv, fmt.Sprintf("GPUTRACE_APP_EVENTS=%s", sidecarPath))

	// Optional NVML sampler runs alongside the target and writes into the
	// same bundle; it is stopped when the target exits.
	var sampler *exec.Cmd
	if captureLinuxOpts.samples {
		samplerPath, samplerErr := exec.LookPath("nvml_sampler")
		if samplerErr != nil {
			fmt.Fprintln(os.Stderr, "capture: --samples requested but nvml_sampler not on PATH; continuing without device samples")
		} else {
			sampler = exec.Command(samplerPath, "-out", out, "-interval", captureLinuxOpts.sampleInterval)
			sampler.Stdout = nil
			sampler.Stderr = os.Stderr
			if err := sampler.Start(); err != nil {
				fmt.Fprintf(os.Stderr, "capture: start sampler: %v; continuing without samples\n", err)
				sampler = nil
			}
		}
	}

	target := exec.Command(args[0], args[1:]...)
	if opts.dir != "" {
		target.Dir = opts.dir
	}
	target.Env = append(os.Environ(), preloadEnv...)
	target.Stdin = cmd.InOrStdin()
	target.Stdout = cmd.OutOrStdout()
	// Streamed, not buffered. Buffering it hid the target's own stderr
	// on every successful run, which is where GPUTRACE_CAPTURE_DEBUG
	// writes: the one channel for diagnosing the shim was invisible
	// through the command that installs it.
	target.Stderr = cmd.ErrOrStderr()

	err = target.Run()
	// Stop the sampler before reporting so its file is complete.
	if sampler != nil {
		if sampler.Process != nil {
			_ = sampler.Process.Signal(syscall.SIGTERM)
		}
		_ = sampler.Wait()
	}
	// Merge the app-events sidecar into the bundle's events stream so span
	// records ride the same decode path as shim-emitted records. The
	// sidecar is optional: an absent file means the target declared nothing.
	if sidecarData, err := os.ReadFile(sidecarPath); err == nil && len(sidecarData) > 0 {
		// Append into an events-glob-matching shard so OpenEvents picks it up.
		mergedPath := filepath.Join(out, fmt.Sprintf("events.app.%d.jsonl", os.Getpid()))
		if err := os.WriteFile(mergedPath, sidecarData, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "capture: merge app-events: %v\n", err)
		}
	}
	// A failing workload still produced a valid (possibly empty) bundle;
	// report both facts rather than discarding evidence.
	fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", out)
	reportCaptureContents(cmd.OutOrStdout(), out, captureLinuxOpts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "capture: target exited with error; bundle retained for inspection\n")
		return reportedCaptureError{err}
	}
	return nil
}

// reportCaptureContents states what the bundle actually holds, at the end
// of the run that produced it.
//
// Two failures this closes are the same failure: an arm that never ran and
// an arm that ran and found nothing return identical output. A --nvtx that
// recorded no markers is indistinguishable from a workload that emits
// none, and a capture that lost activity records is indistinguishable from
// a workload that launched less. Both are counts the tool already has and
// was not printing.
func reportCaptureContents(w io.Writer, bundle string, opts captureLinuxOptions) {
	r, closers, err := cupticapture.OpenEvents(bundle)
	if err != nil {
		return // the bundle stands on its own; this line is a courtesy
	}
	cap, _ := gpuevent.DecodeJSONL(r)
	closers()

	var kernels, transfers, markers int
	for _, e := range cap.Events {
		switch e.Kind {
		case gpuevent.KindKernel:
			kernels++
		case gpuevent.KindMemcpy, gpuevent.KindMemset:
			transfers++
		}
	}
	for _, sp := range cap.Spans {
		if sp.Source == gpuevent.SourceNVTX {
			markers++
		}
	}
	fmt.Fprintf(w, "  %d kernels, %d transfers, %d spans\n", kernels, transfers, len(cap.Spans))
	if opts.nvtx && markers == 0 {
		fmt.Fprintf(w, "  --nvtx: 0 NVTX ranges recorded — either the target emits none, or nothing routed them into CUPTI\n")
	}
	health := gpuevent.MeasureCompleteness(cap)
	if !health.Complete() {
		fmt.Fprintf(w, "  %s\n", health.Summary())
		for _, line := range strings.Split(health.Remedy(), "\n") {
			fmt.Fprintf(w, "  %s\n", line)
		}
	}
}

func init() {
	// Let `gputrace doctor` prove the shim builds without capturing.
	gpudoctor.SetShimBuilder(cupticapture.EnsureShim)

	// Extend the existing capture command with Linux-specific flags and
	// rerouting. The darwin implementation compiles an Objective-C interposer;
	// here we compile a C CUPTI shim and LD_PRELOAD it instead.
	captureCmd.Long = `Run a command under the gputrace GPU capture tracer.

On Linux/NVIDIA this preloads a CUPTI activity shim into the target,
recording every kernel launch, memcpy, and memset as newline-delimited
JSON inside a .gpucapture bundle directory:

  run.gpucapture/
    events.jsonl        activity records (kind, timing, geometry)
    nvml_samples.jsonl  concurrent device samples (--samples)
    meta.json           provenance (command, time, versions)

The bundle feeds directly into analysis and export:

  gputrace analyze run.gpucapture
  gputrace analyze run.gpucapture --suggest
  gputrace cupti run.gpucapture --per-kernel-tracks -o trace.pftrace

Targets must link CUDA dynamically (-cudart=shared for nvcc builds).
Statically-linked CUDA runtimes bypass the interposition points.

Examples:
  gputrace capture -o run.gpucapture -- python3 bench.py
  gputrace capture -o run.gpucapture --samples -- ./matmul`
	captureCmd.Short = "Run a workload under the GPU capture tracer"
	captureCmd.Flags().BoolVar(&captureLinuxOpts.api, "api", captureLinuxOpts.api, "record host-side CUDA runtime/driver API calls (multiplies record volume)")
	captureCmd.Flags().BoolVar(&captureLinuxOpts.nvtx, "nvtx", captureLinuxOpts.nvtx, "record NVTX ranges emitted by the target or the libraries it links")
	captureCmd.Flags().BoolVar(&captureLinuxOpts.samples, "samples", captureLinuxOpts.samples, "Sample NVML device counters during the run")
	captureCmd.Flags().StringVar(&captureLinuxOpts.sampleInterval, "sample-interval", captureLinuxOpts.sampleInterval, "NVML sampling interval")

	origRunE := captureCmd.RunE
	captureCmd.RunE = func(cmd *cobra.Command, args []string) error {
		opts := captureOptions{
			output: cmd.Flag("output").Value.String(),
			dir:    cmd.Flag("dir").Value.String(),
			check:  false,
		}
		// --check is a darwin concept (codesign probe); on Linux report
		// whether injection prerequisites hold instead.
		if len(args) > 0 && args[0] == "--check" || cmd.Flag("check") != nil && cmd.Flag("check").Value.String() == "true" {
			shim, err := cupticapture.PreloadEnv(cupticapture.Options{OutputPath: "/dev/null"})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "capturable: shim ready (%s)\n", shim[1])
			return nil
		}
		if origRunE != nil && opts.output != "" && strings.HasSuffix(opts.output, ".gputrace") {
			// Explicit .gputrace output on Linux means the user wants the
			// darwin flow; let it fail with its own message.
			return origRunE(cmd, args)
		}
		return runCaptureLinux(cmd, &opts, args)
	}
}
