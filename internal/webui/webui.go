// Package webui embeds the desktop/web client single-page application and
// serves it from the gateway's primary HTTP mux. The UI is authored as plain
// HTML/CSS/JS under dist/ and is compiled into the nexus-server binary, so a
// packaged desktop app has no external asset dependencies.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// Handler returns an http.Handler that serves the embedded single-page app
// with SPA-style fallback: any unknown path resolves to index.html.
func Handler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		// Cannot happen for a valid embed, but fail loudly rather than panic
		// at request time.
		panic(err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := strings.TrimPrefix(r.URL.Path, "/")
		if clean == "" {
			clean = "index.html"
		}
		if info, err := fs.Stat(sub, clean); err == nil && !info.IsDir() {
			http.ServeFileFS(w, r, sub, clean)
			return
		}
		// SPA fallback for client-side routes.
		http.ServeFileFS(w, r, sub, "index.html")
	})
}
