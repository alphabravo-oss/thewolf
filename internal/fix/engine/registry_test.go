package engine

import (
	"strings"
	"testing"
)

func TestParseToolDefinitions(t *testing.T) {
	good := `[
	  {"name":"cursor-agent","command":"cursor-agent","args":["--apply","-p","{prompt}"]},
	  {"name":"opencode","command":"opencode","args":["run"],"success_rule":"exit_zero"}
	]`
	defs, err := ParseToolDefinitions([]byte(good))
	if err != nil {
		t.Fatalf("parse good defs: %v", err)
	}
	if len(defs) != 2 {
		t.Fatalf("expected 2 defs, got %d", len(defs))
	}
	if defs[0].Name != "cursor-agent" || defs[1].SuccessRule != "exit_zero" {
		t.Errorf("defs decoded wrong: %+v", defs)
	}

	// Empty / null input is valid (no tools configured).
	if d, err := ParseToolDefinitions([]byte("  ")); err != nil || d != nil {
		t.Errorf("blank input should yield (nil,nil), got (%v,%v)", d, err)
	}

	// Missing required fields error out.
	for _, bad := range []string{
		`[{"command":"x"}]`, // no name
		`[{"name":"x"}]`,    // no command
		`[{"name":"x","command":"y","success_rule":"bogus"}]`, // bad rule
	} {
		if _, err := ParseToolDefinitions([]byte(bad)); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}

func TestRegistry_BuiltinsResolve(t *testing.T) {
	r := NewRegistry()
	for _, name := range []string{"claude-code", "codex", "auto", ""} {
		if _, err := r.Resolve(name); err != nil {
			t.Errorf("built-in %q should resolve: %v", name, err)
		}
	}
	if _, err := r.Resolve("does-not-exist"); err == nil {
		t.Error("unknown tool should error")
	}
}

func TestRegistry_ConfigToolResolves(t *testing.T) {
	r := NewRegistry()
	defs, _ := ParseToolDefinitions([]byte(`[{"name":"cursor-agent","command":"cursor-agent","args":["-p","{prompt}"]}]`))
	r.RegisterToolDefinitions(defs)

	e, err := r.Resolve("cursor-agent")
	if err != nil {
		t.Fatalf("config tool should resolve: %v", err)
	}
	if e.Name() != "cursor-agent" {
		t.Errorf("engine name = %q, want cursor-agent", e.Name())
	}
	// custom: syntax still works.
	if _, err := r.Resolve("custom:aider:raw"); err != nil {
		t.Errorf("custom: syntax should still resolve: %v", err)
	}
}

func TestConfigEngine_RenderArgs(t *testing.T) {
	// {prompt} placeholder present → substituted in place.
	e := NewConfigEngine(ToolDefinition{
		Name: "t", Command: "x", Args: []string{"--repo", "{repo}", "-p", "{prompt}"},
	})
	got := e.renderArgs("FIX THIS", "/work/repo")
	want := []string{"--repo", "/work/repo", "-p", "FIX THIS"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("renderArgs = %v, want %v", got, want)
	}

	// No {prompt} placeholder → prompt appended last.
	e2 := NewConfigEngine(ToolDefinition{Name: "t", Command: "x", Args: []string{"run"}})
	got2 := e2.renderArgs("FIX", "/r")
	if len(got2) != 2 || got2[1] != "FIX" {
		t.Errorf("expected prompt appended, got %v", got2)
	}
}
