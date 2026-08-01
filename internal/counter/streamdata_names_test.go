package counter

import (
	"testing"

	"github.com/tmc/apple/x/plist"
)

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
