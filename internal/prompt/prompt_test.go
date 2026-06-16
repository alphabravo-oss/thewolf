package prompt

import (
	"context"
	"strings"
	"testing"
)

func TestGetDefault_KnownTypes(t *testing.T) {
	tests := []struct {
		promptType string
		section    string
		contains   string
	}{
		{TypeToolAssess, SectionSystemCtx, "senior security engineer"},
		{TypeToolAssess, SectionScoring, "Score each finding"},
		{TypeToolAssess, SectionOutputInstr, "Respond ONLY with valid JSON"},
		{TypeExecSummary, SectionSystemCtx, "principal security architect"},
		{TypeExecSummary, SectionScoring, "Prioritize recommendations"},
		{TypeExecSummary, SectionOutputInstr, "structured_recommendations"},
	}

	for _, tc := range tests {
		t.Run(tc.promptType+"/"+tc.section, func(t *testing.T) {
			got := GetDefault(tc.promptType, tc.section)
			if got == "" {
				t.Fatalf("GetDefault(%q, %q) returned empty string", tc.promptType, tc.section)
			}
			if !strings.Contains(got, tc.contains) {
				t.Errorf("expected to contain %q, got %q", tc.contains, got)
			}
		})
	}
}

func TestGetDefault_UnknownType(t *testing.T) {
	got := GetDefault("unknown_type", SectionSystemCtx)
	if got != "" {
		t.Errorf("expected empty string for unknown type, got %q", got)
	}
}

func TestGetDefault_UnknownSection(t *testing.T) {
	got := GetDefault(TypeToolAssess, "unknown_section")
	if got != "" {
		t.Errorf("expected empty string for unknown section, got %q", got)
	}
}

func TestAllDefaults_ReturnsCopy(t *testing.T) {
	d1 := AllDefaults()
	d2 := AllDefaults()

	// Mutating d1 should not affect d2
	d1[TypeToolAssess][SectionSystemCtx] = "modified"

	if d2[TypeToolAssess][SectionSystemCtx] == "modified" {
		t.Error("AllDefaults should return independent copies")
	}
}

func TestAllDefaults_HasAllTypes(t *testing.T) {
	d := AllDefaults()
	if _, ok := d[TypeToolAssess]; !ok {
		t.Error("missing TypeToolAssess")
	}
	if _, ok := d[TypeExecSummary]; !ok {
		t.Error("missing TypeExecSummary")
	}
}

// mockStore implements PromptStore for testing.
type mockStore struct {
	content string
	err     error
}

func (m *mockStore) ResolvePromptSection(ctx context.Context, promptType, section, collectionID string) (string, error) {
	return m.content, m.err
}

func TestResolve_NilStore(t *testing.T) {
	got := Resolve(context.Background(), nil, TypeToolAssess, SectionSystemCtx, "")
	if got == "" {
		t.Error("Resolve with nil store should return default")
	}
	if !strings.Contains(got, "senior security engineer") {
		t.Error("should return tool assess default system context")
	}
}

func TestResolve_StoreReturnsContent(t *testing.T) {
	store := &mockStore{content: "custom prompt"}
	got := Resolve(context.Background(), store, TypeToolAssess, SectionSystemCtx, "col-1")
	if got != "custom prompt" {
		t.Errorf("expected 'custom prompt', got %q", got)
	}
}

func TestResolve_StoreReturnsEmpty_FallsBackToDefault(t *testing.T) {
	store := &mockStore{content: ""}
	got := Resolve(context.Background(), store, TypeToolAssess, SectionSystemCtx, "")
	if !strings.Contains(got, "senior security engineer") {
		t.Error("should fall back to default when store returns empty")
	}
}

func TestResolve_StoreReturnsError_FallsBackToDefault(t *testing.T) {
	store := &mockStore{content: "", err: context.DeadlineExceeded}
	got := Resolve(context.Background(), store, TypeToolAssess, SectionSystemCtx, "")
	if !strings.Contains(got, "senior security engineer") {
		t.Error("should fall back to default when store returns error")
	}
}

