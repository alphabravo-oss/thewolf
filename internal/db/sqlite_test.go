package db

import (
	"context"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/google/uuid"
)

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite failed: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestCreateAndGetUser(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	user := &models.User{
		ID:           uuid.New().String(),
		Email:        "test@example.com",
		PasswordHash: "$argon2id$v=19$m=65536,t=1,p=4$fakesalt$fakehash",
	}

	if err := store.CreateUser(ctx, user); err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}

	got, err := store.GetUserByID(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetUserByID failed: %v", err)
	}
	if got.Email != "test@example.com" {
		t.Fatalf("expected email test@example.com, got %s", got.Email)
	}

	got2, err := store.GetUserByEmail(ctx, "test@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmail failed: %v", err)
	}
	if got2.ID != user.ID {
		t.Fatalf("expected ID %s, got %s", user.ID, got2.ID)
	}
}

func TestCreateAndListRepos(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	userID := uuid.New().String()
	user := &models.User{ID: userID, Email: "repo@test.com", PasswordHash: "hash"}
	store.CreateUser(ctx, user)

	repo := &models.Repo{
		ID:            uuid.New().String(),
		UserID:        userID,
		Name:          "test-repo",
		SourceType:    models.SourceTypeLocal,
		SourcePath:    "/tmp/test-repo",
		DefaultBranch: "main",
	}

	if err := store.CreateRepo(ctx, repo); err != nil {
		t.Fatalf("CreateRepo failed: %v", err)
	}

	repos, err := store.ListReposByUser(ctx, userID)
	if err != nil {
		t.Fatalf("ListReposByUser failed: %v", err)
	}
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo, got %d", len(repos))
	}
	if repos[0].Name != "test-repo" {
		t.Fatalf("expected name test-repo, got %s", repos[0].Name)
	}
}

func TestCollectionWithRepos(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	userID := uuid.New().String()
	store.CreateUser(ctx, &models.User{ID: userID, Email: "col@test.com", PasswordHash: "hash"})

	repo1 := &models.Repo{ID: uuid.New().String(), UserID: userID, Name: "repo1", SourceType: models.SourceTypeLocal, SourcePath: "/tmp/r1", DefaultBranch: "main"}
	repo2 := &models.Repo{ID: uuid.New().String(), UserID: userID, Name: "repo2", SourceType: models.SourceTypeGitHub, SourcePath: "org/repo2", DefaultBranch: "main"}
	store.CreateRepo(ctx, repo1)
	store.CreateRepo(ctx, repo2)

	col := &models.Collection{ID: uuid.New().String(), UserID: userID, Name: "my-services", Description: "test collection"}
	if err := store.CreateCollection(ctx, col); err != nil {
		t.Fatalf("CreateCollection failed: %v", err)
	}

	store.AddRepoToCollection(ctx, col.ID, repo1.ID)
	store.AddRepoToCollection(ctx, col.ID, repo2.ID)

	repos, err := store.ListReposInCollection(ctx, col.ID)
	if err != nil {
		t.Fatalf("ListReposInCollection failed: %v", err)
	}
	if len(repos) != 2 {
		t.Fatalf("expected 2 repos, got %d", len(repos))
	}

	// Remove one
	store.RemoveRepoFromCollection(ctx, col.ID, repo1.ID)
	repos, _ = store.ListReposInCollection(ctx, col.ID)
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo after removal, got %d", len(repos))
	}
}

