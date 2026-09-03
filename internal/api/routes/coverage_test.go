package routes_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/alphabravocompany/thewolf/internal/api/routes"
	"github.com/alphabravocompany/thewolf/pkg/edition"
	"github.com/alphabravocompany/thewolf/pkg/entitlement"
)

func TestGetCoverageFromToolsYAML(t *testing.T) {
	r := chi.NewRouter()
	r.Get("/api/coverage", routes.GetCoverage)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/coverage", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("coverage: %d %s", w.Code, w.Body.String())
	}
	var env struct {
		Data struct {
			Source    string           `json:"source"`
			ToolCount int              `json:"tool_count"`
			Honesty   string           `json:"honesty"`
			Tools     []map[string]any `json:"tools"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Data.Source != "scanners/tools.yaml" || env.Data.Honesty == "" {
		t.Fatalf("%+v", env.Data)
	}
	if env.Data.ToolCount < 50 || len(env.Data.Tools) != env.Data.ToolCount {
		t.Fatalf("tool_count=%d tools=%d", env.Data.ToolCount, len(env.Data.Tools))
	}
	if env.Data.Tools[0]["depth"] != "federated-scanner" {
		t.Fatalf("depth = %v", env.Data.Tools[0]["depth"])
	}
}

func TestGetCapabilityCommunity404sEnterprise(t *testing.T) {
	entitlement.SetActive(entitlement.Community{})
	t.Cleanup(func() { entitlement.SetActive(nil) })
	r := chi.NewRouter()
	r.Get("/api/capabilities/{name}", routes.GetCapability)
	get := func(name string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/capabilities/"+name, nil))
		return w
	}
	if w := get("enterprise.identity"); w.Code != http.StatusNotFound {
		t.Fatalf("enterprise: %d %s", w.Code, w.Body.String())
	}
	if w := get("cloud.tenancy"); w.Code != http.StatusNotFound {
		t.Fatalf("cloud: %d", w.Code)
	}
	w := get("community.scan")
	if w.Code != http.StatusOK {
		t.Fatalf("community.scan: %d %s", w.Code, w.Body.String())
	}
	entitlement.SetActive(allowCap{cap: entitlement.Identity})
	edition.Default.RegisterService(entitlement.Identity, map[string]string{"module": "identity"})
	t.Cleanup(func() { edition.Default.RegisterService(entitlement.Identity, nil) })
	ok := get("enterprise.identity")
	if ok.Code != http.StatusOK {
		t.Fatalf("entitled: %d %s", ok.Code, ok.Body.String())
	}
}
