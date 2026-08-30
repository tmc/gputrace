package cmd

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/google/pprof/profile"
)

// writeBundle creates a minimal .gpucapture bundle holding the given JSONL
// records, so the CLI path is exercised end to end without a GPU.
func writeBundle(t *testing.T, records string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "cap.gpucapture")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(records), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// spanBundle is a capture whose spans are on the unix clock and whose
// kernels are on the CUPTI clock, joined only by the clock_sync record —
// the arrangement a real MLX capture with GPUTRACE_APP_EVENTS produces.
const spanBundleRecords = `{"kind":"clock_sync","unix_ns":1000000000,"cupti_ns":5000000000}
{"kind":"span","name":"prefill","start_ns":1000000100,"end_ns":1000000400,"clock":"unix","labels":{"phase":"prefill"}}
{"kind":"span","name":"decode","start_ns":1000000400,"end_ns":1000000900,"clock":"unix","labels":{"phase":"decode"}}
{"kind":"span","name":"token","start_ns":1000000400,"end_ns":1000000600,"clock":"unix","eval_seq":1,"labels":{"phase":"decode"}}
{"kind":"kernel","raw_symbol":"_Z5saxpyifPfS_","start_ns":5000000150,"end_ns":5000000250,"stream_id":7,"queued_ns":5000000100,"submitted_ns":5000000120}
{"kind":"kernel","raw_symbol":"_Z4gemvifPfS_","start_ns":5000000450,"end_ns":5000000500,"stream_id":7}
{"kind":"kernel","raw_symbol":"_Z5loosev","start_ns":5000002000,"end_ns":5000002100,"stream_id":7}
`

func runPprofCmd(t *testing.T, args ...string) error {
	t.Helper()
	resetPprofTestFlags()
	t.Cleanup(resetPprofTestFlags)
	rootCmd.SetArgs(args)
	return rootCmd.Execute()
}

func parseProfile(t *testing.T, path string) *profile.Profile {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open profile: %v", err)
	}
	defer f.Close()
	p, err := profile.Parse(f)
	if err != nil {
		t.Fatalf("parse profile: %v", err)
	}
	return p
}

func profileSampleTypes(p *profile.Profile) []string {
	out := make([]string, len(p.SampleType))
	for i, st := range p.SampleType {
		out[i] = st.Type
	}
	return out
}

// renderStack returns one sample's frames outermost first, the order a
// reader sees them in pprof output.
func renderStack(s *profile.Sample) []string {
	out := make([]string, 0, len(s.Location))
	for i := len(s.Location) - 1; i >= 0; i-- {
		out = append(out, s.Location[i].Line[0].Function.Name)
	}
	return out
}

// TestCuptiPprofEmptyCaptureIsAnError is acceptance criterion 2. A bundle
// that traced spans but flushed no kernel records must fail loudly: a
// valid empty profile parses, renders, and says nothing, which is how a
// missing in-process flush goes unnoticed.
func TestCuptiPprofEmptyCaptureIsAnError(t *testing.T) {
	bundle := writeBundle(t, `{"kind":"clock_sync","unix_ns":1,"cupti_ns":2}
{"kind":"span","name":"prefill","start_ns":10,"end_ns":20,"clock":"unix"}
`)
	out := filepath.Join(t.TempDir(), "empty.pb.gz")
	err := runPprofCmd(t, "pprof", bundle, "-o", out)
	if err == nil {
		t.Fatal("exporting a capture with no kernel records succeeded")
	}
	for _, want := range []string{"no kernel records", "flush"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name the likely cause (%q)", err, want)
		}
	}
	if _, statErr := os.Stat(out); statErr == nil {
		t.Error("a profile was written for an empty capture")
	}
}

// TestCuptiPprofRendersSpanStacks is acceptance criterion 4, asserted on
// the rendered stacks rather than on the exporter exiting zero. The spans
// arrive on the unix clock, so this also fails if clock_sync is skipped.
func TestCuptiPprofRendersSpanStacks(t *testing.T) {
	bundle := writeBundle(t, spanBundleRecords)
	out := filepath.Join(t.TempDir(), "spans.pb.gz")
	if err := runPprofCmd(t, "pprof", bundle, "-o", out); err != nil {
		t.Fatalf("pprof: %v", err)
	}
	p := parseProfile(t, out)

	if len(p.Sample) != 3 {
		t.Fatalf("samples = %d, want 3", len(p.Sample))
	}
	deep := 0
	stacks := map[string][]string{}
	for _, s := range p.Sample {
		frames := renderStack(s)
		stacks[frames[len(frames)-1]] = frames
		if len(frames) > 1 {
			deep++
		}
	}
	// Two of the three kernels fall inside a span; the third is outside
	// every span and must survive under the unattributed root.
	if deep != 3 {
		t.Errorf("%d of 3 samples have stack depth > 1, want 3", deep)
	}
	if got, want := stacks["saxpy"], []string{"prefill", "saxpy"}; !reflect.DeepEqual(got, want) {
		t.Errorf("prefill stack = %v, want %v", got, want)
	}
	// decode encloses token encloses the kernel: innermost last.
	if got, want := stacks["gemv"], []string{"decode", "token", "gemv"}; !reflect.DeepEqual(got, want) {
		t.Errorf("decode stack = %v, want %v", got, want)
	}
	if got, want := stacks["loose"], []string{"unattributed", "loose"}; !reflect.DeepEqual(got, want) {
		t.Errorf("unattributed stack = %v, want %v", got, want)
	}
}

