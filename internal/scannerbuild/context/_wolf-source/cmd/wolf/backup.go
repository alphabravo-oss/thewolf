package main

import (
	"archive/tar"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newBackupCmd() *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Write a tar of the SQLite database (and a manifest of artifact paths)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if out == "" {
				return fmt.Errorf("-o is required")
			}
			dsn, err := sqliteDSNPath()
			if err != nil {
				return err
			}
			return backupSQLite(dsn, out, os.Getenv("WOLF_BACKUP_PASSWORD"), os.Getenv("WOLF_WORKSPACE_ROOT"))
		},
	}
	cmd.Flags().StringVarP(&out, "output", "o", "", "output .tar path")
	return cmd
}

func newRestoreCmd() *cobra.Command {
	var in string
	var force bool
	cmd := &cobra.Command{
		Use:   "restore",
		Short: "Restore a SQLite database from a wolf backup tar",
		RunE: func(cmd *cobra.Command, args []string) error {
			if in == "" {
				return fmt.Errorf("-i is required")
			}
			dsn, err := sqliteDSNPath()
			if err != nil {
				return err
			}
			return restoreSQLite(in, dsn, os.Getenv("WOLF_BACKUP_PASSWORD"), force)
		},
	}
	cmd.Flags().StringVarP(&in, "input", "i", "", "input .tar or .tar.enc path")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing database")
	return cmd
}

func sqliteDSNPath() (string, error) {
	if strings.EqualFold(envOr("WOLF_DB_DRIVER", "sqlite"), "postgres") {
		return "", fmt.Errorf("postgres driver is not supported")
	}
	dsn := strings.TrimSpace(os.Getenv("WOLF_DB_DSN"))
	if dsn == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", fmt.Errorf("WOLF_DB_DSN unset and no home directory")
		}
		dsn = filepath.Join(home, ".wolf", "wolf.db")
	}
	if dsn == ":memory:" || strings.Contains(dsn, "://") {
		return "", fmt.Errorf("cannot backup or restore %q", dsn)
	}
	return dsn, nil
}

func backupSQLite(dbPath, out, password, workspaceRoot string) error {
	if _, err := os.Stat(dbPath); err != nil {
		return fmt.Errorf("database %s: %w", dbPath, err)
	}
	artifactsRoot := ""
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		artifactsRoot = filepath.Join(home, ".wolf", "artifacts")
	}
	manifest, err := json.Marshal(map[string]any{
		"created_at":     time.Now().UTC().Format(time.RFC3339),
		"db":             "wolf.db",
		"artifacts_root": artifactsRoot,
		"workspace_root": workspaceRoot,
	})
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp("", "wolf-backup-*.tar")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	tw := tar.NewWriter(tmp)
	if err := tarFile(tw, "wolf.db", dbPath); err != nil {
		_ = tw.Close()
		_ = tmp.Close()
		return err
	}
	if err := tw.WriteHeader(&tar.Header{Name: "manifest.json", Mode: 0o644, Size: int64(len(manifest)), ModTime: time.Now()}); err != nil {
		_ = tw.Close()
		_ = tmp.Close()
		return err
	}
	if _, err := tw.Write(manifest); err != nil {
		_ = tw.Close()
		_ = tmp.Close()
		return err
	}
	if err := tw.Close(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	plain, err := os.ReadFile(tmpName)
	if err != nil {
		return err
	}
	dest := out
	payload := plain
	if strings.TrimSpace(password) != "" {
		enc, err := encryptBackup(plain, password)
		if err != nil {
			return err
		}
		payload = enc
		if !strings.HasSuffix(dest, ".enc") {
			dest += ".enc"
		}
	}
	return os.WriteFile(dest, payload, 0o600)
}

func restoreSQLite(in, dest, password string, force bool) error {
	raw, err := os.ReadFile(in)
	if err != nil {
		return err
	}
	if strings.HasSuffix(in, ".enc") {
		if strings.TrimSpace(password) == "" {
			return fmt.Errorf("WOLF_BACKUP_PASSWORD is required to restore an encrypted backup")
		}
		raw, err = decryptBackup(raw, password)
		if err != nil {
			return err
		}
	}
	if _, err := os.Stat(dest); err == nil && !force {
		return fmt.Errorf("%s exists (use --force)", dest)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return err
	}
	tr := tar.NewReader(bytes.NewReader(raw))
	found := false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if hdr.Name != "wolf.db" {
			continue
		}
		tmp := dest + ".tmp"
		f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, tr); err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
			return err
		}
		if err := f.Close(); err != nil {
			_ = os.Remove(tmp)
			return err
		}
		if err := os.Rename(tmp, dest); err != nil {
			_ = os.Remove(tmp)
			return err
		}
		found = true
		break
	}
	if !found {
		return fmt.Errorf("backup does not contain wolf.db")
	}
	return nil
}

func tarFile(tw *tar.Writer, name, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: info.Size(), ModTime: info.ModTime()}); err != nil {
		return err
	}
	_, err = io.Copy(tw, f)
	return err
}

func encryptBackup(plain []byte, password string) ([]byte, error) {
	key := sha256.Sum256([]byte(password))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := cryptorand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plain, nil), nil
}

func decryptBackup(raw []byte, password string) ([]byte, error) {
	key := sha256.Sum256([]byte(password))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(raw) < gcm.NonceSize() {
		return nil, fmt.Errorf("encrypted backup is truncated")
	}
	nonce, ciphertext := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	return gcm.Open(nil, nonce, ciphertext, nil)
}
