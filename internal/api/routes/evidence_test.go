package routes_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/api/routes"
)

func TestListEvidenceRequiresAuth(t *testing.T) {
	w := httptest.NewRecorder()
	routes.ListEvidence(w, httptest.NewRequest(http.MethodGet, "/api/v1/evidence", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauth: %d %s", w.Code, w.Body.String())
	}
}
