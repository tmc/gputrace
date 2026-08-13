// Package perfettoviewer serves a local Perfetto UI and trace.
package perfettoviewer

import (
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Config configures a viewer handler.
type Config struct {
	TracePath string
	UIPath    string
	RemoteUI  bool
	Title     string
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
	if config.UIPath != "" {
		info, err := os.Stat(config.UIPath)
		if err != nil {
			return nil, fmt.Errorf("create Perfetto viewer: %w", err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("create Perfetto viewer: UI path is not a directory")
		}
	}
	if config.Title == "" {
		config.Title = filepath.Base(config.TracePath)
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
			Title string
			UIURL string
		}{config.Title, uiURL(config)})
	})
	return mux, nil
}

func uiURL(config Config) string {
	if config.RemoteUI {
		return "https://ui.perfetto.dev/#!/?mode=embedded"
	}
	return "/ui/#!/?mode=embedded"
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
<html><head><meta charset="utf-8"><title>{{.Title}}</title>
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
