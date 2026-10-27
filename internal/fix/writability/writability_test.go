package writability

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/secrets"
)

// TestMain loads a fixed master key so Encrypt/Decrypt work in the github/ssh
// secret-resolution paths without touching ~/.wolf or real keys.
func TestMain(m *testing.M) {
	secrets.SetMasterKey(bytes.Repeat([]byte{0x42}, 32))
	os.Exit(m.Run())
}

// --- stub probes -----------------------------------------------------------

type stubLocal struct {
	writable bool
	reason   string
	err      error
}

func (s stubLocal) Check(context.Context, string) (bool, string, error) {
	return s.writable, s.reason, s.err
}

type stubGitHub struct {
	info GitHubPushInfo
	err  error
	// captured args, so tests can assert the token/owner/repo wiring.
	gotToken, gotOwner, gotRepo string
}

func (s *stubGitHub) PushInfo(_ context.Context, token, owner, repo string) (GitHubPushInfo, error) {
	s.gotToken, s.gotOwner, s.gotRepo = token, owner, repo
	return s.info, s.err
}

type stubSSH struct {
	ok     bool
	reason string
	err    error
}

func (s stubSSH) CanPush(context.Context, *models.RemoteNode, string) (bool, string, error) {
	return s.ok, s.reason, s.err
}

func okParse(raw string) (string, string, error) { return "acme", "widget", nil }

// stubStore is a tiny db.Store that only answers the two methods the
// writability preflight calls: ListSecretsByUser and GetRemoteNodeByID.
type stubStore struct {
	db.Store
	secrets []models.Secret
	node    *models.RemoteNode
	nodeErr error
}

func (s stubStore) ListSecretsByUser(context.Context, string) ([]models.Secret, error) {
	return s.secrets, nil
}

func (s stubStore) GetRemoteNodeByID(_ context.Context, _ string) (*models.RemoteNode, error) {
	if s.nodeErr != nil {
		return nil, s.nodeErr
	}
	return s.node, nil
}

func (s stubStore) GetSecretByID(context.Context, string) (*models.Secret, error) {
	return nil, errors.New("not found")
}

type credStore struct {
	stubStore
	byID map[string]models.Secret
}

func (s credStore) GetSecretByID(_ context.Context, id string) (*models.Secret, error) {
	sec, ok := s.byID[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return &sec, nil
}

func ghSecret(t *testing.T, value string) models.Secret {
	t.Helper()
	enc, err := secrets.Encrypt(value)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	return models.Secret{UserID: "u1", KeyType: models.KeyTypeGitHubToken, EncryptedValue: enc}
}

// --- local -----------------------------------------------------------------

func TestCheck_Local(t *testing.T) {
	tests := []struct {
		name      string
		probe     stubLocal
		wantWrite bool
	}{
		{"writable git tree", stubLocal{writable: true}, true},
		{"not writable", stubLocal{writable: false, reason: "local path is not writable"}, false},
		{"probe error", stubLocal{err: errors.New("stat boom")}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &models.Repo{SourceType: models.SourceTypeLocal, SourcePath: "/x"}
			res := Check(context.Background(), repo, stubStore{}, Probes{Local: tt.probe})
			if res.Writable != tt.wantWrite {
				t.Fatalf("writable = %v, want %v (reason=%q)", res.Writable, tt.wantWrite, res.Reason)
			}
			if res.Reason == "" {
				t.Fatalf("reason must never be empty")
			}
		})
	}
}

func TestCheck_LocalNoProbe(t *testing.T) {
	repo := &models.Repo{SourceType: models.SourceTypeLocal, SourcePath: "/x"}
	res := Check(context.Background(), repo, stubStore{}, Probes{})
	if res.Writable {
		t.Fatal("missing probe must yield not-writable")
	}
}

// --- github ----------------------------------------------------------------

