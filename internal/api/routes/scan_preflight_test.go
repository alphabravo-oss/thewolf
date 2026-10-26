package routes_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/api/routes"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
	"github.com/alphabravocompany/thewolf/internal/scan/detector"
	"github.com/alphabravocompany/thewolf/internal/scan/runner"
)

type preflightTestPlugin struct {
	name          string
	category      models.Category
	languages     []models.Language
	applicability func(string) error
}

func (p preflightTestPlugin) Name() string                 { return p.name }
func (p preflightTestPlugin) Category() models.Category    { return p.category }
func (p preflightTestPlugin) Languages() []models.Language { return p.languages }
func (p preflightTestPlugin) CheckAvailable() bool         { return true }
func (p preflightTestPlugin) Applicability(path, _ string) error {
	if p.applicability != nil {
		return p.applicability(path)
	}
	return nil
}
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
	if forbidden.Code != http.StatusNotFound {
		t.Fatalf("cross-owner preflight: expected 404, got %d: %s", forbidden.Code, forbidden.Body.String())
	}

	repoID := env.createRepo(t)
	invalid := env.doRequest(http.MethodPost, "/api/scans/preflight", map[string]interface{}{
		"repo_id": repoID, "profile": "targeted",
	})
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid targeted preflight: expected 400, got %d: %s", invalid.Code, invalid.Body.String())
	}
}

func TestScanPreflightDistinguishesUnavailableFromMissingImages(t *testing.T) {
	env := setupTestEnv(t)
	repoID := env.createRepo(t)
	registry := plugin.NewRegistry()
	registry.Register(preflightTestPlugin{name: "semgrep", category: models.CategorySAST})
	registry.Register(preflightTestPlugin{name: "gokart", category: models.CategorySAST})
	routes.SetHandler(env.Store, registry)

	dir := t.TempDir()
	docker := filepath.Join(dir, "docker")
	calls := filepath.Join(dir, "calls")
	if err := os.WriteFile(docker, []byte(`#!/bin/sh
echo inspect >> "$WOLF_PROBE_CALLS"
case "$WOLF_PROBE_CASE" in
  present) exit 0 ;;
  missing) echo 'Error response from daemon: No such image: shared-scanner' >&2; exit 1 ;;
  unavailable) echo 'Cannot connect to Docker' >&2; exit 1 ;;
  *) exit 2 ;;
esac
`), 0700); err != nil {
		t.Fatal(err)
	}
	previous := container.Default()
	t.Cleanup(func() { container.SetDefault(previous) })
	for _, name := range []string{"present", "missing", "unavailable"} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(calls, nil, 0600); err != nil {
				t.Fatal(err)
			}
			container.SetDefault(&container.Config{
				Image: "shared-scanner", DockerPath: docker,
				DockerEnvironment: []string{"WOLF_PROBE_CASE=" + name, "WOLF_PROBE_CALLS=" + calls},
			})
			response := env.doRequest(http.MethodPost, "/api/scans/preflight", map[string]interface{}{
				"repo_id": repoID, "tools": []string{"semgrep", "gokart"},
			})
			if name == "unavailable" {
				if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "readiness_unavailable") {
					t.Fatalf("Docker outage must fail readiness: %d %s", response.Code, response.Body.String())
				}
			} else {
				var body struct {
					Data struct {
						Missing   []struct{ Tool, Image string }
						ToolCount int `json:"tool_count"`
					} `json:"data"`
				}
				if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil {
					t.Fatalf("preflight: %d %s", response.Code, response.Body.String())
				}
				wantMissing := 0
				if name == "missing" {
					wantMissing = 2
				}
				if body.Data.ToolCount != 2 || len(body.Data.Missing) != wantMissing {
					t.Fatalf("unexpected selection: %s", response.Body.String())
				}
			}
			probes, err := os.ReadFile(calls)
			if err != nil || string(probes) != "inspect\n" {
				t.Fatalf("shared image should be probed exactly once: %q %v", probes, err)
			}
		})
	}
}

// Exercise the HTTP preflight and the real runner independently against a fresh
// tree, then repeat with stale cached languages. Neither may plan from the cache
// when the tree the executor will scan is available locally.
func TestScanPreflightSelectionMatchesExecutionForLocalTree(t *testing.T) {
	env := setupTestEnv(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	reg := plugin.NewRegistry()
	reg.Register(preflightTestPlugin{name: "gosec", category: models.CategorySAST, languages: []models.Language{models.LangGo}})
	reg.Register(preflightTestPlugin{name: "bandit", category: models.CategorySAST, languages: []models.Language{models.LangPython}})
	reg.Register(preflightTestPlugin{name: "gitleaks", category: models.CategorySecrets})
	reg.Register(preflightTestPlugin{name: "kubescape", category: models.CategoryInfra, applicability: plugin.KubernetesApplicability})
	routes.SetHandler(env.Store, reg)
	previous := container.Default()
	container.SetDefault(nil)
	t.Cleanup(func() { container.SetDefault(previous) })
	repo := &models.Repo{ID: uuid.NewString(), UserID: env.UserID, Name: "fixture", SourceType: models.SourceTypeLocal, SourcePath: dir}
	if err := env.Store.CreateRepo(t.Context(), repo); err != nil {
		t.Fatal(err)
	}
	for _, cached := range []string{"", `{"python":10}`} {
		if err := env.Store.UpdateRepoDetection(t.Context(), repo.ID, cached, "[]"); err != nil {
			t.Fatal(err)
		}
		w := env.doRequest(http.MethodPost, "/api/scans/preflight", map[string]any{"repo_id": repo.ID, "profile": "standard"})
		var preflight struct {
			Data struct {
				Tools     []string
				ToolCount int `json:"tool_count"`
			}
		}
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &preflight) != nil {
			t.Fatalf("preflight: %d %s", w.Code, w.Body.String())
		}
		detected, err := detector.Detect(dir)
		if err != nil {
			t.Fatal(err)
		}
		var languages []models.Language
		for language := range detected.Languages {
			languages = append(languages, language)
		}
		var selected []string
		_, err = runner.Run(t.Context(), runner.RunConfig{Registry: reg, RepoPath: dir, Languages: languages, OnToolsSelected: func(tools []string) { selected = tools }})
		if err != nil {
			t.Fatal(err)
		}
		slices.Sort(selected)
		if !slices.Equal(preflight.Data.Tools, selected) || preflight.Data.ToolCount != len(selected) || !slices.Equal(selected, []string{"gitleaks", "gosec"}) {
			t.Fatalf("cache %q: preflight %v (%d), execution %v", cached, preflight.Data.Tools, preflight.Data.ToolCount, selected)
		}
	}
}
