// Package profilereplay produces performance data for a captured .gputrace
// bundle by replaying it under Apple's MTLReplayer.
//
// A capture records what a Metal workload did; it carries no timing. MTLReplayer
// replays that capture on the GPU with the profiler attached and writes a
// .gpuprofiler_raw payload holding streamData and the Counters, Profiling and
// Timeline shards. The replay is headless: MTLReplayer is an LSUIElement agent,
// so no window opens and the frontmost application does not change.
//
// The payload alone is a profiler-only bundle. Embed reassembles it with the
// original capture stream when the capture-dependent commands are needed too.
package profilereplay

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/tmc/gputrace/internal/profilerraw"
)

// AppPath is the system MTLReplayer application bundle.
const AppPath = "/System/Library/CoreServices/MTLReplayer.app"

var (
	// ErrUnavailable reports that MTLReplayer is not installed.
	ErrUnavailable = errors.New("MTLReplayer is not installed")

	// ErrNoCapture reports that a trace holds no capture stream to replay.
	ErrNoCapture = errors.New("no capture stream to replay")

	// ErrNoProfilerData reports that a replay wrote no profiler payload.
	ErrNoProfilerData = errors.New("replay wrote no profiler data")

	// ErrReplayerBusy reports that another MTLReplayer process is currently active.
	ErrReplayerBusy = errors.New("MTLReplayer is currently running another capture replay")
)

// Available reports whether MTLReplayer can be run on this machine.
func Available() error {
	info, err := os.Stat(AppPath)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("%w: %s", ErrUnavailable, AppPath)
	}
	return nil
}

// Replayable reports whether path is a trace bundle MTLReplayer can replay.
// A bundle without a capture stream — a profiler-only export, or the output of
// an earlier replay — has nothing to replay and is rejected here rather than
// after a launch that would report success having done nothing.
func Replayable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a .gputrace bundle", path)
	}
	for _, name := range []string{"capture", "unsorted-capture"} {
		if _, err := os.Stat(filepath.Join(path, name)); err == nil {
			return nil
		}
	}
	return fmt.Errorf("%w: %s", ErrNoCapture, path)
}

// DefaultOutput is where Profile writes when Options.Output is empty: the
// input's name with a -perfdata suffix, beside the input.
//
//	run.gputrace -> run-perfdata.gputrace
func DefaultOutput(in string) string {
	trimmed := strings.TrimSuffix(filepath.Clean(in), ".gputrace")
	return trimmed + "-perfdata.gputrace"
}

// Options controls where a replay writes and what it assembles.
type Options struct {
	// Output is the path to write. Empty means DefaultOutput of the input.
	// It must not already exist.
	Output string

	// Embed copies the input's capture stream in alongside the profiler
	// payload, producing a self-contained trace. Without it the output holds
	// the profiler payload only, which is what MTLReplayer writes natively and
	// is enough for profiler, timing, timeline and pprof. The capture-dependent
	// commands — kernels, buffer bindings, grid sizes — need the copy.
	Embed bool

	// Wait waits for another MTLReplayer run to finish instead of returning
	// ErrReplayerBusy. Replays remain non-overlapping across processes.
	Wait bool
}

// Profile replays in under the profiler and returns the path it wrote.
func Profile(ctx context.Context, in string, opts Options) (string, error) {
	unlock, err := acquireLock(ctx, opts.Wait)
	if err != nil {
		return "", err
	}
	defer unlock()

	if opts.Output == "" {
		opts.Output = DefaultOutput(in)
	}
	if err := Available(); err != nil {
		return "", err
	}
	if err := Replayable(in); err != nil {
		return "", err
	}
	if _, err := os.Stat(opts.Output); err == nil {
		return "", fmt.Errorf("%s already exists", opts.Output)
	}

	// open hands --args to a process whose working directory is not ours, so a
	// relative path there resolves somewhere else and the replay silently reads
	// or writes the wrong thing.
	inAbs, err := filepath.Abs(in)
	if err != nil {
		return "", err
	}
	outAbs, err := filepath.Abs(opts.Output)
	if err != nil {
		return "", err
	}

	dest := outAbs
	if opts.Embed {
		// Replay to a sibling scratch directory. The output bundle is assembled
		// only after the payload is known good, so a failed replay leaves no
		// half-built trace behind.
		scratch, err := os.MkdirTemp(filepath.Dir(outAbs), ".profile-replay-")
		if err != nil {
			return "", err
		}
		defer os.RemoveAll(scratch)
		dest = filepath.Join(scratch, "payload")
	}

	if err := run(ctx, inAbs, dest); err != nil {
		return "", err
	}

	payload := profilerraw.FindDirWithStreamData(dest)
	if payload == "" {
		return "", fmt.Errorf("%w: %s holds no .gpuprofiler_raw with streamData", ErrNoProfilerData, dest)
	}
	if !opts.Embed {
		return outAbs, nil
	}
	if err := embed(inAbs, outAbs, payload); err != nil {
		return "", err
	}
	return outAbs, nil
}

