package routes_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/alphabravocompany/thewolf/internal/api/routes"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/secrets"
	"github.com/alphabravocompany/thewolf/internal/sshclient"
)

// fakeDiscoverRunner returns three known repo paths whenever it sees the
// find ... -name .git command, mimicking the on-host walk.
type fakeDiscoverRunner struct{}

func (fakeDiscoverRunner) Run(_ context.Context, _ sshclient.Config, command string) (sshclient.Result, error) {
	if strings.Contains(command, "find ") && strings.Contains(command, "-name .git") {
		out := strings.Join([]string{
			"REPO\tapi\t/srv/code/api\tmain\tabc123",
			"REPO\tweb\t/srv/code/web\tdevelop\tdef456",
			"REPO\tworker\t/srv/code/worker\tmain\tfff999",
			"",
		}, "\n")
		return sshclient.Result{Stdout: out}, nil
	}
	return sshclient.Result{Stdout: "wolf-ok"}, nil
}

func TestNodeDiscoverRepos(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()

	// Crypto for the SSH credential secret.
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	secrets.SetMasterKey(key)

	enc, err := secrets.Encrypt("not-a-real-key")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	secret := &models.Secret{
		ID:             "ssh-secret-1",
		UserID:         env.UserID,
		KeyType:        models.KeyTypeSSHPrivate,
		KeyName:        "dev-key",
		EncryptedValue: enc,
	}
	if err := env.Store.CreateSecret(context.Background(), secret); err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}
	credID := secret.ID
	node := &models.RemoteNode{
		ID:                 "node-discover",
		UserID:             env.UserID,
		Name:               "dev",
		Host:               "dev.example.com",
		Port:               22,
		Username:           "alice",
		AuthType:           "private_key",
		CredentialSecretID: &credID,
		BasePath:           "/srv/code",
		Enabled:            true,
	}
	if err := env.Store.CreateRemoteNode(context.Background(), node); err != nil {
		t.Fatalf("CreateRemoteNode: %v", err)
	}

	// Inject the fake SSH runner.
	routes.SSHRunnerOverride = fakeDiscoverRunner{}
	t.Cleanup(func() { routes.SSHRunnerOverride = nil })

	// Mount the new route on a fresh router.
	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(auth.Middleware)
		r.Post("/api/nodes/{id}/discover-repos", routes.DiscoverNodeRepos)
	})

	body, _ := json.Marshal(map[string]string{"base_path": "/srv/code"})
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/"+node.ID+"/discover-repos", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+env.Token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	var resp struct {
		Data []struct {
			Name      string `json:"name"`
			Path      string `json:"path"`
			Branch    string `json:"branch"`
			CommitSHA string `json:"commit_sha"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data) != 3 {
		t.Fatalf("got %d entries, want 3 (%+v)", len(resp.Data), resp.Data)
	}
	want := map[string]struct {
		Branch    string
		CommitSHA string
	}{
		"/srv/code/api":    {"main", "abc123"},
		"/srv/code/web":    {"develop", "def456"},
		"/srv/code/worker": {"main", "fff999"},
	}
	for _, row := range resp.Data {
		exp, ok := want[row.Path]
		if !ok {
			t.Errorf("unexpected path %q", row.Path)
			continue
		}
		if row.Branch != exp.Branch {
			t.Errorf("path %q: branch = %q, want %q", row.Path, row.Branch, exp.Branch)
		}
		if row.CommitSHA != exp.CommitSHA {
			t.Errorf("path %q: commit_sha = %q, want %q", row.Path, row.CommitSHA, exp.CommitSHA)
		}
		if row.Name == "" {
			t.Errorf("path %q: name is empty", row.Path)
		}
	}
}
