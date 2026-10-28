package db

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jmoiron/sqlx"
	sqlite3 "github.com/mattn/go-sqlite3"
)

// migrationSeededTables hold only rows the Postgres migrations insert; they
// are replaced by the source rows. Any other non-empty table aborts the copy.
var migrationSeededTables = map[string]bool{"settings": true, "scanner_release_maintenance": true}

type pgColumn struct {
	Name     string  `db:"column_name"`
	Type     string  `db:"data_type"`
	Nullable string  `db:"is_nullable"`
	Default  *string `db:"column_default"`
}

// MigrateSQLiteToPostgres copies a stopped SQLite deployment into a fresh
// PostgreSQL database in one transaction. The SQLite file is opened read-only.
// Progress and the count summary go to out; cell values are never printed.
func MigrateSQLiteToPostgres(ctx context.Context, sqlitePath, postgresDSN string, out io.Writer) error {
	if _, err := os.Stat(sqlitePath); err != nil {
		return fmt.Errorf("sqlite source: %w", err)
	}
	source, err := sqlx.Open("sqlite3", "file:"+sqlitePath+"?mode=ro&_query_only=on")
	if err != nil {
		return err
	}
	defer func() { _ = source.Close() }()
	// One connection and one read transaction give every table the same snapshot.
	source.SetMaxOpenConns(1)
	read, err := source.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("open sqlite: %w", err)
	}
	defer func() { _ = read.Rollback() }()
	var sourceTables []string
	if err := read.SelectContext(ctx, &sourceTables,
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`); err != nil {
		return fmt.Errorf("read sqlite schema: %w", err)
	}

	// Schema comes from the normal store migrations, never from the source.
	store, err := NewPostgres(postgresDSN)
	if err != nil {
		return fmt.Errorf("prepare postgres schema: %w", err)
	}
	_ = store.Close()
	conn, err := pgx.Connect(ctx, postgresDSN)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(context.Background()) }()
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	var targetTables []string
	rows, err := tx.Query(ctx, `SELECT table_name FROM information_schema.tables
		WHERE table_schema = current_schema() AND table_type = 'BASE TABLE' ORDER BY table_name`)
	if err != nil {
		return err
	}
	if targetTables, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
		return err
	}
	for _, table := range targetTables {
		var used bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM `+quoteBackupIdentifier(table)+`)`).Scan(&used); err != nil {
			return err
		}
		if used && !migrationSeededTables[table] {
			return fmt.Errorf("target table %s already has rows; migrate into an empty database", table)
		}
	}
	for _, table := range sourceTables {
		if !slices.Contains(targetTables, table) {
			_, _ = fmt.Fprintf(out, "skip %s: not in the postgres schema\n", table)
		}
	}
	order, err := postgresInsertOrder(ctx, tx, targetTables)
	if err != nil {
		return err
	}

	type count struct {
		table          string
		source, target int64
	}
	var counts []count
	for _, table := range order {
		if !slices.Contains(sourceTables, table) {
			_, _ = fmt.Fprintf(out, "skip %s: not in the sqlite source\n", table)
			continue
		}
		started := time.Now()
		copied, err := copySQLiteTable(ctx, read, tx, table, out)
		if err != nil {
			return fmt.Errorf("copy %s: %w", table, redactPostgresError(err))
		}
		var sourceCount, targetCount int64
		if err := read.GetContext(ctx, &sourceCount, `SELECT count(*) FROM `+quoteBackupIdentifier(table)); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM `+quoteBackupIdentifier(table)).Scan(&targetCount); err != nil {
			return err
		}
		counts = append(counts, count{table, sourceCount, targetCount})
		_, _ = fmt.Fprintf(out, "copied %s: %d rows in %s\n", table, copied, time.Since(started).Round(time.Millisecond))
	}
	if err := resetPostgresSequences(ctx, tx); err != nil {
		return err
	}

	summary := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(summary, "TABLE\tSQLITE\tPOSTGRES\t")
	mismatch := false
	for _, c := range counts {
		mark := ""
		if c.source != c.target {
			mismatch, mark = true, "MISMATCH"
		}
		_, _ = fmt.Fprintf(summary, "%s\t%d\t%d\t%s\n", c.table, c.source, c.target, mark)
	}
	_ = summary.Flush()
	if mismatch {
		return errors.New("row counts differ; postgres transaction rolled back")
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	// Bulk-loaded tables have no planner statistics until autovacuum runs.
	_, err = conn.Exec(ctx, "ANALYZE")
	return err
}

// postgresInsertOrder sorts tables so referenced rows load first. Foreign keys
// stay enforced; self-references are checked at the end of each COPY.
func postgresInsertOrder(ctx context.Context, tx pgx.Tx, tables []string) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT DISTINCT child.relname::text, parent.relname::text
		FROM pg_constraint c
		JOIN pg_class child ON child.oid = c.conrelid
		JOIN pg_class parent ON parent.oid = c.confrelid
		WHERE c.contype = 'f' AND c.connamespace = current_schema()::regnamespace AND c.conrelid <> c.confrelid`)
	if err != nil {
		return nil, err
	}
	parents := map[string][]string{}
	for rows.Next() {
		var child, parent string
		if err := rows.Scan(&child, &parent); err != nil {
			return nil, err
		}
		parents[child] = append(parents[child], parent)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var order []string
	done := map[string]bool{}
	for len(order) < len(tables) {
		progressed := false
		for _, table := range tables {
			if done[table] || slices.ContainsFunc(parents[table], func(p string) bool { return !done[p] }) {
				continue
			}
			done[table], progressed = true, true
			order = append(order, table)
		}
		if !progressed {
			return nil, errors.New("foreign keys form a cycle; cannot order the copy")
		}
	}
	return order, nil
}

