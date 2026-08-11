// Package capture launches a Metal workload under Apple's GPUToolsCapture
// interposer and collects the resulting .gputrace bundle.
//
// The interposer is loaded with DYLD_INSERT_LIBRARIES, so it reaches only
// targets that dyld will honor that variable for. Hardened-runtime binaries
// with library validation, and Apple platform binaries, silently drop it: the
// process runs normally and produces no trace. [Eligible] reports that up front
// rather than letting the caller discover it from an empty output directory.
package capture

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Options configure a capture run.
type Options struct {
	// Output is the .gputrace bundle to create. It must not already exist.
	Output string

	// Dir is the working directory for the target. Empty means inherit.
	Dir string

	// Env supplies additional environment entries in "K=V" form, appended
	// after the interposer variables so a caller can override them.
	Env []string

	// Stdout and Stderr receive the target's output. Nil discards it.
	Stdout, Stderr *bytes.Buffer
}

// ErrNotInterposable reports that dyld will not load the interposer into the
// target, so no trace can be produced. The message names the specific reason.
var ErrNotInterposable = errors.New("target does not accept an interposer")

// Eligible reports whether dyld will honor DYLD_INSERT_LIBRARIES for the
// binary at path. It returns a nil error when the target is interposable, and
// an error wrapping [ErrNotInterposable] naming the disqualifying property
// otherwise.
//
// Two properties disqualify a target:
//
//   - the library-validation code-directory flag (0x2000), set by the hardened
//     runtime, unless the binary also carries the
//     com.apple.security.cs.disable-library-validation entitlement;
//   - a platform identifier, which SIP enforces for Apple-shipped executables
//     regardless of their code-directory flags. /System/Applications/Chess.app
//     carries flags=0x0 and is still not interposable, so the flags alone are
//     not a sufficient test.
//
// A target that fails this check is not merely slow to capture: it produces no
// trace at all, with no error from dyld.
func Eligible(path string) error {
	// -dvvv is required: the platform identifier is not printed at -dv.
	out, err := exec.Command("codesign", "-dvvv", "--entitlements", "-", path).CombinedOutput()
	if err != nil {
		// An unsigned binary makes codesign exit non-zero. Unsigned targets
		// accept the interposer, so that is not a failure here.
		if strings.Contains(string(out), "not signed") {
			return nil
		}
		return fmt.Errorf("inspect signature of %s: %w", path, err)
	}
	s := string(out)
	if strings.Contains(s, "library-validation") &&
		!strings.Contains(s, "com.apple.security.cs.disable-library-validation") {
		return fmt.Errorf("%s has library validation enabled: %w", filepath.Base(path), ErrNotInterposable)
	}
	if strings.Contains(s, "Platform identifier=") {
		return fmt.Errorf("%s is an Apple platform binary: %w", filepath.Base(path), ErrNotInterposable)
	}
	return nil
}

// Run executes argv under the interposer and returns the path to the captured
// bundle. It checks [Eligible] first and does not launch an ineligible target.
func Run(ctx context.Context, opts Options, argv ...string) (string, error) {
	if len(argv) == 0 {
		return "", errors.New("capture: no command")
	}
	if opts.Output == "" {
		return "", errors.New("capture: no output path")
	}
	output, err := filepath.Abs(opts.Output)
	if err != nil {
		return "", fmt.Errorf("capture: resolve output path: %w", err)
	}
	opts.Output = output
	if _, err := os.Stat(opts.Output); err == nil {
		return "", fmt.Errorf("capture: %s already exists", opts.Output)
	}
	lock := opts.Output + ".capture-lock"
	if _, err := os.Stat(lock); err == nil {
		// The interposer creates this file and Run removes it, so a leftover
		// means either a capture is running now or one was killed before it
		// could clean up. Those need opposite responses, and only the caller
		// can tell them apart, so say what the file is and who wrote it.
		return "", fmt.Errorf("capture: election lock %s already exists: "+
			"another capture is running, or one was killed; %s", lock,
			lockHolder(lock))
	}
	defer os.Remove(lock)
	dylib, err := injector()
	if err != nil {
		return "", fmt.Errorf("capture: %w", err)
	}

	bin, err := exec.LookPath(argv[0])
	if err != nil {
		return "", fmt.Errorf("capture: %w", err)
	}
	if err := Eligible(bin); err != nil {
		return "", err
	}

	cmd := exec.CommandContext(ctx, bin, argv[1:]...)
	cmd.Dir = opts.Dir
	cmd.Env = append(env(opts.Output, lock, dylib), opts.Env...)
	if opts.Stdout != nil {
		cmd.Stdout = opts.Stdout
	}
	if opts.Stderr != nil {
		cmd.Stderr = opts.Stderr
	}
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("capture: %s: %w", argv[0], err)
	}

	// The target exiting zero does not mean a usable trace was written, and
	// neither does the bundle existing. A capture that starts and records
	// nothing still leaves a directory behind, so check for recorded content.
	if _, err := os.Stat(opts.Output); err != nil {
		return "", fmt.Errorf("capture: %s exited cleanly but wrote no bundle to %s: "+
			"no capture was triggered", argv[0], opts.Output)
	}
	if err := recorded(opts.Output); err != nil {
		return "", err
	}
	return opts.Output, nil
}

