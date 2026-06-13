package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
)

type scannerPlanMockPlugin struct {
	name      string
	category  models.Category
	languages []models.Language
	available bool
}

type scannerPlanDecision struct {
	Tool       string `json:"tool"`
	ReasonCode string `json:"reason_code"`
}

func (m scannerPlanMockPlugin) Name() string                 { return m.name }
func (m scannerPlanMockPlugin) Category() models.Category    { return m.category }
func (m scannerPlanMockPlugin) Languages() []models.Language { return m.languages }
func (m scannerPlanMockPlugin) CheckAvailable() bool         { return m.available }
func (m scannerPlanMockPlugin) Execute(context.Context, models.ExecuteOpts) ([]models.Finding, error) {
	return nil, nil
}

func TestScannerVersionCheckFresh(t *testing.T) {
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		check models.ScannerVersionCheck
		want  bool
	}{
		{
			name: "current within success ttl",
			check: models.ScannerVersionCheck{
				Status:    models.ScannerVersionCurrent,
				CheckedAt: now.Add(-23 * time.Hour),
			},
			want: true,
		},
		{
			name: "current beyond success ttl",
			check: models.ScannerVersionCheck{
				Status:    models.ScannerVersionCurrent,
				CheckedAt: now.Add(-25 * time.Hour),
			},
			want: false,
		},
		{
			name: "failure within failure ttl",
			check: models.ScannerVersionCheck{
				Status:    models.ScannerVersionCheckFailed,
				CheckedAt: now.Add(-30 * time.Minute),
			},
			want: true,
		},
		{
			name: "failure beyond failure ttl",
			check: models.ScannerVersionCheck{
				Status:    models.ScannerVersionCheckFailed,
				CheckedAt: now.Add(-2 * time.Hour),
			},
			want: false,
		},
		{
			name: "zero checked_at",
			check: models.ScannerVersionCheck{
				Status: models.ScannerVersionCurrent,
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := scannerVersionCheckFresh(tt.check, now); got != tt.want {
				t.Fatalf("scannerVersionCheckFresh() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestScannerUpdateForce(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "/api/v1/scanners/tools/check-updates?force=true", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !scannerUpdateForce(req) {
		t.Fatal("force=true query should force refresh")
	}

	req, err = http.NewRequest(http.MethodPost, "/api/v1/scanners/tools/check-updates", strings.NewReader(`{"force":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if !scannerUpdateForce(req) {
		t.Fatal("force JSON body should force refresh")
	}

	req, err = http.NewRequest(http.MethodPost, "/api/v1/scanners/tools/check-updates", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if scannerUpdateForce(req) {
		t.Fatal("empty JSON body should not force refresh")
	}
}

func TestScannersPlanWithLanguageOverride(t *testing.T) {
	reg := plugin.NewRegistry()
	reg.Register(scannerPlanMockPlugin{name: "gosec", category: models.CategorySAST, languages: []models.Language{models.LangGo}, available: true})
	reg.Register(scannerPlanMockPlugin{name: "bandit", category: models.CategorySAST, languages: []models.Language{models.LangPython}, available: true})
	reg.Register(scannerPlanMockPlugin{name: "gitleaks", category: models.CategorySecrets, available: true})
	SetHandler(nil, reg)
	t.Cleanup(func() { DefaultHandler = nil })

	body := bytes.NewBufferString(`{"languages":["go"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scanners/plan", body)
	w := httptest.NewRecorder()
	ScannersPlan(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Data struct {
			DetectionSource string                `json:"detection_source"`
			Languages       []string              `json:"languages"`
			Run             []scannerPlanDecision `json:"run"`
			Skip            []scannerPlanDecision `json:"skip"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Data.DetectionSource != "request" {
		t.Fatalf("detection source = %q", resp.Data.DetectionSource)
	}
	if !hasDecision(resp.Data.Run, "gosec", "language_match") {
		t.Fatalf("expected gosec language match in run decisions: %+v", resp.Data.Run)
	}
	if !hasDecision(resp.Data.Run, "gitleaks", "all_language_scanner") {
		t.Fatalf("expected gitleaks all-language run decision: %+v", resp.Data.Run)
	}
	if !hasDecision(resp.Data.Skip, "bandit", "no_language_match") {
		t.Fatalf("expected bandit no-language skip decision: %+v", resp.Data.Skip)
	}
}

func hasDecision(values []scannerPlanDecision, tool, reason string) bool {
	for _, value := range values {
		if value.Tool == tool && value.ReasonCode == reason {
			return true
		}
	}
	return false
}
