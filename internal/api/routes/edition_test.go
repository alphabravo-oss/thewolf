package routes_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/alphabravocompany/thewolf/internal/api/routes"
)

func TestGetEditionIsPublicCommunity(t *testing.T) {
	r := chi.NewRouter()
	r.Get("/api/edition", routes.GetEdition)
	r.Get("/api/license", routes.GetLicense)
	r.Get("/api/mcp/status", routes.MCPStatus)
	r.Post("/api/mcp", routes.HandleMCP)

	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	w := get("/api/edition")
	if w.Code != http.StatusOK {
		t.Fatalf("edition: %d %s", w.Code, w.Body.String())
	}
	if w.Header().Get("ETag") == "" {
		t.Fatal("edition ETag")
	}
	var env struct {
		Data struct {
			Edition string          `json:"edition"`
			MCP     map[string]any  `json:"mcp"`
			Ent     map[string]bool `json:"entitlements"`
			Limits  struct {
				Repos    int    `json:"repos"`
				Users    int    `json:"users"`
				Workers  int    `json:"workers"`
				Source   string `json:"source"`
				Enforced bool   `json:"enforced"`
			} `json:"limits"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Data.Edition != "community" {
		t.Fatalf("edition = %q", env.Data.Edition)
	}
	if env.Data.Ent["enterprise.identity"] {
		t.Fatal("community must not grant enterprise.identity")
	}
	if enabled, _ := env.Data.MCP["enabled"].(bool); enabled {
		t.Fatal("MCP must default off")
	}
	if env.Data.Limits.Repos != 5 || env.Data.Limits.Users != 3 || env.Data.Limits.Workers != 1 {
		t.Fatalf("limits = %+v", env.Data.Limits)
	}
	if env.Data.Limits.Enforced || env.Data.Limits.Source != "synthetic" {
		t.Fatalf("limits should be synthetic and off by default: %+v", env.Data.Limits)
	}
	if env.Data.Ent["enterprise.plugins"] || env.Data.Ent["enterprise.compliance"] {
		t.Fatal("community must not grant remaining enterprise caps")
	}

	lic := get("/api/license")
	if lic.Code != http.StatusOK {
		t.Fatalf("license: %d", lic.Code)
	}
	var licEnv struct {
		Data struct {
			DataIntact bool `json:"data_intact"`
			Fallback   bool `json:"community_fallback"`
			Valid      bool `json:"valid"`
		} `json:"data"`
	}
	if err := json.Unmarshal(lic.Body.Bytes(), &licEnv); err != nil {
		t.Fatal(err)
	}
	if licEnv.Data.Valid || !licEnv.Data.DataIntact || !licEnv.Data.Fallback {
		t.Fatalf("license status = %+v", licEnv.Data)
	}

	mcp := httptest.NewRequest(http.MethodPost, "/api/mcp", nil)
	mw := httptest.NewRecorder()
	r.ServeHTTP(mw, mcp)
	if mw.Code != http.StatusNotFound {
		t.Fatalf("mcp disabled: expected 404, got %d %s", mw.Code, mw.Body.String())
	}
}

func TestCommunityLicenseValidateAndInstall(t *testing.T) {
	r := chi.NewRouter()
	r.Post("/api/license/validate", routes.ValidateLicense)
	r.Post("/api/license/install", routes.InstallLicense)

	post := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	val := post("/api/license/validate", `{"license":"anything"}`)
	if val.Code != http.StatusOK {
		t.Fatalf("validate: %d %s", val.Code, val.Body.String())
	}
	var env struct {
		Data struct {
			Valid             bool   `json:"valid"`
			CommercialLicense bool   `json:"commercial_license"`
			Reason            string `json:"reason"`
		} `json:"data"`
	}
	if err := json.Unmarshal(val.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Data.Valid || env.Data.CommercialLicense || env.Data.Reason == "" {
		t.Fatalf("validate = %+v", env.Data)
	}

	missing := post("/api/license/install", `{}`)
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("install empty: %d %s", missing.Code, missing.Body.String())
	}
	inst := post("/api/license/install", `{"license":"blob"}`)
	if inst.Code != http.StatusConflict {
		t.Fatalf("install: expected 409, got %d %s", inst.Code, inst.Body.String())
	}
}
