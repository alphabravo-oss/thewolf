package auth

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestCredentialsHasKeyFor(t *testing.T) {
	c := Credentials{AnthropicKey: "a", OpenAIKey: "o"}
	if !c.HasKeyFor("claude") || !c.HasKeyFor("codex") || !c.HasKeyFor("opencode") {
		t.Fatal("expected keys to satisfy claude, codex, and opencode")
	}
	if (Credentials{}).HasKeyFor("claude") {
		t.Fatal("empty creds must not satisfy claude")
	}
	onlyAnthropic := Credentials{AnthropicKey: "a"}
	if !onlyAnthropic.HasKeyFor("opencode") || onlyAnthropic.HasKeyFor("codex") {
		t.Fatal("anthropic-only should satisfy opencode but not codex")
	}
	if !(Credentials{XAIKey: "x"}).HasKeyFor("grok") {
		t.Fatal("xAI key should satisfy grok")
	}
}

func TestCredentialsEnv(t *testing.T) {
	env := Credentials{AnthropicKey: "ak", OpenAIKey: "ok"}.Env()
	if len(env) != 2 {
		t.Fatalf("env = %v", env)
	}
}

func TestWriteReadStatus(t *testing.T) {
	dir := t.TempDir()
	engines := []EngineStatus{{Name: "api", Available: true, Auth: "api_key"}}
	if err := WriteStatus(dir, engines); err != nil {
		t.Fatal(err)
	}
	got, err := ReadStatus(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "api" || got[0].Auth != "api_key" {
		t.Fatalf("got %+v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, StatusFileName)); err != nil {
		t.Fatal(err)
	}
}

func TestReadStatusMissing(t *testing.T) {
	got, err := ReadStatus(t.TempDir())
	if err != nil || got != nil {
		t.Fatalf("missing file should be (nil, nil), got %v / %v", got, err)
	}
}

func TestResolveFallsBackToEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "from-env")
	t.Setenv("OPENAI_API_KEY", "")
	creds := Resolve(context.Background(), nil, "")
	if creds.AnthropicKey != "from-env" {
		t.Fatalf("got %+v", creds)
	}
	_ = models.KeyTypeAnthropicKey
}
