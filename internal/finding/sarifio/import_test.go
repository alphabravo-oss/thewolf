package sarifio

import (
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestImportSARIFMapsWolfMetadata(t *testing.T) {
	data := []byte(`{
	  "version": "2.1.0",
	  "runs": [{
	    "tool": {"driver": {"name": "semgrep", "rules": [{
	      "id": "go.sql",
	      "shortDescription": {"text": "SQL Injection"},
	      "properties": {"cweId": "CWE-89", "category": "sast", "fineCategory": "sql-injection", "fixStrategyId": "parameterize-query"}
	    }]}},
	    "results": [{
	      "ruleId": "go.sql",
	      "level": "error",
	      "message": {"text": "unsafe query"},
	      "locations": [{"physicalLocation": {"artifactLocation": {"uri": "/workspace/app/db.go"}, "region": {"startLine": 42}}}],
	      "partialFingerprints": {"wolfStableFingerprint": "stable-1"},
	      "properties": {"confidence": "high", "baselineState": "new", "severity": "critical"},
	      "suppressions": [{"justification": {"text": "accepted risk"}, "properties": {"wolfSuppressionId": "sup-1"}}]
	    }]
	  }]
	}`)

	got, err := Import(data)
	if err != nil {
		t.Fatalf("Import failed: %v", err)
	}
	if got.ResultCount != 1 || len(got.Findings) != 1 {
		t.Fatalf("unexpected counts: %+v", got)
	}
	f := got.Findings[0]
	if f.ToolName != "semgrep" || f.RuleID != "go.sql" || f.Title != "SQL Injection" {
		t.Fatalf("unexpected finding identity: %+v", f)
	}
	if f.Severity != models.SeverityCritical || f.Category != models.CategorySAST || f.CWEID != "CWE-89" {
		t.Fatalf("unexpected severity/category: %+v", f)
	}
	if f.FilePath != "app/db.go" || f.LineStart != 42 {
		t.Fatalf("path was not normalized: %+v", f)
	}
	if f.StableFingerprint != "stable-1" || f.IdentityVersion == 0 {
		t.Fatalf("fingerprints not populated: %+v", f)
	}
	if !f.Suppressed || f.SuppressionID != "sup-1" || f.SuppressedReason != "accepted risk" {
		t.Fatalf("suppression not imported: %+v", f)
	}
	if strings.Contains(f.SARIFData, `"runs"`) {
		t.Fatalf("finding SARIFData should contain only the result payload, got full document: %.80s", f.SARIFData)
	}
}

func TestImportRejectsUnsupportedVersion(t *testing.T) {
	_, err := Import([]byte(`{"version":"2.0.0","runs":[{}]}`))
	if err == nil {
		t.Fatal("expected unsupported version error")
	}
}

func TestImportRejectsOversizedSARIF(t *testing.T) {
	_, err := Import([]byte(`{"version":"2.1.0","runs":[],"padding":"` + strings.Repeat("x", MaxImportBytes) + `"}`))
	if err == nil {
		t.Fatal("expected oversized SARIF error")
	}
}

func TestImportRejectsTooManyResults(t *testing.T) {
	results := make([]string, MaxImportResults+1)
	for i := range results {
		results[i] = `{"ruleId":"r","message":{"text":"issue"}}`
	}
	data := []byte(`{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"tool"}},"results":[` + strings.Join(results, ",") + `]}]}`)
	_, err := Import(data)
	if err == nil {
		t.Fatal("expected too many results error")
	}
}
