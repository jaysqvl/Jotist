package web

import (
	"bytes"
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

//go:embed dist/*
var staticFiles embed.FS

// SetupStaticRoutes serves the built frontend and falls back to its entry point
// for client-side routes. API authentication belongs to the API router.
func SetupStaticRoutes(router *gin.Engine) {
	dist, err := fs.Sub(staticFiles, "dist")
	if err != nil {
		panic("open embedded frontend: " + err.Error())
	}
	router.NoRoute(gin.WrapH(newSPAHandler(dist)))
}

func newSPAHandler(files fs.FS) http.Handler {
	assets := http.FileServer(http.FS(files))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"API endpoint not found"}`))
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), "/")
		if name != "" && !fs.ValidPath(name) {
			http.Error(w, "Invalid asset path", http.StatusForbidden)
			return
		}
		if info, err := fs.Stat(files, name); err == nil && !info.IsDir() {
			// The standard server owns MIME types, HEAD, and byte-range handling.
			// Some hosts do not register the PWA manifest extension.
			if path.Ext(name) == ".webmanifest" {
				w.Header().Set("Content-Type", "application/manifest+json")
			}
			assets.ServeHTTP(w, r)
			return
		}
		if name == "assets" || strings.HasPrefix(name, "assets/") || path.Ext(name) != "" {
			// Missing bundles and PWA files must not receive HTML with status 200.
			http.NotFound(w, r)
			return
		}

		index, err := fs.ReadFile(files, "index.html")
		if err != nil {
			http.Error(w, "Error loading page", http.StatusInternalServerError)
			return
		}
		http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(index))
	})
}
