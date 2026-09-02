// Package gpudoctor diagnoses the GPU profiling environment: which tools
// are installed, which of them actually work against the running driver,
// and what to do about the ones that do not.
//
// Every check reports a status and, when something is wrong, the exact
// remediation. The checks exist because each failure they describe was
// first met the slow way, and two of them produce confidently wrong
// results rather than errors — a capture that looks healthy and contains
// no kernels is worse than one that fails.
package gpudoctor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/tmc/gputrace/internal/cupticapture"
	"github.com/tmc/gputrace/internal/gpuevent"
)

// Status ranks a check's outcome.
type Status string

const (
	// StatusOK means the component works for profiling as configured.
	StatusOK Status = "ok"
	// StatusWarn means it works with a caveat the user must know.
	StatusWarn Status = "warn"
	// StatusFail means it is broken or actively misleading here.
	StatusFail Status = "fail"
	// StatusSkip means the check could not run (component absent).
	StatusSkip Status = "skip"
)

// Check is one diagnosis with its evidence and remedy.
type Check struct {
	Name   string   `json:"name"`
	Status Status   `json:"status"`
	Detail string   `json:"detail"`
	Remedy string   `json:"remedy,omitempty"`
	Notes  []string `json:"notes,omitempty"`
}

// Report is the full diagnosis.
type Report struct {
	Checks []Check `json:"checks"`
}

// Worst returns the most severe status in the report.
func (r *Report) Worst() Status {
	worst := StatusOK
	for _, c := range r.Checks {
		if rank(c.Status) > rank(worst) {
			worst = c.Status
		}
	}
	return worst
}

func rank(s Status) int {
	switch s {
	case StatusFail:
		return 3
	case StatusWarn:
		return 2
	case StatusSkip:
		return 1
	}
	return 0
}

// Options selects which checks run.
type Options struct {
	// Target is an optional workload binary to diagnose for
	// capturability. Empty skips the target checks.
	Target string
}

// Run performs every check and returns the report. It never fails: an
// undiagnosable environment is itself the diagnosis.
func Run(opts Options) *Report {
	rep := &Report{}
	driver, driverCUDA := checkDriver(rep)
	checkToolkits(rep)
	checkCUPTI(rep, driverCUDA)
	checkNsight(rep)
	checkNCU(rep)
	checkShimPrereqs(rep)
	if opts.Target != "" {
		// A .gpucapture bundle is a finished capture, not a workload to
		// diagnose for capturability; the useful question about it is
		// whether it is complete.
		if cupticapture.IsBundle(opts.Target) {
			checkCapture(rep, opts.Target)
		} else {
			checkTarget(rep, opts.Target)
		}
	}
	_ = driver
	return rep
}

func add(rep *Report, c Check) { rep.Checks = append(rep.Checks, c) }

// --- driver -------------------------------------------------------------

var driverCUDARe = regexp.MustCompile(`CUDA Version:\s*([0-9]+\.[0-9]+)`)

// checkDriver reports the NVIDIA driver version and the maximum CUDA
// version it supports, which is the ceiling every CUPTI must fit under.
func checkDriver(rep *Report) (driver, cudaVersion string) {
	smi, err := exec.LookPath("nvidia-smi")
	if err != nil {
		add(rep, Check{
			Name:   "nvidia driver",
			Status: StatusFail,
			Detail: "nvidia-smi not on PATH; no NVIDIA driver visible",
			Remedy: "install the NVIDIA driver, or run on a host with a GPU",
		})
		return "", ""
	}
	out, err := exec.Command(smi).Output()
	if err != nil {
		add(rep, Check{
			Name:   "nvidia driver",
			Status: StatusFail,
			Detail: fmt.Sprintf("nvidia-smi failed: %v", err),
			Remedy: "check that the kernel module is loaded (modprobe nvidia)",
		})
		return "", ""
	}
	text := string(out)
	if m := regexp.MustCompile(`Driver Version:\s*([0-9.]+)`).FindStringSubmatch(text); m != nil {
		driver = m[1]
	}
	if m := driverCUDARe.FindStringSubmatch(text); m != nil {
		cudaVersion = m[1]
	}
	name := ""
	if m := regexp.MustCompile(`\|\s+\d+\s+(NVIDIA [^|]+?)\s{2,}`).FindStringSubmatch(text); m != nil {
		name = strings.TrimSpace(m[1])
	}
	detail := fmt.Sprintf("driver %s, CUDA %s", orUnknown(driver), orUnknown(cudaVersion))
	if name != "" {
		detail = name + ": " + detail
	}
	add(rep, Check{Name: "nvidia driver", Status: StatusOK, Detail: detail})
	return driver, cudaVersion
}

