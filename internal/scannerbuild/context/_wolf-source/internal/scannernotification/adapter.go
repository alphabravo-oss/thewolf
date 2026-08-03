// Package scannernotification defines the credential-free delivery contract
// between the release notification worker and deployment-owned adapters.
package scannernotification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

const SchemaVersion = "wolf.scanner-notification-delivery/v1"

type Delivery struct {
	SchemaVersion    string                                     `json:"schema_version"`
	NotificationID   string                                     `json:"notification_id"`
	IdempotencyKey   string                                     `json:"idempotency_key"`
	NotificationType string                                     `json:"notification_type"`
	Destination      scannerrelease.NotificationDestinationType `json:"destination_type"`
	DestinationRef   string                                     `json:"destination_ref"`
	Attempt          int                                        `json:"attempt"`
	Payload          json.RawMessage                            `json:"payload"`
}

type Adapter interface {
	Deliver(context.Context, Delivery) error
}

type Dispatcher struct {
	Webhook Adapter
	Email   Adapter
	SIEM    Adapter
}

func (d Dispatcher) Deliver(ctx context.Context, delivery Delivery) error {
	var adapter Adapter
	switch delivery.Destination {
	case scannerrelease.NotificationDestinationWebhook:
		adapter = d.Webhook
	case scannerrelease.NotificationDestinationEmail:
		adapter = d.Email
	case scannerrelease.NotificationDestinationSIEM:
		adapter = d.SIEM
	default:
		return Permanent("unsupported_destination", fmt.Errorf(
			"unsupported scanner notification destination %q", delivery.Destination,
		))
	}
	if adapter == nil {
		return Permanent("adapter_not_configured", fmt.Errorf(
			"scanner notification %s adapter is not configured", delivery.Destination,
		))
	}
	return adapter.Deliver(ctx, delivery)
}

type DeliveryError struct {
	Class     string
	Retryable bool
	Err       error
}

func (e *DeliveryError) Error() string {
	if e == nil || e.Err == nil {
		return "scanner notification delivery failed"
	}
	return e.Err.Error()
}

func (e *DeliveryError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func Retryable(class string, err error) error {
	return &DeliveryError{Class: class, Retryable: true, Err: err}
}

func Permanent(class string, err error) error {
	return &DeliveryError{Class: class, Retryable: false, Err: err}
}

func Classify(err error) (class string, retryable bool) {
	if err == nil {
		return "", false
	}
	var deliveryError *DeliveryError
	if errors.As(err, &deliveryError) {
		if deliveryError.Class == "" {
			return "delivery_failed", deliveryError.Retryable
		}
		return deliveryError.Class, deliveryError.Retryable
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout", true
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled", true
	}
	return "delivery_failed", true
}