func TestCreateAndGetScan(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	userID := uuid.New().String()
	repoID := uuid.New().String()
	store.CreateUser(ctx, &models.User{ID: userID, Email: "scan@test.com", PasswordHash: "hash"})
	store.CreateRepo(ctx, &models.Repo{ID: repoID, UserID: userID, Name: "scanrepo", SourceType: models.SourceTypeLocal, SourcePath: "/tmp", DefaultBranch: "main"})

	scan := &models.Scan{
		ID:              uuid.New().String(),
		UserID:          userID,
		RepoID:          repoID,
		Branch:          "main",
		Status:          models.ScanStatusPending,
		ToolsSelected:   `["semgrep","trivy"]`,
		ToolsCompleted:  "[]",
		ToolsFailed:     "[]",
		CoverageSummary: "{}",
	}

	if err := store.CreateScan(ctx, scan); err != nil {
		t.Fatalf("CreateScan failed: %v", err)
	}

	got, err := store.GetScanByID(ctx, scan.ID)
	if err != nil {
		t.Fatalf("GetScanByID failed: %v", err)
	}
	if got.Status != models.ScanStatusPending {
		t.Fatalf("expected status pending, got %s", got.Status)
	}
}

func TestCreateAndGetFinding(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	userID := uuid.New().String()
	repoID := uuid.New().String()
	scanID := uuid.New().String()
	store.CreateUser(ctx, &models.User{ID: userID, Email: "find@test.com", PasswordHash: "hash"})
	store.CreateRepo(ctx, &models.Repo{ID: repoID, UserID: userID, Name: "findrepo", SourceType: models.SourceTypeLocal, SourcePath: "/tmp", DefaultBranch: "main"})
	store.CreateScan(ctx, &models.Scan{ID: scanID, UserID: userID, RepoID: repoID, Branch: "main", Status: models.ScanStatusCompleted, ToolsSelected: "[]", ToolsCompleted: "[]", ToolsFailed: "[]", CoverageSummary: "{}"})

	finding := &models.Finding{
		ID:                uuid.New().String(),
		ScanID:            scanID,
		RepoID:            repoID,
		Fingerprint:       "abc123",
		ToolName:          "semgrep",
		Category:          models.CategorySAST,
		Severity:          models.SeverityCritical,
		Title:             "SQL Injection",
		Description:       "User input in SQL query",
		FilePath:          "app.py",
		LineStart:         15,
		LineEnd:           15,
		ToolSeverityScore: 10,
		LocationWeight:    2.0,
		AIContextScore:    8.0,
		CompositeScore:    80.0,
		Status:            models.StatusOpen,
		SARIFData:         "{}",
	}

	if err := store.CreateFinding(ctx, finding); err != nil {
		t.Fatalf("CreateFinding failed: %v", err)
	}

	got, err := store.GetFindingByID(ctx, finding.ID)
	if err != nil {
		t.Fatalf("GetFindingByID failed: %v", err)
	}
	if got.Title != "SQL Injection" {
		t.Fatalf("expected title SQL Injection, got %s", got.Title)
	}
	if got.Severity != models.SeverityCritical {
		t.Fatalf("expected severity critical, got %s", got.Severity)
	}

	// Update status
	store.UpdateFindingStatus(ctx, finding.ID, models.StatusFixed)
	got, _ = store.GetFindingByID(ctx, finding.ID)
	if got.Status != models.StatusFixed {
		t.Fatalf("expected status fixed, got %s", got.Status)
	}
}