// --- CUDA toolkits ------------------------------------------------------

// toolkitDirs lists installed CUDA toolkits by their versioned directory.
func toolkitDirs() []string {
	matches, _ := filepath.Glob("/usr/local/cuda-*")
	sort.Strings(matches)
	return matches
}

func checkToolkits(rep *Report) {
	dirs := toolkitDirs()
	if len(dirs) == 0 {
		add(rep, Check{
			Name:   "cuda toolkit",
			Status: StatusSkip,
			Detail: "no /usr/local/cuda-* toolkit found",
		})
		return
	}
	names := make([]string, 0, len(dirs))
	for _, d := range dirs {
		names = append(names, strings.TrimPrefix(d, "/usr/local/"))
	}
	detail := strings.Join(names, ", ")
	if link, err := os.Readlink("/usr/local/cuda"); err == nil {
		detail += fmt.Sprintf("; /usr/local/cuda -> %s", filepath.Base(link))
	}
	add(rep, Check{Name: "cuda toolkit", Status: StatusOK, Detail: detail})
}

// --- CUPTI --------------------------------------------------------------

// cuptiLibs lists the distinct libcupti libraries on the system, one per
// resolved file: the versioned soname and the real library behind it are
// the same object, and listing both only obscures which toolkits are
// present.
func cuptiLibs() map[string]int {
	libs := map[string]int{} // path -> CUPTI major version
	seen := map[string]bool{}
	globs := []string{
		"/usr/local/cuda-*/lib64/libcupti.so.*",
		"/usr/local/cuda-*/extras/CUPTI/lib64/libcupti.so.*",
		"/usr/lib/*/libcupti.so.*",
	}
	for _, g := range globs {
		matches, _ := filepath.Glob(g)
		for _, m := range matches {
			resolved, err := filepath.EvalSymlinks(m)
			if err != nil {
				resolved = m
			}
			if seen[resolved] {
				continue
			}
			seen[resolved] = true
			major := 0
			if v := sonameVersionRe.FindStringSubmatch(filepath.Base(m)); v != nil {
				major, _ = strconv.Atoi(v[1])
			}
			// Prefer the versioned soname as the display name: it names
			// the ABI a target would load.
			name := m
			if major == 0 {
				name = resolved
			}
			libs[name] = major
		}
	}
	return libs
}

var sonameVersionRe = regexp.MustCompile(`libcupti\.so\.([0-9]+)$`)

