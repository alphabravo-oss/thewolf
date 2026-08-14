// Package install installs fixer CLIs into $HOME/.local/bin so a read-only
// worker image can still gain claude/codex/opencode without a rebuild.
package install

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"bytes"
)

const httpTimeout = 3 * time.Minute

// EnsureLocalBinOnPATH creates $HOME/.local/bin and appends it to PATH.
// Image binaries in /usr/local/bin stay first so a broken HOME install
// cannot shadow the Debian fixer image.
func EnsureLocalBinOnPATH() {
	bin := LocalBin()
	if bin == "" {
		return
	}
	_ = os.MkdirAll(bin, 0o750)
	RemoveBrokenLocalCLIs()
	path := os.Getenv("PATH")
	if path == "" {
		_ = os.Setenv("PATH", bin)
		return
	}
	for _, part := range strings.Split(path, string(os.PathListSeparator)) {
		if part == bin {
			return
		}
	}
	_ = os.Setenv("PATH", path+string(os.PathListSeparator)+bin)
}

// RemoveBrokenLocalCLIs deletes HOME-installed binaries that fail to exec
// (the Alpine musl/libstdc++ mismatch).
func RemoveBrokenLocalCLIs() {
	dir := LocalBin()
	if dir == "" {
		return
	}
	for _, name := range []string{"claude", "codex", "opencode"} {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err := exec.CommandContext(ctx, p, "--version").Run()
		cancel()
		if err != nil {
			_ = os.Remove(p)
		}
	}
}

// LocalBin is $HOME/.local/bin (empty if HOME cannot be resolved).
func LocalBin() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".local", "bin")
}

// Supported reports whether this worker can download the named CLI.
func Supported(name string) bool {
	_, err := specFor(name)
	return err == nil
}

// Installed reports whether the CLI is already on PATH.
func Installed(name string) bool {
	cmd := commandName(name)
	if cmd == "" {
		return false
	}
	EnsureLocalBinOnPATH()
	_, err := exec.LookPath(cmd)
	return err == nil
}

// Install downloads a pinned official binary into $HOME/.local/bin.
func Install(ctx context.Context, name string, logf func(string)) error {
	if logf == nil {
		logf = func(string) {}
	}
	EnsureLocalBinOnPATH()
	if _, err := os.Stat("/etc/alpine-release"); err == nil {
		return fmt.Errorf("%s cannot run on the Alpine API image (missing glibc/libstdc++). Use the separate Debian fixer image (fixer/Dockerfile.engines) and scale that Deployment independently", commandName(name))
	}
	spec, err := specFor(name)
	if err != nil {
		return err
	}
	if Installed(spec.Command) {
		logf(spec.Command + " is already installed")
		return nil
	}
	destDir := LocalBin()
	if destDir == "" {
		return fmt.Errorf("cannot resolve HOME for CLI install")
	}
	if err := os.MkdirAll(destDir, 0o750); err != nil {
		return fmt.Errorf("create %s: %w", destDir, err)
	}

	logf(fmt.Sprintf("downloading %s %s", spec.Command, spec.Version))
	body, err := download(ctx, spec.URL)
	if err != nil {
		return err
	}
	if err := verify(body, spec); err != nil {
		return err
	}
	logf("checksum ok")

	tmpDir, err := os.MkdirTemp("", "wolf-fixer-cli-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	binPath, err := extractNamed(body, spec.Command, tmpDir)
	if err != nil {
		return err
	}
	dest := filepath.Join(destDir, spec.Command)
	if err := os.Rename(binPath, dest); err != nil {
		data, rerr := os.ReadFile(binPath) // #nosec G304
		if rerr != nil {
			return fmt.Errorf("install %s: %w", spec.Command, err)
		}
		if werr := os.WriteFile(dest, data, 0o750); werr != nil {
			return fmt.Errorf("write %s: %w", dest, werr)
		}
	}
	if err := os.Chmod(dest, 0o750); err != nil {
		return err
	}
	EnsureLocalBinOnPATH()
	if _, err := exec.LookPath(spec.Command); err != nil {
		return fmt.Errorf("installed %s to %s but it is still not on PATH", spec.Command, dest)
	}
	logf(fmt.Sprintf("installed %s to %s", spec.Command, dest))
	return nil
}

func commandName(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "claude", "claude-code":
		return "claude"
	case "codex":
		return "codex"
	case "opencode":
		return "opencode"
	default:
		return ""
	}
}

func download(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "wolf-fixer")
	client := &http.Client{Timeout: httpTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	const maxBytes = 200 << 20
	return io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
}

func verify(body []byte, spec cliSpec) error {
	if spec.SHA256 != "" {
		sum := sha256.Sum256(body)
		got := hex.EncodeToString(sum[:])
		if !strings.EqualFold(got, spec.SHA256) {
			return fmt.Errorf("sha256 mismatch for %s", spec.Command)
		}
	}
	if spec.SHA512SRI != "" {
		sum := sha512.Sum512(body)
		got := "sha512-" + base64.StdEncoding.EncodeToString(sum[:])
		if got != spec.SHA512SRI {
			return fmt.Errorf("sha512 mismatch for %s", spec.Command)
		}
	}
	if spec.SHA256 == "" && spec.SHA512SRI == "" {
		return fmt.Errorf("no checksum configured for %s", spec.Command)
	}
	return nil
}

func extractNamed(archive []byte, name, destDir string) (string, error) {
	gr, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return "", fmt.Errorf("not a gzip archive: %w", err)
	}
	defer func() { _ = gr.Close() }()
	tr := tar.NewReader(gr)
	var found string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		base := filepath.Base(hdr.Name)
		if base != name && base != name+"-linux" {
			continue
		}
		out := filepath.Join(destDir, name)
		f, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o750) // #nosec G304
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(f, io.LimitReader(tr, 180<<20)); err != nil {
			_ = f.Close()
			return "", err
		}
		if err := f.Close(); err != nil {
			return "", err
		}
		found = out
		if base == name {
			return found, nil
		}
	}
	if found == "" {
		return "", fmt.Errorf("archive did not contain %s", name)
	}
	return found, nil
}

func linuxMusl() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	if _, err := os.Stat("/etc/alpine-release"); err == nil {
		return true
	}
	return false
}

func goarch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x64"
	case "arm64":
		return "arm64"
	default:
		return runtime.GOARCH
	}
}
