// Package webui embeds the web app (built from web/ into dist/app/ by `make build`, docs/web-ui.md)
// and serves it: files by path, anything else falls back to index.html (the app routes itself).
// A binary built without the app (plain `go build`) serves a page that says so.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed dist
var dist embed.FS

const notBuilt = `<!doctype html><title>yastationd</title><p>This yastationd was built without its web app: build it with <code>make build</code> (docs/building.md). The API is at <code>/api/</code>.`

// Handler serves the app.
func Handler() http.Handler {
	root, _ := fs.Sub(dist, "dist/app")
	if _, err := fs.Stat(root, "index.html"); err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write([]byte(notBuilt))
		})
	}
	files := http.FileServer(http.FS(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(root, p); err != nil {
			r.URL.Path = "/" // a route of the app
		}
		if strings.HasPrefix(p, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable") // hashed names
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}
