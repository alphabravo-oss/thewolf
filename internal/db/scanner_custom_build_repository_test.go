package db

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
	"github.com/alphabravocompany/thewolf/internal/scannertrace"
)

func TestScannerCustomBuildRepositoryContractSQLite(t *testing.T) {
	runScannerCustomBuildRepositoryContract(t, newSQLiteReleaseContractBackend)
}

func TestScannerCustomBuildRepositoryContractPostgres(t *testing.T) {
	runScannerCustomBuildRepositoryContract(t, newPostgresReleaseContractBackend)
}

func TestScannerCustomBuildLogBudgetsSQLite(t *testing.T) {
	backend := newSQLiteReleaseContractBackend(t)
	t.Cleanup(func() { _ = backend.close() })
	repository := backend.persistence
	ctx := context.Background()
	now := time.Now().UTC().Add(time.Second)

	lineLimited := customBuildRequest(false, []string{"default"})
	lineInventory, _, err := repository.CreateCustomBuild(ctx, lineLimited)
	if err != nil {
		t.Fatal(err)
	}
	lineClaim, err := repository.ClaimNextCustomBuild(
		ctx, "line-budget-worker", now, now.Add(time.Hour),
	)
	if err != nil || lineClaim == nil || lineClaim.ID != lineInventory.Build.ID {
		t.Fatalf("line claim = %#v err=%v", lineClaim, err)
	}
	if _, err := repository.StartCustomBuild(
		ctx, lineClaim.ID, lineClaim.LeaseToken, now,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.StartCustomBuildVariant(
		ctx, lineClaim.ID, "default", lineClaim.LeaseToken, now,
	); err != nil {
		t.Fatal(err)
	}
	for sequence := 0; sequence < customBuildMaxLogs; sequence++ {
		if _, err := repository.AppendCustomBuildLog(
			ctx, lineClaim.ID, "default", lineClaim.LeaseToken,
			"x", false, now,
		); err != nil {
			t.Fatalf("append line %d: %v", sequence+1, err)
		}
	}
	if _, err := repository.AppendCustomBuildLog(
		ctx, lineClaim.ID, "default", lineClaim.LeaseToken,
		"over-limit", false, now,
	); !errors.Is(err, scannerrelease.ErrCustomBuildLogBudget) {
		t.Fatalf("line budget error = %v", err)
	}
	if _, err := repository.CompleteCustomBuildVariant(
		ctx, lineClaim.ID, "default", lineClaim.LeaseToken,
		scannerrelease.CustomBuildVariantResult{LoadedLocally: true}, now,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.FinalizeCustomBuild(
		ctx, lineClaim.ID, lineClaim.LeaseToken, now,
	); err != nil {
		t.Fatal(err)
	}

	byteLimited := customBuildRequest(false, []string{"rust"})
	byteInventory, _, err := repository.CreateCustomBuild(ctx, byteLimited)
	if err != nil {
		t.Fatal(err)
	}
	byteNow := time.Now().UTC().Add(time.Second)
	byteClaim, err := repository.ClaimNextCustomBuild(
		ctx, "byte-budget-worker", byteNow, byteNow.Add(time.Hour),
	)
	if err != nil || byteClaim == nil || byteClaim.ID != byteInventory.Build.ID {
		t.Fatalf("byte claim = %#v err=%v", byteClaim, err)
	}
	if _, err := repository.StartCustomBuild(
		ctx, byteClaim.ID, byteClaim.LeaseToken, byteNow,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.StartCustomBuildVariant(
		ctx, byteClaim.ID, "rust", byteClaim.LeaseToken, byteNow,
	); err != nil {
		t.Fatal(err)
	}
	maximumLine := strings.Repeat("b", customBuildMaxLine)
	for sequence := 0; sequence < customBuildMaxBytes/customBuildMaxLine; sequence++ {
		if _, err := repository.AppendCustomBuildLog(
			ctx, byteClaim.ID, "rust", byteClaim.LeaseToken,
			maximumLine, false, byteNow,
		); err != nil {
			t.Fatalf("append byte-budget line %d: %v", sequence+1, err)
		}
	}
	if _, err := repository.AppendCustomBuildLog(
		ctx, byteClaim.ID, "rust", byteClaim.LeaseToken,
		"x", false, byteNow,
	); !errors.Is(err, scannerrelease.ErrCustomBuildLogBudget) {
		t.Fatalf("byte budget error = %v", err)
	}
	if _, err := repository.AppendCustomBuildLog(
		ctx, byteClaim.ID, "rust", byteClaim.LeaseToken,
		maximumLine+"x", false, byteNow,
	); err == nil || errors.Is(err, scannerrelease.ErrCustomBuildLogBudget) {
		t.Fatalf("oversized line error = %v", err)
	}
}

func runScannerCustomBuildRepositoryContract(
	t *testing.T,
	factory func(*testing.T) releaseContractBackend,
) {
	t.Helper()
	backend := factory(t)
	t.Cleanup(func() { _ = backend.close() })
	repository := backend.persistence
	ctx := scannertrace.With(context.Background(), scannertrace.Correlation{
		TraceID:     "1123456789abcdef0123456789abcdef",
		OperationID: "custom-build-contract-operation",
		Component:   "custom-build-contract",
	})

	localRequest := customBuildRequest(false, []string{"default", "jvm"})
	inventory, created, err := repository.CreateCustomBuild(ctx, localRequest)
	if err != nil || !created {
		t.Fatalf("CreateCustomBuild = %#v created=%t err=%v", inventory, created, err)
	}
	if inventory.Build.ReservedVersion == "" ||
		inventory.Build.PublishVersion != nil ||
		inventory.Build.State != scannerrelease.CustomBuildQueued ||
		len(inventory.Variants) != 2 {
		t.Fatalf("created local inventory = %#v", inventory)
	}
	replayRequest := localRequest
	replayRequest.ID = uuid.NewString()
	replay, created, err := repository.CreateCustomBuild(ctx, replayRequest)
	if err != nil || created || replay.Build.ID != inventory.Build.ID {
		t.Fatalf("idempotent replay = %#v created=%t err=%v", replay, created, err)
	}
	conflict := localRequest
	conflict.ID = uuid.NewString()
	conflict.Variants = []string{"rust"}
	if _, _, err := repository.CreateCustomBuild(
		ctx, conflict,
	); !errors.Is(err, scannerrelease.ErrIdempotencyConflict) {
		t.Fatalf("conflicting replay error = %v", err)
	}

	codeQLInventory, created, err := repository.CreateCustomBuild(ctx, scannerrelease.CustomBuildCreateRequest{
		UserID: "user", Variants: []string{"codeql"}, Push: true,
		Platforms: []string{"linux/amd64", "linux/arm64"},
		Namespace: "registry", SecretReference: "opaque-secret",
		Actor: "operator", Reason: "registry publication",
		IdempotencyKey: "codeql-push-" + uuid.NewString(),
	})
	if err != nil || !created || codeQLInventory.Build.ID == "" {
		t.Fatalf("CodeQL push inventory = %#v created=%t err=%v", codeQLInventory, created, err)
	}
	if _, _, err := repository.CreateCustomBuild(ctx, scannerrelease.CustomBuildCreateRequest{
		UserID: "user", Variants: []string{"default"}, Push: false,
		Platforms: []string{"linux/amd64", "linux/arm64"},
		Namespace: "registry", Actor: "operator", Reason: "invalid local",
		IdempotencyKey: "multi-local-" + uuid.NewString(),
	}); err == nil || !strings.Contains(err.Error(), "at most one") {
		t.Fatalf("multi-platform local error = %v", err)
	}

	firstPush := customBuildRequest(true, []string{"default"})
	first, _, err := repository.CreateCustomBuild(ctx, firstPush)
	if err != nil {
		t.Fatal(err)
	}
	secondPush := customBuildRequest(true, []string{"rust"})
	second, _, err := repository.CreateCustomBuild(ctx, secondPush)
	if err != nil {
		t.Fatal(err)
	}
	if first.Build.PublishVersion == nil || second.Build.PublishVersion == nil ||
		*first.Build.PublishVersion == *second.Build.PublishVersion {
		t.Fatalf("publish version reservations: first=%#v second=%#v", first.Build, second.Build)
	}

	now := time.Now().UTC().Add(time.Second)
	claimed, err := repository.ClaimNextCustomBuild(
		ctx, "builder-a", now, now.Add(time.Minute),
	)
	if err != nil || claimed == nil || claimed.ID != inventory.Build.ID ||
		claimed.Attempt != 1 || claimed.LeaseToken == "" {
		t.Fatalf("ClaimNextCustomBuild = %#v err=%v", claimed, err)
	}
	running, err := repository.StartCustomBuild(
		ctx, claimed.ID, claimed.LeaseToken, now.Add(time.Second),
	)
	if err != nil || running.State != scannerrelease.CustomBuildRunning {
		t.Fatalf("StartCustomBuild = %#v err=%v", running, err)
	}
	status, err := repository.HeartbeatCustomBuild(
		ctx, claimed.ID, claimed.LeaseToken, now.Add(61*time.Second),
		now.Add(2*time.Minute),
	)
	if err != nil || !status.Current || status.CancelRequested {
		t.Fatalf("late HeartbeatCustomBuild = %#v err=%v", status, err)
	}
	if reclaimed, err := repository.ReclaimStaleCustomBuilds(
		ctx, now.Add(70*time.Second),
	); err != nil || reclaimed != 0 {
		t.Fatalf("reclaim renewed custom build = %d err=%v", reclaimed, err)
	}
	if _, err := repository.StartCustomBuildVariant(
		ctx, claimed.ID, "default", claimed.LeaseToken, now.Add(62*time.Second),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.AppendCustomBuildLog(
		ctx, claimed.ID, "default", claimed.LeaseToken,
		"token=[REDACTED]", true, now.Add(63*time.Second),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CompleteCustomBuildVariant(
		ctx, claimed.ID, "default", claimed.LeaseToken,
		scannerrelease.CustomBuildVariantResult{
			Refs: []string{"registry/default:test"}, Digest: "sha256:default",
			LoadedLocally: true,
		},
		now.Add(64*time.Second),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.StartCustomBuildVariant(
		ctx, claimed.ID, "jvm", claimed.LeaseToken, now.Add(65*time.Second),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CompleteCustomBuildVariant(
		ctx, claimed.ID, "jvm", claimed.LeaseToken,
		scannerrelease.CustomBuildVariantResult{
			ErrorClass: "build_failed", ErrorDetail: strings.Repeat("x", 1000),
		},
		now.Add(66*time.Second),
	); err != nil {
		t.Fatal(err)
	}
	finalized, err := repository.FinalizeCustomBuild(
		ctx, claimed.ID, claimed.LeaseToken, now.Add(67*time.Second),
	)
	if err != nil || finalized.State != scannerrelease.CustomBuildPartial ||
		len(finalized.ErrorDetail) > 512 {
		t.Fatalf("FinalizeCustomBuild = %#v err=%v", finalized, err)
	}
	logs, err := repository.ListCustomBuildLogs(ctx, claimed.ID, 0, 20)
	if err != nil || len(logs) != 1 || !logs[0].Redacted ||
		logs[0].Sequence != 1 {
		t.Fatalf("custom-build logs = %#v err=%v", logs, err)
	}
	events, err := repository.ListEvents(
		ctx, "custom_build", claimed.ID, 0, 50,
	)
	if err != nil || len(events) < 8 {
		t.Fatalf("custom-build events = %#v err=%v", events, err)
	}
	for _, event := range events {
		if event.TraceID != "1123456789abcdef0123456789abcdef" ||
			event.OperationID != "custom-build-contract-operation" {
			t.Fatalf("event lost correlation: %#v", event)
		}
	}

	retried, err := repository.RetryCustomBuild(
		ctx, finalized.ID, finalized.Version,
		scannerrelease.TransitionCommand{
			Actor: "operator", Reason: "retry failed variant",
			IdempotencyKey: "retry-" + finalized.ID,
		},
		now.Add(9*time.Second),
	)
	if err != nil || retried.State != scannerrelease.CustomBuildQueued {
		t.Fatalf("RetryCustomBuild = %#v err=%v", retried, err)
	}
	retriedInventory, err := repository.GetCustomBuild(ctx, retried.ID)
	if err != nil ||
		retriedInventory.Variants[0].State != scannerrelease.CustomBuildVariantCompleted ||
		retriedInventory.Variants[1].State != scannerrelease.CustomBuildVariantQueued {
		t.Fatalf("retry variant preservation = %#v err=%v", retriedInventory, err)
	}

	cancelRequest := customBuildRequest(false, []string{"codeql"})
	cancelInventory, _, err := repository.CreateCustomBuild(ctx, cancelRequest)
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := repository.RequestCustomBuildCancellation(
		ctx, cancelInventory.Build.ID, cancelInventory.Build.Version,
		scannerrelease.TransitionCommand{
			Actor: "operator", Reason: "cancel queued build",
			IdempotencyKey: "cancel-" + cancelInventory.Build.ID,
		},
		now,
	)
	if err != nil || cancelled.State != scannerrelease.CustomBuildCancelled {
		t.Fatalf("RequestCustomBuildCancellation = %#v err=%v", cancelled, err)
	}

	staleRequest := customBuildRequest(false, []string{"rust"})
	staleRequest.MaxAttempts = 1
	staleInventory, _, err := repository.CreateCustomBuild(ctx, staleRequest)
	if err != nil {
		t.Fatal(err)
	}
	staleClaim, err := claimCustomBuildByID(
		ctx, repository, staleInventory.Build.ID, "stale-builder",
		now.Add(10*time.Second), now.Add(11*time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.StartCustomBuild(
		ctx, staleClaim.ID, staleClaim.LeaseToken, now.Add(10*time.Second),
	); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := repository.ReclaimStaleCustomBuilds(
		ctx, now.Add(12*time.Second),
	)
	if err != nil || reclaimed < 1 {
		t.Fatalf("ReclaimStaleCustomBuilds = %d err=%v", reclaimed, err)
	}
	staleResult, err := repository.GetCustomBuild(ctx, staleClaim.ID)
	if err != nil || staleResult.Build.State != scannerrelease.CustomBuildFailed ||
		staleResult.Build.ErrorClass != "worker_lost" {
		t.Fatalf("stale terminal result = %#v err=%v", staleResult, err)
	}
}

func customBuildRequest(
	push bool,
	variants []string,
) scannerrelease.CustomBuildCreateRequest {
	request := scannerrelease.CustomBuildCreateRequest{
		ID: uuid.NewString(), UserID: "custom-build-user",
		Variants: variants, Push: push, Namespace: "testnamespace",
		Actor: "operator@example.test", Reason: "repository contract",
		IdempotencyKey: "custom-build-" + uuid.NewString(), MaxAttempts: 3,
	}
	if push {
		request.Platforms = []string{"linux/amd64", "linux/arm64"}
		request.SecretReference = "opaque-secret-reference"
	}
	return request
}

func claimCustomBuildByID(
	ctx context.Context,
	repository scannerrelease.Persistence,
	id, workerID string,
	now, leaseUntil time.Time,
) (*scannerrelease.CustomBuild, error) {
	for attempt := 0; attempt < 20; attempt++ {
		claimed, err := repository.ClaimNextCustomBuild(
			ctx, workerID, now, leaseUntil,
		)
		if err != nil {
			return nil, err
		}
		if claimed == nil {
			return nil, errors.New("no custom build available")
		}
		if claimed.ID == id {
			return claimed, nil
		}
		_, _ = repository.RequestCustomBuildCancellation(
			ctx, claimed.ID, claimed.Version,
			scannerrelease.TransitionCommand{
				Actor: "test", Reason: "skip unrelated contract build",
				IdempotencyKey: "skip-" + claimed.ID,
			},
			now,
		)
		_, _ = repository.FinalizeCustomBuild(
			ctx, claimed.ID, claimed.LeaseToken, now,
		)
	}
	return nil, errors.New("custom build was not claimable")
}
