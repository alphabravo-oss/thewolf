package db

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func TestMigrateSQLiteToPostgres(t *testing.T) {
	target := TestPostgresDSN(t)
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "wolf.db")
	source, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile("testdata/sqlite_fixture.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Exec(string(fixture)); err != nil {
		t.Fatal(err)
	}
	// Invalid UTF-8 cannot live in the .sql file.
	if _, err := source.Exec(`UPDATE findings SET title = ? WHERE id = 'f1'`, "truncated \xe2"); err != nil {
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	const user, scanID = "u1", "s1"
	created := time.Date(2026, 10, 1, 12, 34, 56, 123456789, time.UTC)

	var out bytes.Buffer
	if err := MigrateSQLiteToPostgres(ctx, path, target, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if strings.Contains(out.String(), "ciphertext-not-for-logs") || !strings.Contains(out.String(), "findings.title: replaced invalid UTF-8") ||
		!strings.Contains(out.String(), "skip removed_feature: not in the postgres schema") {
		t.Fatalf("unexpected output:\n%s", out.String())
	}
	if err := MigrateSQLiteToPostgres(ctx, path, target, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "already has rows") {
		t.Fatalf("second migration into a used database: %v", err)
	}

	pg, err := NewPostgres(target)
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Close()
	if u, err := pg.GetUserByID(ctx, user); err != nil || u.Email != user+"@example.test" {
		t.Fatalf("user: %+v %v", u, err)
	}
	got, err := pg.GetScanByID(ctx, scanID)
	if err != nil || got.ToolsSelected != `["semgrep","trivy"]` || !got.CreatedAt.Equal(created.Truncate(time.Microsecond)) {
		t.Fatalf("scan: %+v %v (want created %s)", got, err, created)
	}
	findings, err := pg.ListFindingsByScan(ctx, scanID)
	if err != nil || len(findings) != 1 || !findings[0].Suppressed || findings[0].Title != "truncated �" ||
		len(findings[0].CorroboratedBy) != 1 || findings[0].CorroboratedBy[0] != "trivy" {
		t.Fatalf("findings: %+v %v", findings, err)
	}
	if rec, err := pg.GetEnterpriseRecord(ctx, "policy", "p1"); err != nil || rec == nil || rec.Body != `{"a":1}` {
		t.Fatalf("enterprise record: %+v %v", rec, err)
	}
	secrets, err := pg.ListSecretsByUser(ctx, user)
	if err != nil || len(secrets) != 1 {
		t.Fatalf("secrets: %v %v", len(secrets), err)
	}
	if secret, err := pg.GetSecretByID(ctx, secrets[0].ID); err != nil || secret.EncryptedValue != "ciphertext-not-for-logs" {
		t.Fatalf("secret did not round-trip: %v", err)
	}
	if value, err := pg.GetSetting(ctx, "fleet_mode"); err != nil || value != "true" {
		t.Fatalf("setting: %q %v", value, err)
	}
}
