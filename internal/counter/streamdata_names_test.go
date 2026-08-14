package counter

import (
	"slices"
	"testing"

	"github.com/tmc/apple/x/plist"
)

func TestExtractFunctionNamesPreservesArrayShape(t *testing.T) {
	tests := []struct {
		name    string
		objects []any
		root    map[string]any
		want    []string
	}{
		{name: "absent", objects: []any{"$null"}, root: map[string]any{}, want: nil},
		{name: "empty", objects: []any{"$null", map[string]any{"NS.objects": []any{}}}, root: map[string]any{"strings": plist.UID(1)}, want: []string{}},
		{name: "ordered", objects: []any{"$null", map[string]any{"NS.objects": []any{plist.UID(2), plist.UID(3), plist.UID(4)}}, "kernel", "", "/source.metal"}, root: map[string]any{"strings": plist.UID(1)}, want: []string{"kernel", "", "/source.metal"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := extractFunctionNames(test.objects, test.root)
			if !slices.Equal(got, test.want) || (got == nil) != (test.want == nil) {
				t.Fatalf("strings = %#v, want %#v", got, test.want)
			}
		})
	}
}

// compilerNamesArchive builds the $objects slice of an archive whose
// pipelinePerformanceStatistics dictionary carries a "Compile Performance"
// entry per pipeline ID, mirroring the real streamData layout.
func compilerNamesArchive(names map[int]string) []any {
	objects := []any{"$null"}
	add := func(v any) plist.UID {
		objects = append(objects, v)
		return plist.UID(len(objects) - 1)
	}

	var keys, values []any
	for id, name := range names {
		compile := add(map[string]any{
			"NS.keys":    []any{add("Function Name")},
			"NS.objects": []any{add(name)},
		})
		stats := add(map[string]any{
			"NS.keys":    []any{add("Compile Performance")},
			"NS.objects": []any{compile},
		})
		keys = append(keys, add(int64(id)))
		values = append(values, stats)
	}

	objects = append(objects, map[string]any{"NS.keys": keys, "NS.objects": values})
	return objects
}

func TestCompilerFunctionNames(t *testing.T) {
	want := map[int]string{451: "rope_single_bfloat16_", 452: "sdpa_vector"}
	objects := compilerNamesArchive(map[int]string{451: "rope_single_bfloat16_", 452: "sdpa_vector", 787: ""})

	got := compilerFunctionNames(objects, len(objects)-1)
	if len(got) != len(want) {
		t.Fatalf("got %d names, want %d: %v", len(got), len(want), got)
	}
	for id, name := range want {
		if got[id] != name {
			t.Errorf("pipeline %d: got %q, want %q", id, got[id], name)
		}
	}
}

func TestCompilerFunctionNamesMissing(t *testing.T) {
	if got := compilerFunctionNames(nil, 0); got != nil {
		t.Errorf("got %v, want nil", got)
	}
	objects := []any{"$null", "not a dictionary"}
	if got := compilerFunctionNames(objects, 1); got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestApplyCompilerNames(t *testing.T) {
	compilerNames := map[int]string{451: "rope_single_bfloat16_", 452: "sdpa_vector"}

	tests := []struct {
		name string
		info pipelineInfo
		want string
	}{
		{
			name: "strings wins",
			info: pipelineInfo{ID: 451, FunctionName: "from_strings"},
			want: "from_strings",
		},
		{
			name: "compiler name fills empty strings entry",
			info: pipelineInfo{ID: 452},
			want: "sdpa_vector",
		},
		{
			name: "both empty stays unnamed",
			info: pipelineInfo{ID: 787},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			infos := []pipelineInfo{tt.info}
			applyCompilerNames(infos, compilerNames)
			if infos[0].FunctionName != tt.want {
				t.Errorf("got %q, want %q", infos[0].FunctionName, tt.want)
			}
		})
	}
}

// TestDisplayNamePlaceholder documents that a pipeline neither source can name
// still renders as a bracketed placeholder, never as a bare identifier.
func TestDisplayNamePlaceholder(t *testing.T) {
	d := DispatchInfo{PipelineIndex: 3, PipelineID: 787}
	if got := d.DisplayName(); got != "(pipeline_787)" {
		t.Errorf("got %q, want %q", got, "(pipeline_787)")
	}
}
