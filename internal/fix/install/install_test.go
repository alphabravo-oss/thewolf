package install

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"
)

func TestExtractNamed(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	payload := []byte("#!/bin/sh\necho ok\n")
	hdr := &tar.Header{Name: "bin/opencode", Mode: 0755, Size: int64(len(payload))}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	got, err := extractNamed(buf.Bytes(), "opencode", dir)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(payload) {
		t.Fatalf("extracted %q", data)
	}
	if filepath.Base(got) != "opencode" {
		t.Fatalf("path %s", got)
	}
}

func TestVerifySHA256(t *testing.T) {
	body := []byte("hello")
	sum := sha256.Sum256(body)
	spec := cliSpec{Command: "x", SHA256: hex.EncodeToString(sum[:])}
	if err := verify(body, spec); err != nil {
		t.Fatal(err)
	}
	spec.SHA256 = "deadbeef"
	if err := verify(body, spec); err == nil {
		t.Fatal("expected mismatch")
	}
}

func TestEnsureLocalBinAppendsNotPrepends(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", "/usr/local/bin:/usr/bin")
	EnsureLocalBinOnPATH()
	path := os.Getenv("PATH")
	if !strings.HasPrefix(path, "/usr/local/bin") {
		t.Fatalf("image bin must stay first, got %s", path)
	}
	if !strings.Contains(path, LocalBin()) {
		t.Fatalf("local bin missing from PATH: %s", path)
	}
}

func TestSupported(t *testing.T) {
	if !Supported("opencode") || !Supported("claude") || !Supported("codex") {
		t.Fatal("expected CLIs to be supported")
	}
	if Supported("grok") {
		t.Fatal("grok has no worker CLI")
	}
}

func TestInstallBinaryPublishesAtomicallyWithinOpenedRoot(t *testing.T) {
	for _, kind := range []string{"existing", "symlink", "hardlink", "copy failure", "directory"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			outside := filepath.Join(t.TempDir(), "sentinel")
			if err := os.WriteFile(outside, []byte("outside unchanged"), 0600); err != nil {
				t.Fatal(err)
			}
			dest := filepath.Join(dir, "codex")
			var err error
			switch kind {
			case "symlink":
				err = os.Symlink(outside, dest)
			case "hardlink":
				err = os.Link(outside, dest)
			case "directory":
				err = os.Mkdir(dest, 0700)
			default:
				err = os.WriteFile(dest, []byte("previous binary"), 0700)
			}
			if err != nil {
				t.Fatal(err)
			}
			root, err := os.OpenRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = root.Close() }()
			var source io.Reader = strings.NewReader("complete new binary")
			if kind == "copy failure" {
				source = io.MultiReader(strings.NewReader("partial"), iotest.ErrReader(io.ErrUnexpectedEOF))
			}
			err = installBinary(root, "codex", source)
			if kind == "copy failure" || kind == "directory" {
				if err == nil {
					t.Fatal("expected installation failure")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if kind != "directory" {
				got, err := os.ReadFile(dest)
				want := "complete new binary"
				if kind == "copy failure" {
					want = "previous binary"
				}
				if err != nil || string(got) != want {
					t.Fatalf("destination = %q, %v; want %q", got, err, want)
				}
			}
			if kind != "copy failure" && kind != "directory" {
				info, err := os.Lstat(dest)
				if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0750 {
					t.Fatalf("installed entry: %v, %v", info, err)
				}
			}
			got, err := os.ReadFile(outside)
			if err != nil || string(got) != "outside unchanged" {
				t.Fatalf("outside target changed: %q %v", got, err)
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 1 || entries[0].Name() != "codex" {
				t.Fatalf("temporary install leaked: %v, %v", entries, err)
			}
		})
	}

	dir, outside := filepath.Join(t.TempDir(), "bin"), t.TempDir()
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	moved := dir + "-moved"
	if err := os.Rename(dir, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, dir); err != nil {
		t.Fatal(err)
	}
	if err := installBinary(root, "claude", strings.NewReader("pinned destination")); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(moved, "claude")); err != nil || string(got) != "pinned destination" {
		t.Fatalf("pinned install: %q %v", got, err)
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatalf("replacement directory touched: %v %v", entries, err)
	}
	for _, name := range []string{"", "../codex", "other", "claude-code", filepath.Join(outside, "codex")} {
		if err := installBinary(root, name, strings.NewReader("bad")); err == nil {
			t.Fatalf("invalid command accepted: %q", name)
		}
	}
}

func TestExtractNamedRejectsInvalidBinarySize(t *testing.T) {
	for _, size := range []int64{0, (180 << 20) + 1} {
		var archive bytes.Buffer
		gz := gzip.NewWriter(&archive)
		tarWriter := tar.NewWriter(gz)
		if err := tarWriter.WriteHeader(&tar.Header{Name: "bin/codex", Mode: 0755, Size: size}); err != nil {
			t.Fatal(err)
		}
		_ = tarWriter.Close() // Oversized header is enough to reject before reading its body.
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		if _, err := extractNamed(archive.Bytes(), "codex", dir); err == nil {
			t.Fatalf("accepted binary size %d", size)
		}
		if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
			t.Fatalf("invalid binary was created: %v %v", entries, err)
		}
	}
}
