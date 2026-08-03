package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func seedQueuedScan(t *testing.T, store *SQLiteStore, userID, repoID string) *models.Scan {
	t.Helper()
	scan := &models.Scan{
		ID:        uuid.NewString(),
		UserID:    userID,
		RepoID:    repoID,
		Branch:    "main",
		Status:    models.ScanStatusPending,
		AIEnabled: false,
	}
	if err := store.CreateScan(context.Background(), scan); err != nil {
		t.Fatalf("CreateScan: %v", err)
	}
	return scan
}

func seedQueueOwner(t *testing.T, store *SQLiteStore) (string, string) {
	t.Helper()
	ctx := context.Background()
	userID := uuid.NewString()
	repoID := uuid.NewString()
	if err := store.CreateUser(ctx, &models.User{
		ID: userID, Email: userID + "@example.test", PasswordHash: "hash",
	}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := store.CreateRepo(ctx, &models.Repo{
		ID: repoID, UserID: userID, Name: "queue-repo",
		SourceType: models.SourceTypeLocal, SourcePath: t.TempDir(), DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}
	return userID, repoID
}

func TestCreateScanAppliesDurableQueueDefaults(t *testing.T) {
	store := newTestStore(t)
	userID, repoID := seedQueueOwner(t, store)
	scan := seedQueuedScan(t, store, userID, repoID)

	got, err := store.GetScanByID(context.Background(), scan.ID)
	if err != nil {
		t.Fatalf("GetScanByID: %v", err)
	}
	if got.Phase != "queued" || got.MaxAttempts != 2 || got.RequestJSON != "{}" {
		t.Fatalf("unexpected queue defaults: phase=%q max_attempts=%d request=%q",
			got.Phase, got.MaxAttempts, got.RequestJSON)
	}
	if got.ToolsSelected != "[]" || got.ToolsCompleted != "[]" || got.ToolsFailed != "[]" || got.ToolsErrors != "{}" {
		t.Fatalf("legacy JSON fields must be initialized, got selected=%q completed=%q failed=%q errors=%q",
			got.ToolsSelected, got.ToolsCompleted, got.ToolsFailed, got.ToolsErrors)
	}
}

func TestClaimNextScanIsAtomicAndLeaseGuarded(t *testing.T) {
	store := newTestStore(t)
	userID, repoID := seedQueueOwner(t, store)
	queued := seedQueuedScan(t, store, userID, repoID)

	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan *models.Scan, 2)
	errs := make(chan error, 2)
	for _, workerID := range []string{"worker-a", "worker-b"} {
		workerID := workerID
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			claimed, err := store.ClaimNextScan(context.Background(), workerID, "docker", time.Now().Add(time.Minute))
			results <- claimed
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("ClaimNextScan: %v", err)
		}
	}
	var claims []*models.Scan
	for scan := range results {
		if scan != nil {
			claims = append(claims, scan)
		}
	}
	if len(claims) != 1 {
		t.Fatalf("expected exactly one claim, got %d", len(claims))
	}
	claim := claims[0]
	if claim.ID != queued.ID || claim.Attempt != 1 || claim.LeaseToken == "" || claim.Status != models.ScanStatusRunning {
		t.Fatalf("invalid claim: %#v", claim)
	}
	if ok, err := store.HeartbeatScanLease(context.Background(), claim.ID, "stale-token", time.Now().Add(time.Minute)); err != nil || ok {
		t.Fatalf("stale lease heartbeat must fail, ok=%v err=%v", ok, err)
	}
	if ok, err := store.HeartbeatScanLease(context.Background(), claim.ID, claim.LeaseToken, time.Now().Add(time.Minute)); err != nil || !ok {
		t.Fatalf("current lease heartbeat must succeed, ok=%v err=%v", ok, err)
	}
}

