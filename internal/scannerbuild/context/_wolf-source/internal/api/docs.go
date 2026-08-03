package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/alphabravocompany/thewolf/internal/api/openapi"
)

// mountDocs registers the public OpenAPI spec and Swagger UI endpoints.
// They require no authentication — the spec only describes the API; every
// non-public endpoint still demands a credential. The UI assets are served
// from the binary's embedded bundle, so the pages work fully offline.
func mountDocs(r chi.Router) {
	r.Get("/openapi.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(openapi.SpecJSON())
	})
	r.Get("/docs", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(openapi.SwaggerUIHTML))
	})
	r.Get("/docs/redoc", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(openapi.RedocHTML))
	})
	// Embedded UI assets (CSS/JS) — no CDN dependency. The Content-Type is
	// set explicitly: the /api/v1 group's jsonContentType middleware has
	// already stamped these as application/json, and browsers refuse to
	// apply a stylesheet or run a script served with the wrong MIME type.
	assetServer := http.StripPrefix("/api/v1/docs/static/", http.FileServer(http.FS(openapi.AssetsSub())))
	r.Get("/docs/static/*", func(w http.ResponseWriter, req *http.Request) {
		switch {
		case strings.HasSuffix(req.URL.Path, ".css"):
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
		case strings.HasSuffix(req.URL.Path, ".js"):
			w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		}
		assetServer.ServeHTTP(w, req)
	})
}