// checkCUPTI reports which CUPTI libraries exist and whether one matching
// the running driver is among them. A CUPTI built against an older
// toolkit than the driver produces zero activity records rather than an
// error, which reads as "the workload launched no kernels".
func checkCUPTI(rep *Report, driverCUDA string) {
	libs := cuptiLibs()
	if len(libs) == 0 {
		add(rep, Check{
			Name:   "libcupti",
			Status: StatusFail,
			Detail: "no libcupti found; gputrace capture cannot record anything",
			Remedy: "install the CUDA toolkit's CUPTI component (cuda-cupti-*)",
		})
		return
	}
	driverMajor := majorOf(driverCUDA)
	paths := make([]string, 0, len(libs))
	for p := range libs {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	var notes []string
	current, stale := 0, 0
	for _, p := range paths {
		major := libs[p]
		switch {
		case major == 0:
			notes = append(notes, p)
		case driverMajor > 0 && major < driverMajor:
			stale++
			notes = append(notes, fmt.Sprintf("%s  (CUPTI %d under a CUDA %d driver: records nothing)", p, major, driverMajor))
		default:
			current++
			notes = append(notes, p)
		}
	}
	c := Check{Name: "libcupti", Status: StatusOK, Notes: notes}
	switch {
	case driverMajor == 0:
		c.Detail = fmt.Sprintf("%d found; driver CUDA version unknown, so no compatibility verdict", len(paths))
		c.Status = StatusWarn
	case current == 0:
		c.Detail = fmt.Sprintf("every libcupti found predates the CUDA %d driver", driverMajor)
		c.Status = StatusFail
		c.Remedy = fmt.Sprintf("install the CUDA %d toolkit's CUPTI; the ones present emit no activity records at all", driverMajor)
	case stale > 0:
		c.Status = StatusWarn
		c.Detail = fmt.Sprintf("%d match the CUDA %d driver, %d predate it", current, driverMajor, stale)
		c.Remedy = "keep the matching toolkit ahead of the older ones on LD_LIBRARY_PATH; an older CUPTI records nothing rather than failing"
	default:
		c.Detail = fmt.Sprintf("%d found, all matching the CUDA %d driver", len(paths), driverMajor)
	}
	add(rep, c)
}

func majorOf(version string) int {
	if version == "" {
		return 0
	}
	major, _ := strconv.Atoi(strings.SplitN(version, ".", 2)[0])
	return major
}

// --- Nsight Systems -----------------------------------------------------

// nsysVerdict maps an nsys version to how its default CUDA tracing
// behaves. The 2026.1 default is the dangerous one: it enables hardware
// event tracing, whose support probe wrongly succeeds on GB10-class
// parts, after which every kernel record is dropped as incomplete while
// the CPU-side tables populate normally.
func nsysVerdict(version string) (Status, string, string) {
	switch {
	case strings.HasPrefix(version, "2026."):
		return StatusWarn,
			"-t cuda enables hardware tracing, which drops every kernel record on GB10-class parts while the capture still looks healthy",
			"always pass -t cuda-sw with this version; add --cuda-graph-trace=node for CUDA-graph workloads"
	case strings.HasPrefix(version, "2024."):
		return StatusFail,
			"bundles a CUPTI far older than current drivers and produces zero CUPTI events",
			"do not use this nsys; prefer a 2025.3 or newer install, and check what root's PATH resolves to before trusting a sudo run"
	case version == "":
		return StatusSkip, "version not reported", ""
	}
	return StatusOK, "-t cuda falls back to software tracing correctly", ""
}

var nsysVersionRe = regexp.MustCompile(`([0-9]{4}\.[0-9]+\.[0-9]+)`)

// nsysCandidates lists every nsys the user could end up running: the one
// on PATH first, then the ones installed beside each toolkit.
func nsysCandidates() []string {
	var out []string
	seen := map[string]bool{}
	appendPath := func(p string) {
		if p == "" || seen[p] {
			return
		}
		if _, err := os.Stat(p); err != nil {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	if p, err := exec.LookPath("nsys"); err == nil {
		if resolved, err := filepath.EvalSymlinks(p); err == nil {
			appendPath(resolved)
		} else {
			appendPath(p)
		}
	}
	for _, g := range []string{"/usr/local/cuda-*/bin/nsys", "/opt/nvidia/nsight-systems/*/target-linux-*/nsys"} {
		matches, _ := filepath.Glob(g)
		sort.Strings(matches)
		for _, m := range matches {
			if resolved, err := filepath.EvalSymlinks(m); err == nil {
				appendPath(resolved)
			} else {
				appendPath(m)
			}
		}
	}
	return out
}

func nsysVersion(path string) string {
	out, err := exec.Command(path, "--version").Output()
	if err != nil {
		return ""
	}
	if m := nsysVersionRe.FindStringSubmatch(string(out)); m != nil {
		return m[1]
	}
	return ""
}

func checkNsight(rep *Report) {
	candidates := nsysCandidates()
	if len(candidates) == 0 {
		add(rep, Check{
			Name:   "nsight systems",
			Status: StatusSkip,
			Detail: "nsys not installed; gputrace capture does not need it",
		})
		return
	}
	onPath, _ := exec.LookPath("nsys")

	// The status describes the nsys the user will actually run. Other
	// installs are reported as notes: a broken one elsewhere on the disk
	// only matters when something resolves to it, which is exactly what
	// happens under sudo with a different PATH.
	status, remedy := StatusSkip, ""
	othersWorse := false
	var notes []string
	for _, p := range candidates {
		version := nsysVersion(p)
		verdict, detail, fix := nsysVerdict(version)
		marker := " "
		if onPath != "" && samePath(onPath, p) {
			marker = "*"
			status, remedy = verdict, fix
		} else if rank(verdict) >= rank(StatusFail) {
			othersWorse = true
		}
		notes = append(notes, fmt.Sprintf("%s %-9s %s", marker, orUnknown(version), p))
		notes = append(notes, "    "+detail)
	}
	detail := fmt.Sprintf("%d nsys install%s found", len(candidates), plural(len(candidates)))
	if onPath == "" {
		detail += "; none on PATH"
	}
	if othersWorse {
		notes = append(notes, "  another install on this system produces no CUPTI events at all; check what root's PATH resolves to before trusting a sudo run")
		if rank(StatusWarn) > rank(status) {
			status = StatusWarn
		}
	}
	if len(candidates) > 1 && onPath != "" {
		notes = append(notes, "  (* is the one on your PATH)")
	}
	add(rep, Check{Name: "nsight systems", Status: status, Detail: detail, Remedy: remedy, Notes: notes})
}

func samePath(a, b string) bool {
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		ra = a
	}
	return ra == b
}

// --- Nsight Compute -----------------------------------------------------

// profilingRestricted reports whether GPU performance counters are
// restricted to administrators, which is what makes ncu fail with
// ERR_NVGPUCTRPERM for an ordinary user.
//
// It asks ncu itself: the driver parameter is not exposed under /proc or
// /sys on every driver (notably not on GB10), and a sub-second
// --query-metrics probe answers the question that matters — whether this
// user can read counters right now — rather than inferring it. ncu exits
// 0 even when it refuses, so the verdict is in the output.
func profilingRestricted(ncu string) (restricted bool, known bool) {
	out, err := exec.Command(ncu, "--query-metrics").CombinedOutput()
	if err == nil || len(out) > 0 {
		if strings.Contains(string(out), "ERR_NVGPUCTRPERM") {
			return true, true
		}
		if err == nil {
			return false, true
		}
	}
	// Fall back to the driver parameter where the probe could not run.
	data, readErr := os.ReadFile("/proc/driver/nvidia/params")
	if readErr != nil {
		return false, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "RestrictProfilingToAdminUsers:") {
			value := strings.TrimSpace(strings.TrimPrefix(line, "RestrictProfilingToAdminUsers:"))
			return value != "0", true
		}
	}
	return false, false
}

// profilingConfigStaged returns the modprobe drop-in that already sets
// NVreg_RestrictProfilingToAdminUsers=0, or "" if none does.
//
// It exists to tell two states apart that otherwise look identical: the
// setting was never made, and the setting was made and has not taken
// effect. The nvidia module reads the parameter when it loads, and it
// cannot be reloaded while a display server holds it -- on this host the
// module sat at refcount 306 with Xorg and a browser attached -- so a
// drop-in written today governs nothing until the next boot. Repeating
// "write this file" at someone who already wrote it is the unhelpful
// answer, and it is the one this check used to give.
func profilingConfigStaged() string {
	paths, err := filepath.Glob("/etc/modprobe.d/*.conf")
	if err != nil {
		return ""
	}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "#") {
				continue
			}
			if strings.Contains(line, "NVreg_RestrictProfilingToAdminUsers=0") {
				return p
			}
		}
	}
	return ""
}