func TestReclaimStaleScansRequeuesThenExhaustsRetries(t *testing.T) {
	store := newTestStore(t)
	userID, repoID := seedQueueOwner(t, store)
	queued := seedQueuedScan(t, store, userID, repoID)
	ctx := context.Background()

	claim, err := store.ClaimNextScan(ctx, "worker-a", "docker", time.Now().Add(-time.Minute))
	if err != nil || claim == nil {
		t.Fatalf("initial claim: scan=%v err=%v", claim, err)
	}
	if n, err := store.ReclaimStaleScans(ctx, time.Now()); err != nil || n != 1 {
		t.Fatalf("first reclaim: n=%d err=%v", n, err)
	}
	got, _ := store.GetScanByID(ctx, queued.ID)
	if got.Status != models.ScanStatusPending || got.Phase != "queued" {
		t.Fatalf("expected requeue, got status=%s phase=%s", got.Status, got.Phase)
	}

	claim, err = store.ClaimNextScan(ctx, "worker-b", "docker", time.Now().Add(-time.Minute))
	if err != nil || claim == nil || claim.Attempt != 2 {
		t.Fatalf("second claim: scan=%v err=%v", claim, err)
	}
	if n, err := store.ReclaimStaleScans(ctx, time.Now()); err != nil || n != 1 {
		t.Fatalf("second reclaim: n=%d err=%v", n, err)
	}
	got, _ = store.GetScanByID(ctx, queued.ID)
	if got.Status != models.ScanStatusFailed || got.FailureCode != "worker_lost" {
		t.Fatalf("expected exhausted failure, got status=%s code=%s", got.Status, got.FailureCode)
	}
}

func TestFinalizeScanRequiresCurrentLeaseAndRejectsCancellation(t *testing.T) {
	store := newTestStore(t)
	userID, repoID := seedQueueOwner(t, store)
	queued := seedQueuedScan(t, store, userID, repoID)
	ctx := context.Background()

	claim, err := store.ClaimNextScan(ctx, "worker-a", "docker", time.Now().Add(time.Minute))
	if err != nil || claim == nil {
		t.Fatalf("claim: scan=%v err=%v", claim, err)
	}
	now := time.Now().UTC()
	claim.Status = models.ScanStatusCompleted
	claim.Phase = "completed"
	claim.CompletedAt = &now
	if ok, err := store.FinalizeScan(ctx, claim, "stale-token"); err != nil || ok {
		t.Fatalf("stale finalization must fail, ok=%v err=%v", ok, err)
	}
	got, _ := store.GetScanByID(ctx, queued.ID)
	if got.Status != models.ScanStatusRunning {
		t.Fatalf("stale finalization changed status to %s", got.Status)
	}

	if err := store.RequestScanCancellation(ctx, claim.ID, now); err != nil {
		t.Fatalf("RequestScanCancellation: %v", err)
	}
	if ok, err := store.FinalizeScan(ctx, claim, claim.LeaseToken); err != nil || ok {
		t.Fatalf("cancelled finalization must fail, ok=%v err=%v", ok, err)
	}
	got, _ = store.GetScanByID(ctx, queued.ID)
	if got.Status != models.ScanStatusCancelled {
		t.Fatalf("finalization overwrote cancellation with %s", got.Status)
	}
}

