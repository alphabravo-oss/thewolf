package routes_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/api/routes"
)

func TestSchedulingSettingValidation(t *testing.T) {
	env := setupTestEnv(t)
	for _, body := range []string{
		`{"scan_concurrency":"-1","some_other_setting":"should-not-write"}`,
		`{"heavy_scanner_concurrency":"33"}`, `{"network_scanner_concurrency":"1.5"}`,
	} {
		w := httptest.NewRecorder()
		routes.UpdateSettings(w, httptest.NewRequest(http.MethodPut, "/settings", strings.NewReader(body)))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("accepted invalid settings: %d %s", w.Code, w.Body)
		}
	}
	if value, _ := env.Store.GetSetting(t.Context(), "some_other_setting"); value != "" {
		t.Fatal("partially applied rejected settings")
	}
	w := httptest.NewRecorder()
	routes.UpdateSettings(w, httptest.NewRequest(http.MethodPut, "/settings", strings.NewReader(`{"scan_concurrency":"0","heavy_scanner_concurrency":"3","network_scanner_concurrency":""}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("valid settings rejected: %s", w.Body)
	}
	if value, _ := env.Store.GetSetting(t.Context(), "scan_concurrency"); value != "0" {
		t.Fatal("auto setting not persisted")
	}
}
