package scannernotificationworker

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannernotification"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

type recordingDispatcher struct {
	mu         sync.Mutex
	deliveries []scannernotification.Delivery
	results    []error
}

func (d *recordingDispatcher) Deliver(
	_ context.Context,
	delivery scannernotification.Delivery,
) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.deliveries = append(d.deliveries, delivery)
	if len(d.results) == 0 {
		return nil
	}
	result := d.results[0]
	d.results = d.results[1:]
	return result
}

type memoryNotificationStore struct {
	mu            sync.Mutex
	notifications []scannerrelease.Notification
}

func (s *memoryNotificationStore) GetNotification(
	_ context.Context,
	id string,
) (*scannerrelease.Notification, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for index := range s.notifications {
		if s.notifications[index].ID == id {
			copy := s.notifications[index]
			return &copy, nil
		}
	}
	return nil, errors.New("not found")
}

func (s *memoryNotificationStore) ListNotifications(
	context.Context,
	scannerrelease.NotificationFilter,
	scannerrelease.PageRequest,
) (scannerrelease.NotificationPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := append([]scannerrelease.Notification(nil), s.notifications...)
	return scannerrelease.NotificationPage{Items: items}, nil
}

func (s *memoryNotificationStore) NotificationQueueCounts(
	context.Context,
) (scannerrelease.NotificationCounts, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var counts scannerrelease.NotificationCounts
	for _, notification := range s.notifications {
		switch notification.State {
		case scannerrelease.NotificationPending:
			counts.Pending++
		case scannerrelease.NotificationDelivering:
			counts.Delivering++
		case scannerrelease.NotificationRetry:
			counts.Retry++
		case scannerrelease.NotificationDelivered:
			counts.Delivered++
		case scannerrelease.NotificationDeadLetter:
			counts.DeadLetter++
		}
	}
	return counts, nil
}

func (s *memoryNotificationStore) ClaimNextNotification(
	_ context.Context,
	worker string,
	now, leaseUntil time.Time,
) (*scannerrelease.Notification, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for index := range s.notifications {
		item := &s.notifications[index]
		if item.DestinationType == scannerrelease.NotificationDestinationUI ||
			(item.State != scannerrelease.NotificationPending &&
				item.State != scannerrelease.NotificationRetry) ||
			item.AvailableAt.After(now) || item.Attempt >= item.MaxAttempts {
			continue
		}
		item.State = scannerrelease.NotificationDelivering
		item.WorkerID = worker
		item.LeaseToken = "lease-" + item.ID
		item.LeaseExpiresAt = &leaseUntil
		item.Attempt++
		item.Version++
		copy := *item
		return &copy, nil
	}
	return nil, nil
}

func (s *memoryNotificationStore) HeartbeatNotification(
	_ context.Context,
	id, worker, token string,
	now, leaseUntil time.Time,
) (scannerrelease.NotificationLeaseStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for index := range s.notifications {
		item := &s.notifications[index]
		if item.ID == id && item.State == scannerrelease.NotificationDelivering &&
			item.WorkerID == worker && item.LeaseToken == token &&
			item.LeaseExpiresAt != nil && item.LeaseExpiresAt.After(now) {
			item.LeaseExpiresAt = &leaseUntil
			item.Version++
			return scannerrelease.NotificationLeaseStatus{
				Current: true, State: item.State, Version: item.Version,
			}, nil
		}
	}
	return scannerrelease.NotificationLeaseStatus{}, nil
}

func (s *memoryNotificationStore) FinalizeNotification(
	_ context.Context,
	id, worker, token string,
	target scannerrelease.NotificationState,
	availableAt time.Time,
	errorClass, errorDetail string,
	now time.Time,
) (*scannerrelease.Notification, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for index := range s.notifications {
		item := &s.notifications[index]
		if item.ID != id || item.State != scannerrelease.NotificationDelivering ||
			item.WorkerID != worker || item.LeaseToken != token {
			continue
		}
		item.State = target
		item.AvailableAt = availableAt
		item.WorkerID = ""
		item.LeaseToken = ""
		item.LeaseExpiresAt = nil
		item.ErrorClass = errorClass
		item.ErrorDetail = errorDetail
		item.Version++
		if target == scannerrelease.NotificationDelivered {
			item.DeliveredAt = &now
		}
		if target == scannerrelease.NotificationDeadLetter {
			item.DeadLetteredAt = &now
		}
		copy := *item
		return &copy, nil
	}
	return nil, scannerrelease.ErrLeaseNotOwned
}

func (s *memoryNotificationStore) ReclaimStaleNotifications(
	context.Context,
	time.Time,
) (scannerrelease.NotificationReclaimSummary, error) {
	return scannerrelease.NotificationReclaimSummary{}, nil
}

func (s *memoryNotificationStore) RetryDeadLetterNotification(
	context.Context,
	string,
	int64,
	scannerrelease.TransitionCommand,
	time.Time,
) (*scannerrelease.Notification, error) {
	return nil, errors.New("not implemented")
}