// TestCuptiPprofDiffContract is acceptance criterion 3's precondition:
// pprof -diff_base needs identical sample type lists, and reports a
// confusing mismatch rather than a diff when they differ.
func TestCuptiPprofDiffContract(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.pb.gz")
	b := filepath.Join(dir, "b.pb.gz")
	if err := runPprofCmd(t, "pprof", writeBundle(t, spanBundleRecords), "-o", a); err != nil {
		t.Fatalf("pprof a: %v", err)
	}
	// A capture with no spans at all still has to produce the same value
	// vector, or the two sides cannot be diffed.
	noSpans := `{"kind":"kernel","raw_symbol":"_Z5saxpyifPfS_","start_ns":100,"end_ns":200,"stream_id":1}
`
	if err := runPprofCmd(t, "pprof", writeBundle(t, noSpans), "-o", b); err != nil {
		t.Fatalf("pprof b: %v", err)
	}

	pa, pb := parseProfile(t, a), parseProfile(t, b)
	want := []string{"gpu_time", "launch_count", "queue_delay", "idle_after"}
	if got := profileSampleTypes(pa); !reflect.DeepEqual(got, want) {
		t.Errorf("a sample types = %v, want %v", got, want)
	}
	if got := profileSampleTypes(pb); !reflect.DeepEqual(got, want) {
		t.Errorf("b sample types = %v, want %v", got, want)
	}
	// The operation pprof -diff_base itself performs, which is where an
	// incompatible pair fails with a message about the wrong thing.
	if _, err := profile.Merge([]*profile.Profile{pa, pb}); err != nil {
		t.Errorf("profiles are not diffable: %v", err)
	}
}

// TestCuptiPprofDemanglesAndKeepsTheSymbol checks the name pprof displays
// and the symbol the capture reported are both present: one is readable,
// the other is what a disassembler or nsys report can be matched against.
func TestCuptiPprofDemanglesAndKeepsTheSymbol(t *testing.T) {
	bundle := writeBundle(t, spanBundleRecords)
	out := filepath.Join(t.TempDir(), "names.pb.gz")
	if err := runPprofCmd(t, "pprof", bundle, "-o", out); err != nil {
		t.Fatalf("pprof: %v", err)
	}
	p := parseProfile(t, out)
	for _, f := range p.Function {
		if f.Name != "saxpy" {
			continue
		}
		if f.SystemName != "_Z5saxpyifPfS_" {
			t.Errorf("SystemName = %q, want the mangled symbol", f.SystemName)
		}
		return
	}
	// c++filt may be unavailable, in which case the name stays mangled and
	// there is nothing to check.
	t.Skip("c++filt did not demangle _Z5saxpyifPfS_; nothing to assert")
}

// TestDotPprofMatchesTheHandCountedDump is acceptance criterion 5 against
// the fixture the cudagraphdot tests count by hand: eleven kernels in the
// root graph plus one reached through two levels of child graph.
func TestDotPprofMatchesTheHandCountedDump(t *testing.T) {
	dump := "../../../internal/cudagraphdot/testdata/nested_graph.dot"
	if _, err := os.Stat(dump); err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	out := filepath.Join(t.TempDir(), "structure.pb.gz")
	resetDotPprofTestFlags()
	t.Cleanup(resetDotPprofTestFlags)
	rootCmd.SetArgs([]string{"dot-pprof", dump, "-o", out})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("dot-pprof: %v", err)
	}
	p := parseProfile(t, out)
	if got := profileSampleTypes(p); !reflect.DeepEqual(got, []string{"kernel_count", "graph_commits"}) {
		t.Errorf("sample types = %v", got)
	}
	var kernels, commits int64
	for _, s := range p.Sample {
		kernels += s.Value[0]
		commits += s.Value[1]
	}
	if kernels != 12 {
		t.Errorf("kernel_count = %d, want 12 (dotdepth.py reports 12 for this dump)", kernels)
	}
	if commits != 1 {
		t.Errorf("graph_commits = %d, want 1", commits)
	}
	// The grandchild kernel must arrive with the whole descent on its
	// stack, not flattened onto the root.
	var deepest []string
	for _, s := range p.Sample {
		if frames := renderStack(s); len(frames) > len(deepest) {
			deepest = frames
		}
	}
	want := []string{"graph_130", "graph_131", "graph_132"}
	if len(deepest) != 4 || !reflect.DeepEqual(deepest[:3], want) {
		t.Errorf("deepest stack = %v, want %v then a kernel", deepest, want)
	}
}

func TestPprofDotRejectedForMetalTraces(t *testing.T) {
	tracePath := "../../../testdata/traces/01-single-encoder/01-single-encoder-run1.gputrace"
	if _, err := os.Stat(tracePath); os.IsNotExist(err) {
		t.Skipf("trace fixture not found: %s", tracePath)
	}
	err := runPprofCmd(t, "pprof", tracePath, "--dot", t.TempDir(), "-o", filepath.Join(t.TempDir(), "x.pprof"))
	if err == nil {
		t.Fatal("--dot was accepted for a Metal trace")
	}
	if !strings.Contains(err.Error(), "CUDA") {
		t.Errorf("error %q should explain --dot is for CUDA captures", err)
	}
}

func resetDotPprofTestFlags() {
	_ = dotPprofCmd.Flags().Set("output", "")
}
