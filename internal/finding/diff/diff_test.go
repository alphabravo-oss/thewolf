package diff

import (
	"testing"

	"github.com/alphabravocompany/thewolf/internal/finding/identity"
	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestCompareClassifiesNewExistingAndFixed(t *testing.T) {
	existing := finding("base", "app/a.go", "R1", "sql-injection", 10)
	fixed := finding("fixed", "app/b.go", "R2", "weak-crypto", 20)
	currentExisting := existing
	currentExisting.ID = "current"
	currentExisting.LineStart = 14
	currentExisting.LineEnd = 14
	identity.Apply(&currentExisting)
	newFinding := finding("new", "app/c.go", "R3", "hardcoded-secret", 30)

	result := Compare([]models.Finding{existing, fixed}, []models.Finding{currentExisting, newFinding})
	counts := result.Counts()

	if counts.Existing != 1 || counts.New != 1 || counts.Fixed != 1 {
		t.Fatalf("unexpected counts: %+v", counts)
	}
	if result.Existing[0].BaselineState != StateExisting {
		t.Fatalf("existing state = %q", result.Existing[0].BaselineState)
	}
	if result.New[0].BaselineState != StateNew {
		t.Fatalf("new state = %q", result.New[0].BaselineState)
	}
	if result.Fixed[0].BaselineState != StateFixed {
		t.Fatalf("fixed state = %q", result.Fixed[0].BaselineState)
	}
}

func TestCompareComputesIdentityWhenMissing(t *testing.T) {
	baseline := models.Finding{
		ID:           "baseline",
		ToolName:     "gosec",
		RuleID:       "G201",
		FineCategory: "sql-injection",
		FilePath:     "/scan/app/db.go",
		FunctionName: "loadUser",
		CodeSnippet:  "db.Query(query)",
		LineStart:    10,
	}
	current := baseline
	current.ID = "current"
	current.ToolName = "semgrep"
	current.RuleID = "go.sqli"
	current.LineStart = 40

	result := Compare([]models.Finding{baseline}, []models.Finding{current})
	if len(result.Existing) != 1 {
		t.Fatalf("expected identity-computed existing finding, got %+v", result.Counts())
	}
}

func finding(id, path, rule, category string, line int) models.Finding {
	f := models.Finding{
		ID:           id,
		ToolName:     "semgrep",
		Category:     models.CategorySAST,
		RuleID:       rule,
		FineCategory: category,
		FilePath:     path,
		LineStart:    line,
		LineEnd:      line,
		FunctionName: "handler",
		CodeSnippet:  "dangerous(input)",
	}
	identity.Apply(&f)
	return f
}
