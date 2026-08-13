package perfettoviewer

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandlerRemote(t *testing.T) {
	trace := filepath.Join(t.TempDir(), "trace.pftrace")
	if err := os.WriteFile(trace, []byte("trace"), 0o644); err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(Config{TracePath: trace, RemoteUI: true, Title: "MLX trace"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	for _, test := range []struct {
		path, contentType, contains string
	}{
		{"/", "text/html", "https://ui.perfetto.dev/#!/?mode=embedded"},
		{"/trace", "application/octet-stream", "trace"},
		{"/healthz", "text/plain", "ok"},
	} {
		response, err := http.Get(server.URL + test.path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if !strings.HasPrefix(response.Header.Get("Content-Type"), test.contentType) || !strings.Contains(string(body), test.contains) {
			t.Fatalf("GET %s: type %q body %q", test.path, response.Header.Get("Content-Type"), body)
		}
		if test.path == "/trace" && response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("trace Cache-Control = %q", response.Header.Get("Cache-Control"))
		}
	}
}

func TestHandlerLocalUI(t *testing.T) {
	dir := t.TempDir()
	trace := filepath.Join(dir, "trace.pftrace")
	ui := filepath.Join(dir, "ui")
	if err := os.Mkdir(ui, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(trace, []byte("trace"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ui, "index.html"), []byte("perfetto ui"), 0o644); err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(Config{TracePath: trace, UIPath: ui})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "/ui/", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "perfetto ui") {
		t.Fatalf("local UI response = %d %q", response.Code, response.Body.String())
	}
}

func TestHandlerRequiresOneUI(t *testing.T) {
	if _, err := NewHandler(Config{TracePath: "trace"}); err == nil {
		t.Fatal("missing UI mode was accepted")
	}
	if _, err := NewHandler(Config{TracePath: "trace", UIPath: "ui", RemoteUI: true}); err == nil {
		t.Fatal("two UI modes were accepted")
	}
}

func TestUIURLNavigation(t *testing.T) {
	got := uiURL(Config{RemoteUI: true, Navigation: &Navigation{
		ViewStartNS: 10, ViewEndNS: 40, SelectionStartNS: 20, SelectionDurNS: 5, HasSelection: true,
	}})
	for _, want := range []string{"mode=embedded", "visStart=10", "visEnd=40", "ts=20", "dur=5"} {
		if !strings.Contains(got, want) {
			t.Errorf("uiURL = %q, missing %q", got, want)
		}
	}
}
