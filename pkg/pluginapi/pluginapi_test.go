package pluginapi

import (
	"context"
	"testing"
)

type stub struct{}

func (stub) Name() string                          { return "stub" }
func (stub) CheckAvailable() bool                  { return true }
func (stub) Execute(context.Context, string) error { return nil }

func TestKindsStayDistinct(t *testing.T) {
	if len(Kinds()) < 5 {
		t.Fatal(Kinds())
	}
}

func TestPluginContract(t *testing.T) {
	var p Plugin = stub{}
	if p.Name() == "" || !p.CheckAvailable() || p.Execute(context.Background(), ".") != nil {
		t.Fatal("plugin contract")
	}
}