func checkNCU(rep *Report) {
	path, err := exec.LookPath("ncu")
	if err != nil {
		add(rep, Check{
			Name:   "nsight compute",
			Status: StatusSkip,
			Detail: "ncu not on PATH; per-kernel hardware counters unavailable",
			Remedy: "install the CUDA toolkit's Nsight Compute component to use gputrace ncu",
		})
		return
	}
	restricted, known := profilingRestricted(path)
	c := Check{Name: "nsight compute", Status: StatusOK, Detail: path}
	switch {
	case !known:
		c.Status = StatusWarn
		c.Detail = path + "; could not determine counter permission"
	case restricted:
		c.Status = StatusWarn
		c.Detail = path + "; GPU performance counters refused for this user (ERR_NVGPUCTRPERM)"
		if staged := profilingConfigStaged(); staged != "" {
			c.Detail += "; " + staged + " already sets NVreg_RestrictProfilingToAdminUsers=0"
			c.Remedy = "the setting is staged but not in force: the nvidia module reads it at load\n" +
				"time and cannot be reloaded while a display server holds it, so it applies at\n" +
				"the next boot. Until then, run ncu under sudo."
		} else {
			c.Remedy = "run ncu under sudo, or make it permanent:\n" +
				"  echo 'options nvidia NVreg_RestrictProfilingToAdminUsers=0' | sudo tee /etc/modprobe.d/nvidia-profiling.conf\n" +
				"  sudo update-initramfs -u && sudo reboot"
		}
	default:
		c.Detail = path + "; GPU performance counters readable by this user"
	}
	c.Notes = append(c.Notes, "ncu replays each kernel with different counters armed and serializes execution: use it per kernel, never for a whole-run timeline")
	add(rep, c)
}

