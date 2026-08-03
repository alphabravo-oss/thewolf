package scannernotification

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/scannerdiscovery"
)

const defaultMaxAdapterOutput = 64 << 10

type CommandAdapter struct {
	Path           string
	Args           []string
	Environment    []string
	MaxOutputBytes int64
}

type adapterResponse struct {
	Status            string `json:"status"`
	ProviderMessageID string `json:"provider_message_id,omitempty"`
}

func (a CommandAdapter) Deliver(ctx context.Context, delivery Delivery) error {
	if strings.TrimSpace(a.Path) == "" {
		return Permanent("adapter_not_configured", errors.New("notification adapter path is required"))
	}
	if delivery.NotificationID == "" || delivery.IdempotencyKey == "" ||
		delivery.DestinationRef == "" || delivery.NotificationType == "" ||
		delivery.Attempt <= 0 || !json.Valid(delivery.Payload) {
		return Permanent("invalid_delivery", errors.New("notification delivery request is incomplete"))
	}
	delivery.SchemaVersion = SchemaVersion
	encoded, err := json.Marshal(delivery)
	if err != nil {
		return Permanent("invalid_delivery", err)
	}
	maxOutput := a.MaxOutputBytes
	if maxOutput <= 0 {
		maxOutput = defaultMaxAdapterOutput
	}
	command := exec.CommandContext(ctx, a.Path, a.Args...) // #nosec G204 -- explicit binary/args, never a shell
	command.Env = append([]string(nil), a.Environment...)
	command.Stdin = bytes.NewReader(encoded)
	stdout := newBoundedBuffer(maxOutput)
	stderr := newBoundedBuffer(maxOutput)
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return Retryable("timeout", ctx.Err())
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			return Retryable("cancelled", ctx.Err())
		}
		detail := scannerdiscovery.RedactText(strings.TrimSpace(stderr.String()))
		if detail == "" {
			detail = "notification adapter process failed"
		}
		if stdout.Overflowed() || stderr.Overflowed() {
			detail = "notification adapter output exceeded the configured limit"
		}
		return Retryable("adapter_execution", fmt.Errorf("%s: %w", detail, err))
	}
	if stdout.Overflowed() || stderr.Overflowed() {
		return Permanent(
			"invalid_adapter_response",
			errors.New("notification adapter output exceeded the configured limit"),
		)
	}
	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	decoder.DisallowUnknownFields()
	var response adapterResponse
	if err := decoder.Decode(&response); err != nil {
		return Permanent("invalid_adapter_response", fmt.Errorf("decode notification adapter response: %w", err))
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Permanent("invalid_adapter_response", errors.New("notification adapter returned multiple JSON values"))
	}
	if response.Status != "delivered" {
		return Permanent("delivery_rejected", fmt.Errorf(
			"notification adapter returned status %q", response.Status,
		))
	}
	if len(response.ProviderMessageID) > 256 ||
		strings.ContainsAny(response.ProviderMessageID, "\r\n") {
		return Permanent("invalid_adapter_response", errors.New("notification adapter message ID is invalid"))
	}
	return nil
}

type boundedBuffer struct {
	buffer   bytes.Buffer
	limit    int64
	written  int64
	overflow bool
}

func newBoundedBuffer(limit int64) *boundedBuffer {
	return &boundedBuffer{limit: limit}
}

func (b *boundedBuffer) Write(value []byte) (int, error) {
	original := len(value)
	remaining := b.limit - b.written
	if remaining <= 0 {
		b.overflow = true
		return original, nil
	}
	if int64(len(value)) > remaining {
		value = value[:remaining]
		b.overflow = true
	}
	written, err := b.buffer.Write(value)
	b.written += int64(written)
	if err != nil {
		return written, err
	}
	return original, nil
}

func (b *boundedBuffer) Bytes() []byte  { return b.buffer.Bytes() }
func (b *boundedBuffer) String() string { return b.buffer.String() }
func (b *boundedBuffer) Overflowed() bool {
	return b.overflow
}
