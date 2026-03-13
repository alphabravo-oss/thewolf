package models

import (
	"encoding/json"
	"testing"
	"time"
)

func TestSeverityConstants(t *testing.T) {
	tests := []struct {
		severity Severity
		want     string
	}{
		{SeverityCritical, "critical"},
		{SeverityHigh, "high"},
		{SeverityMedium, "medium"},
		{SeverityLow, "low"},
		{SeverityInfo, "info"},
	}
	for _, tt := range tests {
		if string(tt.severity) != tt.want {
			t.Errorf("Severity %v = %q, want %q", tt.severity, string(tt.severity), tt.want)
		}
	}
}

func TestSeverityOrdering(t *testing.T) {
	// Verify severity ordering by numeric ranking convention
	order := []Severity{SeverityInfo, SeverityLow, SeverityMedium, SeverityHigh, SeverityCritical}
	rank := func(s Severity) int {
		switch s {
		case SeverityCritical:
			return 5
		case SeverityHigh:
			return 4
		case SeverityMedium:
			return 3
		case SeverityLow:
			return 2
		case SeverityInfo:
			return 1
		default:
			return 0
		}
	}

	for i := 1; i < len(order); i++ {
		if rank(order[i]) <= rank(order[i-1]) {
			t.Errorf("expected rank(%s) > rank(%s)", order[i], order[i-1])
		}
	}
}

func TestCategoryConstants(t *testing.T) {
	tests := []struct {
		cat  Category
		want string
	}{
		{CategorySAST, "sast"},
		{CategorySCA, "sca"},
		{CategorySecrets, "secrets"},
		{CategoryQuality, "quality"},
		{CategoryContainer, "container"},
		{CategoryDocs, "docs"},
		{CategoryLicense, "license"},
		{CategorySBOM, "sbom"},
	}
	for _, tt := range tests {
		if string(tt.cat) != tt.want {
			t.Errorf("Category %v = %q, want %q", tt.cat, string(tt.cat), tt.want)
		}
	}
}

func TestStatusConstants(t *testing.T) {
	tests := []struct {
		s    Status
		want string
	}{
		{StatusOpen, "open"},
		{StatusFixed, "fixed"},
		{StatusWontFix, "wont_fix"},
		{StatusFalsePositive, "false_positive"},
	}
	for _, tt := range tests {
		if string(tt.s) != tt.want {
			t.Errorf("Status %v = %q, want %q", tt.s, string(tt.s), tt.want)
		}
	}
}

func TestScanStatusConstants(t *testing.T) {
	tests := []struct {
		s    ScanStatus
		want string
	}{
		{ScanStatusPending, "pending"},
		{ScanStatusRunning, "running"},
		{ScanStatusCompleted, "completed"},
		{ScanStatusFailed, "failed"},
		{ScanStatusCancelled, "cancelled"},
	}
	for _, tt := range tests {
		if string(tt.s) != tt.want {
			t.Errorf("ScanStatus %v = %q, want %q", tt.s, string(tt.s), tt.want)
		}
	}
}

func TestFixStatusConstants(t *testing.T) {
	tests := []struct {
		s    FixStatus
		want string
	}{
		{FixStatusPending, "pending"},
		{FixStatusRunning, "running"},
		{FixStatusCompleted, "completed"},
		{FixStatusFailed, "failed"},
		{FixStatusCancelled, "cancelled"},
	}
	for _, tt := range tests {
		if string(tt.s) != tt.want {
			t.Errorf("FixStatus %v = %q, want %q", tt.s, string(tt.s), tt.want)
		}
	}
}

func TestFixItemStatusConstants(t *testing.T) {
	tests := []struct {
		s    FixItemStatus
		want string
	}{
		{FixItemStatusPending, "pending"},
		{FixItemStatusInProgress, "in_progress"},
		{FixItemStatusFixed, "fixed"},
		{FixItemStatusFailed, "failed"},
		{FixItemStatusSkipped, "skipped"},
	}
	for _, tt := range tests {
		if string(tt.s) != tt.want {
			t.Errorf("FixItemStatus %v = %q, want %q", tt.s, string(tt.s), tt.want)
		}
	}
}

