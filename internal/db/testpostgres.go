package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/google/uuid"
)

// migrationFiles is embedded only to fingerprint the schema for test templates.
//
//go:embed migrations/*.sql
var migrationFiles embed.FS

// TestingT is the part of *testing.T the Postgres test helpers need. It keeps
// the "testing" package out of the production binary.
type TestingT interface {
	Helper()
	Skip(args ...any)
	Fatalf(format string, args ...any)
	Cleanup(func())
}

// TestPostgresDSN returns a DSN for a private, fully migrated PostgreSQL
// database that is dropped when the test ends, so tests can run in parallel.
//
// WOLF_TEST_POSTGRES_DSN names any database on a server where the user may
// CREATE DATABASE, e.g. postgres://postgres:test@localhost:5432/postgres?sslmode=disable.
// Migrations run once per schema version into a template database
// (wolf_tpl_<hash>, left behind for the next run); every test then clones it.
// Locally the test is skipped when the variable is unset; under CI (CI is set)
// a missing variable fails so the Postgres suite can never silently skip.
func TestPostgresDSN(t TestingT) string {
	t.Helper()
	base := os.Getenv("WOLF_TEST_POSTGRES_DSN")
	if base == "" {
		if os.Getenv("CI") != "" {
			t.Fatalf("WOLF_TEST_POSTGRES_DSN is required in CI")
		}
		t.Skip("WOLF_TEST_POSTGRES_DSN is not set; see internal/db/testpostgres.go")
	}
	baseURL, err := url.Parse(base)
	if err != nil || (baseURL.Scheme != "postgres" && baseURL.Scheme != "postgresql") {
		t.Fatalf("WOLF_TEST_POSTGRES_DSN must be a postgres:// URL")
	}
	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatalf("open %v", err)
	}
	t.Cleanup(func() { _ = admin.Close() })

	template, err := ensureTestTemplate(admin, baseURL)
	if err != nil {
		t.Fatalf("prepare test template: %v", err)
	}
	name := "wolf_t_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec(`CREATE DATABASE "` + name + `" TEMPLATE "` + template + `"`); err != nil {
		t.Fatalf("create test database: %v", err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(`DROP DATABASE IF EXISTS "` + name + `" WITH (FORCE)`) })
	return testDatabaseDSN(baseURL, name)
}

// NewTestPostgres opens a migrated, isolated PostgreSQL store; see TestPostgresDSN.
func NewTestPostgres(t TestingT) *PostgresStore {
	t.Helper()
	store, err := NewPostgres(TestPostgresDSN(t))
	if err != nil {
		t.Fatalf("open test postgres: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func testDatabaseDSN(base *url.URL, database string) string {
	u := *base
	u.Path = "/" + database
	return u.String()
}

// ensureTestTemplate builds the migrated template database once per schema
// version. The advisory lock serializes parallel test binaries; the template is
// built under a temporary name and renamed, so a crash never leaves a partial one.
func ensureTestTemplate(admin *sql.DB, base *url.URL) (string, error) {
	hash := sha256.New()
	files, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return "", err
	}
	for _, file := range files {
		body, err := migrationFiles.ReadFile("migrations/" + file.Name())
		if err != nil {
			return "", err
		}
		_, _ = fmt.Fprintf(hash, "%s\x00%s\x00", file.Name(), body)
	}
	template := "wolf_tpl_" + hex.EncodeToString(hash.Sum(nil))[:16]

	conn, err := admin.Conn(context.Background())
	if err != nil {
		return "", err
	}
	defer func() { _ = conn.Close() }()
	const lockID = 0x776f6c74
	if _, err := conn.ExecContext(context.Background(), "SELECT pg_advisory_lock($1)", lockID); err != nil {
		return "", err
	}
	defer func() { _, _ = conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", lockID) }()
	var exists bool
	if err := conn.QueryRowContext(context.Background(), "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", template).Scan(&exists); err != nil || exists {
		return template, err
	}
	building := fmt.Sprintf("%s_build_%d", template, os.Getpid())
	_, _ = conn.ExecContext(context.Background(), `DROP DATABASE IF EXISTS "`+building+`" WITH (FORCE)`)
	if _, err := conn.ExecContext(context.Background(), `CREATE DATABASE "`+building+`"`); err != nil {
		return "", err
	}
	store, err := NewPostgres(testDatabaseDSN(base, building))
	if err == nil {
		err = store.Close()
	}
	if err == nil {
		_, err = conn.ExecContext(context.Background(), `ALTER DATABASE "`+building+`" RENAME TO "`+template+`"`)
	}
	if err != nil {
		_, _ = conn.ExecContext(context.Background(), `DROP DATABASE IF EXISTS "`+building+`" WITH (FORCE)`)
		return "", err
	}
	return template, nil
}
