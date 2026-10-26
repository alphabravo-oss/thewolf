package secrets

import (
	"bytes"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestConcurrentMasterKeyLoadingPreservesCiphertext(t *testing.T) {
	t.Setenv("WOLF_MASTER_KEY", "")
	t.Setenv("HOME", t.TempDir())
	SetMasterKey(nil)
	t.Cleanup(func() { SetMasterKey(nil) })
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			for range 10 {
				if err := LoadMasterKey(); err != nil {
					t.Errorf("load key: %v", err)
					return
				}
				ciphertext, err := Encrypt("credential preserved across reload")
				if err != nil {
					t.Error(err)
					return
				}
				if err := LoadMasterKey(); err != nil {
					t.Error(err)
					return
				}
				if plaintext, err := Decrypt(ciphertext); err != nil || plaintext != "credential preserved across reload" {
					t.Errorf("ciphertext lost across reload: %v", err)
					return
				}
			}
		})
	}
	wg.Wait()
	data, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".wolf", "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	key, err := hex.DecodeString(string(data))
	if err != nil || len(key) != 32 {
		t.Fatalf("persisted key encoding changed: %v", err)
	}
	ciphertext, err := Encrypt("persisted key continuity")
	if err != nil {
		t.Fatal(err)
	}
	SetMasterKey(key)
	clear(key) // publication must not retain the caller's mutable slice
	if plaintext, err := Decrypt(ciphertext); err != nil || plaintext != "persisted key continuity" {
		t.Fatalf("published key changed with caller's slice: %v", err)
	}
}

func TestMasterKeyPreservesMalformedFilesAndEnvironmentPrecedence(t *testing.T) {
	t.Setenv("WOLF_MASTER_KEY", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".wolf", "master.key")
	if err := os.Mkdir(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"", "00", "not hexadecimal"} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if err := LoadMasterKey(); err == nil {
			t.Fatal("malformed key accepted")
		}
		if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, []byte(content)) {
			t.Fatal("malformed key was replaced")
		}
	}
	t.Setenv("WOLF_MASTER_KEY", strings.Repeat("ab", 32))
	if err := LoadMasterKey(); err != nil {
		t.Fatalf("valid environment key must override malformed file: %v", err)
	}
	ciphertext, err := Encrypt("environment key")
	if err != nil {
		t.Fatal(err)
	}
	SetMasterKey(bytes.Repeat([]byte{0xab}, 32))
	if plaintext, err := Decrypt(ciphertext); err != nil || plaintext != "environment key" {
		t.Fatal("environment key decoding changed")
	}
	t.Setenv("WOLF_MASTER_KEY", "bad")
	if err := LoadMasterKey(); err == nil {
		t.Fatal("invalid environment key accepted")
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "not hexadecimal" {
		t.Fatal("environment key loading changed the stored file")
	}
}

func TestEncryptDecrypt(t *testing.T) {
	// Set a test key (32 bytes)
	key := []byte("01234567890123456789012345678901")
	SetMasterKey(key)

	plaintext := "my-secret-api-key-value"

	encrypted, err := Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	if encrypted == "" {
		t.Fatal("encrypted string should not be empty")
	}
	if encrypted == plaintext {
		t.Fatal("encrypted should differ from plaintext")
	}

	decrypted, err := Decrypt(encrypted)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}

	if decrypted != plaintext {
		t.Fatalf("expected %q, got %q", plaintext, decrypted)
	}
}

func TestEncryptDifferentNonces(t *testing.T) {
	key := []byte("01234567890123456789012345678901")
	SetMasterKey(key)

	plaintext := "same-value"

	enc1, _ := Encrypt(plaintext)
	enc2, _ := Encrypt(plaintext)

	if enc1 == enc2 {
		t.Fatal("two encryptions of the same value should produce different ciphertexts")
	}

	// Both should decrypt to the same value
	dec1, _ := Decrypt(enc1)
	dec2, _ := Decrypt(enc2)
	if dec1 != dec2 {
		t.Fatal("both should decrypt to the same value")
	}
}

func TestDecryptInvalidData(t *testing.T) {
	key := []byte("01234567890123456789012345678901")
	SetMasterKey(key)

	_, err := Decrypt("not-valid-hex")
	if err == nil {
		t.Fatal("should fail for invalid hex")
	}

	_, err = Decrypt("aabbccdd")
	if err == nil {
		t.Fatal("should fail for invalid ciphertext")
	}
}

func TestNoMasterKey(t *testing.T) {
	SetMasterKey(nil)

	_, err := Encrypt("test")
	if err == nil {
		t.Fatal("should fail without master key")
	}

	_, err = Decrypt("aabb")
	if err == nil {
		t.Fatal("should fail without master key")
	}
}
