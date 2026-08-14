package scantarget

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/secrets"
)

func TestParseGitHubSource(t *testing.T) {
	cases := []struct {
		in        string
		wantOwner string
		wantName  string
		wantErr   bool
	}{
		{"alphabravo-oss/thewolf", "alphabravo-oss", "thewolf", false},
		{"github.com/alphabravo-oss/thewolf", "alphabravo-oss", "thewolf", false},
		{"https://github.com/alphabravo-oss/thewolf", "alphabravo-oss", "thewolf", false},
		{"https://github.com/alphabravo-oss/thewolf.git", "alphabravo-oss", "thewolf", false},
		{"http://github.com/alphabravo-oss/thewolf", "alphabravo-oss", "thewolf", false},
		{"git@github.com:alphabravo-oss/thewolf.git", "alphabravo-oss", "thewolf", false},
		{"  alphabravo-oss/thewolf  ", "alphabravo-oss", "thewolf", false},

		{"", "", "", true},
		{"alphabravo-oss", "", "", true},
		{"alphabravo-oss/thewolf/extra", "", "", true},
		{"alphabravo-oss/ ", "", "", true},
		{"alphabravo oss/thewolf", "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			owner, name, err := ParseGitHubSource(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if owner != tc.wantOwner || name != tc.wantName {
				t.Errorf("got %s/%s; want %s/%s", owner, name, tc.wantOwner, tc.wantName)
			}
		})
	}
}

// TestPrepareGitHubPublic — a repo with no github_token secret falls through
// to an unauthenticated clone (the public-repo path).
func TestPrepareGitHubPublic(t *testing.T) {
	store, err := db.NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	user := &models.User{ID: "u1", Email: "u@e.test", PasswordHash: "x"}
	if err := store.CreateUser(context.Background(), user); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	var got struct {
		owner, name, branch, token string
	}
	cloner := func(owner, name, branch, token string) (string, error) {
		got.owner, got.name, got.branch, got.token = owner, name, branch, token
		// Return a synthetic path so localGitState's git invocations are
		// harmless (no .git dir → empty SHA / unknown dirty).
		return t.TempDir(), nil
	}

	r := Resolver{Store: store, GitHubCloner: cloner}
	repo := &models.Repo{
		ID: "r1", UserID: user.ID,
		SourceType:    models.SourceTypeGitHub,
		SourcePath:    "alphabravo-oss/thewolf",
		DefaultBranch: "main",
	}

	prep, err := r.Prepare(context.Background(), repo, "")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if got.owner != "alphabravo-oss" || got.name != "thewolf" {
		t.Errorf("cloner saw %s/%s, want alphabravo-oss/thewolf", got.owner, got.name)
	}
	if got.branch != "main" {
		t.Errorf("default_branch was not used; branch=%q", got.branch)
	}
	if got.token != "" {
		t.Errorf("expected empty token for public repo, got %q", got.token)
	}
	if prep.SourceType != models.SourceTypeGitHub {
		t.Errorf("SourceType not propagated: %v", prep.SourceType)
	}
	if prep.Cleanup == nil {
		t.Error("Cleanup must be non-nil even when cache is reused")
	}

	syncPrep, err := r.Sync(context.Background(), repo, "release")
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if got.branch != "release" {
		t.Errorf("Sync branch = %q, want release", got.branch)
	}
	if syncPrep.SourceType != models.SourceTypeGitHub {
		t.Errorf("Sync SourceType = %v", syncPrep.SourceType)
	}
}

