package scannertrace

import (
	"context"
	"errors"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

type correlationStore struct {
	value *scannerrelease.OperationCorrelation
	err   error
}

func (s correlationStore) GetOperationCorrelation(
	context.Context,
	string,
	string,
) (*scannerrelease.OperationCorrelation, error) {
	return s.value, s.err
}

func TestResumeRestoresDurableOperation(t *testing.T) {
	stored := &scannerrelease.OperationCorrelation{
		TraceID:     "0123456789abcdef0123456789abcdef",
		OperationID: "operation-durable-123",
	}
	ctx, value, err := Resume(
		context.Background(), correlationStore{value: stored},
		"build", "build-1", "build-worker",
	)
	if err != nil {
		t.Fatal(err)
	}
	fromContext, ok := FromContext(ctx)
	if !ok || value.TraceID != stored.TraceID ||
		fromContext.OperationID != stored.OperationID ||
		fromContext.Component != "build-worker" {
		t.Fatalf("durable correlation was not restored: %#v %#v", value, fromContext)
	}
}

func TestResumeReturnsPersistenceFailure(t *testing.T) {
	expected := errors.New("database unavailable")
	_, _, err := Resume(
		context.Background(), correlationStore{err: expected},
		"rollout", "rollout-1", "rollout-controller",
	)
	if !errors.Is(err, expected) {
		t.Fatalf("expected persistence error, got %v", err)
	}
}
