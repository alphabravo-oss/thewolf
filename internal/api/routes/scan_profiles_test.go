package routes_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/api/routes"
)

func TestListScanProfiles(t *testing.T) {
	w := httptest.NewRecorder()
	routes.ListScanProfiles(w, httptest.NewRequest(http.MethodGet, "/api/v1/scan-profiles", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var env struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Data) < 4 {
		t.Fatalf("profiles = %#v", env.Data)
	}
}
