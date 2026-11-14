package web

import (
	"embed"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
)

//go:embed all:static
var embeddedAssets embed.FS

// StaticFS returns an fs.FS rooted at the static directory, preferring STATIC_DIR on disk when set.
func StaticFS() fs.FS {
	if override := strings.TrimSpace(os.Getenv("STATIC_DIR")); override != "" {
		return os.DirFS(override)
	}
	sub, err := fs.Sub(embeddedAssets, "static")
	if err != nil {
		return embeddedAssets
	}
	return sub
}

func serveFirstFile(w http.ResponseWriter, r *http.Request, fsys fs.FS, contentType string, candidates ...string) {
	for _, p := range candidates {
		data, err := fs.ReadFile(fsys, p)
		if err == nil {
			if contentType != "" {
				w.Header().Set("Content-Type", contentType)
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(data)
			return
		}
	}
	http.NotFound(w, r)
}

// RegisterRoutes mounts the PWA static UI routes onto mux with the same paths as FastAPI.
func RegisterRoutes(mux *http.ServeMux, fsys fs.FS) {
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		serveFirstFile(w, r, fsys, "text/html; charset=utf-8", "dist/index.html", "index.html")
	})

	mux.HandleFunc("GET /manifest.json", func(w http.ResponseWriter, r *http.Request) {
		serveFirstFile(w, r, fsys, "application/manifest+json", "dist/manifest.json", "manifest.json")
	})

	mux.HandleFunc("GET /sw.js", func(w http.ResponseWriter, r *http.Request) {
		serveFirstFile(w, r, fsys, "application/javascript", "dist/sw.js", "sw.js")
	})

	if distAssets, err := fs.Sub(fsys, path.Clean("dist/assets")); err == nil {
		mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServerFS(distAssets)))
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(fsys)))
}
