package db

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestMigrateSQLiteToPostgres(t *testing.T) {
	dsn := os.Getenv("WOLF_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("WOLF_TEST_POSTGRES_DSN is not configured")
	}
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "wolf.db")
	lite, err := NewSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	user, repo := uuid.NewString(), uuid.NewString()
	if err := lite.CreateUser(ctx, &models.User{ID: user, Email: user + "@example.test", PasswordHash: "hash"}); err != nil {
		t.Fatal(err)
	}
	if err := lite.CreateRepo(ctx, &models.Repo{ID: repo, UserID: user, Name: "migrated", SourceType: models.SourceTypeLocal, SourcePath: "/src"}); err != nil {
		t.Fatal(err)
	}
	scan := &models.Scan{ID: uuid.NewString(), UserID: user, RepoID: repo, Status: models.ScanStatusRunning, ToolsSelected: `["semgrep","trivy"]`}
	if err := lite.CreateScan(ctx, scan); err != nil {
		t.Fatal(err)
	}
	finding := models.Finding{ID: uuid.NewString(), ScanID: scan.ID, RepoID: repo, ToolName: "semgrep", Category: models.CategorySAST,
		RuleID: "rule", FilePath: "a.go", Title: "truncated \xe2", Severity: models.SeverityHigh, Status: models.StatusOpen,
		Suppressed: true, CorroboratedBy: []string{"trivy"}}
	if err := lite.CreateFinding(ctx, &finding); err != nil {
		t.Fatal(err)
	}
	if err := lite.PutEnterpriseRecord(ctx, &models.EnterpriseRecord{Kind: "policy", ID: "p1", Name: "Policy", Body: `{"a":1}`}); err != nil {
		t.Fatal(err)
	}
	if err := lite.CreateSecret(ctx, &models.Secret{ID: uuid.NewString(), UserID: user, KeyType: models.KeyType("github"), KeyName: "token", EncryptedValue: "ciphertext-not-for-logs"}); err != nil {
		t.Fatal(err)
	}
	if err := lite.SetSetting(ctx, "fleet_mode", "true"); err != nil {
		t.Fatal(err)
	}
	var created time.Time
	if err := lite.db.Get(&created, `SELECT created_at FROM scans WHERE id = ?`, scan.ID); err != nil {
		t.Fatal(err)
	}
	_ = lite.Close()

	admin, err := NewPostgres(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := "sqlite_migrate_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.db.Exec(`CREATE SCHEMA "` + schema + `"`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.db.Exec(`DROP SCHEMA "` + schema + `" CASCADE`) })
	target := postgresDSNWithSearchPath(t, dsn, schema)

	var out bytes.Buffer
	if err := MigrateSQLiteToPostgres(ctx, path, target, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if strings.Contains(out.String(), "ciphertext-not-for-logs") || !strings.Contains(out.String(), "findings.title: replaced invalid UTF-8") {
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
	got, err := pg.GetScanByID(ctx, scan.ID)
	if err != nil || got.ToolsSelected != scan.ToolsSelected || !got.CreatedAt.Equal(created.Truncate(time.Microsecond)) {
		t.Fatalf("scan: %+v %v (want created %s)", got, err, created)
	}
	findings, err := pg.ListFindingsByScan(ctx, scan.ID)
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
