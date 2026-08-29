// Package cupticapture runs workloads under a CUPTI activity tracer.
//
// The tracer is a small C shim (shim.c) embedded in this package and
// compiled on demand the first time a capture runs, mirroring how exp/
// compiles the Metal interposer. Compilation requires a C compiler and the
// CUDA headers; the built shim is cached under the user cache directory and
// reused until the embedded source changes.
//
// Capture works by LD_PRELOADing the shim into the child process. CUPTI
// activity tracing only observes CUDA work in the process that arms it, so
// injection is required regardless of how gputrace itself links CUDA.
package cupticapture

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

//go:embed embed/shim.c
var shimSource string

// ErrNoCompiler reports that no usable C compiler was found for building
// the shim.
var ErrNoCompiler = fmt.Errorf("cupticapture: no C compiler found")

var (
	buildOnce sync.Once
	shimPath  string
	buildErr  error
)

// cachePath returns the directory where compiled shims are cached.
func cachePath() (string, error) {
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".cache")
	}
	return filepath.Join(base, "gputrace"), nil
}

// findCompiler locates a C compiler, preferring gcc then clang then cc.
func findCompiler() (string, error) {
	for _, cc := range []string{"gcc", "clang", "cc"} {
		if p, err := exec.LookPath(cc); err == nil {
			return p, nil
		}
	}
	return "", ErrNoCompiler
}

// cudaIncludeDirs returns candidate directories holding cupti.h.
func cudaIncludeDirs() []string {
	var dirs []string
	if e := os.Getenv("CUDA_HOME"); e != "" {
		dirs = append(dirs, filepath.Join(e, "include"))
	}
	dirs = append(dirs,
		"/usr/local/cuda/include",
		"/usr/local/cuda-13/include",
		"/usr/local/cuda-13.0/include",
		"/usr/local/cuda-12/include",
		"/usr/local/cuda-12.6/include",
	)
	if e := os.Getenv("CPATH"); e != "" {
		dirs = append(dirs, strings.Split(e, ":")...)
	}
	return dirs
}

// ensureShim builds the shim once per process; concurrent callers wait on
// the same result.
func ensureShim() (string, error) {
	buildOnce.Do(func() {
		cacheDir, err := cachePath()
		if err != nil {
			buildErr = fmt.Errorf("cupticapture: resolve cache dir: %w", err)
			return
		}
		if err := os.MkdirAll(cacheDir, 0o755); err != nil {
			buildErr = fmt.Errorf("cupticapture: create cache dir: %w", err)
			return
		}
		cc, err := findCompiler()
		if err != nil {
			buildErr = err
			return
		}

		// Cache key: hash of the embedded source, so an updated shim
		// rebuilds instead of reusing a stale binary.
		sum := sha256Hex([]byte(shimSource))
		shimPath = filepath.Join(cacheDir, "libcupticapture-"+sum[:16]+".so")
		if _, err := os.Stat(shimPath); err == nil {
			return // cached from a previous run
		}

		srcPath := shimPath + ".c"
		if err := os.WriteFile(srcPath, []byte(shimSource), 0o644); err != nil {
			buildErr = fmt.Errorf("cupticapture: write shim source: %w", err)
			return
		}

		args := []string{"-O2", "-fPIC", "-shared", "-o", shimPath, srcPath}
		for _, dir := range cudaIncludeDirs() {
			args = append(args, "-I"+dir)
		}
		args = append(args, "-ldl", "-lpthread")

		cmd := exec.Command(cc, args...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			buildErr = fmt.Errorf("cupticapture: compile shim (%s): %v\n%s", cc, err, out)
			return
		}
		// Source file is kept beside the shim for debuggability.
	})
	return shimPath, buildErr
}

// Options configures one capture run.
type Options struct {
	OutputPath string // events.jsonl path inside the capture bundle
	Dir        string // working directory for the target
	Debug      bool   // pass through shim diagnostics
	APIRecords bool   // enable host-side CUDA API call records (higher volume)
	NVTX       bool   // enable NVTX marker (range) records
}

// LibCUPTIPath locates the CUPTI shared library. NVTX routes its ranges
// into a profiler through the library named by NVTX_INJECTION64_PATH, so
// capturing markers needs the path, not just the loaded library.
func LibCUPTIPath() (string, error) {
	candidates := []string{
		"libcupti.so",
		"libcupti.so.13",
		"libcupti.so.12",
	}
	if e := os.Getenv("CUDA_HOME"); e != "" {
		candidates = append(candidates, filepath.Join(e, "lib64", "libcupti.so"))
	}
	candidates = append(candidates,
		"/usr/local/cuda/lib64/libcupti.so",
		"/usr/local/cuda/extras/CUPTI/lib64/libcupti.so",
		"/usr/local/cuda-13/lib64/libcupti.so",
		"/usr/local/cuda-12/lib64/libcupti.so",
	)
	for _, c := range candidates {
		if !strings.Contains(c, "/") {
			continue // bare sonames are for the loader, not for a path
		}
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", fmt.Errorf("cupticapture: libcupti.so not found; NVTX ranges need it on disk")
}

// captureEnv builds the shim environment shared by Command and PreloadEnv.
func captureEnv(shim string, opts Options) []string {
	env := []string{
		fmt.Sprintf("GPUTRACE_CAPTURE_OUT=%s", opts.OutputPath),
		fmt.Sprintf("LD_PRELOAD=%s", shim),
	}
	if opts.Debug {
		env = append(env, "GPUTRACE_CAPTURE_DEBUG=1")
	}
	if opts.APIRecords {
		env = append(env, "GPUTRACE_CAPTURE_API=1")
	}
	if opts.NVTX {
		env = append(env, "GPUTRACE_CAPTURE_NVTX=1")
		// A target that loads NVTX dynamically (the common case) only
		// emits into a profiler that named itself here. A missing
		// libcupti is not fatal: statically-linked NVTX still routes
		// through the armed activity API.
		if lib, err := LibCUPTIPath(); err == nil {
			env = append(env, fmt.Sprintf("NVTX_INJECTION64_PATH=%s", lib))
		}
	}
	return env
}

// Command wraps argv with the environment needed to trace it. It returns
// the modified environment and any setup error.
func Command(argv []string, opts Options) ([]string, *[]string, error) {
	shim, err := ensureShim()
	if err != nil {
		return nil, nil, err
	}
	env := append(os.Environ(), captureEnv(shim, opts)...)
	return argv, &env, nil
}

// PreloadEnv returns just the environment additions, for callers managing
// their own exec environment.
func PreloadEnv(opts Options) ([]string, error) {
	shim, err := ensureShim()
	if err != nil {
		return nil, err
	}
	return captureEnv(shim, opts), nil
}
