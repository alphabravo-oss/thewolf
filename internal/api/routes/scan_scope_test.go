package routes

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/scan/report"
)

type profileTestPlugin struct {
	name     string
	category models.Category
}

func (p profileTestPlugin) Name() string                 { return p.name }
func (p profileTestPlugin) Category() models.Category    { return p.category }
func (p profileTestPlugin) Languages() []models.Language { return nil }
func (p profileTestPlugin) CheckAvailable() bool         { return true }
func (p profileTestPlugin) Execute(context.Context, models.ExecuteOpts) ([]models.Finding, error) {
	return nil, nil
}

func TestApplyScannerRunScopeReportsRepositoryFallback(t *testing.T) {
	request := createScanRequest{
		Profile: "targeted", IncludePaths: []string{"src/**"}, ExcludePaths: []string{"vendor/**"},
	}
	record := applyScannerRunScope(
		&models.ScannerRunRecord{ToolName: "repository-tool"},
		request,
		&models.Scan{Attempt: 2, ExecutionBackend: "docker"},
		&report.ScannerPlan{Run: []report.ScannerPlanDecision{{
			Tool: "repository-tool", PathScope: "repository",
		}}},
	)

	var requested, effective map[string]interface{}
	if err := json.Unmarshal([]byte(record.RequestedScope), &requested); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(record.EffectiveScope), &effective); err != nil {
		t.Fatal(err)
	}
	if requested["include_paths"] == nil || requested["exclude_paths"] == nil {
		t.Fatalf("requested scope lost selectors: %s", record.RequestedScope)
	}
	if effective["include_paths"] != nil || effective["exclude_paths"] != nil {
		t.Fatalf("repository-scoped tool overstated effective scope: %s", record.EffectiveScope)
	}
	if !strings.Contains(record.ScopeMessage, "full source snapshot") {
		t.Fatalf("scope fallback is not explained: %q", record.ScopeMessage)
	}
}

func TestApplyScannerRunScopePreservesSupportedGlobs(t *testing.T) {
	request := createScanRequest{
		Profile: "targeted", IncludePaths: []string{"src/**"}, ExcludePaths: []string{"vendor/**"},
	}
	record := applyScannerRunScope(
		&models.ScannerRunRecord{ToolName: "scoped-tool"},
		request,
		nil,
		&report.ScannerPlan{Run: []report.ScannerPlanDecision{{
			Tool: "scoped-tool", PathScope: "file_globs",
		}}},
	)
	if record.EffectiveScope != record.RequestedScope {
		t.Fatalf("supported path scope changed: requested=%s effective=%s",
			record.RequestedScope, record.EffectiveScope)
	}
	if !strings.Contains(record.ScopeMessage, "honors") {
		t.Fatalf("supported scope is not explained: %q", record.ScopeMessage)
	}
}

func TestFullProfileExcludesDASTWithoutAnExplicitTarget(t *testing.T) {
	registry := plugin.NewRegistry()
	registry.Register(profileTestPlugin{name: "semgrep", category: models.CategorySAST})
	registry.Register(profileTestPlugin{name: "nuclei", category: models.CategoryDAST})
	tools := toolsForProfile(
		&Handler{Registry: registry},
		createScanRequest{Profile: "full"},
		nil,
	)
	if len(tools) != 1 || tools[0] != "semgrep" {
		t.Fatalf("full source scan selected tools %#v; DAST must require a target", tools)
	}
}