// recorded reports whether a bundle holds captured command data. An empty
// bundle means the capture was live but saw no command buffers on the device it
// was attached to — a distinct failure from never starting one, and one that
// looks identical from the exit status and the directory's existence.
func recorded(bundle string) error {
	if _, err := os.Stat(filepath.Join(bundle, "unsorted-capture")); err == nil {
		return nil
	}
	return fmt.Errorf("capture: %s holds no command data: the capture ran but "+
		"recorded no command buffers (target may use a different MTLDevice, or "+
		"may not have flushed before exit)", bundle)
}

// env returns the environment for an interposed target.
//
// MTL_CAPTURE_ENABLED is the supported switch that inserts the GPUToolsCapture
// layer; Metal's own error text names it when a capture is attempted without
// it. Do not also set METAL_DEVICE_WRAPPER_TYPE: it substitutes an
// MTLDebugDevice that has no -traceStream, and startCapture then throws.
// lockHolder describes the process recorded in an election lock, so the caller
// can tell a live capture from a leftover. gputrace_elect writes "pid\texe"; a
// truncated or unreadable file means the writer died mid-write, which is itself
// the answer.
func lockHolder(lock string) string {
	data, err := os.ReadFile(lock)
	if err != nil {
		return "cannot read it: " + err.Error()
	}
	pid, exe, _ := strings.Cut(strings.TrimSpace(string(data)), "\t")
	n, err := strconv.Atoi(pid)
	if err != nil {
		return "it names no process; remove it"
	}
	// Signal 0 tests for existence without delivering anything. ESRCH means no
	// such process; EPERM means it exists and is not ours, which still counts.
	if err := syscall.Kill(n, 0); errors.Is(err, syscall.ESRCH) {
		return fmt.Sprintf("pid %d (%s) is gone; remove it", n, exe)
	}
	return fmt.Sprintf("pid %d (%s) is still running", n, exe)
}

func env(output, lock, dylib string) []string {
	return append(os.Environ(),
		"MTL_CAPTURE_ENABLED=1",
		"DYLD_INSERT_LIBRARIES="+dylib,
		"GT_TRACE_OUT="+output,
		"GT_CAPTURE_LOCK="+lock,
	)
}

//go:embed inject.objc
var injectSource string

// injector builds the trigger dylib and returns its path, reusing a cached
// build when the source has not changed. The target starts no capture on its
// own, so the dylib starts one when the target first creates a Metal device.
func injector() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir = filepath.Join(dir, "gputrace")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(injectSource))
	dylib := filepath.Join(dir, fmt.Sprintf("inject-%x.dylib", sum[:8]))
	if _, err := os.Stat(dylib); err == nil {
		return dylib, nil
	}

	src := filepath.Join(dir, "inject.m")
	if err := os.WriteFile(src, []byte(injectSource), 0o644); err != nil {
		return "", err
	}
	cmd := exec.Command("clang", "-dynamiclib", "-fobjc-arc", "-O2",
		"-framework", "Metal", "-framework", "Foundation",
		"-o", dylib, src)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build capture injector: %v: %s", err, out)
	}
	return dylib, nil
}
