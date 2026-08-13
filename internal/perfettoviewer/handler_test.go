package perfettoviewer

import (
	"encoding/json"
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
		if test.path == "/" {
			for _, want := range []string{
				`name="gputrace-perfetto-ui" content="https://ui.perfetto.dev (mutable)"`,
				`postMessage("PING", "*")`,
				`event.data !== "PONG"`,
				`fetch("/trace", {cache: "no-store"})`,
				`localOnly: true`,
			} {
				if !strings.Contains(string(body), want) {
					t.Errorf("host page missing %q", want)
				}
			}
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
	writeUIManifest(t, ui, UIManifest{Schema: UIManifestSchema, Revision: "perfetto-test-revision"})
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
	host := httptest.NewRecorder()
	handler.ServeHTTP(host, httptest.NewRequest("GET", "/", nil))
	if !strings.Contains(host.Body.String(), `name="gputrace-perfetto-ui" content="perfetto-test-revision"`) {
		t.Fatalf("host page does not identify pinned UI revision: %q", host.Body.String())
	}
	if err := os.Mkdir(filepath.Join(ui, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	listing := httptest.NewRecorder()
	handler.ServeHTTP(listing, httptest.NewRequest("GET", "/ui/assets/", nil))
	if listing.Code != http.StatusNotFound {
		t.Fatalf("directory listing response = %d, want 404", listing.Code)
	}
}

func TestReadUIManifest(t *testing.T) {
	for _, test := range []struct {
		name     string
		index    bool
		manifest *UIManifest
		want     string
	}{
		{name: "missing index", manifest: &UIManifest{Schema: UIManifestSchema, Revision: "r1"}, want: "index.html"},
		{name: "missing manifest", index: true, want: UIManifestName},
		{name: "wrong schema", index: true, manifest: &UIManifest{Schema: "v0", Revision: "r1"}, want: "schema"},
		{name: "missing revision", index: true, manifest: &UIManifest{Schema: UIManifestSchema}, want: "revision is required"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			if test.index {
				if err := os.WriteFile(filepath.Join(dir, "index.html"), nil, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if test.manifest != nil {
				writeUIManifest(t, dir, *test.manifest)
			}
			if _, err := ReadUIManifest(dir); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ReadUIManifest error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestHandlerRefusesChangedUIRevision(t *testing.T) {
	dir := t.TempDir()
	trace := filepath.Join(dir, "trace.pftrace")
	ui := filepath.Join(dir, "ui")
	if err := os.Mkdir(ui, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(trace, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ui, "index.html"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	writeUIManifest(t, ui, UIManifest{Schema: UIManifestSchema, Revision: "new"})
	_, err := NewHandler(Config{TracePath: trace, UIPath: ui, UIRevision: "old"})
	if err == nil || !strings.Contains(err.Error(), `UI revision changed from "old" to "new"`) {
		t.Fatalf("NewHandler error = %v", err)
	}
}

func writeUIManifest(t *testing.T, dir string, manifest UIManifest) {
	t.Helper()
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, UIManifestName), data, 0o644); err != nil {
		t.Fatal(err)
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
