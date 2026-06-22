package ai

import (
	"strings"
	"testing"
)

func TestSafeCLIEnvAllowlistExcludesSecrets(t *testing.T) {
	env := safeCLIEnv([]string{
		"PATH=/usr/bin",
		"HOME=/home/wolf",
		"GITHUB_TOKEN=secret",
		"AWS_ACCESS_KEY_ID=secret",
		"OPENAI_API_KEY=secret",
		"CLAUDE_CODE_SESSION=session",
	})
	joined := strings.Join(env, "\n")
	for _, want := range []string{"PATH=/usr/bin", "HOME=/home/wolf"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected %q in filtered env: %v", want, env)
		}
	}
	for _, forbidden := range []string{"GITHUB_TOKEN", "AWS_ACCESS_KEY_ID", "OPENAI_API_KEY", "CLAUDE_CODE_SESSION", "secret"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("filtered env leaked %q: %v", forbidden, env)
		}
	}
}
