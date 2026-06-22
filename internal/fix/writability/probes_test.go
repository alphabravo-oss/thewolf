package writability

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/secrets"
	"github.com/alphabravocompany/thewolf/internal/sshclient"
)

// --- local FS probe (real temp dirs are cheap & allowed) -------------------

func TestLocalFSProbe(t *testing.T) {
	p := localFSProbe{}

	t.Run("writable git tree", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		ok, reason, err := p.Check(dir)
		if err != nil || !ok {
			t.Fatalf("want writable, got ok=%v reason=%q err=%v", ok, reason, err)
		}
	})

	t.Run("not a git tree", func(t *testing.T) {
		dir := t.TempDir()
		ok, reason, _ := p.Check(dir)
		if ok || !strings.Contains(reason, "git work tree") {
			t.Fatalf("want not-git reason, got ok=%v reason=%q", ok, reason)
		}
	})

	t.Run("missing path", func(t *testing.T) {
		ok, reason, _ := p.Check(filepath.Join(t.TempDir(), "nope"))
		if ok || !strings.Contains(reason, "does not exist") {
			t.Fatalf("want not-exist reason, got ok=%v reason=%q", ok, reason)
		}
	})

	t.Run("file not dir", func(t *testing.T) {
		f := filepath.Join(t.TempDir(), "f")
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		ok, reason, _ := p.Check(f)
		if ok || !strings.Contains(reason, "not a directory") {
			t.Fatalf("want not-dir reason, got ok=%v reason=%q", ok, reason)
		}
	})

	t.Run("empty path", func(t *testing.T) {
		ok, _, _ := p.Check("")
		if ok {
			t.Fatal("empty path must be not-writable")
		}
	})
}

// --- ssh probe via a stubbed runner (no real network/docker) ---------------

type fakeRunner struct {
	res     sshclient.Result
	err     error
	lastCmd string
}

func (r *fakeRunner) Run(_ context.Context, _ sshclient.Config, cmd string) (sshclient.Result, error) {
	r.lastCmd = cmd
	return r.res, r.err
}

// sshTestStore answers GetSecretByID so ConfigForNode can build an SSH config.
type sshTestStore struct {
	stubStore
	secret *models.Secret
}

func (s sshTestStore) GetSecretByID(context.Context, string) (*models.Secret, error) {
	return s.secret, nil
}

func newSSHNode(t *testing.T) (*models.RemoteNode, sshTestStore) {
	t.Helper()
	enc, err := secrets.Encrypt("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	secID := "sec-1"
	node := &models.RemoteNode{
		ID:                 "node-1",
		Host:               "example.com",
		Port:               22,
		Username:           "deploy",
		AuthType:           "password",
		CredentialSecretID: &secID,
		Enabled:            true,
	}
	store := sshTestStore{secret: &models.Secret{ID: secID, KeyType: models.KeyTypeSSHPassword, EncryptedValue: enc}}
	return node, store
}

func TestSSHRunnerProbe_PushAccepted(t *testing.T) {
	node, store := newSSHNode(t)
	runner := &fakeRunner{res: sshclient.Result{Stdout: "Everything up-to-date"}}
	p := sshRunnerProbe{store: store, runner: runner}

	ok, reason, err := p.CanPush(context.Background(), node, "/srv/app")
	if err != nil || !ok {
		t.Fatalf("want push accepted, got ok=%v reason=%q err=%v", ok, reason, err)
	}
	if !strings.Contains(runner.lastCmd, "git push --dry-run") {
		t.Fatalf("expected dry-run push in command, got: %s", runner.lastCmd)
	}
	if !strings.Contains(runner.lastCmd, "/srv/app") {
		t.Fatalf("expected remote path in command, got: %s", runner.lastCmd)
	}
}

func TestSSHRunnerProbe_PushRejected(t *testing.T) {
	node, store := newSSHNode(t)
	runner := &fakeRunner{
		res: sshclient.Result{Stdout: "remote work tree is not writable", ExitCode: 46},
		err: errors.New("remote command exited 46"),
	}
	p := sshRunnerProbe{store: store, runner: runner}

	ok, reason, err := p.CanPush(context.Background(), node, "/srv/app")
	if err != nil {
		t.Fatalf("CanPush should fold probe failures into reason, got err=%v", err)
	}
	if ok {
		t.Fatal("rejected push must be not-ok")
	}
	if !strings.Contains(reason, "not writable") {
		t.Fatalf("expected reason to carry remote output, got %q", reason)
	}
}

func TestSSHRunnerProbe_ConfigError(t *testing.T) {
	// A disabled node makes ConfigForNode fail before any runner call.
	node, store := newSSHNode(t)
	node.Enabled = false
	p := sshRunnerProbe{store: store, runner: &fakeRunner{}}

	ok, _, err := p.CanPush(context.Background(), node, "/srv/app")
	if ok || err == nil {
		t.Fatalf("disabled node must surface a config error: ok=%v err=%v", ok, err)
	}
}
