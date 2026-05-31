package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"

	"github.com/google/pprof/profile"
	"github.com/tmc/gputrace/internal/export"
	"github.com/tmc/gputrace/internal/trace"
)

const defaultCaptureGPUTrace = "trace.gputrace"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		cpuProfile = flag.String("cpu", "cpu.pprof", "Input CPU profile")
		gpuTrace   = flag.String("gpu", "", "Input GPU trace (.gputrace)")
		output     = flag.String("o", "merged.pprof", "Output merged profile")
		runCmd     = flag.Bool("run", false, "Run the command (capture mode)")
	)
	flag.Parse()

	if *runCmd {
		args := flag.Args()
		if len(args) == 0 {
			return fmt.Errorf("no command specified to run")
		}
		return runCapture(args, *cpuProfile, *output)
	}

	if *gpuTrace == "" {
		return fmt.Errorf("gpu trace required (use -gpu)")
	}

	return mergeProfiles(*cpuProfile, *gpuTrace, *output)
}

type captureConfig struct {
	args       []string
	cpuProfile string
	gpuTrace   string
	output     string
}

type captureDeps struct {
	run      func(args []string, env []string) error
	validate func(cpuPath, gpuPath string) error
	merge    func(cpuPath, gpuPath, outputPath string) error
}

func runCapture(args []string, cpuProfile, output string) error {
	return runCaptureWithDeps(captureConfig{
		args:       args,
		cpuProfile: cpuProfile,
		gpuTrace:   defaultCaptureGPUTrace,
		output:     output,
	}, captureDeps{
		run:      runCaptureCommand,
		validate: validateCaptureOutputs,
		merge:    mergeProfiles,
	})
}

func runCaptureWithDeps(cfg captureConfig, deps captureDeps) error {
	if len(cfg.args) == 0 {
		return fmt.Errorf("no command specified to run")
	}
	if cfg.cpuProfile == "" {
		return fmt.Errorf("cpu profile path required")
	}
	if cfg.gpuTrace == "" {
		return fmt.Errorf("gpu trace path required")
	}

	fmt.Printf("Running: %v\n", cfg.args)

	env := append(os.Environ(), captureEnv(cfg.gpuTrace)...)
	if err := deps.run(cfg.args, env); err != nil {
		return fmt.Errorf("command execution failed: %w", err)
	}

	// mlxprof cannot force an arbitrary child process to emit Go runtime/pprof
	// data. Treat the expected profile as a required capture artifact and fail
	// before merging if the child did not produce it.
	if err := deps.validate(cfg.cpuProfile, cfg.gpuTrace); err != nil {
		return err
	}

	return deps.merge(cfg.cpuProfile, cfg.gpuTrace, cfg.output)
}

func captureEnv(gpuTrace string) []string {
	return []string{
		"MTL_CAPTURE_ENABLED=1",
		"GPUPROFILER_TRACE_DESTINATION=" + gpuTrace,
	}
}

func runCaptureCommand(args []string, env []string) error {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

func validateCaptureOutputs(cpuPath, gpuPath string) error {
	if err := validateCPUProfile(cpuPath); err != nil {
		return err
	}
	return validateGPUTrace(gpuPath)
}

func validateCPUProfile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("cpu profile %q was not produced by command; configure the target to write runtime/pprof data or pass -cpu with the generated path", path)
		}
		return fmt.Errorf("stat cpu profile %q: %w", path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("cpu profile %q is a directory", path)
	}
	if info.Size() == 0 {
		return fmt.Errorf("cpu profile %q is empty", path)
	}
	return nil
}

func validateGPUTrace(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("gpu trace %q was not produced by command", path)
		}
		return fmt.Errorf("stat gpu trace %q: %w", path, err)
	}
	if !info.IsDir() && info.Size() == 0 {
		return fmt.Errorf("gpu trace %q is empty", path)
	}
	return nil
}

func mergeProfiles(cpuPath, gpuPath, outputPath string) error {
	fmt.Printf("Merging %s and %s -> %s\n", cpuPath, gpuPath, outputPath)

	// Load CPU Profile
	fCPU, err := os.Open(cpuPath)
	if err != nil {
		return fmt.Errorf("open cpu profile: %w", err)
	}
	defer fCPU.Close()
	cpuProf, err := profile.Parse(fCPU)
	if err != nil {
		return fmt.Errorf("parse cpu profile: %w", err)
	}

	// Load GPU Trace
	t, err := trace.Open(gpuPath)
	if err != nil {
		return fmt.Errorf("open gpu trace: %w", err)
	}
	defer t.Close()

	// Convert GPU Trace to Pprof
	gpuProf, err := export.ToPprofWithMetrics(t, nil, nil)
	if err != nil {
		return fmt.Errorf("convert gpu trace: %w", err)
	}

	// Schema Unification
	// We want to merge CPU and GPU profiles.
	// Logic:
	// 1. Adopt CPU profile's PeriodType.
	// 2. Normalize Value types.
	//    CPU: [samples count, cpu nanoseconds]  (Typical Go pprof)
	//    GPU: [time nanoseconds, count, edges, alu, occ, read, write]  (From export.ToPprofWithMetrics)
	//
	// Strategy:
	// Transform GPU profile to match CPU profile structure:
	//   Values: [count, nanoseconds]
	//   Mapping:
	//     GPU Count -> Index 0
	//     GPU Time  -> Index 1

	if len(cpuProf.SampleType) == 2 && cpuProf.SampleType[1].Unit == "nanoseconds" {
		fmt.Println("Adapting GPU profile to match Go CPU profile format...")
		gpuProf.PeriodType = cpuProf.PeriodType
		gpuProf.SampleType = cpuProf.SampleType

		// Remap GPU samples
		for _, s := range gpuProf.Sample {
			// Original GPU: [time, count, edges]
			// Target: [count, time]

			// Extract from original (assuming order from pprof_enhanced.go: time, count, edges)
			// But check pprof_enhanced.go guarantees.
			// It sets: {Type: "time", Unit: "nanoseconds"}, {Type: "count", Unit: "count"}, {Type: "edges", Unit: "count"}

			var timeVal int64
			var countVal int64

			if len(s.Value) >= 2 {
				timeVal = s.Value[0]
				countVal = s.Value[1]
			} else if len(s.Value) == 1 {
				timeVal = s.Value[0]
				countVal = 1
			}

			s.Value = []int64{countVal, timeVal}
		}
	} else {
		// Fallback: Just force PeriodType to match to try standard merge,
		// but if SampleTypes differ in count/unit, pprof.Merge will still fail or drop data.
		fmt.Printf("Warning: CPU profile has unexpected format: %v. Attempting best-effort merge.\n", cpuProf.SampleType)
		gpuProf.PeriodType = cpuProf.PeriodType
		// We can't easily unify values if we don't know the schema.
		// But let's try to match PeriodType at least.
	}

	// Merge
	// Ideally we align timestamps here.
	// For P0, we just merge them content-wise.
	// Note: pprof.Merge treats profiles as samples from the same binary.
	// We are merging "Go" and "Metal".

	merged, err := profile.Merge([]*profile.Profile{cpuProf, gpuProf})
	if err != nil {
		return fmt.Errorf("merge profiles: %w", err)
	}

	// Write
	outF, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("create output: %w", err)
	}
	defer outF.Close()

	return merged.Write(outF)
}