// run launches MTLReplayer and waits for it.
//
// The launch goes through LaunchServices rather than exec. An AMFI launch
// constraint on the *parent* process kills a direct exec of the binary with
// "Launch Constraint Violation (enforcing), error info: c[1]p[1]m[1]e[14]";
// LaunchServices satisfies the constraint, so open works where exec cannot.
//
// -CLI must be argv[1] exactly. MTLReplayer tests strcmp("-CLI", argv[1]) and,
// failing that test, enters NSApplicationMain and sits in a GUI event loop
// until killed. The flag anywhere else hangs the run instead of reporting a
// usage error, so the order below is load-bearing.
func run(ctx context.Context, in, out string) error {
	cmd := exec.CommandContext(ctx, "open", "-W", "-n", "-a", AppPath, "--args",
		"-CLI", in, "-profileTrace", "-collectProfilerData", "-outputPath", out)

	// LaunchServices, not this process, is MTLReplayer's parent, so killing open
	// on cancellation leaves the replayer running: a GPU-heavy orphan that
	// outlives the command that started it. Verified by interrupting a replay
	// and finding both open and MTLReplayer still alive. Match on the output
	// path, which is unique to this run, so a concurrent replay is not killed
	// too. The pattern must not start with "-": pkill parses a leading dash as
	// a flag and exits 2 having killed nothing, which looks like success here.
	cmd.Cancel = func() error {
		_ = exec.Command("/usr/bin/pkill", "-f", regexp.QuoteMeta(out)).Run()
		return cmd.Process.Kill()
	}

	combined, err := cmd.CombinedOutput()

	// open exits 0 having done nothing when it cannot attach to the application
	// it launched, printing "Unable to block on application (GetProcessPID()
	// returned ...)". The exit status is therefore not evidence that a replay
	// ran — the caller's check for a profiler payload is. This branch only
	// turns a confusing silence into a legible message.
	if text := strings.TrimSpace(string(combined)); strings.Contains(text, "Unable to block on application") {
		return fmt.Errorf("MTLReplayer did not start: %s", text)
	}
	if err != nil {
		return fmt.Errorf("run MTLReplayer: %w", err)
	}
	return nil
}

// embed builds a self-contained trace at out from the capture stream at in and
// a validated profiler payload.
func embed(in, out, payload string) error {
	if err := os.CopyFS(out, os.DirFS(in)); err != nil {
		return fmt.Errorf("copy capture stream: %w", err)
	}
	dest := filepath.Join(out, filepath.Base(payload))
	if err := os.Rename(payload, dest); err != nil {
		// The scratch directory and the output can land on different volumes.
		if err := os.CopyFS(dest, os.DirFS(payload)); err != nil {
			return fmt.Errorf("embed profiler data: %w", err)
		}
	}
	if profilerraw.FindDirWithStreamData(out) == "" {
		return fmt.Errorf("%w: %s after embedding", ErrNoProfilerData, out)
	}
	return nil
}

// acquireLock ensures exclusive MTLReplayer execution across processes.
func acquireLock(ctx context.Context, wait bool) (func(), error) {
	lockPath := filepath.Join(os.TempDir(), "gputrace-mtlreplayer.lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("create replayer lock: %w", err)
	}
	if err := flock(ctx, f, wait); err != nil {
		_ = f.Close()
		return nil, err
	}
	for mtlReplayerRunning(ctx) {
		if !wait {
			_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
			_ = f.Close()
			return nil, fmt.Errorf("%w: MTLReplayer process is active", ErrReplayerBusy)
		}
		select {
		case <-ctx.Done():
			_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
			_ = f.Close()
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}

	unlock := func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}
	return unlock, nil
}

func flock(ctx context.Context, f *os.File, wait bool) error {
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return nil
		}
		if !wait {
			return fmt.Errorf("%w: %s", ErrReplayerBusy, f.Name())
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func mtlReplayerRunning(ctx context.Context) bool {
	return exec.CommandContext(ctx, "/usr/bin/pgrep", "-x", "MTLReplayer").Run() == nil
}
