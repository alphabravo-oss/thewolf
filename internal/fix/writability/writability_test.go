package writability

import (
	"bytes"
	"context"
	"errors"
	"os"
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

func (s stubLocal) Check(string) (bool, string, error) { return s.writable, s.reason, s.err }

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

func ghSecret(t *testing.T, value string) models.Secret {
	t.Helper()
	enc, err := secrets.Encrypt(value)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	return models.Secret{KeyType: models.KeyTypeGitHubToken, EncryptedValue: enc}
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
		if res.Writable {
			t.Fatal("no push permission must be not-writable")
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
		SourcePath:   "/srv/app",
		RemoteNodeID: &nodeID,
	}
	node := &models.RemoteNode{ID: nodeID}

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
