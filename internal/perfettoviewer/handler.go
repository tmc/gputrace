// Package perfettoviewer serves a local Perfetto UI and trace.
package perfettoviewer

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	// UIManifestName is the manifest file required at the root of a local UI.
	UIManifestName = "perfetto-ui.json"
	// UIManifestSchema identifies the local UI manifest format.
	UIManifestSchema = "gputrace.perfetto-ui/v1"
)

// UIManifest identifies a pinned local Perfetto UI build.
type UIManifest struct {
	Schema   string `json:"schema"`
	Revision string `json:"revision"`
}

// Config configures a viewer handler.
type Config struct {
	TracePath  string
	UIPath     string
	UIRevision string
	RemoteUI   bool
	Title      string
	Navigation *Navigation
}

// ReadUIManifest validates a local Perfetto UI directory and returns its
// pinned upstream revision.
func ReadUIManifest(path string) (UIManifest, error) {
	info, err := os.Stat(path)
	if err != nil {
		return UIManifest{}, fmt.Errorf("read Perfetto UI manifest: %w", err)
	}
	if !info.IsDir() {
		return UIManifest{}, fmt.Errorf("read Perfetto UI manifest: UI path is not a directory")
	}
	index := filepath.Join(path, "index.html")
	if info, err := os.Stat(index); err != nil {
		return UIManifest{}, fmt.Errorf("read Perfetto UI manifest: index.html: %w", err)
	} else if info.IsDir() {
		return UIManifest{}, fmt.Errorf("read Perfetto UI manifest: index.html is not a file")
	}
	data, err := os.ReadFile(filepath.Join(path, UIManifestName))
	if err != nil {
		return UIManifest{}, fmt.Errorf("read Perfetto UI manifest: %w", err)
	}
	var manifest UIManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return UIManifest{}, fmt.Errorf("read Perfetto UI manifest: decode %s: %w", UIManifestName, err)
	}
	if manifest.Schema != UIManifestSchema {
		return UIManifest{}, fmt.Errorf("read Perfetto UI manifest: schema %q, want %q", manifest.Schema, UIManifestSchema)
	}
	if strings.TrimSpace(manifest.Revision) == "" {
		return UIManifest{}, fmt.Errorf("read Perfetto UI manifest: revision is required")
	}
	return manifest, nil
}

// Navigation selects an initial viewport and optional trace event. Values use
// nanoseconds, matching Perfetto's deep-link parameters.
type Navigation struct {
	ViewStartNS      uint64
	ViewEndNS        uint64
	SelectionStartNS uint64
	SelectionDurNS   uint64
	HasSelection     bool
}

// NewHandler returns the fixed viewer HTTP surface.
func NewHandler(config Config) (http.Handler, error) {
	if config.TracePath == "" {
		return nil, fmt.Errorf("create Perfetto viewer: trace path is required")
	}
	if config.RemoteUI == (config.UIPath != "") {
		return nil, fmt.Errorf("create Perfetto viewer: choose exactly one of local UI or remote UI")
	}
	if _, err := os.Stat(config.TracePath); err != nil {
		return nil, fmt.Errorf("create Perfetto viewer: %w", err)
	}
	uiIdentity := "https://ui.perfetto.dev (mutable)"
	if config.UIPath != "" {
		manifest, err := ReadUIManifest(config.UIPath)
		if err != nil {
			return nil, fmt.Errorf("create Perfetto viewer: %w", err)
		}
		if config.UIRevision != "" && config.UIRevision != manifest.Revision {
			return nil, fmt.Errorf("create Perfetto viewer: UI revision changed from %q to %q", config.UIRevision, manifest.Revision)
		}
		uiIdentity = manifest.Revision
	}
	if config.Title == "" {
		config.Title = filepath.Base(config.TracePath)
	}
	if config.Navigation != nil && config.Navigation.ViewEndNS <= config.Navigation.ViewStartNS {
		return nil, fmt.Errorf("create Perfetto viewer: navigation end must be after start")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /trace", func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Cache-Control", "no-store")
		http.ServeFile(w, request, config.TracePath)
	})
	if config.UIPath != "" {
		mux.Handle("GET /ui/", http.StripPrefix("/ui/", noListFileServer(config.UIPath)))
	}
	mux.HandleFunc("GET /", func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/" {
			http.NotFound(w, request)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = hostPage.Execute(w, struct {
			Title      string
			UIURL      string
			UIIdentity string
		}{config.Title, uiURL(config), uiIdentity})
	})
	return mux, nil
}

func uiURL(config Config) string {
	base := "/ui/"
	if config.RemoteUI {
		base = "https://ui.perfetto.dev/"
	}
	query := url.Values{"mode": {"embedded"}}
	if navigation := config.Navigation; navigation != nil {
		query.Set("visStart", strconv.FormatUint(navigation.ViewStartNS, 10))
		query.Set("visEnd", strconv.FormatUint(navigation.ViewEndNS, 10))
		if navigation.HasSelection {
			query.Set("ts", strconv.FormatUint(navigation.SelectionStartNS, 10))
			query.Set("dur", strconv.FormatUint(navigation.SelectionDurNS, 10))
		}
	}
	return base + "#!/?" + query.Encode()
}

func noListFileServer(root string) http.Handler {
	server := http.FileServer(http.Dir(root))
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		clean := filepath.Clean(request.URL.Path)
		if clean == "." || clean == "/" || strings.HasSuffix(request.URL.Path, "/") {
			index := filepath.Join(root, strings.TrimPrefix(clean, "/"), "index.html")
			if _, err := os.Stat(index); err != nil {
				http.NotFound(w, request)
				return
			}
		}
		server.ServeHTTP(w, request)
	})
}

var hostPage = template.Must(template.New("host").Parse(`<!doctype html>
<html><head><meta charset="utf-8"><meta name="gputrace-perfetto-ui" content="{{.UIIdentity}}"><title>{{.Title}}</title>
<style>html,body,iframe{border:0;height:100%;margin:0;width:100%}</style></head>
<body><iframe title="Perfetto" src="{{.UIURL}}"></iframe><script>
const frame = document.querySelector("iframe");
const timer = setInterval(() => frame.contentWindow.postMessage("PING", "*"), 100);
window.addEventListener("message", async event => {
  if (event.source !== frame.contentWindow || event.data !== "PONG") return;
  clearInterval(timer);
  const buffer = await fetch("/trace", {cache: "no-store"}).then(response => response.arrayBuffer());
  frame.contentWindow.postMessage({perfetto: {
    buffer, title: document.title, fileName: "gputrace.pftrace", localOnly: true
  }}, "*");
});
</script></body></html>`))
