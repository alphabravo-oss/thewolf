package routes_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/api/routes"
)

func TestAdminSaveDatabaseEnvManaged(t *testing.T) {
	t.Setenv("WOLF_DB_DRIVER", "postgres")
	t.Setenv("WOLF_DB_DSN", "postgres://wolf@helm:5432/wolf")
	req := httptest.NewRequest(http.MethodPut, "/database", bytes.NewReader([]byte(`{"dsn":"postgres://wolf@db:5432/wolf"}`)))
	w := httptest.NewRecorder()
	routes.AdminSaveDatabase(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "env_managed" {
		t.Fatalf("%s", w.Body.String())
	}
}