func TestCheck_GitHub(t *testing.T) {
	repo := &models.Repo{SourceType: models.SourceTypeGitHub, SourcePath: "acme/widget", UserID: "u1"}

	t.Run("can push", func(t *testing.T) {
		gh := &stubGitHub{info: GitHubPushInfo{CanPush: true}}
		store := stubStore{secrets: []models.Secret{ghSecret(t, "ghp_token")}}
		res := Check(context.Background(), repo, store, Probes{GitHub: gh, ParseGitHubSource: okParse})
		if !res.Writable {
			t.Fatalf("expected writable, got %q", res.Reason)
		}
		if gh.gotToken != "ghp_token" || gh.gotOwner != "acme" || gh.gotRepo != "widget" {
			t.Fatalf("probe args wrong: token=%q owner=%q repo=%q", gh.gotToken, gh.gotOwner, gh.gotRepo)
		}
	})

	t.Run("no push permission", func(t *testing.T) {
		gh := &stubGitHub{info: GitHubPushInfo{CanPush: false}}
		store := stubStore{secrets: []models.Secret{ghSecret(t, "ghp_token")}}
		res := Check(context.Background(), repo, store, Probes{GitHub: gh, ParseGitHubSource: okParse})
		if !res.Writable {
			t.Fatal("clone-capable token should still be writable")
		}
		if res.CanPush {
			t.Fatal("read-only token must not report can_push")
		}
	})

	t.Run("prefers repo credential", func(t *testing.T) {
		selected := ghSecret(t, "ghp_selected")
		selected.ID = "sec-selected"
		selected.UserID = "u1"
		other := ghSecret(t, "ghp_other")
		other.ID = "sec-other"
		other.UserID = "u1"
		gh := &stubGitHub{info: GitHubPushInfo{CanPush: true}}
		store := credStore{
			stubStore: stubStore{secrets: []models.Secret{other, selected}},
			byID:      map[string]models.Secret{"sec-selected": selected},
		}
		r := *repo
		r.CredentialSecretID = "sec-selected"
		res := Check(context.Background(), &r, store, Probes{GitHub: gh, ParseGitHubSource: okParse})
		if !res.Writable {
			t.Fatalf("expected writable, got %q", res.Reason)
		}
		if gh.gotToken != "ghp_selected" {
			t.Fatalf("used token %q, want selected", gh.gotToken)
		}
	})

	t.Run("rejects unreadable selected secret without substituting credentials", func(t *testing.T) {
		broken := models.Secret{
			ID:             "sec-broken",
			UserID:         "u1",
			KeyType:        models.KeyTypeGitHubToken,
			EncryptedValue: "not-valid-ciphertext",
		}
		other := ghSecret(t, "ghp_other")
		other.ID = "sec-other"
		other.UserID = "u1"
		gh := &stubGitHub{info: GitHubPushInfo{CanPush: true}}
		store := credStore{
			stubStore: stubStore{secrets: []models.Secret{other, broken}},
			byID:      map[string]models.Secret{"sec-broken": broken},
		}
		r := *repo
		r.CredentialSecretID = "sec-broken"
		res := Check(context.Background(), &r, store, Probes{GitHub: gh, ParseGitHubSource: okParse})
		if res.Writable || gh.gotToken != "" {
			t.Fatalf("unreadable selected credential must not fall back: %+v", res)
		}
	})

	t.Run("archived", func(t *testing.T) {
		gh := &stubGitHub{info: GitHubPushInfo{CanPush: true, Archived: true}}
		store := stubStore{secrets: []models.Secret{ghSecret(t, "ghp_token")}}
		res := Check(context.Background(), repo, store, Probes{GitHub: gh, ParseGitHubSource: okParse})
		if res.Writable {
			t.Fatal("archived repo must be not-writable")
		}
	})

	t.Run("no token", func(t *testing.T) {
		gh := &stubGitHub{info: GitHubPushInfo{CanPush: true}}
		store := stubStore{} // no secrets
		res := Check(context.Background(), repo, store, Probes{GitHub: gh, ParseGitHubSource: okParse})
		if res.Writable {
			t.Fatal("missing token must be not-writable")
		}
	})

	t.Run("probe error", func(t *testing.T) {
		gh := &stubGitHub{err: errors.New("403")}
		store := stubStore{secrets: []models.Secret{ghSecret(t, "ghp_token")}}
		res := Check(context.Background(), repo, store, Probes{GitHub: gh, ParseGitHubSource: okParse})
		if res.Writable {
			t.Fatal("probe error must be not-writable")
		}
	})

	t.Run("invalid source", func(t *testing.T) {
		gh := &stubGitHub{info: GitHubPushInfo{CanPush: true}}
		store := stubStore{secrets: []models.Secret{ghSecret(t, "ghp_token")}}
		badParse := func(string) (string, string, error) { return "", "", errors.New("bad source") }
		res := Check(context.Background(), repo, store, Probes{GitHub: gh, ParseGitHubSource: badParse})
		if res.Writable {
			t.Fatal("invalid source must be not-writable")
		}
	})
}

// --- ssh -------------------------------------------------------------------

