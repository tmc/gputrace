package capture

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEligible(t *testing.T) {
	python := homebrewPython()
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"homebrew python3", python, true},
		{"Xcode", "/Applications/Xcode.app/Contents/MacOS/Xcode", false},
		// Chess has flags=0x0(none), so checking code-directory flags alone
		// wrongly accepts it. Its platform identifier is the disqualifier.
		{"Chess", "/System/Applications/Chess.app/Contents/MacOS/Chess", false},
		{"ls", "/bin/ls", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.input == "" {
				t.Skip("Homebrew python3 is not installed")
			}
			if _, err := os.Stat(tt.input); err != nil {
				if errors.Is(err, os.ErrNotExist) {
					t.Skipf("fixture %s is not installed", tt.input)
				}
				t.Fatalf("stat fixture: %v", err)
			}
			err := Eligible(tt.input)
			got := err == nil
			if got != tt.want {
				t.Fatalf("Eligible(%q) eligible = %v, want %v; error = %v", tt.input, got, tt.want, err)
			}
			if !tt.want && !errors.Is(err, ErrNotInterposable) {
				t.Fatalf("Eligible(%q) error = %v, want ErrNotInterposable", tt.input, err)
			}
		})
	}
}

func TestRecorded(t *testing.T) {
	tests := []struct {
		name  string
		input bool
		want  bool
	}{
		{"missing unsorted-capture", false, false},
		{"has unsorted-capture", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bundle := filepath.Join(t.TempDir(), "test.gputrace")
			if err := os.Mkdir(bundle, 0o755); err != nil {
				t.Fatal(err)
			}
			if tt.input {
				if err := os.WriteFile(filepath.Join(bundle, "unsorted-capture"), []byte("capture"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			got := recorded(bundle) == nil
			if got != tt.want {
				t.Fatalf("recorded(%q) = %v, want %v", bundle, got, tt.want)
			}
		})
	}
}

func homebrewPython() string {
	var candidates []string
	if path, err := exec.LookPath("python3"); err == nil {
		candidates = append(candidates, path)
	}
	if prefix := os.Getenv("HOMEBREW_PREFIX"); prefix != "" {
		candidates = append(candidates, filepath.Join(prefix, "bin", "python3"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".local", "homebrew", "bin", "python3"))
	}
	candidates = append(candidates, "/opt/homebrew/bin/python3", "/usr/local/bin/python3")
	for _, path := range candidates {
		resolved, err := filepath.EvalSymlinks(path)
		if err == nil && strings.Contains(resolved, "/Cellar/python") {
			return path
		}
	}
	return ""
}

func TestLockHolderDistinguishesLiveFromStale(t *testing.T) {
	// A leftover lock and a live one need opposite responses, so the message
	// has to tell them apart rather than just reporting that a file exists.
	dir := t.TempDir()
	tests := []struct {
		name string
		body string
		want string
	}{
		{"live", fmt.Sprintf("%d\t/usr/bin/live\n", os.Getpid()), "still running"},
		{"stale", "999999\t/usr/bin/dead\n", "is gone; remove it"},
		{"truncated", "\n", "names no process"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lock := filepath.Join(dir, tt.name+".capture-lock")
			if err := os.WriteFile(lock, []byte(tt.body), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := lockHolder(lock); !strings.Contains(got, tt.want) {
				t.Errorf("lockHolder(%q) = %q, want it to contain %q", tt.body, got, tt.want)
			}
		})
	}
	if got := lockHolder(filepath.Join(dir, "absent")); !strings.Contains(got, "cannot read it") {
		t.Errorf("lockHolder(missing) = %q, want a read error", got)
	}
}
