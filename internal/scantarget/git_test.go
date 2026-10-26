package scantarget

import (
	"context"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/secrets"
	"os"
	"path/filepath"
	"testing"
)

func TestCanonicalGitURLAllowsHTTPSAndSSHOnly(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		host    string
		wantErr bool
	}{
		{"https://Git.Example.com/acme/repo.git/", "https://git.example.com/acme/repo.git", "git.example.com", false},
		{"git@git.example.com:acme/repo.git", "ssh://git@git.example.com/acme/repo.git", "git.example.com", false},
		{"ssh://git@git.example.com/acme/repo.git", "ssh://git@git.example.com/acme/repo.git", "git.example.com", false},
		{"ssh://git@git.example.com:22/acme/repo.git", "ssh://git@git.example.com/acme/repo.git", "git.example.com", false},
		{"https://git.example.com:443/acme/repo.git", "https://git.example.com/acme/repo.git", "git.example.com", false},
		{"file:///tmp/repo", "", "", true},
		{"git://git.example.com/repo", "", "", true},
		{"https://token@git.example.com/repo", "", "", true},
		{"https://user:secret@git.example.com/repo", "", "", true},
		{"ssh://git.example.com/repo", "", "", true},
		{"ssh://-oProxyCommand@git.example.com/repo", "", "", true},
		{"ssh://git@-bad.example.com/repo", "", "", true},
		{"https://git.example.com", "", "", true},
		{"/tmp/repo", "", "", true},
	}
	for _, test := range tests {
		got, host, err := CanonicalGitURL(test.input)
		if (err != nil) != test.wantErr {
			t.Fatalf("CanonicalGitURL(%q) err=%v wantErr=%v", test.input, err, test.wantErr)
		}
		if !test.wantErr && (got != test.want || host != test.host) {
			t.Fatalf("CanonicalGitURL(%q)=(%q,%q), want (%q,%q)", test.input, got, host, test.want, test.host)
		}
	}
}

func TestValidateGitDestinationBlocksLocalhost(t *testing.T) {
	if err := ValidateRemoteDestination(context.Background(), "localhost"); err == nil {
		t.Fatal("expected localhost to be blocked")
	}
	if err := ValidateRemoteDestination(context.Background(), "127.0.0.1"); err == nil {
		t.Fatal("expected loopback IP to be blocked")
	}
}

func TestGitPreparationAndRetainedWorkspaceHaveSameContentIdentity(t *testing.T) {
	source, _ := gitStateFixture(t)
	store, owner := setupSSHRepo(t)
	encrypted, err := secrets.Encrypt("fixture-private-key")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSecret(t.Context(), &models.Secret{ID: "git-fixture-key", UserID: owner.UserID, KeyType: models.KeyTypeSSHPrivate, KeyName: "fixture", EncryptedValue: encrypted, AllowedHosts: `["192.0.2.1"]`, MetadataJSON: `{"known_hosts":"fixture-host-key"}`}); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	// Real Git fetch/checkout over a local upload-pack. The transport executable
	// replaces SSH so this test never contacts the documentation-only IP.
	script := "#!/bin/sh\nfor arg do [ \"$arg\" = -G ] && exit 1; done\nexec git upload-pack \"$WOLF_TEST_GIT_SOURCE\"\n"
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("WOLF_TEST_GIT_SOURCE", source)
	t.Setenv("WOLF_WORKSPACE_ROOT", t.TempDir())
	repo := &models.Repo{SourceType: models.SourceTypeGit, SourcePath: "ssh://git@192.0.2.1/repo", UserID: owner.UserID, CredentialSecretID: "git-fixture-key", DefaultBranch: "main"}
	prepared, err := (Resolver{Store: store}).Prepare(t.Context(), repo, "main")
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Cleanup()
	for _, name := range []string{".wolf-git-key", ".wolf-known-hosts"} {
		if _, err := os.Stat(filepath.Join(prepared.Path, name)); !os.IsNotExist(err) {
			t.Fatalf("preparation credential remained: %v", err)
		}
	}
	retained, err := PrepareExisting(t.Context(), prepared.Path, prepared.SourceType)
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Cleanup()
	if prepared.TreeDigest == "" || prepared.TreeDigest != retained.TreeDigest || prepared.CommitSHA != retained.CommitSHA {
		t.Fatalf("source identity changed on reuse: %q -> %q", prepared.TreeDigest, retained.TreeDigest)
	}
}
