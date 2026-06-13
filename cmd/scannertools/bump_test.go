package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/scannertools/manifest"
)

func TestBumpedImageReferenceUsesTagTemplate(t *testing.T) {
	tool := manifest.Tool{
		Image: manifest.Image{
			Repository:  "acme/tool",
			TagTemplate: "v{{ version }}",
		},
	}
	got := bumpedImageReference("acme/tool:v1.0.0", "1.0.0", "1.2.0", tool)
	if got != "acme/tool:v1.2.0" {
		t.Fatalf("bumpedImageReference = %q", got)
	}
}

func TestBumpVersionsEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "versions.env")
	if err := os.WriteFile(path, []byte("FOO_VERSION=1.0.0\nBAR_VERSION=2.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := bumpVersionsEnv(path, "FOO_VERSION", "1.1.0"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "FOO_VERSION=1.1.0\nBAR_VERSION=2.0.0\n" {
		t.Fatalf("versions.env was not updated as expected:\n%s", data)
	}
}

func TestWriteBumpChangelog(t *testing.T) {
	path, err := writeBumpChangelog(t.TempDir(), "semgrep", "1.92.0", "1.94.1")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	for _, want := range []string{
		"# Scanner Bump: semgrep",
		"- Previous version: 1.92.0",
		"- New version: 1.94.1",
		"`make scanners-validate`",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("changelog missing %q:\n%s", want, body)
		}
	}
}