// --- capture prerequisites ---------------------------------------------

// ShimBuilder builds the capture shim and reports where it landed. The
// command layer supplies it so this package stays free of the capture
// machinery it diagnoses.
type ShimBuilder func() (string, error)

var shimBuilder ShimBuilder

// SetShimBuilder registers how to build the capture shim.
func SetShimBuilder(b ShimBuilder) { shimBuilder = b }

func checkShimPrereqs(rep *Report) {
	var compiler string
	for _, cc := range []string{"gcc", "clang", "cc"} {
		if p, err := exec.LookPath(cc); err == nil {
			compiler = p
			break
		}
	}
	if compiler == "" {
		add(rep, Check{
			Name:   "capture shim",
			Status: StatusFail,
			Detail: "no C compiler (gcc, clang, cc) found; the CUPTI shim cannot be built",
			Remedy: "install a C compiler and the CUDA headers (cupti.h)",
		})
		return
	}
	if shimBuilder == nil {
		add(rep, Check{
			Name:   "capture shim",
			Status: StatusSkip,
			Detail: "compiler " + compiler + "; shim build not probed",
		})
		return
	}
	path, err := shimBuilder()
	if err != nil {
		add(rep, Check{
			Name:   "capture shim",
			Status: StatusFail,
			Detail: fmt.Sprintf("shim build failed: %v", err),
			Remedy: "install the CUDA headers so cupti.h resolves, or set CUDA_HOME",
		})
		return
	}
	add(rep, Check{
		Name:   "capture shim",
		Status: StatusOK,
		Detail: fmt.Sprintf("built with %s -> %s", filepath.Base(compiler), path),
	})
}

// --- target -------------------------------------------------------------

// checkTarget diagnoses one workload binary for capturability: whether
// the CUDA runtime is linked dynamically (LD_PRELOAD only interposes
// dynamic calls) and whether it is a Go binary, which needs an in-process
// flush because a Go process crosses no interposed synchronization point
// and exits via exit_group without running the shim's ELF destructor.
func checkTarget(rep *Report, target string) {
	path, err := exec.LookPath(target)
	if err != nil {
		add(rep, Check{
			Name:   "target " + target,
			Status: StatusFail,
			Detail: fmt.Sprintf("cannot resolve %s: %v", target, err),
		})
		return
	}
	libs, lddErr := sharedLibs(path)
	c := Check{Name: "target " + filepath.Base(path), Status: StatusOK, Detail: path}
	switch {
	case lddErr != nil:
		c.Status = StatusWarn
		c.Detail = path + fmt.Sprintf("; cannot list shared libraries: %v", lddErr)
	case !libs.dynamic:
		c.Status = StatusFail
		c.Detail = path + "; statically linked, so LD_PRELOAD interposes nothing"
		c.Remedy = "rebuild the target against the shared CUDA runtime (nvcc -cudart=shared)"
	case !libs.cuda:
		c.Status = StatusWarn
		c.Detail = path + "; links no CUDA library directly"
		c.Remedy = "if CUDA loads later via dlopen this is fine; otherwise capture will record nothing"
	default:
		c.Detail = path + "; links CUDA dynamically"
	}
	if libs.goBinary {
		c.Notes = append(c.Notes,
			"Go binary: CUPTI only flushes at interposed synchronization points, which a Go process never crosses, and exit_group skips the shim's destructor. Without an in-process cuptiActivityFlushAll the capture is silently empty — check for kernel records before believing any result.")
		if c.Status == StatusOK {
			c.Status = StatusWarn
		}
	}
	add(rep, c)
}

