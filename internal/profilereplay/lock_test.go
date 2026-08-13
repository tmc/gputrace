package profilereplay

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFlockWaitsOrRefuses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "replay.lock")
	first, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if err := flock(context.Background(), first, false); err != nil {
		t.Fatal(err)
	}

	second, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err := flock(context.Background(), second, false); !errors.Is(err, ErrReplayerBusy) {
		t.Fatalf("non-waiting lock error = %v, want ErrReplayerBusy", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := flock(ctx, second, true); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting lock error = %v, want deadline", err)
	}
}
