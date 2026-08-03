package scannerreleaseadapter

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExtractTrivyDatabaseArchiveAcceptsExactRuntimeLayout(t *testing.T) {
	archive := writeTrivyArchive(t, []trivyArchiveTestEntry{
		{name: "trivy.db", value: []byte("sqlite-database")},
		{name: "metadata.json", value: []byte(`{"Version":2,"NextUpdate":"2026-08-01T00:00:00Z"}`)},
	})
	target := filepath.Join(t.TempDir(), "db")
	if err := extractTrivyDatabaseArchive(archive, target, "trivy.db"); err != nil {
		t.Fatal(err)
	}
	for name, expected := range map[string]string{
		"trivy.db": "sqlite-database", "metadata.json": `{"Version":2,"NextUpdate":"2026-08-01T00:00:00Z"}`,
	} {
		value, err := os.ReadFile(filepath.Join(target, name))
		if err != nil || string(value) != expected {
			t.Fatalf("extracted %s=%q err=%v", name, value, err)
		}
	}
}

func TestExtractTrivyDatabaseArchiveRejectsHostileLayouts(t *testing.T) {
	tests := map[string][]trivyArchiveTestEntry{
		"traversal": {
			{name: "../trivy.db", value: []byte("db")},
			{name: "metadata.json", value: []byte(`{}`)},
		},
		"duplicate": {
			{name: "trivy.db", value: []byte("db")},
			{name: "trivy.db", value: []byte("other")},
			{name: "metadata.json", value: []byte(`{}`)},
		},
		"unexpected": {
			{name: "trivy.db", value: []byte("db")},
			{name: "metadata.json", value: []byte(`{}`)},
			{name: "extra", value: []byte("unexpected")},
		},
		"non-regular": {
			{name: "trivy.db", value: []byte("db")},
			{name: "metadata.json", value: []byte(`{}`)},
			{name: "directory", typeflag: tar.TypeDir},
		},
		"missing-metadata": {{name: "trivy.db", value: []byte("db")}},
	}
	for name, entries := range tests {
		t.Run(name, func(t *testing.T) {
			archive := writeTrivyArchive(t, entries)
			if err := extractTrivyDatabaseArchive(
				archive, filepath.Join(t.TempDir(), "db"), "trivy.db",
			); err == nil {
				t.Fatal("hostile Trivy archive was accepted")
			}
		})
	}
}

func TestFindSingleGzipArchiveRejectsAmbiguousAndSymlinkedLayers(t *testing.T) {
	root := t.TempDir()
	first := writeTrivyArchiveIn(t, root, "layer-one.tar.gz", []trivyArchiveTestEntry{
		{name: "trivy.db", value: []byte("db")},
	})
	found, err := findSingleGzipArchive(root)
	if err != nil || found != first {
		t.Fatalf("single archive=%q err=%v", found, err)
	}
	_ = writeTrivyArchiveIn(t, root, "layer-two.tar.gz", []trivyArchiveTestEntry{
		{name: "trivy.db", value: []byte("db")},
	})
	if _, err := findSingleGzipArchive(root); err == nil {
		t.Fatal("ambiguous gzip layers were accepted")
	}
	symlinkRoot := t.TempDir()
	if err := os.Symlink(first, filepath.Join(symlinkRoot, "layer.tar.gz")); err != nil {
		t.Fatal(err)
	}
	if _, err := findSingleGzipArchive(symlinkRoot); err == nil {
		t.Fatal("symlinked Trivy layer was accepted")
	}
}

func TestTrivyReportsProduceMeasuredVulnerabilitySecretAndLicenseEvidence(t *testing.T) {
	report := []byte(`{
  "SchemaVersion": 2,
  "Results": [
    {
      "Vulnerabilities": [{"Severity":"CRITICAL"},{"Severity":"high"},{"Severity":"LOW"}],
      "Secrets": [{"RuleID":"fake-secret"}],
      "Licenses": [{"Name":"Apache-2.0"},{"Name":"MIT"},{"Name":"MIT"},{"Name":"unknown"},{"Name":""}]
    }
  ]
}`)
	database := trivyDatabaseEvidence{Identity: "db@sha256:one", JavaIdentity: "java@sha256:two"}
	summary, err := trivyVulnerabilitySummary(report, database)
	if err != nil || summary["critical"] != 1 || summary["high"] != 1 ||
		summary["database_identity"] != database.Identity ||
		summary["java_database_identity"] != database.JavaIdentity {
		t.Fatalf("vulnerability summary=%+v err=%v", summary, err)
	}
	secrets, err := trivySecretCount(report)
	if err != nil || secrets != 1 {
		t.Fatalf("secret count=%d err=%v", secrets, err)
	}
	licenses, unknown, err := trivyLicenseSummary(report)
	if err != nil || strings.Join(licenses, ",") != "Apache-2.0,MIT" || unknown != 2 {
		t.Fatalf("licenses=%v unknown=%d err=%v", licenses, unknown, err)
	}
	for _, invalid := range [][]byte{
		nil,
		[]byte(`{"SchemaVersion":1,"Results":[]}`),
		[]byte(`{"SchemaVersion":2}`),
		[]byte(`not-json`),
	} {
		if _, err := decodeTrivyReport(invalid); err == nil {
			t.Fatalf("invalid Trivy report accepted: %q", invalid)
		}
	}
}

func TestReadTrivyDatabaseLockRejectsStaleUnknownAndTrailingIdentity(t *testing.T) {
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	valid := `{"schemaVersion":"wolf.scanners/vulnerability-db-lock/v1","provider":"trivy","repository":"ghcr.io/aquasecurity/trivy-db","digest":"sha256:` + strings.Repeat("a", 64) + `","recordedAt":"2026-07-30T11:00:00Z","expiresAt":"2026-08-01T00:00:00Z"}`
	path := filepath.Join(t.TempDir(), "lock.json")
	if err := os.WriteFile(path, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readTrivyDatabaseLock(path, now); err != nil {
		t.Fatalf("valid lock rejected: %v", err)
	}
	for name, value := range map[string]string{
		"trailing":           valid + `{}`,
		"unknown-repository": strings.Replace(valid, "ghcr.io/aquasecurity/trivy-db", "registry.example/trivy-db", 1),
		"stale":              strings.Replace(valid, "2026-08-01T00:00:00Z", "2026-07-30T11:30:00Z", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := readTrivyDatabaseLock(path, now); err == nil {
				t.Fatal("invalid lock was accepted")
			}
		})
	}
}

type trivyArchiveTestEntry struct {
	name     string
	value    []byte
	typeflag byte
}

func writeTrivyArchive(t *testing.T, entries []trivyArchiveTestEntry) string {
	t.Helper()
	return writeTrivyArchiveIn(t, t.TempDir(), "database.tar.gz", entries)
}

func writeTrivyArchiveIn(
	t *testing.T,
	directory, name string,
	entries []trivyArchiveTestEntry,
) string {
	t.Helper()
	path := filepath.Join(directory, name)
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	compressed := gzip.NewWriter(file)
	writer := tar.NewWriter(compressed)
	for _, entry := range entries {
		typeflag := entry.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		size := int64(len(entry.value))
		if typeflag != tar.TypeReg && typeflag != tar.TypeRegA {
			size = 0
		}
		if err := writer.WriteHeader(&tar.Header{
			Name: entry.name, Typeflag: typeflag, Mode: 0o600, Size: size,
		}); err != nil {
			t.Fatal(err)
		}
		if size > 0 {
			if _, err := writer.Write(entry.value); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
