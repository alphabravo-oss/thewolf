package middleware

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/alphabravocompany/thewolf/internal/auth"
)

// AuditEntry is one recorded mutating request, handed to the recorder.
type AuditEntry struct {
	TokenID    string
	UserID     string
	Method     string
	Path       string
	Action     string
	ResourceID string
	StatusCode int
}

// Audit returns middleware that records mutating (POST/PUT/PATCH/DELETE)
// requests via the supplied recorder. Read requests are skipped. The
// recorder is invoked on its own goroutine so audit logging never blocks
// or fails a request.
func Audit(record func(AuditEntry)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
				next.ServeHTTP(w, r)
				return
			}
			sr := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(sr, r)

			if record == nil {
				return
			}
			info := auth.GetAuthInfo(r.Context())
			if info == nil || info.Claims == nil {
				return
			}
			entry := AuditEntry{
				TokenID:    info.TokenID,
				UserID:     info.Claims.UserID,
				Method:     r.Method,
				Path:       r.URL.Path,
				Action:     actionForMethod(r.Method),
				ResourceID: chi.URLParam(r, "id"),
				StatusCode: sr.status,
			}
			go record(entry)
		})
	}
}

func actionForMethod(method string) string {
	switch method {
	case http.MethodPost:
		return "create"
	case http.MethodPut, http.MethodPatch:
		return "update"
	case http.MethodDelete:
		return "delete"
	default:
		return "other"
	}
}

// statusRecorder wraps a ResponseWriter to capture the response status code.
type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wrote {
		s.status = code
		s.wrote = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	s.wrote = true
	return s.ResponseWriter.Write(b)
}
