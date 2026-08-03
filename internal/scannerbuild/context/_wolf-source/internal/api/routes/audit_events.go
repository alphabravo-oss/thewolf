package routes

import (
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/api/middleware"
	"github.com/alphabravocompany/thewolf/internal/models"
)

// AuditSink records an audit entry. The server sets it at startup (to the
// store-backed recorder). nil-safe so handlers can call unconditionally.
var AuditSink func(models.AuditLogEntry)

// RecordAuthEvent emits an authentication audit event for a request the
// mutation middleware can't see — the public /auth group (login, MFA login).
// userID is empty for a failed login on an unknown account.
func RecordAuthEvent(r *http.Request, userID, event, severity string, status int) {
	if AuditSink == nil {
		return
	}
	AuditSink(models.AuditLogEntry{
		ID:         uuid.New().String(),
		UserID:     userID,
		Action:     "auth",
		Method:     r.Method,
		Path:       r.URL.Path,
		StatusCode: status,
		EventType:  event,
		Category:   middleware.CatAuthentication,
		Severity:   severity,
		IP:         middleware.ClientIP(r),
		UserAgent:  r.UserAgent(),
		CreatedAt:  time.Now().UTC(),
	})
}
