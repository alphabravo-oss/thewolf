package middleware

import (
	"net/http"
	"strings"

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
	EventType  string
	Category   string
	Severity   string
	IP         string
	UserAgent  string
}

// ClientIP extracts the best-guess source address: the first X-Forwarded-For
// hop (when behind the bundled proxy) else the request's remote host.
func ClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	host := r.RemoteAddr
	if i := strings.LastIndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	return host
}

// Audit returns middleware that records mutating requests and the small set of
// security-sensitive reads explicitly classified for audit (currently offline
// release-bundle export). Ordinary reads are skipped. The recorder is invoked
// on its own goroutine so audit logging never blocks or fails a request.
func Audit(record func(AuditEntry)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				if !isAuditedRead(r.URL.Path) {
					next.ServeHTTP(w, r)
					return
				}
			case http.MethodHead, http.MethodOptions:
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
			event, category, severity := Classify(r.Method, r.URL.Path)
			entry := AuditEntry{
				TokenID:    info.TokenID,
				UserID:     info.Claims.UserID,
				Method:     r.Method,
				Path:       r.URL.Path,
				Action:     actionForMethod(r.Method),
				ResourceID: chi.URLParam(r, "id"),
				StatusCode: sr.status,
				EventType:  event,
				Category:   category,
				Severity:   severity,
				IP:         ClientIP(r),
				UserAgent:  r.UserAgent(),
			}
			go record(entry)
		})
	}
}

func isAuditedRead(path string) bool {
	path = normalizeAuditPath(path)
	const prefix = "/scanner-supply-chain/releases/"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, "/export") {
		return false
	}
	return strings.Contains(strings.TrimPrefix(path, prefix), "/")
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

// Unwrap exposes the wrapped writer to http.ResponseController so streaming
// handlers (SSE on a POST, e.g. scanner image builds) can reach the
// underlying Flusher/Hijacker even though this recorder only overrides
// WriteHeader/Write.
func (s *statusRecorder) Unwrap() http.ResponseWriter {
	return s.ResponseWriter
}

// Flush forwards to the wrapped writer's Flusher when present, so SSE frames
// reach the client immediately. A plain type assertion on the recorder would
// fail because the embedded interface doesn't promote Flush.
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
