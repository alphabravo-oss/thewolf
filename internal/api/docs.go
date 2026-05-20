package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/alphabravocompany/thewolf/internal/api/openapi"
)

// mountDocs registers the public OpenAPI spec and Swagger UI endpoints.
// They require no authentication — the spec only describes the API; every
// non-public endpoint still demands a credential.
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
}
