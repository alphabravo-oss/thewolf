package validate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/scannertools/manifest"
)

func TestValidateScannerBaseImagesRequiresDigestPins(t *testing.T) {
	root := t.TempDir()
	scanners := filepath.Join(root, "scanners")
	if err := os.MkdirAll(scanners, 0o750); err != nil {
		t.Fatal(err)
	}
	valid := "FROM --platform=linux/amd64 example.test/scanner@sha256:" + strings.Repeat("a", 64) + "\n"
	if err := os.WriteFile(filepath.Join(scanners, "Dockerfile"), []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := validateScannerBaseImages(root); len(got) != 0 {
		t.Fatalf("valid digest pin rejected: %v", got)
	}

	if err := os.WriteFile(
		filepath.Join(scanners, "Dockerfile.min"),
		[]byte("FROM debian:trixie-slim\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	got := validateScannerBaseImages(root)
	if len(got) != 1 || !strings.Contains(got[0], "not pinned by sha256 digest") {
		t.Fatalf("mutable base result = %v", got)
	}
}

func TestValidateVersionsBindsSourceIntegrityVariable(t *testing.T) {
	root := t.TempDir()
	scanners := filepath.Join(root, "scanners")
	if err := os.MkdirAll(scanners, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(scanners, "versions.env"),
		[]byte("DEMO_VERSION=1.2.3\nDEMO_ARCHIVE_SHA256="+strings.Repeat("a", 64)+"\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(scanners, "toolchains.yaml"),
		[]byte("toolchains: {}\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	m := &manifest.Manifest{Tools: map[string]manifest.Tool{
		"demo": {
			PinnedVersion:   "1.2.3",
			VersionVariable: "DEMO_VERSION",
			SourceIntegrity: manifest.SourceIntegrity{
				SHA256:         strings.Repeat("a", 64),
				SHA256Variable: "DEMO_ARCHIVE_SHA256",
			},
		},
	}}
	if got := validateVersions(root, m); len(got) != 0 {
		t.Fatalf("valid integrity variable rejected: %v", got)
	}

	tool := m.Tools["demo"]
	tool.SourceIntegrity.SHA256 = strings.Repeat("b", 64)
	m.Tools["demo"] = tool
	got := validateVersions(root, m)
	if len(got) != 1 || !strings.Contains(got[0], "DEMO_ARCHIVE_SHA256") {
		t.Fatalf("integrity mismatch result = %v", got)
	}
}

func TestValidateDirectArtifactIntegrityRequiresPinnedFailClosedInstallers(t *testing.T) {
	root := t.TempDir()
	installDir := filepath.Join(root, "scanners", "install")
	if err := os.MkdirAll(installDir, 0o750); err != nil {
		t.Fatal(err)
	}
	validScript := []byte("ARCHIVE_SHA256=x\nprintf '%s  %s\\n' \"$ARCHIVE_SHA256\" artifact | sha256sum --check --strict -\n")
	for _, name := range []string{"php.sh", "swift.sh"} {
		if err := os.WriteFile(filepath.Join(installDir, name), validScript, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	m := &manifest.Manifest{Tools: map[string]manifest.Tool{}}
	for _, name := range []string{"phpstan", "swiftlint"} {
		m.Tools[name] = manifest.Tool{
			PinnedVersion: "1.2.3",
			SourceIntegrity: manifest.SourceIntegrity{
				URL:            "https://downloads.example/1.2.3/archive",
				SHA256:         strings.Repeat("a", 64),
				SHA256Variable: "ARCHIVE_SHA256",
			},
		}
	}
	if got := validateDirectArtifactIntegrity(root, m); len(got) != 0 {
		t.Fatalf("valid direct-download integrity rejected: %v", got)
	}
	if err := os.WriteFile(
		filepath.Join(installDir, "php.sh"),
		[]byte("ARCHIVE_SHA256=x\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	got := validateDirectArtifactIntegrity(root, m)
	if len(got) != 1 || !strings.Contains(got[0], "does not fail closed") {
		t.Fatalf("non-verifying direct installer result = %v", got)
	}
}