func TestScanBaselinesAndComparisons(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	userID := uuid.New().String()
	repoID := uuid.New().String()
	baseScanID := uuid.New().String()
	currentScanID := uuid.New().String()
	store.CreateUser(ctx, &models.User{ID: userID, Email: "baseline@test.com", PasswordHash: "hash"})
	store.CreateRepo(ctx, &models.Repo{ID: repoID, UserID: userID, Name: "baseline-repo", SourceType: models.SourceTypeLocal, SourcePath: "/tmp", DefaultBranch: "main"})
	store.CreateScan(ctx, &models.Scan{ID: baseScanID, UserID: userID, RepoID: repoID, Branch: "main", Status: models.ScanStatusCompleted, ToolsSelected: "[]", ToolsCompleted: "[]", ToolsFailed: "[]", CoverageSummary: "{}"})
	store.CreateScan(ctx, &models.Scan{ID: currentScanID, UserID: userID, RepoID: repoID, Branch: "main", Status: models.ScanStatusCompleted, ToolsSelected: "[]", ToolsCompleted: "[]", ToolsFailed: "[]", CoverageSummary: "{}"})

	baseline := &models.ScanBaseline{
		ID:        uuid.New().String(),
		RepoID:    repoID,
		Branch:    "main",
		Name:      "last-good",
		ScanID:    baseScanID,
		CreatedBy: userID,
	}
	if err := store.CreateScanBaseline(ctx, baseline); err != nil {
		t.Fatalf("CreateScanBaseline failed: %v", err)
	}

	got, err := store.GetScanBaselineByName(ctx, repoID, "main", "last-good")
	if err != nil {
		t.Fatalf("GetScanBaselineByName failed: %v", err)
	}
	if got.ScanID != baseScanID || got.Strategy != "named" {
		t.Fatalf("unexpected baseline: %+v", got)
	}

	baselines, err := store.ListScanBaselines(ctx, repoID, "main")
	if err != nil {
		t.Fatalf("ListScanBaselines failed: %v", err)
	}
	if len(baselines) != 1 {
		t.Fatalf("expected 1 baseline, got %d", len(baselines))
	}

	comparison := &models.ScanComparison{
		ID:             uuid.New().String(),
		RepoID:         repoID,
		BaselineScanID: baseScanID,
		CurrentScanID:  currentScanID,
		SummaryJSON:    `{"new":1}`,
	}
	if err := store.UpsertScanComparison(ctx, comparison); err != nil {
		t.Fatalf("UpsertScanComparison failed: %v", err)
	}
	comparison.SummaryJSON = `{"new":2}`
	if err := store.UpsertScanComparison(ctx, comparison); err != nil {
		t.Fatalf("second UpsertScanComparison failed: %v", err)
	}

	gotComparison, err := store.GetScanComparison(ctx, baseScanID, currentScanID)
	if err != nil {
		t.Fatalf("GetScanComparison failed: %v", err)
	}
	if gotComparison.SummaryJSON != `{"new":2}` {
		t.Fatalf("summary = %s", gotComparison.SummaryJSON)
	}
}

func TestFindingSuppressionsCRUD(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	userID := uuid.New().String()
	repoID := uuid.New().String()
	store.CreateUser(ctx, &models.User{ID: userID, Email: "suppress@test.com", PasswordHash: "hash"})
	store.CreateRepo(ctx, &models.Repo{ID: repoID, UserID: userID, Name: "suppress-repo", SourceType: models.SourceTypeLocal, SourcePath: "/tmp", DefaultBranch: "main"})

	suppression := &models.FindingSuppression{
		ID:         uuid.New().String(),
		RepoID:     repoID,
		CreatedBy:  userID,
		ScopeType:  models.SuppressionScopeRule,
		ScopeValue: "G201",
		Reason:     "legacy accepted risk",
	}
	if err := store.CreateFindingSuppression(ctx, suppression); err != nil {
		t.Fatalf("CreateFindingSuppression failed: %v", err)
	}
	got, err := store.GetFindingSuppressionByID(ctx, suppression.ID)
	if err != nil {
		t.Fatalf("GetFindingSuppressionByID failed: %v", err)
	}
	if got.Status != models.SuppressionStatusActive {
		t.Fatalf("status = %q, want active", got.Status)
	}

	suppressions, err := store.ListFindingSuppressions(ctx, repoID, false)
	if err != nil {
		t.Fatalf("ListFindingSuppressions failed: %v", err)
	}
	if len(suppressions) != 1 {
		t.Fatalf("expected 1 active suppression, got %d", len(suppressions))
	}

	if err := store.CreateFindingSuppressionAudit(ctx, &models.FindingSuppressionAudit{
		ID:            uuid.New().String(),
		SuppressionID: suppression.ID,
		Action:        "created",
		ActorID:       userID,
		DetailsJSON:   "{}",
	}); err != nil {
		t.Fatalf("CreateFindingSuppressionAudit failed: %v", err)
	}
	if err := store.RevokeFindingSuppression(ctx, suppression.ID); err != nil {
		t.Fatalf("RevokeFindingSuppression failed: %v", err)
	}
	suppressions, err = store.ListFindingSuppressions(ctx, repoID, false)
	if err != nil {
		t.Fatalf("ListFindingSuppressions after revoke failed: %v", err)
	}
	if len(suppressions) != 0 {
		t.Fatalf("expected 0 active suppressions after revoke, got %d", len(suppressions))
	}
	suppressions, err = store.ListFindingSuppressions(ctx, repoID, true)
	if err != nil {
		t.Fatalf("ListFindingSuppressions include inactive failed: %v", err)
	}
	if len(suppressions) != 1 || suppressions[0].Status != models.SuppressionStatusRevoked {
		t.Fatalf("unexpected inactive suppressions: %+v", suppressions)
	}
}