func TestLoopStatusConstants(t *testing.T) {
	tests := []struct {
		s    LoopStatus
		want string
	}{
		{LoopStatusRunning, "running"},
		{LoopStatusPaused, "paused"},
		{LoopStatusCompleted, "completed"},
		{LoopStatusStopped, "stopped"},
		{LoopStatusFailed, "failed"},
	}
	for _, tt := range tests {
		if string(tt.s) != tt.want {
			t.Errorf("LoopStatus %v = %q, want %q", tt.s, string(tt.s), tt.want)
		}
	}
}

func TestSourceTypeConstants(t *testing.T) {
	tests := []struct {
		s    SourceType
		want string
	}{
		{SourceTypeLocal, "local"},
		{SourceTypeGitHub, "github"},
		{SourceTypeGitLab, "gitlab"},
		{SourceTypeGit, "git"},
	}
	for _, tt := range tests {
		if string(tt.s) != tt.want {
			t.Errorf("SourceType %v = %q, want %q", tt.s, string(tt.s), tt.want)
		}
	}
}

func TestLanguageConstants(t *testing.T) {
	languages := []Language{
		LangPython, LangJavaScript, LangTypeScript, LangGo, LangRust,
		LangJava, LangRuby, LangPHP, LangC, LangCPP, LangShell,
	}
	for _, l := range languages {
		if string(l) == "" {
			t.Error("language constant should not be empty")
		}
	}
}

func TestRescanStrategyConstants(t *testing.T) {
	tests := []struct {
		s    RescanStrategy
		want string
	}{
		{RescanFull, "full"},
		{RescanTargeted, "targeted"},
		{RescanSmart, "smart"},
	}
	for _, tt := range tests {
		if string(tt.s) != tt.want {
			t.Errorf("RescanStrategy %v = %q, want %q", tt.s, string(tt.s), tt.want)
		}
	}
}

func TestValidationResultConstants(t *testing.T) {
	if string(ValidationPass) != "pass" {
		t.Error("ValidationPass wrong")
	}
	if string(ValidationFail) != "fail" {
		t.Error("ValidationFail wrong")
	}
}

func TestArtifactTypeConstants(t *testing.T) {
	tests := []struct {
		a    ArtifactType
		want string
	}{
		{ArtifactSARIF, "sarif"},
		{ArtifactJSON, "json"},
		{ArtifactMarkdown, "markdown"},
		{ArtifactLog, "log"},
		{ArtifactCoverage, "coverage"},
	}
	for _, tt := range tests {
		if string(tt.a) != tt.want {
			t.Errorf("ArtifactType %v = %q, want %q", tt.a, string(tt.a), tt.want)
		}
	}
}

func TestKeyTypeConstants(t *testing.T) {
	tests := []struct {
		k    KeyType
		want string
	}{
		{KeyTypeGitHubToken, "github_token"},
		{KeyTypeGitLabToken, "gitlab_token"},
		{KeyTypeAnthropicKey, "anthropic_key"},
		{KeyTypeOpenAIKey, "openai_key"},
		{KeyTypeCustom, "custom"},
	}
	for _, tt := range tests {
		if string(tt.k) != tt.want {
			t.Errorf("KeyType %v = %q, want %q", tt.k, string(tt.k), tt.want)
		}
	}
}

