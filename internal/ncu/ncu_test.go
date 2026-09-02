package ncu

import (
	"strings"
	"testing"
)

func TestKernelNamePattern(t *testing.T) {
	tests := []struct {
		name  string
		names []string
		want  string
	}{
		{"none", nil, ""},
		{"plain", []string{"saxpy"}, `regex:^(saxpy)$`},
		{
			// A demangled C++ symbol matches ncu's base name, so template
			// arguments, parameters, and namespaces come off.
			name:  "demangled c++",
			names: []string{"mlx::core::cu::rms_norm<__nv_bfloat16>(float*, int)", "scale"},
			want:  `regex:^(rms_norm|scale)$`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := KernelNamePattern(tt.names); got != tt.want {
				t.Errorf("KernelNamePattern() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCommandRequiresWorkload(t *testing.T) {
	if _, err := Command(Options{Path: "/bin/true"}, nil); err == nil {
		t.Error("Command() accepted an empty workload")
	}
}

func TestCommandArgs(t *testing.T) {
	cmd, err := Command(Options{
		Path:        "/bin/true",
		Kernels:     []string{"saxpy"},
		LaunchCount: 7,
		Metrics:     []string{"gpu__time_duration.sum"},
	}, []string{"./workload", "--flag"})
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(cmd.Args, " ")
	for _, want := range []string{
		// Matching must run against the bare function name; the
		// demangled form carries parameters and matches nothing.
		"--kernel-name-base function",
		"--kernel-name regex:^(saxpy)$",
		"--launch-count 7",
		"--metrics gpu__time_duration.sum",
		"--csv",
		"./workload --flag",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("args %q missing %q", args, want)
		}
	}
	if !strings.HasSuffix(args, "./workload --flag") {
		t.Errorf("workload must come last, got %q", args)
	}
}

func TestCommandSudo(t *testing.T) {
	cmd, err := Command(Options{Path: "/usr/local/cuda/bin/ncu", Sudo: true}, []string{"./w"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Args[0] != "sudo" || cmd.Args[1] != "-E" {
		t.Errorf("sudo invocation = %v, want it to start with sudo -E", cmd.Args[:3])
	}
}

// Real ncu output, trimmed: a progress line ahead of the table, one
// unsupported metric reporting n/a, and repeated launches to reduce.
const sampleCSV = `==PROF== Profiling "saxpy" - 0: 0%....50%....100% - 9 passes
"ID","Process ID","Process Name","Host Name","Kernel Name","Context","Stream","Block Size","Grid Size","Device","CC","Section Name","Metric Name","Metric Unit","Metric Value"
"0","19963","plain","127.0.0.1","saxpy","1","7","(256, 1, 1)","(16384, 1, 1)","0","12.1","Command line profiler metrics","dram__throughput.avg.pct_of_peak_sustained_elapsed","","n/a"
"0","19963","plain","127.0.0.1","saxpy","1","7","(256, 1, 1)","(16384, 1, 1)","0","12.1","Command line profiler metrics","gpu__time_duration.sum","us","211.94"
"1","19963","plain","127.0.0.1","saxpy","1","7","(256, 1, 1)","(16384, 1, 1)","0","12.1","Command line profiler metrics","gpu__time_duration.sum","us","203.02"
"2","19963","plain","127.0.0.1","saxpy","1","7","(256, 1, 1)","(16384, 1, 1)","0","12.1","Command line profiler metrics","gpu__time_duration.sum","us","1,205.00"
`

func TestParseCSV(t *testing.T) {
	got, err := ParseCSV(strings.NewReader(sampleCSV))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Kernels) != 1 {
		t.Fatalf("kernels = %d, want 1", len(got.Kernels))
	}
	k := got.Kernels[0]
	if k.Kernel != "saxpy" || k.Launches != 3 {
		t.Errorf("kernel = %q with %d launches, want saxpy with 3", k.Kernel, k.Launches)
	}
	if len(k.Metrics) != 1 {
		t.Fatalf("metrics = %+v, want just the supported one", k.Metrics)
	}
	m := k.Metrics[0]
	if m.Median != 211.94 || m.Min != 203.02 || m.Max != 1205 {
		t.Errorf("median/min/max = %v/%v/%v, want 211.94/203.02/1205 (thousands separator parsed)", m.Median, m.Min, m.Max)
	}
	if m.Unit != "us" || m.Count != 3 {
		t.Errorf("unit/count = %q/%d, want us/3", m.Unit, m.Count)
	}
	// A metric that does not exist on this part is reported, not dropped.
	if len(k.Unsupported) != 1 || !strings.HasPrefix(k.Unsupported[0], "dram__throughput") {
		t.Errorf("Unsupported = %v, want the dram metric", k.Unsupported)
	}
}

func TestParseCSVRejectsEmptyRun(t *testing.T) {
	// A report that exists proves nothing; it must contain rows.
	_, err := ParseCSV(strings.NewReader(`"ID","Process ID","Kernel Name","Metric Name","Metric Value"` + "\n"))
	if err == nil {
		t.Error("ParseCSV() accepted a table with no rows")
	}
	if _, err := ParseCSV(strings.NewReader("==ERROR== ERR_NVGPUCTRPERM\n")); err == nil {
		t.Error("ParseCSV() accepted output with no table")
	}
}

func TestPermissionDenied(t *testing.T) {
	if !PermissionDenied("==ERROR== ERR_NVGPUCTRPERM - The user does not have permission") {
		t.Error("PermissionDenied() missed ERR_NVGPUCTRPERM")
	}
	if PermissionDenied("all good") {
		t.Error("PermissionDenied() fired on clean output")
	}
}

// ncu reports a kernel with its return type and without its namespace,
// while a capture carries the demangled symbol with the namespace and no
// return type. Matching them is what tells "this kernel was measured" from
// "this kernel was silently skipped".
func TestSameKernelAcrossNcuAndCaptureSpelling(t *testing.T) {
	tests := []struct {
		name string
		ncu  string
		capt string
		want bool
	}{
		{
			name: "namespaced capture symbol vs ncu return-typed name",
			ncu:  "void gemv_single<__nv_bfloat16, 8, 4>(const T1 *, const T1 *, T1 *, int, int)",
			capt: "void mlx::core::cu::gemv_single<__nv_bfloat16, 8, 4>",
			want: true,
		},
		{
			name: "same base name, different template arguments still match",
			ncu:  "void rms_norm_small<__nv_bfloat16, 16, 16, 8>(const T1 *)",
			capt: "void mlx::core::cu::rms_norm_small<__nv_bfloat16, 128, 32, 8>",
			want: true,
		},
		{
			name: "different kernels do not match",
			ncu:  "void gemv_single<__nv_bfloat16, 8, 4>(const T1 *)",
			capt: "void mlx::core::cu::rms_norm_small<__nv_bfloat16, 16, 16, 8>",
			want: false,
		},
		{
			name: "bare names match",
			ncu:  "gemv_single",
			capt: "gemv_single",
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SameKernel(tt.ncu, tt.capt); got != tt.want {
				t.Errorf("SameKernel(%q, %q) = %v, want %v", tt.ncu, tt.capt, got, tt.want)
			}
		})
	}
}

// One kernel per invocation: ncu's --launch-count is a budget over every
// launch matching the filter, so asking for several names in one run spends
// it all on whichever launches first.
func TestCommandFiltersOneKernelAtATime(t *testing.T) {
	cmd, err := Command(Options{Kernels: []string{"gemv_single"}, LaunchCount: 3, Path: "/usr/bin/true"}, []string{"prog"})
	if err != nil {
		t.Fatal(err)
	}
	var pattern string
	for i, a := range cmd.Args {
		if a == "--kernel-name" && i+1 < len(cmd.Args) {
			pattern = cmd.Args[i+1]
		}
	}
	if pattern == "" {
		t.Fatal("no --kernel-name filter in the command")
	}
	if strings.Contains(pattern, "|") {
		t.Errorf("--kernel-name = %q, want a single kernel; a multi-kernel filter lets one crowd out the rest", pattern)
	}
}