func TestCheck_SSH(t *testing.T) {
	nodeID := "node-1"
	repo := &models.Repo{
		SourceType:   models.SourceTypeSSH,
		UserID:       "u1",
		SourcePath:   "/srv/app",
		RemoteNodeID: &nodeID,
	}
	node := &models.RemoteNode{ID: nodeID, UserID: "u1"}

	t.Run("push accepted", func(t *testing.T) {
		store := stubStore{node: node}
		res := Check(context.Background(), repo, store, Probes{SSH: stubSSH{ok: true}})
		if !res.Writable {
			t.Fatalf("expected writable, got %q", res.Reason)
		}
	})

	t.Run("push rejected", func(t *testing.T) {
		store := stubStore{node: node}
		res := Check(context.Background(), repo, store, Probes{SSH: stubSSH{ok: false, reason: "read-only remote"}})
		if res.Writable || res.Reason != "read-only remote" {
			t.Fatalf("rejected push wrong: %+v", res)
		}
	})

	t.Run("node not found", func(t *testing.T) {
		store := stubStore{nodeErr: errors.New("missing")}
		res := Check(context.Background(), repo, store, Probes{SSH: stubSSH{ok: true}})
		if res.Writable {
			t.Fatal("missing node must be not-writable")
		}
	})

	t.Run("no remote node id", func(t *testing.T) {
		noNode := &models.Repo{SourceType: models.SourceTypeSSH, SourcePath: "/srv/app"}
		res := Check(context.Background(), noNode, stubStore{}, Probes{SSH: stubSSH{ok: true}})
		if res.Writable {
			t.Fatal("missing remote node id must be not-writable")
		}
	})

	t.Run("probe error", func(t *testing.T) {
		store := stubStore{node: node}
		res := Check(context.Background(), repo, store, Probes{SSH: stubSSH{err: errors.New("dial timeout")}})
		if res.Writable {
			t.Fatal("ssh probe error must be not-writable")
		}
	})
}

// --- misc ------------------------------------------------------------------

func TestCheck_NilRepo(t *testing.T) {
	res := Check(context.Background(), nil, stubStore{}, Probes{})
	if res.Writable {
		t.Fatal("nil repo must be not-writable")
	}
}

func TestCheck_UnsupportedSource(t *testing.T) {
	repo := &models.Repo{SourceType: models.SourceTypeGitLab, SourcePath: "x"}
	res := Check(context.Background(), repo, stubStore{}, Probes{})
	if res.Writable {
		t.Fatal("unsupported source must be not-writable")
	}
}

func TestSelectedGitHubCredentialOwnership(t *testing.T) {
	for _, owner := range []string{"another-user", ""} {
		t.Run("secret-owner-"+owner, func(t *testing.T) {
			secret := ghSecret(t, "selected-token")
			secret.UserID = owner
			store := credStore{byID: map[string]models.Secret{"selected": secret}, stubStore: stubStore{secrets: []models.Secret{ghSecret(t, "fallback-token")}}}
			repo := &models.Repo{UserID: "u1", CredentialSecretID: "selected"}
			if token, reason := GitHubToken(t.Context(), store, repo); token != "" || reason == "" {
				t.Fatalf("unowned credential accepted: %q", reason)
			}
			repo.UserID = ""
			if token, _ := GitHubToken(t.Context(), store, repo); token != "" {
				t.Fatal("ownerless repo acquired credentials")
			}
		})
	}
}

func TestDefaultPreflightRejectsUnsupportedSSHWithoutConnecting(t *testing.T) {
	result := Check(t.Context(), &models.Repo{SourceType: models.SourceTypeSSH}, nil, DefaultProbes(nil))
	if result.Writable || result.CanPush || !strings.Contains(result.Reason, "register a local clone") {
		t.Fatalf("misleading SSH readiness: %+v", result)
	}
}

func TestSSHPreflightRejectsAnotherOwnersNode(t *testing.T) {
	nodeID := "node"
	result := Check(t.Context(), &models.Repo{SourceType: models.SourceTypeSSH, UserID: "u1", RemoteNodeID: &nodeID}, stubStore{node: &models.RemoteNode{ID: nodeID, UserID: "u2"}}, Probes{SSH: stubSSH{ok: true}})
	if result.Writable || !strings.Contains(result.Reason, "owner") {
		t.Fatalf("unowned node accepted: %+v", result)
	}
}

func TestGitHubProbeErrorDoesNotExposeCredential(t *testing.T) {
	result := Check(t.Context(), &models.Repo{SourceType: models.SourceTypeGitHub, UserID: "u1", SourcePath: "acme/widget"}, stubStore{secrets: []models.Secret{ghSecret(t, "private-token")}}, Probes{GitHub: &stubGitHub{err: errors.New("rejected private-token")}, ParseGitHubSource: okParse})
	if result.Writable || strings.Contains(result.Reason, "private-token") {
		t.Fatalf("unsafe permission response: %+v", result)
	}
}

