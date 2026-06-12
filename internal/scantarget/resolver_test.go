package scantarget

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/secrets"
	"github.com/alphabravocompany/thewolf/internal/sshclient"
)

type fakeSSHRunner struct {
	dirty string
	tar   []byte
}

func (f fakeSSHRunner) Run(ctx context.Context, cfg sshclient.Config, command string) (sshclient.Result, error) {
	if strings.Contains(command, "git rev-parse --is-inside-work-tree") {
		dirty := f.dirty
		if dirty == "" {
			dirty = "clean"
		}
		return sshclient.Result{Stdout: strings.Join([]string{
			"PATH\t/home/alice/code/repo",
			"IS_GIT\ttrue",
			"CURRENT\tfeature",
			"COMMIT\tabc123",
			"DIRTY\t" + dirty,
			"BRANCH\tfeature",
			"BRANCH\tmain",
			"",
		}, "\n")}, nil
	}
	if strings.Contains(command, "git archive") {
		return sshclient.Result{Stdout: base64.StdEncoding.EncodeToString(f.tar)}, nil
	}
	return sshclient.Result{Stdout: "wolf-ok"}, nil
}

func TestResolverPrepareSSHExtractsArchive(t *testing.T) {
	store, repo := setupSSHRepo(t)
	prepared, err := (Resolver{Store: store, Runner: fakeSSHRunner{tar: testTar(t)}}).Prepare(context.Background(), repo, "feature")
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	defer prepared.Cleanup()
	if prepared.SourceType != models.SourceTypeSSH {
		t.Fatalf("SourceType = %q", prepared.SourceType)
	}
	if prepared.CommitSHA != "abc123" || prepared.DirtyState != "clean" {
		t.Fatalf("unexpected provenance: commit=%q dirty=%q", prepared.CommitSHA, prepared.DirtyState)
	}
	if prepared.PreparedWorkspace == "" {
		t.Fatalf("PreparedWorkspace was empty")
	}
	if _, err := os.Stat(filepath.Join(prepared.Path, "main.go")); err != nil {
		t.Fatalf("expected extracted main.go: %v", err)
	}
}

func TestResolverPrepareSSHRejectsDirtyByDefault(t *testing.T) {
	store, repo := setupSSHRepo(t)
	_, err := (Resolver{Store: store, Runner: fakeSSHRunner{dirty: "dirty", tar: testTar(t)}}).Prepare(context.Background(), repo, "feature")
	if err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("Prepare() error = %v, want uncommitted changes error", err)
	}
}

func TestResolverPrepareSSHAllowsDirtyWhenConfigured(t *testing.T) {
	store, repo := setupSSHRepo(t)
	if err := store.SetSetting(context.Background(), "remote_scan_dirty_policy", "allow"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	prepared, err := (Resolver{Store: store, Runner: fakeSSHRunner{dirty: "dirty", tar: testTar(t)}}).Prepare(context.Background(), repo, "feature")
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	defer prepared.Cleanup()
	if prepared.DirtyState != "dirty" {
		t.Fatalf("DirtyState = %q, want dirty", prepared.DirtyState)
	}
}

func setupSSHRepo(t *testing.T) (*db.SQLiteStore, *models.Repo) {
	t.Helper()
	secrets.SetMasterKey(bytes.Repeat([]byte{1}, 32))
	store, err := db.NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	user := &models.User{ID: "user-1", Email: "user@example.com", PasswordHash: "hash"}
	if err := store.CreateUser(context.Background(), user); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	encrypted, err := secrets.Encrypt("not-a-real-key")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	secret := &models.Secret{
		ID:             "secret-1",
		UserID:         user.ID,
		KeyType:        models.KeyTypeSSHPrivate,
		KeyName:        "dev-key",
		EncryptedValue: encrypted,
	}
	if err := store.CreateSecret(context.Background(), secret); err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}
	node := &models.RemoteNode{
		ID:                 "node-1",
		UserID:             user.ID,
		Name:               "dev",
		Host:               "dev.example.com",
		Port:               22,
		Username:           "alice",
		AuthType:           "private_key",
		CredentialSecretID: ptr("secret-1"),
		Enabled:            true,
	}
	if err := store.CreateRemoteNode(context.Background(), node); err != nil {
		t.Fatalf("CreateRemoteNode: %v", err)
	}
	repo := &models.Repo{
		ID:            "repo-1",
		UserID:        user.ID,
		Name:          "repo",
		SourceType:    models.SourceTypeSSH,
		SourcePath:    "/home/alice/code/repo",
		RemoteNodeID:  ptr("node-1"),
		RemotePath:    "/home/alice/code/repo",
		DefaultBranch: "feature",
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}
	if err := store.CreateRepo(context.Background(), repo); err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}
	return store, repo
}

func testTar(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	body := []byte("package main\n")
	if err := tw.WriteHeader(&tar.Header{Name: "main.go", Mode: 0o644, Size: int64(len(body))}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return buf.Bytes()
}

func ptr(s string) *string {
	return &s
}