func TestWorkerRetriesWithStableIdempotencyThenDelivers(t *testing.T) {
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	store := &memoryNotificationStore{notifications: []scannerrelease.Notification{{
		ID: "notification-1", NotificationType: "release_published",
		DestinationType: scannerrelease.NotificationDestinationWebhook,
		DestinationRef:  "security", State: scannerrelease.NotificationPending,
		PayloadJSON: `{"schema_version":"wolf.scanner-notification/v1"}`,
		MaxAttempts: 4, AvailableAt: now, Version: 1,
	}}}
	dispatcher := &recordingDispatcher{results: []error{
		scannernotification.Retryable(
			"provider_unavailable",
			errors.New("https://user:super-secret@example.test failed"),
		),
		nil,
	}}
	worker, err := New(Config{
		Store: store, Dispatcher: dispatcher, WorkerID: "worker-a",
		HeartbeatInterval: time.Second, LeaseDuration: 3 * time.Second,
		DeliveryTimeout: time.Second, DrainTimeout: time.Second,
		BaseBackoff: time.Second, MaxBackoff: time.Minute,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	processed, err := worker.RunOnce(context.Background())
	if err != nil || !processed {
		t.Fatalf("first RunOnce processed=%v err=%v", processed, err)
	}
	first, _ := store.GetNotification(context.Background(), "notification-1")
	if first.State != scannerrelease.NotificationRetry ||
		first.Attempt != 1 || !first.AvailableAt.After(now) ||
		first.ErrorClass != "provider_unavailable" ||
		first.ErrorDetail == "" ||
		containsSecret(first.ErrorDetail) {
		t.Fatalf("retry state = %#v", first)
	}
	now = first.AvailableAt.Add(time.Millisecond)
	processed, err = worker.RunOnce(context.Background())
	if err != nil || !processed {
		t.Fatalf("second RunOnce processed=%v err=%v", processed, err)
	}
	delivered, _ := store.GetNotification(context.Background(), "notification-1")
	if delivered.State != scannerrelease.NotificationDelivered ||
		delivered.Attempt != 2 || delivered.DeliveredAt == nil {
		t.Fatalf("delivered state = %#v", delivered)
	}
	if len(dispatcher.deliveries) != 2 {
		t.Fatalf("deliveries = %#v", dispatcher.deliveries)
	}
	for _, delivery := range dispatcher.deliveries {
		if delivery.IdempotencyKey != "notification-1" ||
			delivery.NotificationID != "notification-1" {
			t.Fatalf("unstable delivery identity = %#v", delivery)
		}
	}
}

func TestWorkerPermanentlyRejectsIntoDeadLetter(t *testing.T) {
	now := time.Now().UTC()
	store := &memoryNotificationStore{notifications: []scannerrelease.Notification{{
		ID: "notification-2", NotificationType: "gate_failure",
		DestinationType: scannerrelease.NotificationDestinationEmail,
		DestinationRef:  "approvers", State: scannerrelease.NotificationPending,
		PayloadJSON: `{"schema_version":"wolf.scanner-notification/v1"}`,
		MaxAttempts: 8, AvailableAt: now, Version: 1,
	}}}
	dispatcher := &recordingDispatcher{results: []error{
		scannernotification.Permanent("invalid_destination", errors.New("unknown alias")),
	}}
	worker, err := New(Config{
		Store: store, Dispatcher: dispatcher, WorkerID: "worker-b",
		HeartbeatInterval: time.Second, LeaseDuration: 3 * time.Second,
		DeliveryTimeout: time.Second, DrainTimeout: time.Second,
		BaseBackoff: time.Second, MaxBackoff: time.Minute,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if processed, err := worker.RunOnce(context.Background()); err != nil || !processed {
		t.Fatalf("RunOnce processed=%v err=%v", processed, err)
	}
	dead, _ := store.GetNotification(context.Background(), "notification-2")
	if dead.State != scannerrelease.NotificationDeadLetter ||
		dead.DeadLetteredAt == nil || dead.ErrorClass != "invalid_destination" {
		t.Fatalf("dead-letter state = %#v", dead)
	}
}

func TestNotificationBackoffIsDeterministicAndBounded(t *testing.T) {
	first := notificationBackoff("notification-1", 1, time.Second, time.Minute)
	if first != notificationBackoff("notification-1", 1, time.Second, time.Minute) {
		t.Fatal("backoff jitter is not deterministic")
	}
	if first < 750*time.Millisecond || first >= 1250*time.Millisecond {
		t.Fatalf("first backoff = %s", first)
	}
	if capped := notificationBackoff(
		"notification-1", 100, time.Second, time.Minute,
	); capped > time.Minute {
		t.Fatalf("capped backoff = %s", capped)
	}
}

func containsSecret(value string) bool {
	return strings.Contains(value, "super-secret")
}
