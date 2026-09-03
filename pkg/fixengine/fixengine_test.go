package fixengine

import (
	"context"
	"testing"
)

type stub struct{}

func (stub) Name() string    { return "stub" }
func (stub) Available() bool { return true }
func (stub) Fix(context.Context, Request) (*Result, error) {
	return &Result{Success: true, EditsInPlace: true}, nil
}

func TestEngineProtocol(t *testing.T) {
	var e Engine = stub{}
	if e.Name() == "" || !e.Available() {
		t.Fatal("stub")
	}
	res, err := e.Fix(context.Background(), Request{RepoPath: "."})
	if err != nil || res == nil || !res.Success {
		t.Fatalf("fix: %v %#v", err, res)
	}
	if UntrustedRepoFiles == "" {
		t.Fatal("threat note missing")
	}
	if len(CommunityHarnesses()) < 4 {
		t.Fatal("Community harnesses must stay in the public tree")
	}
}