func copySQLiteTable(ctx context.Context, read *sqlx.Tx, tx pgx.Tx, table string, out io.Writer) (int64, error) {
	var sourceColumns []string
	if err := read.SelectContext(ctx, &sourceColumns, `SELECT name FROM pragma_table_info(?)`, table); err != nil {
		return 0, err
	}
	var targetColumns []pgColumn
	rows, err := tx.Query(ctx, `SELECT column_name::text, data_type::text, is_nullable::text, column_default::text
		FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = $1
		ORDER BY ordinal_position`, table)
	if err != nil {
		return 0, err
	}
	if targetColumns, err = pgx.CollectRows(rows, pgx.RowToStructByPos[pgColumn]); err != nil {
		return 0, err
	}
	var columns []pgColumn
	for _, column := range targetColumns {
		switch {
		case slices.Contains(sourceColumns, column.Name):
			columns = append(columns, column)
		case column.Nullable == "NO" && column.Default == nil:
			return 0, fmt.Errorf("NOT NULL column %s has no sqlite source and no default", column.Name)
		default:
			_, _ = fmt.Fprintf(out, "  %s.%s: not in sqlite, using the postgres default\n", table, column.Name)
		}
	}
	names := make([]string, len(columns))
	quoted := make([]string, len(columns))
	for i, column := range columns {
		names[i], quoted[i] = column.Name, quoteBackupIdentifier(column.Name)
	}
	for _, name := range sourceColumns {
		if !slices.Contains(names, name) {
			_, _ = fmt.Fprintf(out, "  %s.%s: not in postgres, dropped\n", table, name)
		}
	}
	if migrationSeededTables[table] {
		if _, err := tx.Exec(ctx, `DELETE FROM `+quoteBackupIdentifier(table)); err != nil {
			return 0, err
		}
	}
	source, err := read.QueryxContext(ctx, `SELECT `+strings.Join(quoted, ", ")+` FROM `+quoteBackupIdentifier(table))
	if err != nil {
		return 0, err
	}
	defer func() { _ = source.Close() }()
	copySource := &sqliteCopySource{rows: source, columns: columns, repaired: map[string]int{}}
	copied, err := tx.CopyFrom(ctx, pgx.Identifier{table}, names, copySource)
	for _, name := range names {
		if n := copySource.repaired[name]; n > 0 {
			_, _ = fmt.Fprintf(out, "  %s.%s: replaced invalid UTF-8/NUL bytes in %d values\n", table, name, n)
		}
	}
	return copied, err
}

type sqliteCopySource struct {
	rows     *sqlx.Rows
	columns  []pgColumn
	repaired map[string]int
	err      error
}

func (s *sqliteCopySource) Next() bool { return s.err == nil && s.rows.Next() }

