package middleware

import (
	"net/http"

	"github.com/alphabravocompany/thewolf/internal/api/response"
)

// MaxBodySize limits request body size. Returns 413 if exceeded.
func MaxBodySize(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RecoverMaxBytesError wraps the Recoverer to also catch MaxBytesError panics
// and turn them into 413 responses.
func RecoverMaxBytesError(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				if err, ok := rec.(*http.MaxBytesError); ok {
					_ = err
					response.WriteError(w, http.StatusRequestEntityTooLarge,
						"payload_too_large", "request body exceeds size limit")
					return
				}
				panic(rec) // re-panic if not MaxBytesError
			}
		}()
		next.ServeHTTP(w, r)
	})
}
