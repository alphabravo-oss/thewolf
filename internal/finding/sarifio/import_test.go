package sarifio

import (
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
}

func TestImportRejectsUnsupportedVersion(t *testing.T) {
	_, err := Import([]byte(`{"version":"2.0.0","runs":[{}]}`))
	if err == nil {
		t.Fatal("expected unsupported version error")
	}
}