func TestFinalizeScanClearsExecutionOwnership(t *testing.T) {
	store := newTestStore(t)
	userID, repoID := seedQueueOwner(t, store)
	queued := seedQueuedScan(t, store, userID, repoID)
	ctx := context.Background()

	claim, err := store.ClaimNextScan(ctx, "worker-a", "docker", time.Now().Add(time.Minute))
	if err != nil || claim == nil {
		t.Fatalf("claim: scan=%v err=%v", claim, err)
	}
	now := time.Now().UTC()
	claim.Status = models.ScanStatusCompleted
	claim.Phase = "completed"
	claim.CompletedAt = &now
	if ok, err := store.FinalizeScan(ctx, claim, claim.LeaseToken); err != nil || !ok {
		t.Fatalf("finalize: ok=%v err=%v", ok, err)
	}
	got, err := store.GetScanByID(ctx, queued.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != models.ScanStatusCompleted || got.ClaimedBy != "" || got.LeaseToken != "" ||
		got.LeaseExpiresAt != nil || got.HeartbeatAt != nil {
		t.Fatalf("terminal scan retained execution ownership: %#v", got)
	}
}

func TestUpdateScanRejectsStaleOrCancelledExecutorState(t *testing.T) {
	t.Run("cancelled", func(t *testing.T) {
		store := newTestStore(t)
		userID, repoID := seedQueueOwner(t, store)
		queued := seedQueuedScan(t, store, userID, repoID)
		ctx := context.Background()
		claim, err := store.ClaimNextScan(ctx, "worker-a", "docker", time.Now().Add(time.Minute))
		if err != nil || claim == nil {
			t.Fatalf("claim: scan=%v err=%v", claim, err)
		}
		if err := store.RequestScanCancellation(ctx, claim.ID, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		claim.ToolsSelected = `["stale-tool"]`
		if err := store.UpdateScan(ctx, claim); err != nil {
			t.Fatal(err)
		}
		got, _ := store.GetScanByID(ctx, queued.ID)
		if got.Status != models.ScanStatusCancelled || got.ToolsSelected != "[]" {
			t.Fatalf("cancelled scan accepted stale state: status=%s tools=%s", got.Status, got.ToolsSelected)
		}
	})

	t.Run("reclaimed", func(t *testing.T) {
		store := newTestStore(t)
		userID, repoID := seedQueueOwner(t, store)
		queued := seedQueuedScan(t, store, userID, repoID)
		ctx := context.Background()
		oldClaim, err := store.ClaimNextScan(ctx, "worker-a", "docker", time.Now().Add(-time.Minute))
		if err != nil || oldClaim == nil {
			t.Fatalf("old claim: scan=%v err=%v", oldClaim, err)
		}
		if _, err := store.ReclaimStaleScans(ctx, time.Now()); err != nil {
			t.Fatal(err)
		}
		newClaim, err := store.ClaimNextScan(ctx, "worker-b", "docker", time.Now().Add(time.Minute))
		if err != nil || newClaim == nil {
			t.Fatalf("new claim: scan=%v err=%v", newClaim, err)
		}
		oldClaim.ToolsSelected = `["stale-tool"]`
		if err := store.UpdateScan(ctx, oldClaim); err != nil {
			t.Fatal(err)
		}
		got, _ := store.GetScanByID(ctx, queued.ID)
		if got.LeaseToken != newClaim.LeaseToken || got.ToolsSelected != "[]" {
			t.Fatalf("reclaimed scan accepted stale state: lease=%s tools=%s", got.LeaseToken, got.ToolsSelected)
		}
	})
}

func TestCreateFindingsForScanLeaseRejectsStaleOwnership(t *testing.T) {
	store := newTestStore(t)
	userID, repoID := seedQueueOwner(t, store)
	queued := seedQueuedScan(t, store, userID, repoID)
	ctx := context.Background()

	oldClaim, err := store.ClaimNextScan(ctx, "worker-a", "docker", time.Now().Add(-time.Minute))
	if err != nil || oldClaim == nil {
		t.Fatalf("old claim: scan=%v err=%v", oldClaim, err)
	}
	if _, err := store.ReclaimStaleScans(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	newClaim, err := store.ClaimNextScan(ctx, "worker-b", "docker", time.Now().Add(time.Minute))
	if err != nil || newClaim == nil {
		t.Fatalf("new claim: scan=%v err=%v", newClaim, err)
	}
	staleFinding := []models.Finding{{
		ID: uuid.NewString(), ScanID: queued.ID, RepoID: repoID,
		ToolName: "semgrep", Category: models.CategorySAST,
		Severity: models.SeverityHigh, Title: "stale", Fingerprint: uuid.NewString(),
	}}
	if ok, err := store.CreateFindingsForScanLease(ctx, staleFinding, queued.ID, oldClaim.LeaseToken); err != nil || ok {
		t.Fatalf("stale finding insert must fail, ok=%v err=%v", ok, err)
	}
	findings, err := store.ListFindingsByScan(ctx, queued.ID)
	if err != nil || len(findings) != 0 {
		t.Fatalf("stale findings were persisted: findings=%v err=%v", findings, err)
	}
	currentFinding := []models.Finding{{
		ID: uuid.NewString(), ScanID: queued.ID, RepoID: repoID,
		ToolName: "semgrep", Category: models.CategorySAST,
		Severity: models.SeverityHigh, Title: "current", Fingerprint: uuid.NewString(),
	}}
	if ok, err := store.CreateFindingsForScanLease(ctx, currentFinding, queued.ID, newClaim.LeaseToken); err != nil || !ok {
		t.Fatalf("current finding insert must succeed, ok=%v err=%v", ok, err)
	}
	findings, err = store.ListFindingsByScan(ctx, queued.ID)
	if err != nil || len(findings) != 1 || findings[0].Title != "current" {
		t.Fatalf("current findings missing: findings=%v err=%v", findings, err)
	}
}

func TestScannerRunsAndEventsRejectStaleLeaseOwnership(t *testing.T) {
	store := newTestStore(t)
	userID, repoID := seedQueueOwner(t, store)
	queued := seedQueuedScan(t, store, userID, repoID)
	ctx := context.Background()

	oldClaim, err := store.ClaimNextScan(ctx, "worker-a", "docker", time.Now().Add(-time.Minute))
	if err != nil || oldClaim == nil {
		t.Fatalf("old claim: scan=%v err=%v", oldClaim, err)
	}
	if _, err := store.ReclaimStaleScans(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	newClaim, err := store.ClaimNextScan(ctx, "worker-b", "docker", time.Now().Add(time.Minute))
	if err != nil || newClaim == nil {
		t.Fatalf("new claim: scan=%v err=%v", newClaim, err)
	}

	currentRun := &models.ScannerRunRecord{
		ScanID: queued.ID, ToolName: "semgrep", Status: "running",
		LeaseToken: newClaim.LeaseToken,
	}
	if err := store.UpsertScannerRunRecord(ctx, currentRun); err != nil {
		t.Fatalf("current scanner-run write failed: %v", err)
	}
	staleRun := &models.ScannerRunRecord{
		ScanID: queued.ID, ToolName: "semgrep", Status: "completed",
		LeaseToken: oldClaim.LeaseToken,
	}
	if err := store.UpsertScannerRunRecord(ctx, staleRun); !errors.Is(err, ErrStaleScanLease) {
		t.Fatalf("stale scanner-run write error = %v, want ErrStaleScanLease", err)
	}
	runs, err := store.ListScannerRunRecords(ctx, queued.ID)
	if err != nil || len(runs) != 1 || runs[0].Status != "running" {
		t.Fatalf("stale scanner-run write changed state: runs=%v err=%v", runs, err)
	}

	currentEvent := &models.ScanEvent{
		ScanID: queued.ID, EventType: "scan_progress", DataJSON: `{"worker":"current"}`,
		LeaseToken: newClaim.LeaseToken,
	}
	if err := store.AppendScanEvent(ctx, currentEvent); err != nil {
		t.Fatalf("current event write failed: %v", err)
	}
	staleEvent := &models.ScanEvent{
		ScanID: queued.ID, EventType: "scan_progress", DataJSON: `{"worker":"stale"}`,
		LeaseToken: oldClaim.LeaseToken,
	}
	if err := store.AppendScanEvent(ctx, staleEvent); !errors.Is(err, ErrStaleScanLease) {
		t.Fatalf("stale event write error = %v, want ErrStaleScanLease", err)
	}
	events, err := store.ListScanEvents(ctx, queued.ID, 0, 100)
	if err != nil || len(events) != 1 || events[0].DataJSON != `{"worker":"current"}` {
		t.Fatalf("stale event was persisted: events=%v err=%v", events, err)
	}
}

func TestScanEventsAreMonotonicAndReplayable(t *testing.T) {
	store := newTestStore(t)
	userID, repoID := seedQueueOwner(t, store)
	scan := seedQueuedScan(t, store, userID, repoID)
	ctx := context.Background()

	for _, eventType := range []string{"scan_status", "tools_selected", "scan_progress"} {
		event := &models.ScanEvent{ScanID: scan.ID, EventType: eventType, DataJSON: `{"ok":true}`}
		if err := store.AppendScanEvent(ctx, event); err != nil {
			t.Fatalf("AppendScanEvent(%s): %v", eventType, err)
		}
	}
	events, err := store.ListScanEvents(ctx, scan.ID, 1, 100)
	if err != nil {
		t.Fatalf("ListScanEvents: %v", err)
	}
	if len(events) != 2 || events[0].Sequence != 2 || events[1].Sequence != 3 {
		t.Fatalf("unexpected replay sequence: %#v", events)
	}
}

func TestDurableToolOutputIsBoundedAndEmitsDropMarker(t *testing.T) {
	store := newTestStore(t)
	userID, repoID := seedQueueOwner(t, store)
	scan := seedQueuedScan(t, store, userID, repoID)
	ctx := context.Background()

	for i := 0; i < models.MaxDurableToolOutputEventsPerScan; i++ {
		event := &models.ScanEvent{
			ScanID: scan.ID, EventType: models.ScanEventTypeToolOutput,
			DataJSON: fmt.Sprintf(`{"type":"tool_output","line":"%d"}`, i),
		}
		if err := store.AppendScanEvent(ctx, event); err != nil {
			t.Fatalf("AppendScanEvent(%d): %v", i, err)
		}
	}
	overflow := &models.ScanEvent{
		ScanID: scan.ID, EventType: models.ScanEventTypeToolOutput,
		DataJSON: `{"type":"tool_output","line":"overflow"}`,
	}
	if err := store.AppendScanEvent(ctx, overflow); err != nil {
		t.Fatalf("first overflow should persist a marker: %v", err)
	}
	if overflow.EventType != models.ScanEventTypeToolOutputDropped ||
		!strings.Contains(overflow.DataJSON, `"reason":"event_limit"`) ||
		!strings.Contains(overflow.DataJSON, `"type":"tool_output"`) ||
		!strings.Contains(overflow.DataJSON, "complete output remains in scan artifacts") {
		t.Fatalf("overflow event was not converted to an explicit drop marker: %#v", overflow)
	}
	if err := store.AppendScanEvent(ctx, &models.ScanEvent{
		ScanID: scan.ID, EventType: models.ScanEventTypeToolOutput,
		DataJSON: `{"type":"tool_output","line":"dropped"}`,
	}); !errors.Is(err, ErrScanEventDropped) {
		t.Fatalf("subsequent overflow error = %v, want ErrScanEventDropped", err)
	}

	var count int
	if err := store.db.GetContext(ctx, &count,
		`SELECT COUNT(*) FROM scan_events WHERE scan_id = ?`, scan.ID); err != nil {
		t.Fatal(err)
	}
	if count != models.MaxDurableToolOutputEventsPerScan+1 {
		t.Fatalf("persisted event count = %d, want %d",
			count, models.MaxDurableToolOutputEventsPerScan+1)
	}

	// Status/progress events are operational state and must remain durable
	// after verbose tool output is capped.
	if err := store.AppendScanEvent(ctx, &models.ScanEvent{
		ScanID: scan.ID, EventType: "scan_progress",
		DataJSON: `{"type":"scan_progress","status":"completed"}`,
	}); err != nil {
		t.Fatalf("non-output event was incorrectly dropped: %v", err)
	}
}

func TestOversizedDurableToolOutputIsReplacedWithTruncationMarker(t *testing.T) {
	store := newTestStore(t)
	userID, repoID := seedQueueOwner(t, store)
	scan := seedQueuedScan(t, store, userID, repoID)
	event := &models.ScanEvent{
		ScanID: scan.ID, EventType: models.ScanEventTypeToolOutput,
		DataJSON: `{"type":"tool_output","line":"` +
			strings.Repeat("x", models.MaxDurableScanEventDataBytes) + `"}`,
	}
	originalBytes := len(event.DataJSON)

	if err := store.AppendScanEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if event.EventType != models.ScanEventTypeToolOutputTruncated {
		t.Fatalf("event type = %q, want truncation marker", event.EventType)
	}
	if len(event.DataJSON) > models.MaxDurableScanEventDataBytes ||
		!strings.Contains(event.DataJSON, `"reason":"event_too_large"`) ||
		!strings.Contains(event.DataJSON, `"type":"tool_output"`) ||
		!strings.Contains(event.DataJSON, fmt.Sprintf(`"original_bytes":%d`, originalBytes)) {
		t.Fatalf("unexpected truncation marker: %s", event.DataJSON)
	}
}

func TestFindScanByIdempotencyKey(t *testing.T) {
	store := newTestStore(t)
	userID, repoID := seedQueueOwner(t, store)
	scan := seedQueuedScan(t, store, userID, repoID)
	scan.IdempotencyKey = "build-42"
	scan.RequestDigest = "digest"
	if err := store.UpdateScan(context.Background(), scan); err != nil {
		t.Fatalf("UpdateScan: %v", err)
	}

	got, err := store.FindScanByIdempotencyKey(context.Background(), userID, "build-42")
	if err != nil || got.ID != scan.ID {
		t.Fatalf("FindScanByIdempotencyKey: got=%v err=%v", got, err)
	}
	if _, err := store.FindScanByIdempotencyKey(context.Background(), userID, "missing"); err != sql.ErrNoRows {
		t.Fatalf("expected sql.ErrNoRows, got %v", err)
	}
}