// TestPrepareGitHubPrivate — a github_token secret for the repo's user is
// decrypted and passed to the cloner.
func TestPrepareGitHubPrivate(t *testing.T) {
	store, err := db.NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	t.Setenv("WOLF_MASTER_KEY", strings.Repeat("ab", 32))
	if err := secrets.LoadMasterKey(); err != nil {
		t.Fatalf("LoadMasterKey: %v", err)
	}

	user := &models.User{ID: "u1", Email: "u@e.test", PasswordHash: "x"}
	if err := store.CreateUser(context.Background(), user); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	const plaintext = "ghp_supersecret_token_value"
	enc, err := secrets.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if err := store.CreateSecret(context.Background(), &models.Secret{
		ID: "s1", UserID: user.ID,
		KeyType:        models.KeyTypeGitHubToken,
		KeyName:        "default",
		EncryptedValue: enc,
	}); err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}

	var seenToken string
	cloner := func(_, _, _, token string) (string, error) {
		seenToken = token
		return t.TempDir(), nil
	}

	r := Resolver{Store: store, GitHubCloner: cloner}
	repo := &models.Repo{
		ID: "r1", UserID: user.ID,
		SourceType: models.SourceTypeGitHub,
		SourcePath: "https://github.com/private-org/private-repo.git",
	}

	if _, err := r.Prepare(context.Background(), repo, "develop"); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if seenToken != plaintext {
		t.Errorf("cloner saw token %q, want %q", seenToken, plaintext)
	}
}

func TestPrepareGitHubUsesSelectedCredential(t *testing.T) {
	store, err := db.NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	t.Setenv("WOLF_MASTER_KEY", strings.Repeat("cd", 32))
	if err := secrets.LoadMasterKey(); err != nil {
		t.Fatalf("LoadMasterKey: %v", err)
	}

	user := &models.User{ID: "u1", Email: "u@e.test", PasswordHash: "x"}
	if err := store.CreateUser(context.Background(), user); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	for id, token := range map[string]string{
		"first":    "ghp_first_token",
		"selected": "ghp_selected_token",
	} {
		enc, err := secrets.Encrypt(token)
		if err != nil {
			t.Fatalf("Encrypt(%s): %v", id, err)
		}
		if err := store.CreateSecret(context.Background(), &models.Secret{
			ID:             id,
			UserID:         user.ID,
			KeyType:        models.KeyTypeGitHubToken,
			KeyName:        id,
			EncryptedValue: enc,
		}); err != nil {
			t.Fatalf("CreateSecret(%s): %v", id, err)
		}
	}

	var seenToken string
	cloner := func(_, _, _, token string) (string, error) {
		seenToken = token
		return t.TempDir(), nil
	}

	r := Resolver{Store: store, GitHubCloner: cloner}
	repo := &models.Repo{
		ID: "r1", UserID: user.ID,
		SourceType:         models.SourceTypeGitHub,
		SourcePath:         "private-org/private-repo",
		CredentialSecretID: "selected",
	}

	if _, err := r.Prepare(context.Background(), repo, "main"); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if seenToken != "ghp_selected_token" {
		t.Errorf("cloner saw token %q, want selected token", seenToken)
	}
}

// TestPrepareGitHubBadSource — a malformed SourcePath is rejected without
// calling the cloner.
func TestPrepareGitHubBadSource(t *testing.T) {
	called := false
	cloner := func(string, string, string, string) (string, error) {
		called = true
		return "", nil
	}
	r := Resolver{GitHubCloner: cloner}
	repo := &models.Repo{
		ID: "r1", UserID: "u1",
		SourceType: models.SourceTypeGitHub,
		SourcePath: "not a github source",
	}
	if _, err := r.Prepare(context.Background(), repo, ""); err == nil {
		t.Fatal("expected error for malformed source")
	}
	if called {
		t.Error("cloner must not be invoked when source parsing fails")
	}
}

// TestPrepareGitHubCloneError — wraps the cloner's error without leaking
// the token.
func TestPrepareGitHubCloneError(t *testing.T) {
	cloner := func(string, string, string, string) (string, error) {
		return "", fmt.Errorf("authentication required")
	}
	r := Resolver{GitHubCloner: cloner}
	repo := &models.Repo{
		ID: "r1", UserID: "u1",
		SourceType: models.SourceTypeGitHub,
		SourcePath: "owner/repo",
	}
	_, err := r.Prepare(context.Background(), repo, "")
	if err == nil {
		t.Fatal("expected clone error to propagate")
	}
	if !strings.Contains(err.Error(), "github.com/owner/repo") {
		t.Errorf("error should identify the repo, got: %v", err)
	}
}
