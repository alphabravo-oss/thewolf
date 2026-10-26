package serve

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/auth"
)

func TestJWTSecretConcurrentInitializationAndSessionContinuity(t *testing.T) {
	t.Setenv("WOLF_MASTER_KEY", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	keys := make([][]byte, 16)
	var wg sync.WaitGroup
	for i := range keys {
		wg.Go(func() {
			var err error
			keys[i], err = resolveJWTSecret()
			if err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	data, err := os.ReadFile(filepath.Join(home, ".wolf", "jwt-secret"))
	if err != nil || len(data) != 32 {
		t.Fatalf("persisted JWT key is not 32 raw bytes: %v", err)
	}
	for _, key := range keys {
		if !bytes.Equal(key, data) {
			t.Fatal("JWT startup processes chose different keys")
		}
	}
	auth.SetJWTSecret(data)
	tokens, err := auth.GenerateToken("user", "user@example.test")
	if err != nil {
		t.Fatal(err)
	}
	auth.SetJWTSecret(nil)
	key, err := resolveJWTSecret()
	if err != nil {
		t.Fatal(err)
	}
	auth.SetJWTSecret(key)
	if _, err := auth.ValidateToken(tokens.AccessToken); err != nil {
		t.Fatalf("access token lost across key reload: %v", err)
	}
	if _, err := auth.ValidateRefreshToken(tokens.RefreshToken); err != nil {
		t.Fatalf("refresh token lost across key reload: %v", err)
	}
}

func TestJWTSecretPreservesInvalidFilesAndFailsWithoutPersistence(t *testing.T) {
	t.Setenv("WOLF_MASTER_KEY", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".wolf", "jwt-secret")
	if err := os.Mkdir(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"", "short"} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := resolveJWTSecret(); err == nil {
			t.Fatal("invalid JWT file must fail, not generate an ephemeral key")
		}
		if data, err := os.ReadFile(path); err != nil || string(data) != content {
			t.Fatal("invalid JWT file replaced")
		}
	}
	t.Setenv("WOLF_MASTER_KEY", strings.Repeat("ab", 32))
	if key, err := resolveJWTSecret(); err != nil || string(key) != strings.Repeat("ab", 32) {
		t.Fatalf("environment JWT key precedence/raw encoding changed: %v", err)
	}
	t.Setenv("WOLF_MASTER_KEY", "")
	t.Setenv("HOME", path) // a file cannot hold the key directory
	if _, err := resolveJWTSecret(); err == nil {
		t.Fatal("unwritable key directory must fail")
	}
	t.Setenv("HOME", "")
	if _, err := resolveJWTSecret(); err == nil {
		t.Fatal("missing home directory must fail")
	}
}

func TestServeRejectsInvalidMasterKeyBeforeBackendInitialization(t *testing.T) {
	t.Setenv("WOLF_MASTER_KEY", "invalid")
	t.Setenv("WOLF_DB_DRIVER", "unsupported")
	t.Setenv("PATH", t.TempDir())
	if err := Run(context.Background(), Options{}); err == nil || !strings.Contains(err.Error(), "load secrets master key") {
		t.Fatalf("expected key initialization failure before database/backend initialization: %v", err)
	}
}