func TestQualityPoliciesAndGateResults(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	userID := uuid.New().String()
	repoID := uuid.New().String()
	scanID := uuid.New().String()
	store.CreateUser(ctx, &models.User{ID: userID, Email: "gate@test.com", PasswordHash: "hash"})
	store.CreateRepo(ctx, &models.Repo{ID: repoID, UserID: userID, Name: "gate-repo", SourceType: models.SourceTypeLocal, SourcePath: "/tmp", DefaultBranch: "main"})
	store.CreateScan(ctx, &models.Scan{ID: scanID, UserID: userID, RepoID: repoID, Branch: "main", Status: models.ScanStatusCompleted, ToolsSelected: "[]", ToolsCompleted: "[]", ToolsFailed: "[]", CoverageSummary: "{}"})

	policy := &models.QualityPolicy{
		ID:        uuid.New().String(),
		Name:      "default-security-gate",
		Scope:     "global",
		Mode:      "warn",
		RulesJSON: "[]",
		Enabled:   true,
		CreatedBy: userID,
	}
	if err := store.UpsertQualityPolicy(ctx, policy); err != nil {
		t.Fatalf("UpsertQualityPolicy failed: %v", err)
	}
	policies, err := store.ListQualityPolicies(ctx, "global", "")
	if err != nil {
		t.Fatalf("ListQualityPolicies failed: %v", err)
	}
	if len(policies) != 1 {
		t.Fatalf("expected 1 policy, got %d", len(policies))
	}

	result := &models.QualityGateResult{
		ID:               uuid.New().String(),
		ScanID:           scanID,
		PolicyID:         policy.ID,
		Status:           "fail",
		SummaryJSON:      `{"status":"fail"}`,
		MatchedRulesJSON: "[]",
	}
	if err := store.UpsertQualityGateResult(ctx, result); err != nil {
		t.Fatalf("UpsertQualityGateResult failed: %v", err)
	}
	result.Status = "warn"
	result.SummaryJSON = `{"status":"warn"}`
	if err := store.UpsertQualityGateResult(ctx, result); err != nil {
		t.Fatalf("second UpsertQualityGateResult failed: %v", err)
	}
	got, err := store.GetQualityGateResult(ctx, scanID, policy.ID)
	if err != nil {
		t.Fatalf("GetQualityGateResult failed: %v", err)
	}
	if got.Status != "warn" {
		t.Fatalf("status = %q, want warn", got.Status)
	}
	results, err := store.ListQualityGateResults(ctx, scanID)
	if err != nil {
		t.Fatalf("ListQualityGateResults failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 gate result, got %d", len(results))
	}
}

