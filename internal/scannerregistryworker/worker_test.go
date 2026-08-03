package scannerregistryworker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/scannerregistry"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

type staticClientFactory struct {
	client scannerregistry.Client
	host   string
}

func (f staticClientFactory) Single(
	context.Context,
	*scannerrelease.RegistryTarget,
) (scannerregistry.Client, string, error) {
	return f.client, f.host, nil
}

func (f staticClientFactory) Pair(
	context.Context,
	*scannerrelease.RegistryTarget,
	*scannerrelease.RegistryTarget,
) (scannerregistry.Client, string, string, error) {
	return f.client, f.host, f.host, nil
}

func TestWorkerDeletesOnlyRepositoryAuthorizedOrphan(t *testing.T) {
	var deleted bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodDelete:
			deleted = true
			w.WriteHeader(http.StatusAccepted)
		case http.MethodHead:
			if deleted {
				http.NotFound(w, r)
			} else {
				w.Header().Set("Docker-Content-Digest", strings.TrimPrefix(r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:], ""))
				w.WriteHeader(http.StatusOK)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	host := strings.TrimPrefix(server.URL, "http://")
	store, err := db.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	repository := store.ScannerReleases()
	target := &scannerrelease.RegistryTarget{
		ID: uuid.NewString(), Name: "mirror", Type: scannerrelease.RegistryMirror,
		Host: server.URL, Namespace: "wolf", PlatformPolicyJSON: "{}",
		Enabled: true, CreatedBy: "test",
	}
	if err := repository.CreateRegistryTarget(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	retainUntil := now.Add(-time.Hour)
	object := &scannerrelease.RegistryQuarantineObject{
		RegistryTargetID: target.ID, Repository: "wolf/scanners",
		Digest: "sha256:" + strings.Repeat("a", 64), ObjectKind: "manifest",
		State: "orphaned", RetainUntil: &retainUntil,
		DiscoveredAt: now.Add(-24 * time.Hour),
	}
	if err := repository.UpsertRegistryQuarantineObject(context.Background(), object); err != nil {
		t.Fatal(err)
	}
	job := &scannerrelease.RegistryJob{
		RegistryTargetID: target.ID, Kind: scannerrelease.RegistryJobCleanup,
		ReSignPolicy: scannerrelease.RegistryReSignForbidden,
		Actor:        "test", Reason: "remove expired orphan",
		IdempotencyKey: "cleanup:" + uuid.NewString(), MaxAttempts: 2,
		AvailableAt: now,
	}
	if err := repository.CreateRegistryJob(context.Background(), job, scannerrelease.TransitionCommand{
		Actor: "test", Reason: job.Reason, IdempotencyKey: job.IdempotencyKey,
	}); err != nil {
		t.Fatal(err)
	}
	worker, err := New(Config{
		Store: repository,
		Clients: staticClientFactory{
			client: scannerregistry.Client{
				HTTP: server.Client(),
				Endpoints: map[string]scannerregistry.Endpoint{
					host: {BaseURL: server.URL},
				},
			},
			host: host,
		},
		WorkerID: "registry-worker-test", Once: true,
		HeartbeatInterval: time.Second, LeaseDuration: 10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	completed, err := repository.GetRegistryJob(context.Background(), job.ID)
	if err != nil || completed.State != scannerrelease.RegistryJobCompleted {
		t.Fatalf("cleanup job = %#v err=%v", completed, err)
	}
	objects, err := repository.ListRegistryQuarantineObjects(
		context.Background(), target.ID, "deleted", 10,
	)
	if err != nil || len(objects) != 1 || !deleted {
		t.Fatalf("deleted objects = %#v registry_deleted=%v err=%v", objects, deleted, err)
	}
}
