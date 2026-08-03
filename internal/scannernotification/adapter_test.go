package scannernotification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

type adapterFunc func(context.Context, Delivery) error

func (f adapterFunc) Deliver(ctx context.Context, delivery Delivery) error {
	return f(ctx, delivery)
}

func TestDispatcherUsesDestinationSpecificAdapterAndStableIdempotency(t *testing.T) {
	t.Parallel()
	var received Delivery
	dispatcher := Dispatcher{
		Webhook: adapterFunc(func(_ context.Context, delivery Delivery) error {
			received = delivery
			return nil
		}),
	}
	delivery := Delivery{
		NotificationID: "notification-1", IdempotencyKey: "notification-1",
		NotificationType: "release_published",
		Destination:      scannerrelease.NotificationDestinationWebhook,
		DestinationRef:   "release-operations", Attempt: 1,
		Payload: json.RawMessage(`{"safe":true}`),
	}
	if err := dispatcher.Deliver(context.Background(), delivery); err != nil {
		t.Fatal(err)
	}
	if received.IdempotencyKey != delivery.IdempotencyKey ||
		received.DestinationRef != "release-operations" {
		t.Fatalf("received delivery = %#v", received)
	}
}

func TestDispatcherRejectsMissingAdapterWithoutRetry(t *testing.T) {
	t.Parallel()
	err := (Dispatcher{}).Deliver(context.Background(), Delivery{
		Destination: scannerrelease.NotificationDestinationSIEM,
	})
	class, retryable := Classify(err)
	if class != "adapter_not_configured" || retryable {
		t.Fatalf("classification = %q retryable=%t err=%v", class, retryable, err)
	}
	var deliveryError *DeliveryError
	if !errors.As(err, &deliveryError) {
		t.Fatalf("error type = %T", err)
	}
}

func TestCommandAdapterUsesBoundedShellFreeJSONContract(t *testing.T) {
	t.Parallel()
	adapter := CommandAdapter{
		Path: os.Args[0],
		Args: []string{"-test.run=TestNotificationCommandAdapterHelperProcess", "--"},
		Environment: []string{
			"WOLF_TEST_NOTIFICATION_ADAPTER_HELPER=delivered",
		},
	}
	err := adapter.Deliver(context.Background(), Delivery{
		NotificationID:   "notification-command-1",
		IdempotencyKey:   "notification-command-1",
		NotificationType: "release_published",
		Destination:      scannerrelease.NotificationDestinationWebhook,
		DestinationRef:   "security", Attempt: 1,
		Payload: json.RawMessage(`{"safe":true}`),
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCommandAdapterRejectsOversizedResponsePermanently(t *testing.T) {
	t.Parallel()
	adapter := CommandAdapter{
		Path: os.Args[0],
		Args: []string{"-test.run=TestNotificationCommandAdapterHelperProcess", "--"},
		Environment: []string{
			"WOLF_TEST_NOTIFICATION_ADAPTER_HELPER=oversized",
		},
		MaxOutputBytes: 16,
	}
	err := adapter.Deliver(context.Background(), Delivery{
		NotificationID:   "notification-command-2",
		IdempotencyKey:   "notification-command-2",
		NotificationType: "gate_failure",
		Destination:      scannerrelease.NotificationDestinationSIEM,
		DestinationRef:   "primary", Attempt: 1,
		Payload: json.RawMessage(`{"safe":true}`),
	})
	class, retryable := Classify(err)
	if class != "invalid_adapter_response" || retryable {
		t.Fatalf("classification = %q retryable=%t err=%v", class, retryable, err)
	}
}

func TestNotificationCommandAdapterHelperProcess(t *testing.T) {
	mode := os.Getenv("WOLF_TEST_NOTIFICATION_ADAPTER_HELPER")
	if mode == "" {
		return
	}
	var delivery Delivery
	if err := json.NewDecoder(os.Stdin).Decode(&delivery); err != nil {
		t.Fatal(err)
	}
	if delivery.SchemaVersion != SchemaVersion ||
		delivery.NotificationID == "" ||
		delivery.IdempotencyKey != delivery.NotificationID ||
		delivery.DestinationRef == "" ||
		!json.Valid(delivery.Payload) {
		t.Fatalf("invalid helper delivery = %#v", delivery)
	}
	switch mode {
	case "delivered":
		_, _ = fmt.Fprint(os.Stdout, `{"status":"delivered","provider_message_id":"provider-1"}`)
	case "oversized":
		_, _ = fmt.Fprint(os.Stdout, strings.Repeat("x", 1024))
	default:
		t.Fatalf("unknown helper mode %q", mode)
	}
	os.Exit(0)
}
