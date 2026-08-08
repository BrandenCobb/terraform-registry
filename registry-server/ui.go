package main

import (
	_ "embed"
	"net/http"
	"strings"
)

//go:embed ui/index.html
var uiIndexHTML string

//go:embed ui/style.css
var uiStyleCSS string

//go:embed ui/app.js
var uiAppJS string

func uiHandler(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	switch {
	case path == "/ui" || path == "/ui/" || path == "/ui/index.html":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(uiIndexHTML))
	case strings.HasSuffix(path, "/ui/style.css"):
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		_, _ = w.Write([]byte(uiStyleCSS))
	case strings.HasSuffix(path, "/ui/app.js"):
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		_, _ = w.Write([]byte(uiAppJS))
	default:
		// Serve index.html for SPA routing
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(uiIndexHTML))
	}
}
