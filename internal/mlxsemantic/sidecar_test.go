package mlxsemantic

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	identity := Identity{UUID: "trace", ContentDigest: "sha256:abc"}
	valid := Sidecar{
		Schema: SchemaV1,
		Trace:  identity,
		Nodes: []Node{
			{ID: "run", Kind: "run", Name: "run"},
			{ID: "op", ParentID: "run", Kind: "operation", Name: "matmul"},
		},
		Links: []Link{{ID: "link", SemanticID: "op", Target: Target{Kind: "dispatch", Index: 1}}},
	}
	if err := valid.Validate(identity, map[string]int{"dispatch": 2}); err != nil {
		t.Fatal(err)
	}
	report, err := valid.Analyze(identity, map[string]int{"dispatch": 2, "encoder": 3})
	if err != nil {
		t.Fatal(err)
	}
	if report.Nodes != 2 || report.UsedNodes != 2 || report.UnusedNodes != 0 {
		t.Fatalf("node coverage = %+v, want two used nodes", report)
	}
	if report.MatchedTargets["dispatch"] != 1 || report.UnmatchedTargets["dispatch"] != 1 || report.UnmatchedTargets["encoder"] != 3 {
		t.Fatalf("target coverage = %+v", report)
	}
	withUnused := valid
	withUnused.Nodes = append(append([]Node(nil), valid.Nodes...), Node{ID: "unused", Kind: "operation", Name: "unused"})
	report, err = withUnused.Analyze(identity, map[string]int{"dispatch": 2})
	if err != nil {
		t.Fatal(err)
	}
	if report.UnusedNodes != 1 {
		t.Fatalf("unused nodes = %d, want 1", report.UnusedNodes)
	}

	tests := []struct {
		name string
		edit func(*Sidecar)
		want string
	}{
		{"schema", func(s *Sidecar) { s.Schema = "v2" }, "unsupported schema"},
		{"missing identity", func(s *Sidecar) { s.Trace.UUID = "" }, "UUID and content digest are required"},
		{"uuid", func(s *Sidecar) { s.Trace.UUID = "other" }, "does not match"},
		{"digest", func(s *Sidecar) { s.Trace.ContentDigest = "sha256:no" }, "digest does not match"},
		{"duplicate node", func(s *Sidecar) { s.Nodes = append(s.Nodes, s.Nodes[0]) }, "duplicate node"},
		{"parent", func(s *Sidecar) { s.Nodes[1].ParentID = "missing" }, "unknown parent"},
		{"cycle", func(s *Sidecar) { s.Nodes[0].ParentID = "op" }, "hierarchy cycle"},
		{"duplicate link", func(s *Sidecar) { s.Links = append(s.Links, s.Links[0]) }, "duplicate link"},
		{"unknown semantic node", func(s *Sidecar) { s.Links[0].SemanticID = "missing" }, "unknown semantic node"},
		{"unsupported target", func(s *Sidecar) { s.Links[0].Target.Kind = "native_label" }, "unsupported target kind"},
		{"negative target", func(s *Sidecar) { s.Links[0].Target.Index = -1 }, "out of range"},
		{"target", func(s *Sidecar) { s.Links[0].Target.Index = 2 }, "out of range"},
		{"ambiguous", func(s *Sidecar) {
			s.Nodes = append(s.Nodes, Node{ID: "other", Kind: "operation", Name: "other"})
			s.Links = append(s.Links, Link{ID: "other-link", SemanticID: "other", Target: Target{Kind: "dispatch", Index: 1}})
		}, "is ambiguous"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := valid
			got.Nodes = append([]Node(nil), valid.Nodes...)
			got.Links = append([]Link(nil), valid.Links...)
			test.edit(&got)
			if err := got.Validate(identity, map[string]int{"dispatch": 2}); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestDigestStable(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "b"), []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := Digest(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Digest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || !strings.HasPrefix(first, "sha256:") {
		t.Fatalf("digests = %q, %q", first, second)
	}
}

func TestReadRejectsSemanticReceiptWithoutTraceLinks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipt.json")
	if err := os.WriteFile(path, []byte(`{"runtime":{"version":"0.31.1"},"receipt":{"schema_version":1}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Read(path)
	if err == nil || !strings.Contains(err.Error(), "semantic receipt is not attachable without trace identity and explicit GPU target links") {
		t.Fatalf("Read error = %v", err)
	}
}
