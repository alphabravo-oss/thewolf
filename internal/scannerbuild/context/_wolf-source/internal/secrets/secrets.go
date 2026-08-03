package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

var masterKey []byte

// LoadMasterKey loads the master encryption key from ~/.wolf/master.key or WOLF_MASTER_KEY env.
// If neither exists, it generates a new key and saves it.
func LoadMasterKey() error {
	// Check env var first
	if envKey := os.Getenv("WOLF_MASTER_KEY"); envKey != "" {
		decoded, err := hex.DecodeString(envKey)
		if err != nil {
			return fmt.Errorf("decode WOLF_MASTER_KEY: %w", err)
		}
		if len(decoded) != 32 {
			return fmt.Errorf("WOLF_MASTER_KEY must be 32 bytes (64 hex chars)")
		}
		masterKey = decoded
		return nil
	}

	// Check file
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get home dir: %w", err)
	}

	keyDir := filepath.Join(home, ".wolf")
	keyPath := filepath.Join(keyDir, "master.key")

	// #nosec G304 -- reads secrets file path from validated config
	data, err := os.ReadFile(keyPath)
	if err == nil {
		decoded, err := hex.DecodeString(string(data))
		if err != nil {
			return fmt.Errorf("decode master.key: %w", err)
		}
		if len(decoded) != 32 {
			return fmt.Errorf("master.key must be 32 bytes")
		}
		masterKey = decoded
		return nil
	}

	// Generate new key
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return fmt.Errorf("generate key: %w", err)
	}

	if err := os.MkdirAll(keyDir, 0700); err != nil {
		return fmt.Errorf("create ~/.wolf: %w", err)
	}

	encoded := hex.EncodeToString(key)
	if err := os.WriteFile(keyPath, []byte(encoded), 0600); err != nil {
		return fmt.Errorf("write master.key: %w", err)
	}

	masterKey = key
	return nil
}

// SetMasterKey sets the master key directly (useful for testing).
func SetMasterKey(key []byte) {
	masterKey = key
}

// Encrypt encrypts plaintext using AES-256-GCM.
func Encrypt(plaintext string) (string, error) {
	if len(masterKey) == 0 {
		return "", fmt.Errorf("master key not loaded")
	}

	block, err := aes.NewCipher(masterKey)
	if err != nil {
		return "", fmt.Errorf("create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create gcm: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return hex.EncodeToString(ciphertext), nil
}

// Decrypt decrypts an AES-256-GCM encrypted hex string.
func Decrypt(encrypted string) (string, error) {
	if len(masterKey) == 0 {
		return "", fmt.Errorf("master key not loaded")
	}

	data, err := hex.DecodeString(encrypted)
	if err != nil {
		return "", fmt.Errorf("decode hex: %w", err)
	}

	block, err := aes.NewCipher(masterKey)
	if err != nil {
		return "", fmt.Errorf("create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create gcm: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}

	return string(plaintext), nil
}