func TestSecretsCRUD(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	userID := uuid.New().String()
	store.CreateUser(ctx, &models.User{ID: userID, Email: "sec@test.com", PasswordHash: "hash"})

	secret := &models.Secret{
		ID:             uuid.New().String(),
		UserID:         userID,
		KeyType:        models.KeyTypeGitHubToken,
		KeyName:        "github-token",
		EncryptedValue: "encrypted-data-here",
	}

	if err := store.CreateSecret(ctx, secret); err != nil {
		t.Fatalf("CreateSecret failed: %v", err)
	}

	secs, err := store.ListSecretsByUser(ctx, userID)
	if err != nil {
		t.Fatalf("ListSecretsByUser failed: %v", err)
	}
	if len(secs) != 1 {
		t.Fatalf("expected 1 secret, got %d", len(secs))
	}

	// DockerHub credential: PAT stored as the encrypted value, the username
	// reuses KeyName. Confirm the dockerhub_token key_type round-trips.
	dockerSecret := &models.Secret{
		ID:             uuid.New().String(),
		UserID:         userID,
		KeyType:        models.KeyTypeDockerHubToken,
		KeyName:        "alphabravodevops",
		EncryptedValue: "encrypted-dockerhub-pat",
	}
	if err := store.CreateSecret(ctx, dockerSecret); err != nil {
		t.Fatalf("CreateSecret (dockerhub) failed: %v", err)
	}

	secs, err = store.ListSecretsByUser(ctx, userID)
	if err != nil {
		t.Fatalf("ListSecretsByUser failed: %v", err)
	}
	if len(secs) != 2 {
		t.Fatalf("expected 2 secrets, got %d", len(secs))
	}

	var found *models.Secret
	for i := range secs {
		if secs[i].ID == dockerSecret.ID {
			found = &secs[i]
			break
		}
	}
	if found == nil {
		t.Fatal("dockerhub secret not returned by ListSecretsByUser")
	}
	if found.KeyType != models.KeyTypeDockerHubToken {
		t.Fatalf("key_type did not round-trip: got %q, want %q", found.KeyType, models.KeyTypeDockerHubToken)
	}
	if found.KeyName != "alphabravodevops" {
		t.Fatalf("key_name did not round-trip: got %q, want %q", found.KeyName, "alphabravodevops")
	}

	store.DeleteSecret(ctx, dockerSecret.ID)
	store.DeleteSecret(ctx, secret.ID)
	secs, _ = store.ListSecretsByUser(ctx, userID)
	if len(secs) != 0 {
		t.Fatalf("expected 0 secrets after delete, got %d", len(secs))
	}
}

func TestScanArtifactMetadataRoundTrip(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	userID := uuid.New().String()
	store.CreateUser(ctx, &models.User{ID: userID, Email: "artifact@test.com", PasswordHash: "hash"})
	repoID := uuid.New().String()
	store.CreateRepo(ctx, &models.Repo{
		ID:            repoID,
		UserID:        userID,
		Name:          "artifact-repo",
		SourceType:    models.SourceTypeLocal,
		SourcePath:    "/tmp/artifact-repo",
		DefaultBranch: "main",
	})
	scanID := uuid.New().String()
	if err := store.CreateScan(ctx, &models.Scan{
		ID:              scanID,
		UserID:          userID,
		RepoID:          repoID,
		Branch:          "main",
		Status:          models.ScanStatusCompleted,
		ToolsSelected:   "[]",
		ToolsCompleted:  "[]",
		ToolsFailed:     "[]",
		CoverageSummary: "{}",
	}); err != nil {
		t.Fatalf("CreateScan failed: %v", err)
	}

	if err := store.CreateScanArtifact(ctx, &models.ScanArtifact{
		ID:             uuid.New().String(),
		ScanID:         scanID,
		ArtifactType:   models.ArtifactSARIF,
		FilePath:       "/tmp/combined.sarif",
		FileSize:       123,
		ChecksumSHA256: "abc123",
		RedactionLevel: "internal_report",
	}); err != nil {
		t.Fatalf("CreateScanArtifact failed: %v", err)
	}
	if err := store.CreateScanArtifact(ctx, &models.ScanArtifact{
		ID:           uuid.New().String(),
		ScanID:       scanID,
		ArtifactType: models.ArtifactLog,
		FilePath:     "/tmp/gosec.log",
		FileSize:     456,
	}); err != nil {
		t.Fatalf("CreateScanArtifact with default redaction failed: %v", err)
	}

	artifacts, err := store.ListScanArtifacts(ctx, scanID)
	if err != nil {
		t.Fatalf("ListScanArtifacts failed: %v", err)
	}
	if len(artifacts) != 2 {
		t.Fatalf("expected 2 artifacts, got %d", len(artifacts))
	}
	byType := map[models.ArtifactType]models.ScanArtifact{}
	for _, artifact := range artifacts {
		byType[artifact.ArtifactType] = artifact
	}
	if byType[models.ArtifactSARIF].ChecksumSHA256 != "abc123" ||
		byType[models.ArtifactSARIF].RedactionLevel != "internal_report" {
		t.Fatalf("artifact metadata not preserved: %+v", byType[models.ArtifactSARIF])
	}
	if byType[models.ArtifactLog].RedactionLevel != "internal_report" {
		t.Fatalf("default redaction level not applied: %+v", byType[models.ArtifactLog])
	}
}

