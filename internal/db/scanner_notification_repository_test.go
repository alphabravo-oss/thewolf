package db

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

func TestScannerNotificationRepositoryContractSQLite(t *testing.T) {
	runScannerNotificationRepositoryContract(t, newSQLiteReleaseContractBackend)
}

func TestScannerNotificationRepositoryContractPostgres(t *testing.T) {
	runScannerNotificationRepositoryContract(t, newPostgresReleaseContractBackend)
}

func runScannerNotificationRepositoryContract(
	t *testing.T,
	factory func(*testing.T) releaseContractBackend,
) {
	t.Helper()
	backend := factory(t)
	t.Cleanup(func() { _ = backend.close() })
	ctx := context.Background()
	repository := backend.persistence

	policy := newPolicy("notification:"+uuid.NewString(), 1)
	policy.RulesJSON = `{"notifications":{"destinations":[` +
		`"webhook:security-operations","email:release-approvers","siem:primary"]}}`
	if err := repository.CreatePolicy(ctx, policy); err != nil {
		t.Fatalf("CreatePolicy: %v", err)
	}
	candidate := createAwaitingApprovalCandidate(t, ctx, repository, policy)

	page, err := repository.ListNotifications(
		ctx,
		scannerrelease.NotificationFilter{
			NotificationType: "candidate_ready_for_approval",
		},
		scannerrelease.PageRequest{Limit: 100},
	)
	if err != nil {
		t.Fatalf("ListNotifications: %v", err)
	}
	var candidateNotifications []scannerrelease.Notification
	for _, item := range page.Items {
		if item.AggregateID == candidate.ID {
			candidateNotifications = append(candidateNotifications, item)
		}
	}
	if len(candidateNotifications) != 4 {
		t.Fatalf("candidate notifications = %#v, want UI plus three external rows", candidateNotifications)
	}
	destinationStates := map[scannerrelease.NotificationDestinationType]scannerrelease.NotificationState{}
	for _, notification := range candidateNotifications {
		destinationStates[notification.DestinationType] = notification.State
		if len(notification.PayloadJSON) > maxNotificationPayloadBytes {
			t.Fatalf("notification payload length = %d", len(notification.PayloadJSON))
		}
		if strings.Contains(notification.PayloadJSON, "super-secret-canary") ||
			strings.Contains(notification.PayloadJSON, "operator@example.test") {
			t.Fatalf("notification payload copied private command material: %s", notification.PayloadJSON)
		}
		if notification.PolicyID != policy.ID || notification.PolicyRevision != policy.Revision {
			t.Fatalf("notification policy identity = %#v", notification)
		}
	}
	if destinationStates[scannerrelease.NotificationDestinationUI] != scannerrelease.NotificationDelivered ||
		destinationStates[scannerrelease.NotificationDestinationWebhook] != scannerrelease.NotificationPending ||
		destinationStates[scannerrelease.NotificationDestinationEmail] != scannerrelease.NotificationPending ||
		destinationStates[scannerrelease.NotificationDestinationSIEM] != scannerrelease.NotificationPending {
		t.Fatalf("notification destination states = %#v", destinationStates)
	}

	now := time.Now().UTC().Add(time.Second)
	claimed, err := repository.ClaimNextNotification(
		ctx, "notification-worker-a", now, now.Add(time.Minute),
	)
	if err != nil || claimed == nil || claimed.AggregateID != candidate.ID ||
		claimed.State != scannerrelease.NotificationDelivering ||
		claimed.Attempt != 1 || claimed.LeaseToken == "" {
		t.Fatalf("ClaimNextNotification = %#v err=%v", claimed, err)
	}
	stale, err := repository.HeartbeatNotification(
		ctx, claimed.ID, "notification-worker-a", "wrong-token",
		now.Add(time.Second), now.Add(time.Minute),
	)
	if err != nil || stale.Current {
		t.Fatalf("stale heartbeat = %#v err=%v", stale, err)
	}
	current, err := repository.HeartbeatNotification(
		ctx, claimed.ID, "notification-worker-a", claimed.LeaseToken,
		now.Add(time.Second), now.Add(2*time.Minute),
	)
	if err != nil || !current.Current {
		t.Fatalf("current heartbeat = %#v err=%v", current, err)
	}

	retryAt := now.Add(30 * time.Second)
	retrying, err := repository.FinalizeNotification(
		ctx, claimed.ID, "notification-worker-a", claimed.LeaseToken,
		scannerrelease.NotificationRetry, retryAt,
		"provider_unavailable", "credential [REDACTED]", now.Add(2*time.Second),
	)
	if err != nil || retrying.State != scannerrelease.NotificationRetry ||
		!retrying.AvailableAt.Equal(retryAt) || retrying.WorkerID != "" {
		t.Fatalf("retry finalization = %#v err=%v", retrying, err)
	}
	early, err := repository.ClaimNextNotification(
		ctx, "notification-worker-b", retryAt.Add(-time.Second), retryAt.Add(time.Minute),
	)
	if err != nil {
		t.Fatalf("early claim: %v", err)
	}
	// Other destinations for this same event remain immediately eligible; an
	// early claim must never return the delayed notification.
	if early != nil && early.ID == retrying.ID {
		t.Fatal("retry notification was claimed before available_at")
	}
	if early != nil {
		if _, err := repository.FinalizeNotification(
			ctx, early.ID, "notification-worker-b", early.LeaseToken,
			scannerrelease.NotificationDelivered, retryAt, "", "",
			retryAt.Add(-500*time.Millisecond),
		); err != nil {
			t.Fatalf("complete other destination: %v", err)
		}
	}

	reclaimed, err := claimNotificationByID(
		ctx, repository, retrying.ID, "notification-worker-c",
		retryAt.Add(time.Second),
	)
	if err != nil || reclaimed == nil || reclaimed.Attempt != 2 {
		t.Fatalf("retry claim = %#v err=%v", reclaimed, err)
	}
	dead, err := repository.FinalizeNotification(
		ctx, reclaimed.ID, "notification-worker-c", reclaimed.LeaseToken,
		scannerrelease.NotificationDeadLetter, retryAt,
		"delivery_rejected", "provider rejected payload", retryAt.Add(2*time.Second),
	)
	if err != nil || dead.State != scannerrelease.NotificationDeadLetter ||
		dead.DeadLetteredAt == nil {
		t.Fatalf("dead-letter finalization = %#v err=%v", dead, err)
	}
	if _, err := repository.FinalizeNotification(
		ctx, dead.ID, "notification-worker-c", reclaimed.LeaseToken,
		scannerrelease.NotificationDelivered, retryAt, "", "", retryAt.Add(3*time.Second),
	); !errors.Is(err, scannerrelease.ErrLeaseNotOwned) {
		t.Fatalf("stale finalization error = %v, want ErrLeaseNotOwned", err)
	}

	operatorCommand := scannerrelease.TransitionCommand{
		Actor: "security-admin@example.test", Reason: "provider routing corrected",
		IdempotencyKey: "operator-retry:" + dead.ID,
	}
	operatorRetry, err := repository.RetryDeadLetterNotification(
		ctx, dead.ID, dead.Version, operatorCommand, retryAt.Add(4*time.Second),
	)
	if err != nil || operatorRetry.State != scannerrelease.NotificationRetry ||
		operatorRetry.Attempt != 0 || operatorRetry.DeadLetteredAt != nil {
		t.Fatalf("operator retry = %#v err=%v", operatorRetry, err)
	}
	replayed, err := repository.RetryDeadLetterNotification(
		ctx, dead.ID, dead.Version, operatorCommand, retryAt.Add(5*time.Second),
	)
	if err != nil || replayed.Version != operatorRetry.Version {
		t.Fatalf("idempotent operator retry = %#v err=%v", replayed, err)
	}
	finalClaim, err := claimNotificationByID(
		ctx, repository, dead.ID, "notification-worker-d", retryAt.Add(6*time.Second),
	)
	if err != nil || finalClaim == nil || finalClaim.Attempt != 1 {
		t.Fatalf("operator retry claim = %#v err=%v", finalClaim, err)
	}
	delivered, err := repository.FinalizeNotification(
		ctx, finalClaim.ID, "notification-worker-d", finalClaim.LeaseToken,
		scannerrelease.NotificationDelivered, retryAt, "", "", retryAt.Add(7*time.Second),
	)
	if err != nil || delivered.State != scannerrelease.NotificationDelivered ||
		delivered.DeliveredAt == nil {
		t.Fatalf("delivered finalization = %#v err=%v", delivered, err)
	}

	staleCandidate := createAwaitingApprovalCandidate(t, ctx, repository, policy)
	staleClaim, err := claimNotificationForAggregate(
		ctx, repository, staleCandidate.ID, "notification-worker-lost",
		retryAt.Add(8*time.Second),
	)
	if err != nil {
		t.Fatalf("claim stale notification: %v", err)
	}
	if staleClaim == nil {
		t.Fatal("claim stale notification returned nil")
	}
	if _, err := backend.exec(
		ctx, `UPDATE scanner_release_notifications SET max_attempts = 1 WHERE id = ?`,
		staleClaim.ID,
	); err != nil {
		t.Fatalf("bound stale attempt budget: %v", err)
	}
	summary, err := repository.ReclaimStaleNotifications(
		ctx, retryAt.Add(8*time.Second).Add(2*time.Minute),
	)
	if err != nil || summary.DeadLettered < 1 {
		t.Fatalf("ReclaimStaleNotifications = %#v err=%v", summary, err)
	}
	staleDead, err := repository.GetNotification(ctx, staleClaim.ID)
	if err != nil || staleDead.State != scannerrelease.NotificationDeadLetter ||
		staleDead.ErrorClass != "worker_lost" {
		t.Fatalf("reclaimed notification = %#v err=%v", staleDead, err)
	}

	events, err := repository.ListEvents(ctx, "notification", dead.ID, 0, 100)
	if err != nil {
		t.Fatalf("ListEvents(notification): %v", err)
	}
	var claimedEvent, retryEvent, deadLetterEvent, operatorEvent, deliveredEvent bool
	for _, event := range events {
		switch event.EventType {
		case "notification.claimed":
			claimedEvent = true
		case "notification.retry":
			retryEvent = true
		case "notification.dead_letter":
			deadLetterEvent = true
		case "notification.operator_retry":
			operatorEvent = true
		case "notification.delivered":
			deliveredEvent = true
		}
	}
	if !claimedEvent || !retryEvent || !deadLetterEvent || !operatorEvent || !deliveredEvent {
		t.Fatalf("notification audit events = %#v", events)
	}
}

