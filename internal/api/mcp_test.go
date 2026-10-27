package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/auth/apikey"
	"github.com/alphabravocompany/thewolf/internal/models"
	_ "github.com/alphabravocompany/thewolf/plugins"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type bearerTransport string

func (token bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+string(token))
	return http.DefaultTransport.RoundTrip(r)
}

func TestMCPClientAuthorizationAndExecution(t *testing.T) {
	grantRemediation(t)
	t.Setenv("WOLF_MCP_ENABLED", "1")
	t.Setenv("WOLF_SCAN_EXECUTION_MODE", "queued")
	srv, store, jwt := newTestServer(t)
	httpServer := httptest.NewServer(srv.Router)
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	connect := func(token string) *mcp.ClientSession {
		t.Helper()
		client := mcp.NewClient(&mcp.Implementation{Name: "wolf-test", Version: "1"}, nil)
		session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
			Endpoint: httpServer.URL + "/api/v1/mcp", HTTPClient: &http.Client{Transport: bearerTransport(token)},
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = session.Close() })
		return session
	}
	limited := connect(createToken(t, srv, jwt, []string{apikey.ScopeReadRepos}))
	denied, err := limited.CallTool(ctx, &mcp.CallToolParams{Name: "wolf.findings.list", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if !denied.IsError {
		t.Fatalf("findings scope bypass: %+v", denied)
	}
	allowed, err := limited.CallTool(ctx, &mcp.CallToolParams{Name: "wolf.repos.list", Arguments: map[string]any{}})
	if err != nil || allowed.IsError {
		t.Fatalf("repository read failed: %v %+v", err, allowed)
	}
	session := connect(jwt)
	resources, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: "wolf://edition"})
	if err != nil || len(resources.Contents) != 1 || resources.Contents[0].URI != "wolf://edition" {
		t.Fatalf("resource contract: %v %+v", err, resources)
	}
	tools, err := session.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) < 15 {
		t.Fatalf("tool discovery: %v %+v", err, tools)
	}
	user, err := store.GetUserByEmail(ctx, "dev@example.com")
	if err != nil {
		t.Fatal(err)
	}
	repo := &models.Repo{ID: "mcp-repo", UserID: user.ID, Name: "mcp", SourceType: models.SourceTypeLocal, SourcePath: t.TempDir(), DefaultBranch: "main"}
	if err := store.CreateRepo(ctx, repo); err != nil {
		t.Fatal(err)
	}
	arguments := map[string]any{"body": map[string]any{"repo_id": repo.ID, "tools": []string{"bandit"}}, "idempotency_key": "mcp-create-test"}
	var scanID string
	for range 2 {
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "wolf.scans.create", Arguments: arguments})
		if err != nil || result.IsError {
			t.Fatalf("create scan: %v %+v", err, result)
		}
		var out struct {
			Data models.Scan `json:"data"`
		}
		if err := json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &out); err != nil {
			t.Fatal(err)
		}
		if out.Data.ID == "" || (scanID != "" && scanID != out.Data.ID) {
			t.Fatalf("idempotency lost: %+v", out)
		}
		scanID = out.Data.ID
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "wolf.scans.cancel", Arguments: map[string]any{"id": scanID}})
	if err != nil || result.IsError {
		t.Fatalf("cancel scan: %v %+v", err, result)
	}
	// Cancelling a running scan is accepted at once; the executor records the terminal state shortly after.
	var scan *models.Scan
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		scan, err = store.GetScanByID(ctx, scanID)
		if err != nil || scan.Status == models.ScanStatusCancelled || time.Now().After(deadline) {
			break
		}
	}
	if err != nil || scan.Status != models.ScanStatusCancelled {
		t.Fatalf("cancel was not persisted: %v %+v", err, scan)
	}
	scan.CommitSHA, scan.DirtyState = strings.Repeat("a", 40), "clean"
	if err := store.UpdateScan(ctx, scan); err != nil {
		t.Fatal(err)
	}
	coverageArgs := map[string]any{"id": scanID, "body": map[string]any{"commit_sha": scan.CommitSHA, "reports": []map[string]string{{"format": "lcov", "content": strings.Repeat("\n", 1<<20) + "SF:src/app.ts\nDA:1,1\nend_of_record\n"}}}}
	result, err = limited.CallTool(ctx, &mcp.CallToolParams{Name: "wolf.scans.import-coverage", Arguments: coverageArgs})
	if err != nil || !result.IsError {
		t.Fatalf("coverage write scope bypass: %v %+v", err, result)
	}
	result, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "wolf.scans.import-coverage", Arguments: coverageArgs})
	if err != nil || result.IsError {
		t.Fatalf("coverage upload through MCP: %v %+v", err, result)
	}
	stored, err := store.GetTestCoverage(ctx, scanID)
	if err != nil || stored.CommitSHA != scan.CommitSHA {
		t.Fatalf("coverage was not stored: %v", err)
	}
	for _, name := range []string{"wolf.scans.test-coverage", "wolf.scans.coverage-files"} {
		result, err = session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: map[string]any{"id": scanID}})
		if err != nil || result.IsError {
			t.Fatalf("coverage read through %s: %v %+v", name, err, result)
		}
	}
	fixArgs := map[string]any{"body": map[string]any{"repo_id": repo.ID, "scan_id": scanID}, "idempotency_key": "mcp-fix-request"}
	disabled, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "wolf.fixes.create", Arguments: fixArgs})
	if err != nil || !disabled.IsError {
		t.Fatalf("MCP bypassed fixing disabled: %v %+v", err, disabled)
	}
	if err := store.SetSetting(ctx, "autofix_enabled", "true"); err != nil {
		t.Fatal(err)
	}
	deniedFix, err := limited.CallTool(ctx, &mcp.CallToolParams{Name: "wolf.fixes.create", Arguments: fixArgs})
	if err != nil || !deniedFix.IsError {
		t.Fatalf("MCP bypassed write:fixes: %v %+v", err, deniedFix)
	}
	var fixID string
	for range 2 {
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "wolf.fixes.create", Arguments: fixArgs})
		if err != nil || result.IsError {
			t.Fatalf("MCP fix create: %v %+v", err, result)
		}
		var envelope struct {
			Data models.FixJob `json:"data"`
		}
		if err := json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Data.ID == "" || (fixID != "" && envelope.Data.ID != fixID) || envelope.Data.Mode != models.FixModeDryRun {
			t.Fatalf("MCP duplicate/publication intent: %+v", envelope.Data)
		}
		fixID = envelope.Data.ID
	}
	fixArgs["body"].(map[string]any)["mode"] = "push"
	conflict, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "wolf.fixes.create", Arguments: fixArgs})
	if err != nil || !conflict.IsError {
		t.Fatalf("MCP changed push intent reused key: %v %+v", err, conflict)
	}
	delete(fixArgs, "idempotency_key")
	missing, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "wolf.fixes.create", Arguments: fixArgs})
	if err == nil && !missing.IsError {
		t.Fatal("MCP fix creation accepted no key")
	}
	audited := false
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline) && !audited; {
		entries, err := store.ListAuditLog(ctx, 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if strings.Contains(entry.Path, "/fixes") && entry.Method == "POST" && entry.StatusCode == http.StatusCreated && entry.UserID == user.ID {
				audited = true
			}
		}
		if !audited {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if !audited {
		t.Fatal("MCP fix creation bypassed REST audit")
	}

}
