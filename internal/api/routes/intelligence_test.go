package routes_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"strings"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/api/routes"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/pkg/edition"
	"github.com/alphabravocompany/thewolf/pkg/entitlement"
	"github.com/alphabravocompany/thewolf/pkg/intelligence"
	"github.com/alphabravocompany/thewolf/pkg/verification"
)

type allowCap struct{ cap string }

func (a allowCap) Allows(c string) bool {
	if strings.HasPrefix(c, "enterprise.") {
		return c == a.cap
	}
	return true
}

func TestAttackPathAndVerifyRequireEntitlement(t *testing.T) {
	e := setupTestEnv(t)
	ctx := context.Background()
	entitlement.SetActive(entitlement.Community{})
	t.Cleanup(func() { entitlement.SetActive(nil) })

	repoID := e.createRepo(t)
	now := time.Now()
	scan := &models.Scan{
		ID: uuid.New().String(), UserID: e.UserID, RepoID: repoID,
		Status: models.ScanStatusCompleted, CreatedAt: now, UpdatedAt: now,
	}
	if err := e.Store.CreateScan(ctx, scan); err != nil {
		t.Fatal(err)
	}
	if err := e.Store.CreateFinding(ctx, &models.Finding{
		ID: uuid.New().String(), ScanID: scan.ID, RepoID: repoID,
		Fingerprint: "fp-intel", Title: "xss", Severity: models.SeverityHigh,
		Status: models.StatusOpen, ToolName: "semgrep", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	routes.DualWriteVulnerabilities(ctx, routes.DefaultHandler, scan)
	listed := e.doRequest(http.MethodGet, "/api/vulnerabilities", nil)
	var env struct {
		Data []models.Vulnerability `json:"data"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &env); err != nil || len(env.Data) != 1 {
		t.Fatalf("list: %d %s", listed.Code, listed.Body.String())
	}
	id := env.Data[0].ID
	if w := e.doRequest(http.MethodGet, "/api/vulnerabilities/"+id+"/attack-path", nil); w.Code != http.StatusNotFound {
		t.Fatalf("community attack-path: %d %s", w.Code, w.Body.String())
	}
	if w := e.doRequest(http.MethodPost, "/api/vulnerabilities/"+id+"/verify", map[string]any{"environment": "production"}); w.Code != http.StatusNotFound {
		t.Fatalf("community verify: %d %s", w.Code, w.Body.String())
	}

	edition.Default.RegisterService(entitlement.Intelligence, intelligence.Consensus{})
	edition.Default.RegisterService(entitlement.Verification, verification.DenyEngine{})
	t.Cleanup(func() {
		edition.Default.RegisterService(entitlement.Intelligence, nil)
		edition.Default.RegisterService(entitlement.Verification, nil)
	})
	entitlement.SetActive(allowCap{cap: entitlement.Intelligence})
	w := e.doRequest(http.MethodGet, "/api/vulnerabilities/"+id+"/attack-path", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("entitled attack-path: %d %s", w.Code, w.Body.String())
	}
	entitlement.SetActive(allowCap{cap: entitlement.Verification})
	vw := e.doRequest(http.MethodPost, "/api/vulnerabilities/"+id+"/verify", map[string]any{})
	if vw.Code != http.StatusOK {
		t.Fatalf("entitled verify: %d %s", vw.Code, vw.Body.String())
	}
	var venv struct {
		Data verification.Result `json:"data"`
	}
	if err := json.Unmarshal(vw.Body.Bytes(), &venv); err != nil {
		t.Fatal(err)
	}
	if venv.Data.Status != verification.StatusDenied || venv.Data.Reason != "production-deny default" {
		t.Fatalf("verify = %+v", venv.Data)
	}
}
