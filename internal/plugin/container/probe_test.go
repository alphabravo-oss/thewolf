package container

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLocalImageDigestUsesExecutionConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "docker")
	if err := os.WriteFile(path, []byte(`#!/bin/sh
case "$WOLF_DIGEST_CASE" in
  local) exit 0 ;;
  registry) printf 'registry.example/scanner@sha256:fixture\n' ;;
  missing) echo 'Error response from daemon: No such image: scanner' >&2; exit 1 ;;
  unavailable) echo 'Cannot connect to Docker' >&2; exit 1 ;;
  blocked) exec /bin/sleep 30 ;;
  *) exit 2 ;;
esac
`), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"local", "registry", "missing", "unavailable", "blocked"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
			defer cancel()
			cfg := &Config{DockerPath: path, DockerEnvironment: []string{"WOLF_DIGEST_CASE=" + name}}
			digest, err := LocalImageDigest(ctx, cfg, "scanner")
			wantError := name == "missing" || name == "blocked" || name == "unavailable"
			if (err != nil) != wantError {
				t.Fatalf("digest=%q error=%v", digest, err)
			}
			if name == "registry" && digest != "sha256:fixture" {
				t.Fatalf("digest = %q", digest)
			}
			if name == "local" && digest != "" {
				t.Fatalf("local image has invented registry digest %q", digest)
			}
			if errors.Is(err, ErrImageMissing) != (name == "missing") {
				t.Fatalf("missing-image classification: %v", err)
			}
		})
	}
}
