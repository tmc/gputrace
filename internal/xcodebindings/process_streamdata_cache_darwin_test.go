//go:build darwin

package xcodebindings

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// seedModelCache installs a completed entry for path and returns it. It fails
// the test if path cannot be keyed, since that would silently disable caching.
func seedModelCache(t *testing.T, path string, model ProcessedStreamData) {
	t.Helper()
	key, ok := modelCacheKey(path)
	if !ok {
		t.Fatalf("modelCacheKey(%q) not cacheable", path)
	}
	entry := &processedModel{done: make(chan struct{}), model: model}
	close(entry.done)

	modelCacheMu.Lock()
	modelCache[key] = entry
	modelCacheMu.Unlock()
	t.Cleanup(func() {
		modelCacheMu.Lock()
		delete(modelCache, key)
		modelCacheMu.Unlock()
	})
}

func tempArchive(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "streamData")
	if err := os.WriteFile(path, []byte("archive"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// A cached archive must not re-enter the private framework.
func TestWithProcessedModelUsesCache(t *testing.T) {
	path := tempArchive(t)
	seedModelCache(t, path, ProcessedStreamData{Path: path, DrawCount: 7})

	var got ProcessedStreamData
	if err := WithProcessedModel(context.Background(), path, func(m *ProcessedStreamData) error {
		got = *m
		return nil
	}); err != nil {
		t.Fatalf("WithProcessedModel: %v", err)
	}
	if got.DrawCount != 7 {
		t.Errorf("DrawCount = %d, want 7 (cached model)", got.DrawCount)
	}
}

// Rewriting the archive must invalidate the cached entry rather than serve a
// stale model. The rebuild is expected to fail here, since the fake archive is
// not a real one; what matters is that the cached value is not returned.
func TestModelCacheKeyChangesWithContent(t *testing.T) {
	path := tempArchive(t)
	before, ok := modelCacheKey(path)
	if !ok {
		t.Fatal("not cacheable")
	}

	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(path, []byte("archive-modified"), 0o644); err != nil {
		t.Fatal(err)
	}
	after, ok := modelCacheKey(path)
	if !ok {
		t.Fatal("not cacheable after rewrite")
	}
	if before == after {
		t.Errorf("cache key unchanged after rewrite: %q", before)
	}
}

func TestModelCacheKeyMissingFile(t *testing.T) {
	if _, ok := modelCacheKey(filepath.Join(t.TempDir(), "absent")); ok {
		t.Error("modelCacheKey reported a missing file as cacheable")
	}
}

// A context canceled while a build is in flight must return promptly instead
// of blocking for the length of the disassembly.
func TestWithProcessedModelCancelDuringBuild(t *testing.T) {
	path := tempArchive(t)
	key, ok := modelCacheKey(path)
	if !ok {
		t.Fatal("not cacheable")
	}
	// An entry that never completes stands in for an in-flight build.
	entry := &processedModel{done: make(chan struct{})}
	modelCacheMu.Lock()
	modelCache[key] = entry
	modelCacheMu.Unlock()
	t.Cleanup(func() {
		modelCacheMu.Lock()
		delete(modelCache, key)
		modelCacheMu.Unlock()
	})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	done := make(chan error, 1)
	go func() {
		done <- WithProcessedModel(ctx, path, func(*ProcessedStreamData) error {
			t.Error("callback ran for a canceled context")
			return nil
		})
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("WithProcessedModel did not return after cancellation")
	}
}

func TestWithProcessedModelNilCallback(t *testing.T) {
	if err := WithProcessedModel(context.Background(), tempArchive(t), nil); err == nil {
		t.Error("expected an error for a nil callback")
	}
}
