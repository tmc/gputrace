// Package mlxsemantic validates semantic attribution for MLX GPU traces.
package mlxsemantic

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const SchemaV1 = "gputrace.mlx-semantics/v1"

// Sidecar carries application semantics without inventing GPU timing or
// ownership relationships.
type Sidecar struct {
	Schema   string   `json:"schema"`
	Trace    Identity `json:"trace"`
	Producer Producer `json:"producer"`
	Nodes    []Node   `json:"nodes"`
	Links    []Link   `json:"links"`
}

type Identity struct {
	UUID          string `json:"uuid"`
	ContentDigest string `json:"content_digest"`
}

type Producer struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type Node struct {
	ID       string         `json:"id"`
	ParentID string         `json:"parent_id,omitempty"`
	Kind     string         `json:"kind"`
	Name     string         `json:"name"`
	Attrs    map[string]any `json:"attrs,omitempty"`
}

type Link struct {
	ID         string `json:"id"`
	SemanticID string `json:"semantic_id"`
	Target     Target `json:"target"`
}

type Target struct {
	Kind  string `json:"kind"`
	Index int    `json:"index"`
}

// Read reads one JSON sidecar and rejects trailing data.
func Read(path string) (*Sidecar, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read MLX sidecar: %w", err)
	}
	defer f.Close()
	decoder := json.NewDecoder(f)
	decoder.DisallowUnknownFields()
	var sidecar Sidecar
	if err := decoder.Decode(&sidecar); err != nil {
		return nil, fmt.Errorf("read MLX sidecar: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("multiple JSON values")
		}
		return nil, fmt.Errorf("read MLX sidecar: %w", err)
	}
	return &sidecar, nil
}

// Validate checks schema, trace identity, hierarchy, and target references.
func (s *Sidecar) Validate(identity Identity, targetCounts map[string]int) error {
	if s == nil {
		return fmt.Errorf("validate MLX sidecar: nil sidecar")
	}
	if s.Schema != SchemaV1 {
		return fmt.Errorf("validate MLX sidecar: unsupported schema %q", s.Schema)
	}
	if s.Trace.UUID == "" || s.Trace.ContentDigest == "" {
		return fmt.Errorf("validate MLX sidecar: trace UUID and content digest are required")
	}
	if s.Trace.UUID != identity.UUID {
		return fmt.Errorf("validate MLX sidecar: trace UUID %q does not match %q", s.Trace.UUID, identity.UUID)
	}
	if s.Trace.ContentDigest != identity.ContentDigest {
		return fmt.Errorf("validate MLX sidecar: trace content digest does not match")
	}

	nodes := make(map[string]Node)
	for _, node := range s.Nodes {
		if node.ID == "" || node.Kind == "" || node.Name == "" {
			return fmt.Errorf("validate MLX sidecar: node id, kind, and name are required")
		}
		if _, ok := nodes[node.ID]; ok {
			return fmt.Errorf("validate MLX sidecar: duplicate node %q", node.ID)
		}
		nodes[node.ID] = node
	}
	for _, node := range s.Nodes {
		if node.ParentID != "" {
			if _, ok := nodes[node.ParentID]; !ok {
				return fmt.Errorf("validate MLX sidecar: node %q has unknown parent %q", node.ID, node.ParentID)
			}
		}
		for parent, seen := node.ParentID, map[string]bool{node.ID: true}; parent != ""; {
			if seen[parent] {
				return fmt.Errorf("validate MLX sidecar: hierarchy cycle at %q", parent)
			}
			seen[parent] = true
			parent = nodes[parent].ParentID
		}
	}

	links := make(map[string]bool)
	targets := make(map[Target]string)
	for _, link := range s.Links {
		if link.ID == "" {
			return fmt.Errorf("validate MLX sidecar: link id is required")
		}
		if links[link.ID] {
			return fmt.Errorf("validate MLX sidecar: duplicate link %q", link.ID)
		}
		links[link.ID] = true
		if _, ok := nodes[link.SemanticID]; !ok {
			return fmt.Errorf("validate MLX sidecar: link %q has unknown semantic node %q", link.ID, link.SemanticID)
		}
		count, ok := targetCounts[link.Target.Kind]
		if !ok {
			return fmt.Errorf("validate MLX sidecar: link %q has unsupported target kind %q", link.ID, link.Target.Kind)
		}
		if link.Target.Index < 0 || link.Target.Index >= count {
			return fmt.Errorf("validate MLX sidecar: link %q target %s index %d is out of range", link.ID, link.Target.Kind, link.Target.Index)
		}
		if previous, ok := targets[link.Target]; ok && previous != link.SemanticID {
			return fmt.Errorf("validate MLX sidecar: target %s index %d is ambiguous between %q and %q", link.Target.Kind, link.Target.Index, previous, link.SemanticID)
		}
		targets[link.Target] = link.SemanticID
	}
	return nil
}

// Digest computes a stable SHA-256 identity for a file or directory tree.
// Directory entries are ordered by slash-separated relative path.
func Digest(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("digest trace: %w", err)
	}
	h := sha256.New()
	if !info.IsDir() {
		if err := hashFile(h, path, filepath.Base(path)); err != nil {
			return "", err
		}
		return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
	}
	var paths []string
	err = filepath.WalkDir(path, func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(path, current)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("digest trace: %w", err)
	}
	sort.Strings(paths)
	for _, rel := range paths {
		if err := hashFile(h, filepath.Join(path, filepath.FromSlash(rel)), rel); err != nil {
			return "", err
		}
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

func hashFile(w io.Writer, path, name string) error {
	if strings.ContainsRune(name, 0) {
		return fmt.Errorf("digest trace: invalid path %q", name)
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("digest trace: %w", err)
	}
	defer f.Close()
	if _, err := io.WriteString(w, name+"\x00"); err != nil {
		return fmt.Errorf("digest trace: %w", err)
	}
	if _, err := io.Copy(w, f); err != nil {
		return fmt.Errorf("digest trace: %w", err)
	}
	return nil
}