type targetLibs struct {
	dynamic  bool
	cuda     bool
	goBinary bool
}

// sharedLibs inspects a binary's dynamic dependencies and origin.
func sharedLibs(path string) (targetLibs, error) {
	var out targetLibs
	ldd, err := exec.Command("ldd", path).CombinedOutput()
	text := string(ldd)
	if err != nil && !strings.Contains(text, "not a dynamic executable") {
		return out, err
	}
	out.dynamic = !strings.Contains(text, "not a dynamic executable")
	out.cuda = strings.Contains(text, "libcudart") || strings.Contains(text, "libcuda.so")
	// A Go binary carries the runtime's build metadata; `go version` on a
	// binary reports it and fails on anything else.
	if _, err := exec.Command("go", "version", "-m", path).Output(); err == nil {
		if v, err := exec.Command("go", "version", path).Output(); err == nil && strings.Contains(string(v), "go1.") {
			out.goBinary = true
		}
	}
	return out, nil
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// --- finished captures ----------------------------------------------------

// checkCapture diagnoses a bundle that already exists, rather than an
// environment that is about to produce one.
//
// The target checks warn that a Go binary without an in-process flush
// yields a silently empty capture. That warning is well aimed and it is
// not enough: a capture can also come back half, and half looks entirely
// healthy — it renders, it summarizes, it diffs, and every number it
// produces is a share of the run presented as the run. This check reads
// the bundle and says which of the three it is.
func checkCapture(rep *Report, path string) {
	r, closers, err := cupticapture.OpenEvents(path)
	if err != nil {
		add(rep, Check{
			Name:   "bundle " + filepath.Base(path),
			Status: StatusFail,
			Detail: fmt.Sprintf("cannot read %s: %v", path, err),
		})
		return
	}
	cap, decodeErr := gpuevent.DecodeJSONL(r)
	closers()

	var kernels int
	for _, e := range cap.Events {
		if e.Kind == gpuevent.KindKernel {
			kernels++
		}
	}
	c := Check{Name: "bundle " + filepath.Base(path), Status: StatusOK}
	if decodeErr != nil {
		c.Notes = append(c.Notes, fmt.Sprintf("decode stopped early: %v", decodeErr))
	}
	health := gpuevent.MeasureCompleteness(cap)
	switch {
	case kernels == 0:
		c.Status = StatusFail
		c.Detail = fmt.Sprintf("no kernel records (%d api, %d spans, %d device samples)",
			len(cap.APIs), len(cap.Spans), len(cap.Samples))
		c.Remedy = "the target likely exited without flushing CUPTI's activity buffers; a Go target needs an in-process cuptiActivityFlushAll before exit"
	case !health.Complete():
		c.Status = StatusFail
		c.Detail = fmt.Sprintf("%d kernel records, but the %s", kernels, health.Summary())
		c.Remedy = health.Remedy()
	default:
		c.Detail = fmt.Sprintf("%d kernel records; %s", kernels, health.Summary())
	}
	// Requested instrumentation that recorded nothing: a flag that did
	// nothing and a workload that emits nothing read identically unless
	// the count is stated.
	if health.ExpectedGraphKernels > 0 {
		c.Notes = append(c.Notes, fmt.Sprintf("%d of %d kernel executions came from CUDA graphs, which carry no queue timestamps; queue_delay can only describe the eager remainder",
			health.GraphKernels, kernels))
	}
	if len(cap.Spans) == 0 {
		c.Notes = append(c.Notes, "no application spans: pprof stacks will be the kernel name alone. Spans come from a GPUTRACE_APP_EVENTS sidecar, in-process labels, or --nvtx ranges the target actually emits.")
	}
	if cap.UnpairedMarkers > 0 {
		c.Notes = append(c.Notes, fmt.Sprintf("%d NVTX markers never formed a range; the capture ended mid-range or the target emits unmatched markers", cap.UnpairedMarkers))
	}
	add(rep, c)
}