// --- gitlab ----------------------------------------------------------------

type stubGitLab struct {
	info                          GitLabProjectInfo
	err                           error
	gotHost, gotToken, gotProject string
}

func (s *stubGitLab) ProjectInfo(_ context.Context, host, token, project string) (GitLabProjectInfo, error) {
	s.gotHost, s.gotToken, s.gotProject = host, token, project
	return s.info, s.err
}

func glSecret(t *testing.T, value, hosts string) models.Secret {
	t.Helper()
	sec := ghSecret(t, value)
	sec.KeyType = models.KeyTypeGitLabToken
	sec.AllowedHosts = hosts
	return sec
}

func TestCheck_GitLab(t *testing.T) {
	repoFor := func(url string) *models.Repo {
		return &models.Repo{UserID: "u1", SourceType: models.SourceTypeGit, SourcePath: url, CredentialSecretID: "cred"}
	}
	good := credStore{byID: map[string]models.Secret{"cred": glSecret(t, "glpat-secret", `["gitlab.example.com"]`)}}
	url := "https://gitlab.example.com:8443/team/sub/app.git"
	tests := []struct {
		name         string
		repo         *models.Repo
		store        credStore
		info         GitLabProjectInfo
		probeErr     error
		wantWritable bool
		wantCanPush  bool
		wantReason   string
	}{
		{"can push", repoFor(url), good, GitLabProjectInfo{CanPush: true, MergeRequests: true}, nil, true, true, "can push"},
		{"reporter role", repoFor(url), good, GitLabProjectInfo{MergeRequests: true}, nil, true, false, "Developer"},
		{"mrs disabled", repoFor(url), good, GitLabProjectInfo{CanPush: true}, nil, true, false, "merge requests are disabled"},
		{"archived", repoFor(url), good, GitLabProjectInfo{CanPush: true, Archived: true, MergeRequests: true}, nil, false, false, "archived"},
		{"no credential", &models.Repo{UserID: "u1", SourceType: models.SourceTypeGit, SourcePath: url}, good, GitLabProjectInfo{}, nil, false, false, "select a gitlab_token"},
		{"ssh url", repoFor("ssh://git@gitlab.example.com/team/app.git"), good, GitLabProjectInfo{}, nil, false, false, "https"},
		{"wrong host binding", repoFor("https://other.example.com/team/app.git"), good, GitLabProjectInfo{}, nil, false, false, "not allowed for host other.example.com"},
		{"probe error hides token", repoFor(url), good, GitLabProjectInfo{}, errors.New("401 glpat-secret rejected"), false, false, "[REDACTED]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			probe := &stubGitLab{info: tt.info, err: tt.probeErr}
			res := Check(t.Context(), tt.repo, tt.store, Probes{GitLab: probe})
			if res.Writable != tt.wantWritable || res.CanPush != tt.wantCanPush || !strings.Contains(res.Reason, tt.wantReason) {
				t.Fatalf("got %+v; want writable=%v canPush=%v reason~%q", res, tt.wantWritable, tt.wantCanPush, tt.wantReason)
			}
			if strings.Contains(res.Reason, "glpat-secret") {
				t.Fatal("token leaked into reason")
			}
		})
	}
	probe := &stubGitLab{info: GitLabProjectInfo{CanPush: true, MergeRequests: true}}
	Check(t.Context(), repoFor(url), good, Probes{GitLab: probe})
	if probe.gotHost != "gitlab.example.com:8443" || probe.gotProject != "team/sub/app" || probe.gotToken != "glpat-secret" {
		t.Fatalf("probe args = %q %q %q", probe.gotHost, probe.gotProject, probe.gotToken)
	}
}

func TestGitLabTokenRejectsOtherOwnerAndType(t *testing.T) {
	foreign := glSecret(t, "tok", `["gitlab.com"]`)
	foreign.UserID = "someone-else"
	wrongType := ghSecret(t, "tok")
	wrongType.AllowedHosts = `["gitlab.com"]`
	store := credStore{byID: map[string]models.Secret{"foreign": foreign, "wrong": wrongType}}
	for _, id := range []string{"foreign", "wrong", "missing"} {
		repo := &models.Repo{UserID: "u1", SourceType: models.SourceTypeGitLab, SourcePath: "https://gitlab.com/g/p.git", CredentialSecretID: id}
		if token, reason := GitLabToken(t.Context(), store, repo); token != "" || reason == "" {
			t.Fatalf("%s accepted: %q", id, reason)
		}
	}
}
