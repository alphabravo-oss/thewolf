package engine

import (
	"context"
	"testing"

	"github.com/alphabravocompany/thewolf/pkg/fixengine"
)

type protoStub struct{}

func (protoStub) Name() string    { return "proto-stub" }
func (protoStub) Available() bool { return true }
func (protoStub) Fix(context.Context, FixRequest) (*FixResult, error) {
	return &FixResult{Success: true, EditsInPlace: true}, nil
}

func TestAdaptPreservesHarnessOutcome(t *testing.T) {
	e := Adapt(protoStub{})
	if e.Name() != "proto-stub" || !e.Available() {
		t.Fatalf("adapter identity")
	}
	res, err := e.Fix(context.Background(), fixengine.Request{})
	if err != nil || res == nil || !res.Success || !res.EditsInPlace {
		t.Fatalf("adapt fix: %v %#v", err, res)
	}
}