func TestBuildToolAssess(t *testing.T) {
	data := ToolAssessData{
		ToolName:   "gosec",
		RepoName:   "my-repo",
		Languages:  map[string]int{"go": 15},
		Frameworks: []string{"gin"},
		Findings: []FindingData{
			{
				Index:        0,
				Severity:     "high",
				Title:        "SQL Injection",
				Description:  "User input in SQL query",
				FilePath:     "db/query.go",
				LineStart:    42,
				ModuleName:   "db",
				FunctionName: "RunQuery",
				FilePurpose:  "data-access",
				Dependents:   3,
			},
		},
	}

	result := BuildToolAssess("SYSTEM", "SCORING", "OUTPUT", data)

	checks := []string{
		"SYSTEM",
		"gosec",
		"my-repo",
		"go(15)",
		"gin",
		"SQL Injection",
		"db/query.go:42",
		"mod:db",
		"fn:RunQuery",
		"role:data-access",
		"deps:3",
		"SCORING",
		"OUTPUT",
	}

	for _, c := range checks {
		if !strings.Contains(result, c) {
			t.Errorf("expected result to contain %q", c)
		}
	}
}

func TestBuildToolAssess_TruncatesLongDescription(t *testing.T) {
	longDesc := strings.Repeat("x", 100)
	data := ToolAssessData{
		ToolName: "test",
		RepoName: "repo",
		Findings: []FindingData{
			{
				Index:       0,
				Severity:    "low",
				Title:       "Test Finding",
				Description: longDesc,
				FilePath:    "file.go",
				LineStart:   1,
			},
		},
	}

	result := BuildToolAssess("SYS", "SCORE", "OUT", data)
	// The description should be truncated to 80 chars
	if strings.Contains(result, longDesc) {
		t.Error("long description should be truncated")
	}
}

func TestBuildToolAssess_SkipsDuplicateDescTitle(t *testing.T) {
	data := ToolAssessData{
		ToolName: "test",
		RepoName: "repo",
		Findings: []FindingData{
			{
				Index:       0,
				Severity:    "low",
				Title:       "Same Text",
				Description: "Same Text",
				FilePath:    "f.go",
				LineStart:   1,
			},
		},
	}

	result := BuildToolAssess("SYS", "SCORE", "OUT", data)
	// When desc == title, desc is not appended separately
	lines := strings.Split(result, "\n")
	for _, line := range lines {
		if strings.Contains(line, "Same Text") && strings.Contains(line, "| Same Text") {
			t.Error("should not duplicate title as description")
		}
	}
}

func TestBuildExecSummary(t *testing.T) {
	data := ExecSummaryData{
		RepoName:      "my-repo",
		Languages:     map[string]int{"python": 20},
		Frameworks:    []string{"django"},
		TotalFindings: 15,
		BySeverity:    map[string]int{"high": 3, "medium": 12},
		ByTool:        map[string]int{"semgrep": 10, "trivy": 5},
		ToolSummaries: map[string]string{"semgrep": "Found code issues", "trivy": "Dep vulns"},
		TopIssues: []TopIssue{
			{Severity: "high", ContextScore: 9, Title: "SQL injection", Impact: "Data breach"},
		},
	}

	result := BuildExecSummary("SYSTEM", "SCORING", "OUTPUT", data)

	checks := []string{
		"SYSTEM",
		"my-repo",
		"15 findings",
		"python(20)",
		"django",
		"semgrep",
		"trivy",
		"Found code issues",
		"SQL injection",
		"Data breach",
		"9/10",
		"SCORING",
		"OUTPUT",
	}

	for _, c := range checks {
		if !strings.Contains(result, c) {
			t.Errorf("expected result to contain %q", c)
		}
	}
}

func TestBuildExecSummary_MinimalData(t *testing.T) {
	data := ExecSummaryData{
		RepoName:      "minimal",
		TotalFindings: 0,
	}

	result := BuildExecSummary("SYS", "SCORE", "OUT", data)

	if !strings.Contains(result, "minimal") {
		t.Error("should contain repo name")
	}
	if !strings.Contains(result, "0 findings") {
		t.Error("should contain finding count")
	}
}