func createAwaitingApprovalCandidate(
	t *testing.T,
	ctx context.Context,
	repository scannerrelease.Persistence,
	policy *scannerrelease.Policy,
) *scannerrelease.Candidate {
	t.Helper()
	candidate := newCandidate(policy)
	if err := repository.CreateCandidate(
		ctx, candidate, command("notification/create:"+candidate.ID),
	); err != nil {
		t.Fatalf("CreateCandidate: %v", err)
	}
	version := candidate.Version
	for _, state := range []scannerrelease.CandidateState{
		scannerrelease.CandidateQueued,
		scannerrelease.CandidateBuilding,
		scannerrelease.CandidateTesting,
		scannerrelease.CandidateSecurityReview,
		scannerrelease.CandidateAwaitingApproval,
	} {
		transition := command("notification/" + candidate.ID + "/" + string(state))
		transition.PayloadJSON = `{"token":"super-secret-canary"}`
		transition.Reason = "secret-bearing reason super-secret-canary"
		updated, err := repository.TransitionCandidate(
			ctx, candidate.ID, version, state, transition,
		)
		if err != nil {
			t.Fatalf("advance candidate to %s: %v", state, err)
		}
		version = updated.Version
	}
	return candidate
}

func claimNotificationByID(
	ctx context.Context,
	repository scannerrelease.Persistence,
	id, worker string,
	now time.Time,
) (*scannerrelease.Notification, error) {
	for range 20 {
		claimed, err := repository.ClaimNextNotification(
			ctx, worker, now, now.Add(time.Minute),
		)
		if err != nil || claimed == nil {
			return claimed, err
		}
		if claimed.ID == id {
			return claimed, nil
		}
		if _, err := repository.FinalizeNotification(
			ctx, claimed.ID, worker, claimed.LeaseToken,
			scannerrelease.NotificationDelivered, now, "", "", now.Add(time.Second),
		); err != nil {
			return nil, err
		}
	}
	return nil, errors.New("notification was not claimable")
}

func claimNotificationForAggregate(
	ctx context.Context,
	repository scannerrelease.Persistence,
	aggregateID, worker string,
	now time.Time,
) (*scannerrelease.Notification, error) {
	for range 20 {
		claimed, err := repository.ClaimNextNotification(
			ctx, worker, now, now.Add(time.Minute),
		)
		if err != nil || claimed == nil {
			return claimed, err
		}
		if claimed.AggregateID == aggregateID {
			return claimed, nil
		}
		if _, err := repository.FinalizeNotification(
			ctx, claimed.ID, worker, claimed.LeaseToken,
			scannerrelease.NotificationDelivered, now, "", "", now.Add(time.Second),
		); err != nil {
			return nil, err
		}
	}
	return nil, errors.New("aggregate notification was not claimable")
}