func TestFindingJSONSerialization(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	f := Finding{
		ID:              "f-123",
		ScanID:          "s-456",
		RepoID:          "r-789",
		Fingerprint:     "abc123",
		ToolName:        "semgrep",
		Category:        CategorySAST,
		Severity:        SeverityHigh,
		Title:           "SQL Injection",
		Description:     "User input in query",
		FilePath:        "db.go",
		LineStart:       42,
		LineEnd:         42,
		CompositeScore:  8.5,
		Status:          StatusOpen,
		AIFixSuggestion: "Use parameterized queries",
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	data, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded Finding
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.ID != f.ID {
		t.Errorf("ID = %q, want %q", decoded.ID, f.ID)
	}
	if decoded.Severity != f.Severity {
		t.Errorf("Severity = %q, want %q", decoded.Severity, f.Severity)
	}
	if decoded.Category != f.Category {
		t.Errorf("Category = %q, want %q", decoded.Category, f.Category)
	}
	if decoded.CompositeScore != f.CompositeScore {
		t.Errorf("CompositeScore = %f, want %f", decoded.CompositeScore, f.CompositeScore)
	}
	if decoded.AIFixSuggestion != f.AIFixSuggestion {
		t.Errorf("AIFixSuggestion = %q, want %q", decoded.AIFixSuggestion, f.AIFixSuggestion)
	}
}

func TestFindingOmitEmpty(t *testing.T) {
	f := Finding{
		ID:        "f-1",
		Title:     "Test",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	data, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	json.Unmarshal(data, &raw)

	// cwe_id and rule_id should be omitted when empty
	if _, ok := raw["cwe_id"]; ok {
		t.Error("expected cwe_id to be omitted")
	}
	if _, ok := raw["rule_id"]; ok {
		t.Error("expected rule_id to be omitted")
	}
}

func TestScanJSONSerialization(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	colID := "col-1"
	s := Scan{
		ID:           "s-1",
		UserID:       "u-1",
		RepoID:       "r-1",
		CollectionID: &colID,
		Branch:       "main",
		Status:       ScanStatusRunning,
		FindingCount: 5,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded Scan
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.ID != s.ID {
		t.Errorf("ID = %q, want %q", decoded.ID, s.ID)
	}
	if decoded.Status != ScanStatusRunning {
		t.Errorf("Status = %q, want %q", decoded.Status, ScanStatusRunning)
	}
	if decoded.CollectionID == nil || *decoded.CollectionID != colID {
		t.Error("CollectionID not preserved")
	}
}

func TestRepoJSONSerialization(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	r := Repo{
		ID:            "r-1",
		UserID:        "u-1",
		Name:          "my-repo",
		SourceType:    SourceTypeGitHub,
		SourcePath:    "https://github.com/org/repo",
		DefaultBranch: "main",
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded Repo
	json.Unmarshal(data, &decoded)

	if decoded.SourceType != SourceTypeGitHub {
		t.Errorf("SourceType = %q, want %q", decoded.SourceType, SourceTypeGitHub)
	}
}

func TestFixJSONSerialization(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	f := Fix{
		ID:                "fix-1",
		UserID:            "u-1",
		ScanID:            "s-1",
		Status:            FixStatusCompleted,
		FindingsAttempted: 10,
		FindingsFixed:     8,
		FindingsFailed:    2,
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	data, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded Fix
	json.Unmarshal(data, &decoded)

	if decoded.FindingsFixed != 8 {
		t.Errorf("FindingsFixed = %d, want 8", decoded.FindingsFixed)
	}
	if decoded.Status != FixStatusCompleted {
		t.Errorf("Status = %q, want %q", decoded.Status, FixStatusCompleted)
	}
}

func TestLoopJSONSerialization(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	l := Loop{
		ID:              "loop-1",
		UserID:          "u-1",
		RepoID:          "r-1",
		Status:          LoopStatusRunning,
		MaxIterations:   5,
		RescanStrategy:  RescanSmart,
		SeverityFilter:  "critical,high",
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	data, err := json.Marshal(l)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded Loop
	json.Unmarshal(data, &decoded)

	if decoded.MaxIterations != 5 {
		t.Errorf("MaxIterations = %d, want 5", decoded.MaxIterations)
	}
	if decoded.RescanStrategy != RescanSmart {
		t.Errorf("RescanStrategy = %q, want %q", decoded.RescanStrategy, RescanSmart)
	}
}

func TestSecretJSONOmitsEncryptedValue(t *testing.T) {
	s := Secret{
		ID:             "sec-1",
		UserID:         "u-1",
		KeyType:        KeyTypeGitHubToken,
		KeyName:        "github",
		EncryptedValue: "super-secret-encrypted",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// EncryptedValue has json:"-" tag so should not appear
	str := string(data)
	if contains(str, "super-secret-encrypted") {
		t.Error("EncryptedValue should not be in JSON output")
	}
	if contains(str, "encrypted_value") {
		t.Error("encrypted_value key should not be in JSON output")
	}
}

func TestUserJSONOmitsPasswordHash(t *testing.T) {
	u := User{
		ID:           "u-1",
		Email:        "test@test.com",
		PasswordHash: "argon2id$secret",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	data, err := json.Marshal(u)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	str := string(data)
	if contains(str, "argon2id$secret") {
		t.Error("PasswordHash should not be in JSON output")
	}
}

func contains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
