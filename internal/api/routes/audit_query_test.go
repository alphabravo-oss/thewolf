package routes_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/api/routes"
)

func TestAuditDateValidation(t *testing.T) {
	_, store := setupTestRouter(t)
	defer store.Close()
	for _, tc := range []struct {
		query string
		want  int
	}{
		{"from=2026-10-01&before=2026-11-01", 200},
		{"from=2026-10-01T12:00:00Z", 200},
		{"from=invalid", 400},
		{"before=2026-02-30", 400},
		{"from=2026-11-01&before=2026-10-01", 400},
		{"from=2026-10-01&before=2026-10-01", 400},
	} {
		w := httptest.NewRecorder()
		routes.ListAuditLog(w, httptest.NewRequest(http.MethodGet, "/audit-log?"+tc.query, nil))
		if w.Code != tc.want {
			t.Errorf("%s: %d: %s", tc.query, w.Code, w.Body.String())
		}
	}
}
