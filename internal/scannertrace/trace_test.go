package scannertrace

import (
	"context"
	"testing"
)

func TestCorrelationRoundTripAndChild(t *testing.T) {
	root := New("api")
	ctx := With(context.Background(), root)
	got, ok := FromContext(ctx)
	if !ok || got.TraceID != root.TraceID || got.OperationID != root.OperationID {
		t.Fatalf("unexpected context correlation: %#v, %v", got, ok)
	}
	child := Child(got, "registry-worker")
	if child.TraceID != root.TraceID || child.OperationID != root.OperationID ||
		child.ParentSpanID != root.SpanID || child.SpanID == root.SpanID {
		t.Fatalf("child did not preserve the operation trace: %#v", child)
	}
}

func TestParseTraceparentRejectsMalformedAndZeroValues(t *testing.T) {
	valid := "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01"
	traceID, spanID, ok := ParseTraceparent(valid)
	if !ok || traceID != "0123456789abcdef0123456789abcdef" ||
		spanID != "0123456789abcdef" {
		t.Fatalf("valid traceparent rejected: %q %q %v", traceID, spanID, ok)
	}
	invalid := []string{
		"",
		"01-0123456789abcdef0123456789abcdef-0123456789abcdef-01",
		"00-00000000000000000000000000000000-0123456789abcdef-01",
		"00-0123456789abcdef0123456789abcdef-0000000000000000-01",
		"00-0123456789ABCDEF0123456789ABCDEF-0123456789abcdef-01",
		"00-0123456789abcdef0123456789abcdef-0123456789abcdef-zz",
	}
	for _, value := range invalid {
		if _, _, accepted := ParseTraceparent(value); accepted {
			t.Errorf("accepted invalid traceparent %q", value)
		}
	}
}

func TestNormalizeRejectsUntrustedOperationAndComponentValues(t *testing.T) {
	value := Normalize(Correlation{
		TraceID:     "not-a-trace",
		OperationID: "authorization: bearer secret",
		Component:   "bad\nfield",
	}, "")
	if !ValidTraceID(value.TraceID) || !ValidOperationID(value.OperationID) {
		t.Fatalf("normalize did not replace malformed identifiers: %#v", value)
	}
	if value.Component != "scanner-release" {
		t.Fatalf("unsafe component was not replaced: %q", value.Component)
	}
}