func (s *sqliteCopySource) Values() ([]any, error) {
	values, err := s.rows.SliceScan()
	if err != nil {
		return nil, err
	}
	for i, column := range s.columns {
		if values[i], err = postgresCell(values[i], column.Type); err != nil {
			s.err = fmt.Errorf("column %s: %w", column.Name, err)
			return nil, s.err
		}
		// PostgreSQL text rejects NUL and invalid UTF-8 that SQLite accepted
		// (e.g. output truncated mid-rune). Keep the row; replace only those bytes.
		if text, ok := values[i].(string); ok && (!utf8.ValidString(text) || strings.ContainsRune(text, 0)) {
			values[i] = strings.ReplaceAll(strings.ToValidUTF8(text, "\uFFFD"), "\x00", "")
			s.repaired[column.Name]++
		}
	}
	return values, nil
}

func (s *sqliteCopySource) Err() error {
	if s.err != nil {
		return s.err
	}
	return s.rows.Err()
}

// postgresCell converts a go-sqlite3 value by the TARGET column type. Errors
// name the type only so secret values never reach logs.
func postgresCell(value any, targetType string) (any, error) {
	if text, ok := value.([]byte); ok && targetType != "bytea" {
		value = string(text)
	}
	if value == nil {
		return nil, nil
	}
	switch targetType {
	case "boolean":
		return canonicalBackupBoolean(value)
	case "timestamp with time zone", "timestamp without time zone", "date":
		switch typed := value.(type) {
		case time.Time:
			return typed.UTC(), nil
		case int64: // unix seconds
			return time.Unix(typed, 0).UTC(), nil
		case string: // same parse go-sqlite3 applies to TIMESTAMP columns
			text := strings.TrimSuffix(strings.TrimSpace(typed), "Z")
			for _, layout := range sqlite3.SQLiteTimestampFormats {
				if parsed, err := time.ParseInLocation(layout, text, time.UTC); err == nil {
					return parsed.UTC(), nil
				}
			}
		}
	case "integer", "bigint", "smallint":
		switch typed := value.(type) {
		case int64:
			return typed, nil
		case bool:
			if typed {
				return int64(1), nil
			}
			return int64(0), nil
		case string:
			return strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		}
	case "real", "double precision", "numeric":
		switch typed := value.(type) {
		case float64:
			return typed, nil
		case int64:
			return float64(typed), nil
		case string:
			return strconv.ParseFloat(strings.TrimSpace(typed), 64)
		}
	case "bytea":
		if text, ok := value.(string); ok {
			return []byte(text), nil
		}
		return value, nil
	default: // text, json, jsonb and other text-encoded types
		switch typed := value.(type) {
		case string:
			return typed, nil
		case int64:
			return strconv.FormatInt(typed, 10), nil
		case float64:
			return strconv.FormatFloat(typed, 'g', -1, 64), nil
		case bool:
			return strconv.FormatBool(typed), nil
		case time.Time:
			return typed.UTC().Format(time.RFC3339Nano), nil
		}
	}
	return nil, fmt.Errorf("cannot convert sqlite %T to postgres %s", value, targetType)
}

func resetPostgresSequences(ctx context.Context, tx pgx.Tx) error {
	rows, err := tx.Query(ctx, `SELECT table_name::text, column_name::text FROM information_schema.columns
		WHERE table_schema = current_schema() AND (column_default LIKE 'nextval(%' OR is_identity = 'YES')`)
	if err != nil {
		return err
	}
	type serial struct{ Table, Column string }
	serials, err := pgx.CollectRows(rows, pgx.RowToStructByPos[serial])
	if err != nil {
		return err
	}
	for _, s := range serials {
		column := quoteBackupIdentifier(s.Column)
		if _, err := tx.Exec(ctx, `SELECT setval(pg_get_serial_sequence($1, $2), COALESCE(max(`+column+`), 1), max(`+column+`) IS NOT NULL) FROM `+quoteBackupIdentifier(s.Table),
			quoteBackupIdentifier(s.Table), s.Column); err != nil {
			return fmt.Errorf("reset sequence %s.%s: %w", s.Table, s.Column, err)
		}
	}
	return nil
}

// redactPostgresError drops DETAIL, which can echo a whole failing row.
func redactPostgresError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return fmt.Errorf("%s (SQLSTATE %s, constraint %q, column %q)", pgErr.Message, pgErr.Code, pgErr.ConstraintName, pgErr.ColumnName)
	}
	return err
}
