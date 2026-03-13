package secrets

import (
	"testing"
)

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
