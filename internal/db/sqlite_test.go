package db

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/alphabravocompany/thewolf/internal/models"
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
		ID:             uuid.New().String(),
		UserID:         userID,
		RepoID:         repoID,
		Branch:         "main",
		Status:         models.ScanStatusPending,
		ToolsSelected:  `["semgrep","trivy"]`,
		ToolsCompleted: "[]",
		ToolsFailed:    "[]",
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

	store.DeleteSecret(ctx, secret.ID)
	secs, _ = store.ListSecretsByUser(ctx, userID)
	if len(secs) != 0 {
		t.Fatalf("expected 0 secrets after delete, got %d", len(secs))
	}
}
