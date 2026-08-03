package routes_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/api/routes"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
)

type preflightTestPlugin struct {
	name     string
	category models.Category
}

func (p preflightTestPlugin) Name() string                 { return p.name }
func (p preflightTestPlugin) Category() models.Category    { return p.category }
func (p preflightTestPlugin) Languages() []models.Language { return nil }
func (p preflightTestPlugin) CheckAvailable() bool         { return true }
func (p preflightTestPlugin) Execute(context.Context, models.ExecuteOpts) ([]models.Finding, error) {
	return nil, nil
}

func TestScanPreflightUsesRemoteProfileSelectionWithoutChangingLegacyResponse(t *testing.T) {
	env := setupTestEnv(t)
	repoID := env.createRepo(t)
	registry := plugin.NewRegistry()
	registry.Register(preflightTestPlugin{name: "semgrep", category: models.CategorySAST})
	registry.Register(preflightTestPlugin{name: "trivy", category: models.CategorySCA})
	registry.Register(preflightTestPlugin{name: "nuclei", category: models.CategoryDAST})
	routes.SetHandler(env.Store, registry)

	previousContainer := container.Default()
	container.SetDefault(nil)
	t.Cleanup(func() { container.SetDefault(previousContainer) })

	response := env.doRequest(http.MethodPost, "/api/scans/preflight", map[string]interface{}{
		"repo_id": repoID, "profile": "full",
		"include_paths": []string{"src/**"}, "exclude_paths": []string{"vendor/**"},
	})
	if response.Code != http.StatusOK {
		t.Fatalf("preflight: expected 200, got %d: %s", response.Code, response.Body.String())
	}
	var body struct {
		Data struct {
			Missing      []interface{} `json:"missing"`
			MissingCount int           `json:"missing_count"`
			ToolCount    int           `json:"tool_count"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Missing == nil || body.Data.MissingCount != 0 {
		t.Fatalf("legacy missing response shape changed: %s", response.Body.String())
	}
	if body.Data.ToolCount != 2 {
		t.Fatalf("full code profile should select both non-DAST tools, got %d: %s",
			body.Data.ToolCount, response.Body.String())
	}

	categoryResponse := env.doRequest(http.MethodPost, "/api/scans/preflight", map[string]interface{}{
		"repo_id": repoID, "categories": []string{"sast"},
	})
	if categoryResponse.Code != http.StatusOK {
		t.Fatalf("category preflight: expected 200, got %d: %s", categoryResponse.Code, categoryResponse.Body.String())
	}
	if err := json.Unmarshal(categoryResponse.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.ToolCount != 1 {
		t.Fatalf("SAST category should select one tool, got %d: %s",
			body.Data.ToolCount, categoryResponse.Body.String())
	}
}

func TestScanPreflightEnforcesRepoOwnershipAndSelectorValidation(t *testing.T) {
	env := setupTestEnv(t)
	registry := plugin.NewRegistry()
	registry.Register(preflightTestPlugin{name: "semgrep", category: models.CategorySAST})
	routes.SetHandler(env.Store, registry)

	otherUserID := uuid.NewString()
	if err := env.Store.CreateUser(context.Background(), &models.User{
		ID: otherUserID, Email: "preflight-other@example.test", PasswordHash: "hash",
	}); err != nil {
		t.Fatal(err)
	}
	otherRepo := &models.Repo{
		ID: uuid.NewString(), UserID: otherUserID, Name: "other",
		SourceType: models.SourceTypeLocal, SourcePath: "/tmp/other", DefaultBranch: "main",
	}
	if err := env.Store.CreateRepo(context.Background(), otherRepo); err != nil {
		t.Fatal(err)
	}
	forbidden := env.doRequest(http.MethodPost, "/api/scans/preflight", map[string]interface{}{
		"repo_id": otherRepo.ID,
	})
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("cross-owner preflight: expected 403, got %d: %s", forbidden.Code, forbidden.Body.String())
	}

	repoID := env.createRepo(t)
	invalid := env.doRequest(http.MethodPost, "/api/scans/preflight", map[string]interface{}{
		"repo_id": repoID, "profile": "targeted",
	})
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid targeted preflight: expected 400, got %d: %s", invalid.Code, invalid.Body.String())
	}
}