func TestSARIFImportMetadataRoundTrip(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	userID := uuid.New().String()
	store.CreateUser(ctx, &models.User{ID: userID, Email: "sarif@test.com", PasswordHash: "hash"})
	repoID := uuid.New().String()
	store.CreateRepo(ctx, &models.Repo{
		ID:            repoID,
		UserID:        userID,
		Name:          "sarif-repo",
		SourceType:    models.SourceTypeLocal,
		SourcePath:    "/tmp/sarif-repo",
		DefaultBranch: "main",
	})
	scanID := uuid.New().String()
	store.CreateScan(ctx, &models.Scan{
		ID:              scanID,
		UserID:          userID,
		RepoID:          repoID,
		Branch:          "main",
		Status:          models.ScanStatusCompleted,
		ToolsSelected:   "[]",
		ToolsCompleted:  "[]",
		ToolsFailed:     "[]",
		CoverageSummary: "{}",
	})

	imp := &models.SARIFImport{
		ID:             uuid.New().String(),
		RepoID:         repoID,
		ScanID:         scanID,
		Source:         "github-code-scanning",
		ChecksumSHA256: "sha256",
		ResultCount:    2,
		ImportedCount:  2,
		CreatedBy:      userID,
	}
	if err := store.CreateSARIFImport(ctx, imp); err != nil {
		t.Fatalf("CreateSARIFImport failed: %v", err)
	}
	imports, err := store.ListSARIFImportsByRepo(ctx, repoID)
	if err != nil {
		t.Fatalf("ListSARIFImportsByRepo failed: %v", err)
	}
	if len(imports) != 1 || imports[0].ScanID != scanID || imports[0].ChecksumSHA256 != "sha256" {
		t.Fatalf("unexpected imports: %+v", imports)
	}
}

