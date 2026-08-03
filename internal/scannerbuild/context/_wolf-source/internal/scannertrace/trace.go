// Package scannertrace carries bounded, non-secret correlation identifiers
// across scanner release API requests, durable queues, and worker processes.
package scannertrace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/alphabravocompany/thewolf/internal/wolflog"
)

const (
	HeaderOperationID = "X-Wolf-Operation-ID"
	HeaderTraceID     = "X-Wolf-Trace-ID"
	HeaderTraceParent = "Traceparent"
)

var operationPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{7,127}$`)

type contextKey struct{}

// Correlation is safe to persist and log. It must never contain actor input,
// credentials, repository URLs, or other potentially sensitive metadata.
type Correlation struct {
	TraceID           string `db:"trace_id" json:"trace_id"`
	OperationID       string `db:"operation_id" json:"operation_id"`
	ParentOperationID string `db:"parent_operation_id" json:"parent_operation_id,omitempty"`
	SpanID            string `db:"-" json:"span_id,omitempty"`
	ParentSpanID      string `db:"-" json:"parent_span_id,omitempty"`
	Component         string `db:"origin_component" json:"origin_component,omitempty"`
}

// New returns cryptographically random identifiers suitable for a new root
// operation. Random-source failure is handled with UUID entropy rather than
// returning an empty correlation identifier.
func New(component string) Correlation {
	return Correlation{
		TraceID:     randomHex(16),
		OperationID: "op_" + uuid.NewString(),
		SpanID:      randomHex(8),
		Component:   sanitizeComponent(component),
	}
}

// Child creates a new local span while preserving the durable operation.
func Child(parent Correlation, component string) Correlation {
	parent = Normalize(parent, component)
	return Correlation{
		TraceID:           parent.TraceID,
		OperationID:       parent.OperationID,
		ParentOperationID: parent.ParentOperationID,
		SpanID:            randomHex(8),
		ParentSpanID:      parent.SpanID,
		Component:         sanitizeComponent(component),
	}
}

// Normalize rejects malformed inbound identifiers and fills missing values.
func Normalize(value Correlation, component string) Correlation {
	if !ValidTraceID(value.TraceID) {
		value.TraceID = randomHex(16)
	}
	if !ValidOperationID(value.OperationID) {
		value.OperationID = "op_" + uuid.NewString()
	}
	if value.ParentOperationID != "" && !ValidOperationID(value.ParentOperationID) {
		value.ParentOperationID = ""
	}
	if !ValidSpanID(value.SpanID) {
		value.SpanID = randomHex(8)
	}
	if value.ParentSpanID != "" && !ValidSpanID(value.ParentSpanID) {
		value.ParentSpanID = ""
	}
	if component != "" {
		value.Component = sanitizeComponent(component)
	} else {
		value.Component = sanitizeComponent(value.Component)
	}
	return value
}

func With(ctx context.Context, value Correlation) context.Context {
	value = Normalize(value, value.Component)
	logger := wolflog.L().With().
		Str("trace_id", value.TraceID).
		Str("operation_id", value.OperationID).
		Str("component", value.Component).
		Logger()
	if value.ParentOperationID != "" {
		logger = logger.With().Str("parent_operation_id", value.ParentOperationID).Logger()
	}
	ctx = context.WithValue(ctx, contextKey{}, value)
	return logger.WithContext(ctx)
}

func FromContext(ctx context.Context) (Correlation, bool) {
	if ctx == nil {
		return Correlation{}, false
	}
	value, ok := ctx.Value(contextKey{}).(Correlation)
	if !ok || !ValidTraceID(value.TraceID) || !ValidOperationID(value.OperationID) {
		return Correlation{}, false
	}
	return value, true
}

func Ensure(ctx context.Context, component string) (context.Context, Correlation) {
	if current, ok := FromContext(ctx); ok {
		child := Child(current, component)
		return With(ctx, child), child
	}
	value := New(component)
	return With(ctx, value), value
}

func Logger(ctx context.Context) *zerolog.Logger {
	return wolflog.FromContext(ctx)
}

func ValidTraceID(value string) bool {
	return validLowerHex(value, 32) && value != strings.Repeat("0", 32)
}

func ValidSpanID(value string) bool {
	return validLowerHex(value, 16) && value != strings.Repeat("0", 16)
}

func ValidOperationID(value string) bool {
	return operationPattern.MatchString(value)
}

// ParseTraceparent accepts only W3C version 00 trace context. Future versions
// are intentionally ignored until their parsing rules are implemented.
func ParseTraceparent(value string) (traceID, parentSpanID string, ok bool) {
	parts := strings.Split(strings.TrimSpace(value), "-")
	if len(parts) != 4 || parts[0] != "00" || !ValidTraceID(parts[1]) ||
		!ValidSpanID(parts[2]) || !validLowerHex(parts[3], 2) {
		return "", "", false
	}
	return parts[1], parts[2], true
}

func Traceparent(value Correlation) string {
	value = Normalize(value, value.Component)
	return "00-" + value.TraceID + "-" + value.SpanID + "-01"
}

func randomHex(bytes int) string {
	buffer := make([]byte, bytes)
	if _, err := rand.Read(buffer); err == nil {
		return hex.EncodeToString(buffer)
	}
	raw := strings.ReplaceAll(uuid.NewString(), "-", "")
	for len(raw) < bytes*2 {
		raw += strings.ReplaceAll(uuid.NewString(), "-", "")
	}
	return raw[:bytes*2]
}

func validLowerHex(value string, length int) bool {
	if len(value) != length || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func sanitizeComponent(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "scanner-release"
	}
	if len(value) > 64 {
		value = value[:64]
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') ||
			(character >= '0' && character <= '9') ||
			character == '-' || character == '_' || character == '.' {
			continue
		}
		return "scanner-release"
	}
	return value
}
