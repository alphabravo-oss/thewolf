package install

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