func TestScannerRunRecordUpsertRoundTrip(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	userID := uuid.New().String()
	store.CreateUser(ctx, &models.User{ID: userID, Email: "scanner-run@test.com", PasswordHash: "hash"})
	repoID := uuid.New().String()
	store.CreateRepo(ctx, &models.Repo{
		ID:            repoID,
		UserID:        userID,
		Name:          "scanner-run-repo",
		SourceType:    models.SourceTypeLocal,
		SourcePath:    "/tmp/scanner-run-repo",
		DefaultBranch: "main",
	})
	scanID := uuid.New().String()
	store.CreateScan(ctx, &models.Scan{
		ID:              scanID,
		UserID:          userID,
		RepoID:          repoID,
		Branch:          "main",
		Status:          models.ScanStatusCompleted,
		ToolsSelected:   "[]",
		ToolsCompleted:  "[]",
		ToolsFailed:     "[]",
		CoverageSummary: "{}",
	})
	started := time.Now().UTC()
	if err := store.UpsertScannerRunRecord(ctx, &models.ScannerRunRecord{
		ID:           uuid.New().String(),
		ScanID:       scanID,
		ToolName:     "gosec",
		Status:       "running",
		Category:     "sast",
		Image:        "wolf-scanners:latest",
		CommandJSON:  "{}",
		ParserStatus: "pending",
		StartedAt:    &started,
	}); err != nil {
		t.Fatalf("UpsertScannerRunRecord running failed: %v", err)
	}
	finished := started.Add(2 * time.Second)
	if err := store.UpsertScannerRunRecord(ctx, &models.ScannerRunRecord{
		ID:           uuid.New().String(),
		ScanID:       scanID,
		ToolName:     "gosec",
		Status:       "completed",
		Category:     "sast",
		Image:        "wolf-scanners:latest",
		CommandJSON:  "{}",
		DurationMS:   2000,
		FindingCount: 3,
		ParserStatus: "parsed",
		StartedAt:    &started,
		FinishedAt:   &finished,
	}); err != nil {
		t.Fatalf("UpsertScannerRunRecord completed failed: %v", err)
	}
	records, err := store.ListScannerRunRecords(ctx, scanID)
	if err != nil {
		t.Fatalf("ListScannerRunRecords failed: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	got := records[0]
	if got.Status != "completed" || got.DurationMS != 2000 || got.FindingCount != 3 || got.ParserStatus != "parsed" {
		t.Fatalf("unexpected scanner run record: %+v", got)
	}
	if got.StartedAt == nil || got.FinishedAt == nil {
		t.Fatalf("expected start and finish timestamps: %+v", got)
	}
}

// TestDeleteRepoCascadeWithAILogs is a regression test: ai_logs references
// scans(id) without ON DELETE CASCADE, so DeleteScanCascade must delete
// ai_logs rows explicitly. Before the fix, a repo with AI-assessed scans
// could not be deleted — the DELETE FROM scans hit a foreign-key violation.
func TestDeleteRepoCascadeWithAILogs(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	userID := uuid.New().String()
	store.CreateUser(ctx, &models.User{ID: userID, Email: "del@test.com", PasswordHash: "hash"})

	repoID := uuid.New().String()
	store.CreateRepo(ctx, &models.Repo{
		ID: repoID, UserID: userID, Name: "delrepo",
		SourceType: models.SourceTypeLocal, SourcePath: "/tmp/delrepo", DefaultBranch: "main",
	})

	scanID := uuid.New().String()
	if err := store.CreateScan(ctx, &models.Scan{
		ID: scanID, UserID: userID, RepoID: repoID, Branch: "main",
		Status:        models.ScanStatusCompleted,
		ToolsSelected: "[]", ToolsCompleted: "[]", ToolsFailed: "[]", CoverageSummary: "{}",
	}); err != nil {
		t.Fatalf("CreateScan failed: %v", err)
	}

	// An AI log row pointing at the scan — the FK that blocked deletion.
	if err := store.CreateAILog(ctx, &models.AILog{
		ID: uuid.New().String(), ScanID: scanID,
		Provider: "anthropic", Phase: "assessment", Prompt: "p",
	}); err != nil {
		t.Fatalf("CreateAILog failed: %v", err)
	}

	scanIDs, err := store.DeleteRepoCascade(ctx, repoID)
	if err != nil {
		t.Fatalf("DeleteRepoCascade failed: %v", err)
	}
	if len(scanIDs) != 1 || scanIDs[0] != scanID {
		t.Fatalf("expected cascade to report scan %s, got %v", scanID, scanIDs)
	}

	if _, err := store.GetRepoByID(ctx, repoID); err == nil {
		t.Fatal("expected repo to be gone after cascade delete")
	}
}
