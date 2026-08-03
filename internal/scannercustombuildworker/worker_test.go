package scannercustombuildworker_test

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/scannerbuild"
	"github.com/alphabravocompany/thewolf/internal/scannercustombuildworker"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

func TestWorkerPersistsPartialResultRedactsLogsAndRetrySkipsSuccess(t *testing.T) {
	store, repository := newWorkerStore(t)
	request := newWorkerBuildRequest([]string{"default", "jvm"}, true)
	inventory, _, err := repository.CreateCustomBuild(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	var mutex sync.Mutex
	var calls []scannerbuild.BuildRequest
	failJVM := true
	worker := newTestWorker(t, repository,
		func(
			_ context.Context,
			buildRequest scannerbuild.BuildRequest,
			onLine func(string),
		) (scannerbuild.BuildResult, error) {
			mutex.Lock()
			calls = append(calls, buildRequest)
			mutex.Unlock()
			onLine("token=registry-secret password=also-secret")
			onLine(
				"encoded=" + base64.StdEncoding.EncodeToString(
					[]byte("registry-secret"),
				) + string([]byte{0xff, 0xfe}) + "\x00" +
					strings.Repeat("界", 3000),
			)
			if buildRequest.Variant == "jvm" && failJVM {
				return scannerbuild.BuildResult{}, errors.New("daemon detail must not persist")
			}
			return scannerbuild.BuildResult{
				Refs:   []string{buildRequest.Namespace + "/" + buildRequest.Variant},
				Digest: "sha256:" + buildRequest.Variant,
			}, nil
		},
		scannercustombuildworker.CredentialResolverFunc(
			func(context.Context, string, string) (string, string, error) {
				return "registry-user", "registry-secret", nil
			},
		),
		time.Minute,
	)
	worked, err := worker.Once(context.Background())
	if err != nil || !worked {
		t.Fatalf("Once worked=%t err=%v", worked, err)
	}
	result, err := repository.GetCustomBuild(context.Background(), inventory.Build.ID)
	if err != nil || result.Build.State != scannerrelease.CustomBuildPartial {
		t.Fatalf("partial result = %#v err=%v", result, err)
	}
	if result.Build.SecretReference != request.SecretReference {
		t.Fatal("worker persistence lost the opaque secret reference")
	}
	logs, err := repository.ListCustomBuildLogs(
		context.Background(), inventory.Build.ID, 0, 20,
	)
	if err != nil || len(logs) != 4 {
		t.Fatalf("logs = %#v err=%v", logs, err)
	}
	for _, log := range logs {
		if strings.Contains(log.Line, "registry-secret") ||
			strings.Contains(log.Line, "also-secret") ||
			!log.Redacted {
			t.Fatalf("unsafe persisted log: %#v", log)
		}
		if !utf8.ValidString(log.Line) || len(log.Line) > 8192 ||
			strings.Contains(
				log.Line,
				base64.StdEncoding.EncodeToString([]byte("registry-secret")),
			) || strings.ContainsRune(log.Line, '\x00') {
			t.Fatalf("unsanitized persisted log: %#v", log)
		}
	}
	for _, call := range calls {
		if call.DockerHubUser != "registry-user" ||
			call.DockerHubPAT != "registry-secret" ||
			!call.Push ||
			call.Platforms != "linux/amd64,linux/arm64" {
			t.Fatalf("executor request = %#v", call)
		}
	}

	retried, err := repository.RetryCustomBuild(
		context.Background(), result.Build.ID, result.Build.Version,
		scannerrelease.TransitionCommand{
			Actor: "operator", Reason: "retry the failed JVM image",
			IdempotencyKey: "retry-" + result.Build.ID,
		},
		time.Now().UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	failJVM = false
	before := len(calls)
	worked, err = worker.Once(context.Background())
	if err != nil || !worked {
		t.Fatalf("retry Once worked=%t err=%v", worked, err)
	}
	result, err = repository.GetCustomBuild(context.Background(), retried.ID)
	if err != nil || result.Build.State != scannerrelease.CustomBuildCompleted {
		t.Fatalf("retried result = %#v err=%v", result, err)
	}
	if got := calls[before:]; len(got) != 1 || got[0].Variant != "jvm" {
		t.Fatalf("retry rebuilt completed variants: %#v", got)
	}
	_ = store
}

func TestWorkerLogBudgetMarkerAndUnexpectedLogPersistenceFailure(t *testing.T) {
	t.Run("budget marker once", func(t *testing.T) {
		_, repository := newWorkerStore(t)
		request := newWorkerBuildRequest([]string{"default"}, false)
		inventory, _, err := repository.CreateCustomBuild(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		store := &controlledLogStore{
			Persistence: repository,
			failWith:    scannerrelease.ErrCustomBuildLogBudget,
			failCount:   1,
		}
		worker := newTestWorkerWithStore(t, store,
			func(
				_ context.Context,
				_ scannerbuild.BuildRequest,
				onLine func(string),
			) (scannerbuild.BuildResult, error) {
				onLine("first")
				onLine("second")
				onLine("third")
				return scannerbuild.BuildResult{LoadedLocally: true}, nil
			},
			nil,
			time.Minute,
		)
		if worked, err := worker.Once(context.Background()); err != nil || !worked {
			t.Fatalf("Once worked=%t err=%v", worked, err)
		}
		if store.calls != 2 || len(store.accepted) != 1 ||
			store.accepted[0] != "[build log truncated: durable log budget exhausted]" {
			t.Fatalf("budget marker calls=%d accepted=%#v", store.calls, store.accepted)
		}
		result, err := repository.GetCustomBuild(context.Background(), inventory.Build.ID)
		if err != nil || result.Build.State != scannerrelease.CustomBuildCompleted {
			t.Fatalf("budget result = %#v err=%v", result, err)
		}
	})

	t.Run("lease loss fails safely", func(t *testing.T) {
		_, repository := newWorkerStore(t)
		request := newWorkerBuildRequest([]string{"default"}, false)
		inventory, _, err := repository.CreateCustomBuild(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		store := &controlledLogStore{
			Persistence: repository,
			failWith:    scannerrelease.ErrLeaseNotOwned,
			failCount:   1,
		}
		worker := newTestWorkerWithStore(t, store,
			func(
				ctx context.Context,
				_ scannerbuild.BuildRequest,
				onLine func(string),
			) (scannerbuild.BuildResult, error) {
				onLine("lease-sensitive")
				<-ctx.Done()
				return scannerbuild.BuildResult{}, ctx.Err()
			},
			nil,
			time.Minute,
		)
		worked, err := worker.Once(context.Background())
		if !worked || !errors.Is(err, scannerrelease.ErrLeaseNotOwned) {
			t.Fatalf("lease-loss Once worked=%t err=%v", worked, err)
		}
		result, getErr := repository.GetCustomBuild(
			context.Background(), inventory.Build.ID,
		)
		if getErr != nil || result.Build.State != scannerrelease.CustomBuildRunning {
			t.Fatalf("lease-loss result = %#v err=%v", result, getErr)
		}
	})
}

func TestWorkerCredentialFailureIsBoundedAndTerminal(t *testing.T) {
	_, repository := newWorkerStore(t)
	request := newWorkerBuildRequest([]string{"default", "rust"}, true)
	inventory, _, err := repository.CreateCustomBuild(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	worker := newTestWorker(t, repository,
		func(
			context.Context,
			scannerbuild.BuildRequest,
			func(string),
		) (scannerbuild.BuildResult, error) {
			t.Fatal("executor must not run when registry credentials cannot be resolved")
			return scannerbuild.BuildResult{}, nil
		},
		scannercustombuildworker.CredentialResolverFunc(
			func(context.Context, string, string) (string, string, error) {
				return "", "", errors.New("secret backend detail")
			},
		),
		time.Minute,
	)
	if worked, err := worker.Once(context.Background()); err != nil || !worked {
		t.Fatalf("Once worked=%t err=%v", worked, err)
	}
	result, err := repository.GetCustomBuild(context.Background(), inventory.Build.ID)
	if err != nil || result.Build.State != scannerrelease.CustomBuildFailed ||
		result.Build.ErrorClass != "variant_failure" {
		t.Fatalf("credential failure result = %#v err=%v", result, err)
	}
	for _, variant := range result.Variants {
		if variant.State != scannerrelease.CustomBuildVariantFailed ||
			variant.ErrorClass != "credential_unavailable" ||
			strings.Contains(variant.ErrorDetail, "backend") {
			t.Fatalf("unsafe credential failure variant = %#v", variant)
		}
	}
}

func TestWorkerHeartbeatsWhileLoadingClaimedInventory(t *testing.T) {
	_, repository := newWorkerStore(t)
	request := newWorkerBuildRequest([]string{"default"}, false)
	inventory, _, err := repository.CreateCustomBuild(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	store := &slowInventoryStore{
		Persistence: repository,
		delay:       250 * time.Millisecond,
	}
	worker := newTestWorkerWithStore(t, store,
		func(
			context.Context,
			scannerbuild.BuildRequest,
			func(string),
		) (scannerbuild.BuildResult, error) {
			return scannerbuild.BuildResult{
				Refs:   []string{"testregistry/default"},
				Digest: "sha256:" + strings.Repeat("a", 64),
			}, nil
		},
		nil,
		time.Minute,
	)
	if worked, err := worker.Once(context.Background()); err != nil || !worked {
		t.Fatalf("Once worked=%t err=%v", worked, err)
	}
	result, err := repository.GetCustomBuild(context.Background(), inventory.Build.ID)
	if err != nil || result.Build.State != scannerrelease.CustomBuildCompleted {
		t.Fatalf("slow inventory result = %#v err=%v", result, err)
	}
}

func TestWorkerTimeoutAndCooperativeCancellationBecomeTerminal(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		_, repository := newWorkerStore(t)
		request := newWorkerBuildRequest([]string{"default", "jvm"}, false)
		inventory, _, err := repository.CreateCustomBuild(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		worker := newTestWorker(t, repository,
			func(
				ctx context.Context,
				_ scannerbuild.BuildRequest,
				_ func(string),
			) (scannerbuild.BuildResult, error) {
				<-ctx.Done()
				return scannerbuild.BuildResult{}, ctx.Err()
			},
			nil,
			25*time.Millisecond,
		)
		if worked, err := worker.Once(context.Background()); err != nil || !worked {
			t.Fatalf("Once worked=%t err=%v", worked, err)
		}
		result, err := repository.GetCustomBuild(context.Background(), inventory.Build.ID)
		if err != nil || result.Build.State != scannerrelease.CustomBuildFailed {
			t.Fatalf("timeout result = %#v err=%v", result, err)
		}
		for _, variant := range result.Variants {
			if variant.State != scannerrelease.CustomBuildVariantFailed ||
				variant.ErrorClass != "timeout" {
				t.Fatalf("timeout variant = %#v", variant)
			}
		}
	})

	t.Run("cancel", func(t *testing.T) {
		_, repository := newWorkerStore(t)
		request := newWorkerBuildRequest([]string{"default", "jvm"}, false)
		inventory, _, err := repository.CreateCustomBuild(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		started := make(chan struct{})
		worker := newTestWorker(t, repository,
			func(
				ctx context.Context,
				_ scannerbuild.BuildRequest,
				_ func(string),
			) (scannerbuild.BuildResult, error) {
				select {
				case <-started:
				default:
					close(started)
				}
				<-ctx.Done()
				return scannerbuild.BuildResult{}, ctx.Err()
			},
			nil,
			time.Minute,
		)
		resultChannel := make(chan error, 1)
		go func() {
			_, runErr := worker.Once(context.Background())
			resultChannel <- runErr
		}()
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("worker did not start")
		}
		current, err := repository.GetCustomBuild(
			context.Background(), inventory.Build.ID,
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := repository.RequestCustomBuildCancellation(
			context.Background(), current.Build.ID, current.Build.Version,
			scannerrelease.TransitionCommand{
				Actor: "operator", Reason: "cooperative cancellation test",
				IdempotencyKey: "cancel-" + current.Build.ID,
			},
			time.Now().UTC(),
		); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-resultChannel:
			if err != nil {
				t.Fatalf("worker cancellation: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("worker did not observe cancellation")
		}
		result, err := repository.GetCustomBuild(
			context.Background(), inventory.Build.ID,
		)
		if err != nil || result.Build.State != scannerrelease.CustomBuildCancelled {
			t.Fatalf("cancel result = %#v err=%v", result, err)
		}
		for _, variant := range result.Variants {
			if variant.State != scannerrelease.CustomBuildVariantCancelled {
				t.Fatalf("cancelled variant = %#v", variant)
			}
		}
	})
}

func newWorkerStore(
	t *testing.T,
) (*db.SQLiteStore, scannerrelease.Persistence) {
	t.Helper()
	store, err := db.NewSQLite(t.TempDir() + "/worker.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, store.ScannerReleases()
}

func newWorkerBuildRequest(
	variants []string,
	push bool,
) scannerrelease.CustomBuildCreateRequest {
	request := scannerrelease.CustomBuildCreateRequest{
		ID: uuid.NewString(), UserID: "worker-user", Variants: variants,
		Push: push, Namespace: "testregistry", Actor: "operator",
		Reason: "worker contract", IdempotencyKey: uuid.NewString(),
		MaxAttempts: 3,
	}
	if push {
		request.Platforms = []string{"linux/amd64", "linux/arm64"}
		request.SecretReference = "opaque-secret-id"
	}
	return request
}

func newTestWorker(
	t *testing.T,
	repository scannerrelease.Persistence,
	executor func(
		context.Context,
		scannerbuild.BuildRequest,
		func(string),
	) (scannerbuild.BuildResult, error),
	credentials scannercustombuildworker.CredentialResolver,
	timeout time.Duration,
) *scannercustombuildworker.Worker {
	t.Helper()
	return newTestWorkerWithStore(
		t, repository, executor, credentials, timeout,
	)
}

func newTestWorkerWithStore(
	t *testing.T,
	store scannercustombuildworker.Store,
	executor func(
		context.Context,
		scannerbuild.BuildRequest,
		func(string),
	) (scannerbuild.BuildResult, error),
	credentials scannercustombuildworker.CredentialResolver,
	timeout time.Duration,
) *scannercustombuildworker.Worker {
	t.Helper()
	worker, err := scannercustombuildworker.New(scannercustombuildworker.Config{
		Store: store, Executor: scannercustombuildworker.ExecutorFunc(executor),
		Credentials: credentials, WorkerID: "custom-build-worker-test",
		PollInterval: time.Millisecond, HeartbeatInterval: 5 * time.Millisecond,
		LeaseDuration: 100 * time.Millisecond, OperationTimeout: timeout,
		Once: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return worker
}

type controlledLogStore struct {
	scannerrelease.Persistence
	failWith  error
	failCount int
	calls     int
	accepted  []string
}

type slowInventoryStore struct {
	scannerrelease.Persistence
	once  sync.Once
	delay time.Duration
}

func (store *slowInventoryStore) GetCustomBuild(
	ctx context.Context,
	id string,
) (*scannerrelease.CustomBuildInventory, error) {
	store.once.Do(func() {
		timer := time.NewTimer(store.delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
		case <-timer.C:
		}
	})
	return store.Persistence.GetCustomBuild(ctx, id)
}

func (store *controlledLogStore) AppendCustomBuildLog(
	_ context.Context,
	buildID, variant, _ string,
	line string,
	redacted bool,
	at time.Time,
) (*scannerrelease.CustomBuildLog, error) {
	store.calls++
	if store.failCount > 0 {
		store.failCount--
		return nil, store.failWith
	}
	store.accepted = append(store.accepted, line)
	return &scannerrelease.CustomBuildLog{
		BuildID: buildID, Sequence: int64(len(store.accepted)),
		Variant: variant, Line: line, Redacted: redacted, CreatedAt: at,
	}, nil
}
