//go:build linux

package cmd

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/buildinfo"
	"github.com/tmc/gputrace/internal/cupticapture"
)

// captureLinuxOptions mirrors the darwin captureOptions subset meaningful on
// Linux. It is registered as additional behavior of the same `capture`
// command so the CLI surface stays identical across platforms.
type captureLinuxOptions struct {
	samples        bool
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
	if fi, err := os.Stat(out); err == nil && fi.IsDir() && len(args) > 0 {
		return fmt.Errorf("capture: output bundle %s already exists", out)
	}

	eventsPath := filepath.Join(out, cupticapture.EventsFileName)
	meta := cupticapture.Meta{
		Command:         args,
		Dir:             opts.dir,
		GPUTRACEVersion: buildinfo.EffectiveVersion(),
	}
	if err := cupticapture.CreateBundle(out, meta); err != nil {
		return fmt.Errorf("capture: create bundle: %w", err)
	}

	preloadEnv, err := cupticapture.PreloadEnv(cupticapture.Options{
		OutputPath: eventsPath,
	})
	if err != nil {
		return err
	}

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
	var stderr bytes.Buffer
	target.Stdout = cmd.OutOrStdout()
	target.Stderr = &stderr

	err = target.Run()
	// Stop the sampler before reporting so its file is complete.
	if sampler != nil {
		if sampler.Process != nil {
			_ = sampler.Process.Signal(syscall.SIGTERM)
		}
		_ = sampler.Wait()
	}
	// A failing workload still produced a valid (possibly empty) bundle;
	// report both facts rather than discarding evidence.
	fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", out)
	if err != nil {
		if stderr.Len() > 0 {
			fmt.Fprint(os.Stderr, stderr.String())
		}
		fmt.Fprintf(os.Stderr, "capture: target exited with error; bundle retained for inspection\n")
		return reportedCaptureError{err}
	}
	return nil
}

func init() {
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
