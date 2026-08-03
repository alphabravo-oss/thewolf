package middleware

import (
	"net/http"

	"github.com/alphabravocompany/thewolf/internal/api/response"
)

// MaxBodySize limits request body size. Returns 413 if exceeded.
func MaxBodySize(maxBytes int64) func(http.Handler) http.Handler {
	return MaxBodySizeForRequest(maxBytes, nil)
}

// MaxBodySizeForRequest applies the default limit unless limitFor returns a
// positive route-specific override. This keeps ordinary JSON requests small
// while allowing explicitly selected streaming upload endpoints to define a
// larger, independently validated bound.
func MaxBodySizeForRequest(
	maxBytes int64,
	limitFor func(*http.Request) int64,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			limit := maxBytes
			if limitFor != nil {
				if override := limitFor(r); override > 0 {
					limit = override
				}
			}
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, limit)
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
