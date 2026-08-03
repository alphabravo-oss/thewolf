package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

// backupTableOrder is both the payload allowlist and referential insertion
// order. Application secret tables and recovery bookkeeping are intentionally
// absent.
var backupTableOrder = []string{
	"scanner_update_policies",
	"scanner_registry_targets",
	"scanner_signer_profiles",
	"scanner_discovery_runs",
	"scanner_update_items",
	"scanner_release_candidates",
	"scanner_build_runs",
	"scanner_build_steps",
	"scanner_custom_builds",
	"scanner_custom_build_variants",
	"scanner_custom_build_logs",
	"scanner_releases",
	"scanner_release_tools",
	"scanner_release_images",
	"scanner_release_artifacts",
	"scanner_release_approvals",
	"scanner_rollouts",
	"scanner_rollout_cohorts",
	"scanner_worker_release_status",
	"scanner_operation_correlations",
	"scanner_release_events",
	"scanner_schedule_leases",
	"scanner_rollout_claims",
	"scanner_release_sequence_counters",
	"scanner_release_name_reservations",
	"scanner_release_notifications",
	"scanner_release_alerts",
	"scanner_registry_jobs",
	"scanner_registry_image_observations",
	"scanner_registry_quarantine_objects",
}

var sanitizedBackupColumns = map[string]struct{}{
	"lease_token":          {},
	"proposal_lease_token": {},
	"deletion_lease_token": {},
}

var backupSanitizedFields = []string{
	"*.lease_token",
	"scanner_release_candidates.proposal_lease_token",
	"scanner_registry_quarantine_objects.deletion_lease_token",
	"active lease expirations reset to 1970-01-01T00:00:00Z",
	"active worker identity and heartbeat cleared",
	"deleting quarantine objects reset to delete_failed",
}

var canonicalBackupBooleanColumns = map[string]map[string]struct{}{
	"scanner_update_policies": {
		"enabled": {},
	},
	"scanner_registry_targets": {
		"enabled": {},
	},
	"scanner_signer_profiles": {
		"workload_identity": {},
	},
	"scanner_build_steps": {
		"protected": {},
	},
	"scanner_releases": {
		"imported": {}, "legacy": {}, "protected": {}, "rollback_eligible": {},
	},
	"scanner_release_artifacts": {
		"protected": {},
	},
	"scanner_registry_quarantine_objects": {
		"protected": {},
	},
	"scanner_custom_builds": {
		"push": {},
	},
	"scanner_custom_build_variants": {
		"loaded_locally": {}, "pushed": {},
	},
}

func validateBackupCommand(command scannerrelease.BackupCommand, restore bool) error {
	if strings.TrimSpace(command.Actor) == "" ||
		strings.TrimSpace(command.Reason) == "" ||
		strings.TrimSpace(command.IdempotencyKey) == "" {
		return errors.New("backup operation requires actor, reason, and idempotency key")
	}
	if len(command.Actor) > 320 || len(command.Reason) > 2048 ||
		len(command.IdempotencyKey) > 200 {
		return errors.New("backup operation metadata exceeds its bounded size")
	}
	if restore && command.MaintenanceConfirmation != scannerrelease.RestoreConfirmation {
		return errors.New("restore requires the exact maintenance confirmation phrase")
	}
	return nil
}

func (r *scannerReleaseRepository) ExportReleaseBackup(
	ctx context.Context,
	command scannerrelease.BackupCommand,
) (*scannerrelease.ReleaseBackup, error) {
	if err := validateBackupCommand(command, false); err != nil {
		return nil, err
	}
	if existing, err := r.getBackupOperation(
		ctx, "export", command.IdempotencyKey,
	); err == nil {
		if existing.State == "completed" {
			return nil, fmt.Errorf(
				"%w: export key already completed as operation %s",
				scannerrelease.ErrIdempotencyConflict, existing.ID,
			)
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	started := utcNow()
	tx, err := r.db.BeginTxx(ctx, &sql.TxOptions{
		ReadOnly: true, Isolation: sql.LevelSerializable,
	})
	if err != nil {
		_ = r.recordBackupFailure(ctx, "export", command, "", err, started)
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	backup, err := r.exportReleaseBackupTx(ctx, tx, started)
	if err != nil {
		_ = r.recordBackupFailure(ctx, "export", command, "", err, started)
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		_ = r.recordBackupFailure(ctx, "export", command, "", err, started)
		return nil, err
	}
	counts := backupCounts(backup)
	if err := r.recordBackupCompletion(
		ctx, "export", command, backup.PayloadDigest, counts, started,
	); err != nil {
		return nil, err
	}
	return backup, nil
}

func (r *scannerReleaseRepository) exportReleaseBackupTx(
	ctx context.Context,
	tx *sqlx.Tx,
	createdAt time.Time,
) (*scannerrelease.ReleaseBackup, error) {
	backup := &scannerrelease.ReleaseBackup{
		Format: scannerrelease.BackupFormat, Version: scannerrelease.BackupFormatVersion,
		RequiredMigration: scannerrelease.BackupMigration, Complete: true,
		SourceBackend: r.db.DriverName(), CreatedAt: createdAt.UTC(),
		SanitizedFields: append([]string(nil), backupSanitizedFields...),
	}
	for _, tableName := range backupTableOrder {
		table, err := readBackupTable(ctx, tx, tableName)
		if err != nil {
			return nil, fmt.Errorf("export %s: %w", tableName, err)
		}
		backup.Tables = append(backup.Tables, table)
	}
	backup.SchemaFingerprint = backupSchemaFingerprint(backup.Tables)
	backup.PayloadDigest = backupPayloadDigest(backup)
	return backup, nil
}

func readBackupTable(
	ctx context.Context,
	tx *sqlx.Tx,
	tableName string,
) (scannerrelease.BackupTable, error) {
	orderColumns := firstBackupOrderColumns(tableName)
	quotedOrder := make([]string, len(orderColumns))
	for index, column := range orderColumns {
		quotedOrder[index] = quoteBackupIdentifier(column)
	}
	rows, err := tx.QueryxContext(
		ctx, `SELECT * FROM `+quoteBackupIdentifier(tableName)+
			` ORDER BY `+strings.Join(quotedOrder, ","),
	)
	if err != nil {
		return scannerrelease.BackupTable{}, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return scannerrelease.BackupTable{}, err
	}
	columnTypes, err := rows.ColumnTypes()
	if err != nil {
		return scannerrelease.BackupTable{}, err
	}
	booleanColumns := make(map[string]bool, len(columnTypes))
	for _, columnType := range columnTypes {
		booleanColumns[columnType.Name()] = strings.EqualFold(
			columnType.DatabaseTypeName(), "BOOLEAN",
		)
	}
	for column := range canonicalBackupBooleanColumns[tableName] {
		booleanColumns[column] = true
	}
	table := scannerrelease.BackupTable{Name: tableName, Columns: columns}
	for rows.Next() {
		values, err := rows.SliceScan()
		if err != nil {
			return scannerrelease.BackupTable{}, err
		}
		raw := make(map[string]any, len(columns))
		for index, column := range columns {
			raw[column] = values[index]
		}
		cells := make([]scannerrelease.BackupCell, len(values))
		for index, value := range values {
			value = sanitizedBackupValue(tableName, columns[index], value, raw)
			if booleanColumns[columns[index]] {
				value, err = canonicalBackupBoolean(value)
				if err != nil {
					return scannerrelease.BackupTable{}, fmt.Errorf(
						"column %s: %w", columns[index], err,
					)
				}
			}
			cell, err := backupCell(value)
			if err != nil {
				return scannerrelease.BackupTable{}, fmt.Errorf(
					"column %s: %w", columns[index], err,
				)
			}
			cells[index] = cell
		}
		table.Rows = append(table.Rows, cells)
	}
	if err := rows.Err(); err != nil {
		return scannerrelease.BackupTable{}, err
	}
	table.RowCount = len(table.Rows)
	table.Digest = backupTableDigest(table)
	return table, nil
}

func canonicalBackupBoolean(value any) (any, error) {
	switch typed := value.(type) {
	case nil, bool:
		return typed, nil
	case int64:
		if typed == 0 || typed == 1 {
			return typed == 1, nil
		}
	case int:
		if typed == 0 || typed == 1 {
			return typed == 1, nil
		}
	case []byte:
		return canonicalBackupBoolean(string(typed))
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "0", "false":
			return false, nil
		case "1", "true":
			return true, nil
		}
	}
	return nil, fmt.Errorf("invalid BOOLEAN database value %v", value)
}

func sanitizedBackupValue(
	table, column string,
	value any,
	row map[string]any,
) any {
	if _, sanitize := sanitizedBackupColumns[column]; sanitize {
		return ""
	}
	if table == "scanner_custom_builds" &&
		backupText(row["lease_token"]) != "" {
		switch column {
		case "worker_id":
			return ""
		case "heartbeat_at":
			return nil
		}
	}
	leaseTokenColumn := ""
	switch column {
	case "lease_expires_at":
		leaseTokenColumn = "lease_token"
	case "proposal_lease_expires_at":
		leaseTokenColumn = "proposal_lease_token"
	case "deletion_lease_expires_at":
		leaseTokenColumn = "deletion_lease_token"
	}
	if leaseTokenColumn != "" && backupText(row[leaseTokenColumn]) != "" {
		return time.Unix(0, 0).UTC()
	}
	if table == "scanner_registry_quarantine_objects" &&
		backupText(row["deletion_lease_token"]) != "" {
		switch column {
		case "state":
			if backupText(value) == "deleting" {
				return "delete_failed"
			}
		case "error_detail":
			return "deletion lease cleared by release backup"
		}
	}
	return value
}

func backupText(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		return ""
	}
}

func firstBackupOrderColumns(table string) []string {
	switch table {
	case "scanner_worker_release_status":
		return []string{"worker_id"}
	case "scanner_release_sequence_counters":
		return []string{"period_key"}
	case "scanner_rollout_claims":
		return []string{"rollout_id"}
	case "scanner_release_name_reservations":
		return []string{"candidate_id"}
	case "scanner_schedule_leases":
		return []string{"schedule_key", "period_key"}
	case "scanner_operation_correlations":
		return []string{"aggregate_type", "aggregate_id"}
	case "scanner_custom_build_logs":
		return []string{"build_id", "sequence"}
	default:
		return []string{"id"}
	}
}

func backupCell(value any) (scannerrelease.BackupCell, error) {
	switch typed := value.(type) {
	case nil:
		return scannerrelease.BackupCell{Kind: "null"}, nil
	case string:
		return scannerrelease.BackupCell{Kind: "string", Value: typed}, nil
	case []byte:
		return scannerrelease.BackupCell{Kind: "string", Value: string(typed)}, nil
	case time.Time:
		return scannerrelease.BackupCell{
			Kind: "time", Value: typed.UTC().Format(time.RFC3339Nano),
		}, nil
	case bool:
		return scannerrelease.BackupCell{
			Kind: "bool", Value: strconv.FormatBool(typed),
		}, nil
	case int64:
		return scannerrelease.BackupCell{
			Kind: "int", Value: strconv.FormatInt(typed, 10),
		}, nil
	case int:
		return scannerrelease.BackupCell{
			Kind: "int", Value: strconv.Itoa(typed),
		}, nil
	case float64:
		return scannerrelease.BackupCell{
			Kind: "float", Value: strconv.FormatFloat(typed, 'g', -1, 64),
		}, nil
	default:
		return scannerrelease.BackupCell{}, fmt.Errorf("unsupported database value type %T", value)
	}
}

func backupTableDigest(table scannerrelease.BackupTable) string {
	payload := struct {
		Name    string                        `json:"name"`
		Columns []string                      `json:"columns"`
		Rows    [][]scannerrelease.BackupCell `json:"rows"`
	}{
		Name: table.Name, Columns: table.Columns, Rows: table.Rows,
	}
	encoded, _ := json.Marshal(payload)
	return sha256String(encoded)
}

func backupSchemaFingerprint(tables []scannerrelease.BackupTable) string {
	type schemaTable struct {
		Name    string   `json:"name"`
		Columns []string `json:"columns"`
	}
	schema := make([]schemaTable, 0, len(tables))
	for _, table := range tables {
		schema = append(schema, schemaTable{Name: table.Name, Columns: table.Columns})
	}
	encoded, _ := json.Marshal(schema)
	return sha256String(encoded)
}

func backupPayloadDigest(backup *scannerrelease.ReleaseBackup) string {
	copy := *backup
	copy.PayloadDigest = ""
	encoded, _ := json.Marshal(copy)
	return sha256String(encoded)
}

func sha256String(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func backupCounts(backup *scannerrelease.ReleaseBackup) map[string]int {
	counts := make(map[string]int, len(backup.Tables))
	for _, table := range backup.Tables {
		counts[table.Name] = table.RowCount
	}
	return counts
}

func (r *scannerReleaseRepository) PreflightReleaseRestore(
	ctx context.Context,
	backup *scannerrelease.ReleaseBackup,
) (scannerrelease.BackupPreflight, error) {
	if err := validateReleaseBackup(backup); err != nil {
		return scannerrelease.BackupPreflight{}, err
	}
	currentTables, err := r.currentBackupSchema(ctx)
	if err != nil {
		return scannerrelease.BackupPreflight{}, err
	}
	preflight := scannerrelease.BackupPreflight{
		Valid: true, Format: backup.Format, Version: backup.Version,
		PayloadDigest:     backup.PayloadDigest,
		SchemaFingerprint: backup.SchemaFingerprint,
		SourceBackend:     backup.SourceBackend, TargetBackend: r.db.DriverName(),
		TableCounts: backupCounts(backup),
	}
	if currentFingerprint := backupSchemaFingerprint(currentTables); currentFingerprint != backup.SchemaFingerprint {
		preflight.Reasons = append(preflight.Reasons, "schema_fingerprint_mismatch")
	}
	empty, err := r.backupTargetEmpty(ctx, nil)
	if err != nil {
		return scannerrelease.BackupPreflight{}, err
	}
	preflight.TargetEmpty = empty
	if !empty {
		preflight.Reasons = append(preflight.Reasons, "target_release_domain_not_empty")
	}
	preflight.Restorable = len(preflight.Reasons) == 0
	return preflight, nil
}

func validateReleaseBackup(backup *scannerrelease.ReleaseBackup) error {
	if backup == nil {
		return errors.New("backup document is required")
	}
	if backup.Format != scannerrelease.BackupFormat ||
		backup.Version != scannerrelease.BackupFormatVersion ||
		backup.RequiredMigration != scannerrelease.BackupMigration {
		return errors.New("unsupported scanner release backup format or version")
	}
	if !backup.Complete {
		return errors.New("partial scanner release backups cannot be restored")
	}
	if backup.CreatedAt.IsZero() {
		return errors.New("scanner release backup creation time is required")
	}
	if !slices.Equal(backup.SanitizedFields, backupSanitizedFields) {
		return errors.New("scanner release backup sanitization contract mismatch")
	}
	if backup.PayloadDigest == "" ||
		backup.PayloadDigest != backupPayloadDigest(backup) {
		return errors.New("scanner release backup payload checksum mismatch")
	}
	if len(backup.Tables) != len(backupTableOrder) {
		return errors.New("scanner release backup table set is incomplete")
	}
	for index, expectedName := range backupTableOrder {
		table := backup.Tables[index]
		if table.Name != expectedName {
			return fmt.Errorf(
				"scanner release backup table order mismatch at %d: got %s want %s",
				index, table.Name, expectedName,
			)
		}
		if table.RowCount != len(table.Rows) ||
			table.Digest != backupTableDigest(table) {
			return fmt.Errorf("scanner release backup table %s checksum/count mismatch", table.Name)
		}
		for _, row := range table.Rows {
			if len(row) != len(table.Columns) {
				return fmt.Errorf("scanner release backup table %s has a partial row", table.Name)
			}
			for _, cell := range row {
				if err := validateBackupCell(cell); err != nil {
					return fmt.Errorf("scanner release backup table %s: %w", table.Name, err)
				}
			}
		}
		if err := validateSanitizedBackupTable(backup.CreatedAt, table); err != nil {
			return err
		}
	}
	if backup.SchemaFingerprint != backupSchemaFingerprint(backup.Tables) {
		return errors.New("scanner release backup schema fingerprint mismatch")
	}
	return nil
}

func validateSanitizedBackupTable(
	createdAt time.Time,
	table scannerrelease.BackupTable,
) error {
	columnIndexes := make(map[string]int, len(table.Columns))
	for index, column := range table.Columns {
		columnIndexes[column] = index
	}
	tokenExpiryColumns := map[string]string{
		"lease_token":          "lease_expires_at",
		"proposal_lease_token": "proposal_lease_expires_at",
		"deletion_lease_token": "deletion_lease_expires_at",
	}
	for _, row := range table.Rows {
		for tokenColumn, expiryColumn := range tokenExpiryColumns {
			tokenIndex, ok := columnIndexes[tokenColumn]
			if !ok {
				continue
			}
			if row[tokenIndex].Kind != "string" || row[tokenIndex].Value != "" {
				return fmt.Errorf(
					"scanner release backup table %s contains a lease capability",
					table.Name,
				)
			}
			expiryIndex, ok := columnIndexes[expiryColumn]
			if !ok || row[expiryIndex].Kind == "null" {
				continue
			}
			if row[expiryIndex].Kind != "time" {
				return fmt.Errorf(
					"scanner release backup table %s has invalid sanitized lease expiry",
					table.Name,
				)
			}
			expiry, err := time.Parse(time.RFC3339Nano, row[expiryIndex].Value)
			if err != nil || expiry.After(createdAt) {
				return fmt.Errorf(
					"scanner release backup table %s retains an active lease expiry",
					table.Name,
				)
			}
		}
		if table.Name == "scanner_registry_quarantine_objects" {
			if stateIndex, ok := columnIndexes["state"]; ok &&
				row[stateIndex].Value == "deleting" {
				return errors.New("scanner release backup retains an in-progress quarantine deletion")
			}
		}
	}
	return nil
}

func validateBackupCell(cell scannerrelease.BackupCell) error {
	switch cell.Kind {
	case "null", "string":
		return nil
	case "time":
		_, err := time.Parse(time.RFC3339Nano, cell.Value)
		return err
	case "bool":
		_, err := strconv.ParseBool(cell.Value)
		return err
	case "int":
		_, err := strconv.ParseInt(cell.Value, 10, 64)
		return err
	case "float":
		_, err := strconv.ParseFloat(cell.Value, 64)
		return err
	default:
		return fmt.Errorf("unknown backup cell kind %q", cell.Kind)
	}
}

func (r *scannerReleaseRepository) currentBackupSchema(
	ctx context.Context,
) ([]scannerrelease.BackupTable, error) {
	tables := make([]scannerrelease.BackupTable, 0, len(backupTableOrder))
	for _, name := range backupTableOrder {
		rows, err := r.db.QueryxContext(
			ctx, `SELECT * FROM `+quoteBackupIdentifier(name)+` WHERE 1 = 0`,
		)
		if err != nil {
			return nil, err
		}
		columns, columnsErr := rows.Columns()
		_ = rows.Close()
		if columnsErr != nil {
			return nil, columnsErr
		}
		tables = append(tables, scannerrelease.BackupTable{Name: name, Columns: columns})
	}
	return tables, nil
}

func (r *scannerReleaseRepository) backupTargetEmpty(
	ctx context.Context,
	tx *sqlx.Tx,
) (bool, error) {
	for _, table := range backupTableOrder {
		var count int
		query := `SELECT COUNT(*) FROM ` + quoteBackupIdentifier(table)
		var err error
		if tx == nil {
			err = r.db.GetContext(ctx, &count, query)
		} else {
			err = tx.GetContext(ctx, &count, query)
		}
		if err != nil {
			return false, err
		}
		if count != 0 {
			return false, nil
		}
	}
	return true, nil
}

func (r *scannerReleaseRepository) RestoreReleaseBackup(
	ctx context.Context,
	backup *scannerrelease.ReleaseBackup,
	command scannerrelease.BackupCommand,
) (*scannerrelease.BackupRestoreResult, error) {
	if err := validateBackupCommand(command, true); err != nil {
		return nil, err
	}
	now := utcNow()
	if existing, err := r.getBackupOperation(
		ctx, "restore", command.IdempotencyKey,
	); err == nil {
		suppliedDigest := ""
		if backup != nil {
			suppliedDigest = backup.PayloadDigest
		}
		if existing.PayloadDigest != suppliedDigest {
			return nil, scannerrelease.ErrIdempotencyConflict
		}
		if existing.State == "completed" {
			var counts map[string]int
			_ = json.Unmarshal([]byte(existing.TableCountsJSON), &counts)
			restoredAt := existing.StartedAt
			if existing.CompletedAt != nil {
				restoredAt = *existing.CompletedAt
			}
			return &scannerrelease.BackupRestoreResult{
				OperationID: existing.ID, State: existing.State,
				PayloadDigest: existing.PayloadDigest, TableCounts: counts,
				RestoredAt: restoredAt, Idempotent: true,
			}, nil
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if err := validateReleaseBackup(backup); err != nil {
		digest := ""
		if backup != nil {
			digest = backup.PayloadDigest
		}
		_ = r.recordBackupFailure(ctx, "restore", command, digest, err, now)
		return nil, err
	}
	preflight, err := r.PreflightReleaseRestore(ctx, backup)
	if err != nil {
		_ = r.recordBackupFailure(ctx, "restore", command, backup.PayloadDigest, err, now)
		return nil, err
	}
	if !preflight.Restorable {
		failure := fmt.Errorf(
			"scanner release restore preflight failed: %s",
			strings.Join(preflight.Reasons, ","),
		)
		_ = r.recordBackupFailure(ctx, "restore", command, backup.PayloadDigest, failure, now)
		return nil, failure
	}
	maintenance, err := r.acquireRestoreMaintenance(
		ctx, command.Actor, now, now.Add(30*time.Minute),
	)
	if err != nil {
		_ = r.recordBackupFailure(ctx, "restore", command, backup.PayloadDigest, err, now)
		return nil, err
	}
	defer func() {
		_ = r.releaseRestoreMaintenance(
			context.Background(), maintenance.LeaseToken, utcNow(),
		)
	}()
	tx, err := r.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		_ = r.recordBackupFailure(ctx, "restore", command, backup.PayloadDigest, err, now)
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	failRestore := func(failure error) error {
		_ = tx.Rollback()
		_ = r.recordBackupFailure(
			ctx, "restore", command, backup.PayloadDigest, failure, now,
		)
		return failure
	}
	if err := r.acquireRestoreDatabaseLock(ctx, tx, maintenance.LeaseToken, now); err != nil {
		return nil, failRestore(err)
	}
	empty, err := r.backupTargetEmpty(ctx, tx)
	if err != nil {
		return nil, failRestore(err)
	}
	if !empty {
		return nil, failRestore(errors.New("scanner release restore target changed after preflight"))
	}
	targetSchema, err := currentBackupSchemaTx(ctx, tx)
	if err != nil {
		return nil, failRestore(err)
	}
	if backupSchemaFingerprint(targetSchema) != backup.SchemaFingerprint {
		return nil, failRestore(errors.New("scanner release restore target schema changed after preflight"))
	}
	for _, table := range backup.Tables {
		if err := r.restoreBackupTable(ctx, tx, table); err != nil {
			return nil, failRestore(fmt.Errorf("restore %s: %w", table.Name, err))
		}
	}
	if err := r.verifyRestoreIntegrity(ctx, tx, backup); err != nil {
		return nil, failRestore(err)
	}
	counts := backupCounts(backup)
	completedAt := utcNow()
	operationID := uuid.NewString()
	countsJSON, _ := json.Marshal(counts)
	if _, err := r.execTx(ctx, tx,
		`INSERT INTO scanner_release_backup_operations
		 (id, operation_type, state, actor, reason, idempotency_key,
		  payload_digest, format_version, table_counts_json, error_detail,
		  started_at, completed_at)
		 VALUES (?, 'restore', 'completed', ?, ?, ?, ?, ?, ?, '', ?, ?)
		 ON CONFLICT(operation_type, idempotency_key) DO UPDATE SET
		  state = 'completed', actor = excluded.actor, reason = excluded.reason,
		  payload_digest = excluded.payload_digest,
		  format_version = excluded.format_version,
		  table_counts_json = excluded.table_counts_json, error_detail = '',
		  started_at = excluded.started_at, completed_at = excluded.completed_at`,
		operationID, command.Actor, command.Reason, command.IdempotencyKey,
		backup.PayloadDigest, backup.Version, string(countsJSON), now, completedAt,
	); err != nil {
		return nil, failRestore(err)
	}
	if err := tx.Commit(); err != nil {
		_ = r.recordBackupFailure(ctx, "restore", command, backup.PayloadDigest, err, now)
		return nil, err
	}
	operation, err := r.getBackupOperation(ctx, "restore", command.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	return &scannerrelease.BackupRestoreResult{
		OperationID: operation.ID, State: operation.State,
		PayloadDigest: backup.PayloadDigest, TableCounts: counts,
		RestoredAt: completedAt,
	}, nil
}

func currentBackupSchemaTx(
	ctx context.Context,
	tx *sqlx.Tx,
) ([]scannerrelease.BackupTable, error) {
	tables := make([]scannerrelease.BackupTable, 0, len(backupTableOrder))
	for _, name := range backupTableOrder {
		rows, err := tx.QueryxContext(
			ctx, `SELECT * FROM `+quoteBackupIdentifier(name)+` WHERE 1 = 0`,
		)
		if err != nil {
			return nil, err
		}
		columns, columnsErr := rows.Columns()
		_ = rows.Close()
		if columnsErr != nil {
			return nil, columnsErr
		}
		tables = append(tables, scannerrelease.BackupTable{Name: name, Columns: columns})
	}
	return tables, nil
}

func (r *scannerReleaseRepository) restoreBackupTable(
	ctx context.Context,
	tx *sqlx.Tx,
	table scannerrelease.BackupTable,
) error {
	if len(table.Rows) == 0 {
		return nil
	}
	quotedColumns := make([]string, len(table.Columns))
	placeholders := make([]string, len(table.Columns))
	for index, column := range table.Columns {
		quotedColumns[index] = quoteBackupIdentifier(column)
		placeholders[index] = "?"
	}
	query := `INSERT INTO ` + quoteBackupIdentifier(table.Name) +
		` (` + strings.Join(quotedColumns, ",") + `) VALUES (` +
		strings.Join(placeholders, ",") + `)`
	targetBooleanColumns, err := backupBooleanColumns(ctx, tx, table.Name)
	if err != nil {
		return err
	}
	for _, row := range table.Rows {
		values := make([]any, len(row))
		for index, cell := range row {
			value, err := restoreCell(cell, targetBooleanColumns[table.Columns[index]])
			if err != nil {
				return err
			}
			values[index] = value
		}
		if _, err := r.execTx(ctx, tx, query, values...); err != nil {
			return err
		}
	}
	return nil
}

func backupBooleanColumns(
	ctx context.Context,
	tx *sqlx.Tx,
	tableName string,
) (map[string]bool, error) {
	rows, err := tx.QueryxContext(
		ctx, `SELECT * FROM `+quoteBackupIdentifier(tableName)+` WHERE 1 = 0`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	types, err := rows.ColumnTypes()
	if err != nil {
		return nil, err
	}
	result := make(map[string]bool)
	for _, columnType := range types {
		result[columnType.Name()] = strings.EqualFold(
			columnType.DatabaseTypeName(), "BOOLEAN",
		)
	}
	return result, nil
}

func restoreCell(cell scannerrelease.BackupCell, targetBoolean bool) (any, error) {
	switch cell.Kind {
	case "null":
		return nil, nil
	case "string":
		return cell.Value, nil
	case "time":
		return time.Parse(time.RFC3339Nano, cell.Value)
	case "bool":
		value, err := strconv.ParseBool(cell.Value)
		return value, err
	case "int":
		value, err := strconv.ParseInt(cell.Value, 10, 64)
		if err != nil {
			return nil, err
		}
		if targetBoolean && (value == 0 || value == 1) {
			return value == 1, nil
		}
		return value, nil
	case "float":
		return strconv.ParseFloat(cell.Value, 64)
	default:
		return nil, fmt.Errorf("unknown backup cell kind %q", cell.Kind)
	}
}

func (r *scannerReleaseRepository) verifyRestoreIntegrity(
	ctx context.Context,
	tx *sqlx.Tx,
	backup *scannerrelease.ReleaseBackup,
) error {
	for _, expected := range backup.Tables {
		actual, err := readBackupTable(ctx, tx, expected.Name)
		if err != nil {
			return err
		}
		if actual.RowCount != expected.RowCount ||
			actual.Digest != expected.Digest {
			return fmt.Errorf(
				"restored table %s does not match immutable backup identity",
				expected.Name,
			)
		}
	}
	var inconsistent int
	if err := tx.GetContext(ctx, &inconsistent,
		`SELECT COUNT(*)
		 FROM scanner_operation_correlations AS correlation
		 LEFT JOIN scanner_release_events AS event
		   ON event.aggregate_type = correlation.aggregate_type
		  AND event.aggregate_id = correlation.aggregate_id
		 WHERE event.id IS NULL`); err != nil {
		return err
	}
	if inconsistent != 0 {
		return errors.New("restored operation correlation has no aggregate event")
	}
	if err := tx.GetContext(ctx, &inconsistent,
		`SELECT COUNT(*)
		 FROM scanner_release_events AS event
		 JOIN scanner_operation_correlations AS correlation
		   ON correlation.aggregate_type = event.aggregate_type
		  AND correlation.aggregate_id = event.aggregate_id
		 WHERE (event.trace_id <> '' OR event.operation_id <> '' OR
		        event.parent_operation_id <> '')
		   AND (event.trace_id <> correlation.trace_id OR
		        event.operation_id <> correlation.operation_id OR
		        event.parent_operation_id <> correlation.parent_operation_id)`); err != nil {
		return err
	}
	if inconsistent != 0 {
		return errors.New("restored event and operation correlation identities disagree")
	}
	if strings.Contains(strings.ToLower(r.db.DriverName()), "sqlite") {
		rows, err := tx.QueryxContext(ctx, `PRAGMA foreign_key_check`)
		if err != nil {
			return err
		}
		defer rows.Close()
		if rows.Next() {
			return errors.New("restored scanner release backup violates foreign-key integrity")
		}
	}
	return nil
}

func (r *scannerReleaseRepository) acquireRestoreMaintenance(
	ctx context.Context,
	owner string,
	now, expires time.Time,
) (*scannerrelease.MaintenanceStatus, error) {
	token := uuid.NewString()
	result, err := r.db.ExecContext(ctx, r.db.Rebind(
		`UPDATE scanner_release_maintenance
		 SET mode = 'restore', owner = ?, lease_token = ?, lease_expires_at = ?,
		     version = version + 1, updated_at = ?
		 WHERE id = 'scanner-release'
		   AND (mode = 'normal' OR lease_expires_at IS NULL OR lease_expires_at <= ?)`),
		owner, token, expires, now, now)
	if err != nil {
		return nil, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return nil, errors.New("scanner release restore maintenance is already owned")
	}
	return r.GetReleaseMaintenanceStatus(ctx)
}

func (r *scannerReleaseRepository) acquireRestoreDatabaseLock(
	ctx context.Context,
	tx *sqlx.Tx,
	token string,
	now time.Time,
) error {
	if strings.Contains(strings.ToLower(r.db.DriverName()), "postgres") {
		quoted := make([]string, 0, len(backupTableOrder))
		for _, table := range backupTableOrder {
			quoted = append(quoted, quoteBackupIdentifier(table))
		}
		if _, err := tx.ExecContext(
			ctx, `LOCK TABLE `+strings.Join(quoted, ",")+` IN ACCESS EXCLUSIVE MODE`,
		); err != nil {
			return err
		}
	}
	result, err := r.execTx(ctx, tx,
		`UPDATE scanner_release_maintenance SET updated_at = ?
		 WHERE id = 'scanner-release' AND mode = 'restore' AND lease_token = ?`,
		now, token)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return errors.New("scanner release restore maintenance lease was lost")
	}
	return nil
}

func (r *scannerReleaseRepository) releaseRestoreMaintenance(
	ctx context.Context,
	token string,
	now time.Time,
) error {
	_, err := r.db.ExecContext(ctx, r.db.Rebind(
		`UPDATE scanner_release_maintenance
		 SET mode = 'normal', owner = '', lease_token = '', lease_expires_at = NULL,
		     version = version + 1, updated_at = ?
		 WHERE id = 'scanner-release' AND lease_token = ?`),
		now, token)
	return err
}

func (r *scannerReleaseRepository) GetReleaseMaintenanceStatus(
	ctx context.Context,
) (*scannerrelease.MaintenanceStatus, error) {
	var status scannerrelease.MaintenanceStatus
	if err := r.get(ctx, &status,
		`SELECT * FROM scanner_release_maintenance WHERE id = 'scanner-release'`); err != nil {
		return nil, err
	}
	return &status, nil
}

func (r *scannerReleaseRepository) ListBackupOperations(
	ctx context.Context,
	limit int,
) ([]scannerrelease.BackupOperation, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var operations []scannerrelease.BackupOperation
	return operations, r.selectRows(ctx, &operations,
		`SELECT * FROM scanner_release_backup_operations
		 ORDER BY started_at DESC, id DESC LIMIT ?`, limit)
}

func (r *scannerReleaseRepository) getBackupOperation(
	ctx context.Context,
	operationType, key string,
) (*scannerrelease.BackupOperation, error) {
	var operation scannerrelease.BackupOperation
	if err := r.get(ctx, &operation,
		`SELECT * FROM scanner_release_backup_operations
		 WHERE operation_type = ? AND idempotency_key = ?`,
		operationType, key); err != nil {
		return nil, err
	}
	return &operation, nil
}

func (r *scannerReleaseRepository) recordBackupCompletion(
	ctx context.Context,
	operationType string,
	command scannerrelease.BackupCommand,
	payloadDigest string,
	counts map[string]int,
	started time.Time,
) error {
	completed := utcNow()
	countsJSON, _ := json.Marshal(counts)
	_, err := r.db.ExecContext(ctx, r.db.Rebind(
		`INSERT INTO scanner_release_backup_operations
		 (id, operation_type, state, actor, reason, idempotency_key,
		  payload_digest, format_version, table_counts_json, error_detail,
		  started_at, completed_at)
		 VALUES (?, ?, 'completed', ?, ?, ?, ?, ?, ?, '', ?, ?)
		 ON CONFLICT(operation_type, idempotency_key) DO UPDATE SET
		  state = 'completed', actor = excluded.actor, reason = excluded.reason,
		  payload_digest = excluded.payload_digest,
		  format_version = excluded.format_version,
		  table_counts_json = excluded.table_counts_json, error_detail = '',
		  started_at = excluded.started_at, completed_at = excluded.completed_at`),
		uuid.NewString(), operationType, command.Actor, command.Reason,
		command.IdempotencyKey, payloadDigest, scannerrelease.BackupFormatVersion,
		string(countsJSON), started, completed)
	return err
}

func (r *scannerReleaseRepository) recordBackupFailure(
	ctx context.Context,
	operationType string,
	command scannerrelease.BackupCommand,
	payloadDigest string,
	failure error,
	started time.Time,
) error {
	if payloadDigest == "" {
		payloadDigest = "unavailable"
	}
	detail := boundedRegistryJobError(failure.Error())
	completed := utcNow()
	_, err := r.db.ExecContext(ctx, r.db.Rebind(
		`INSERT INTO scanner_release_backup_operations
		 (id, operation_type, state, actor, reason, idempotency_key,
		  payload_digest, format_version, table_counts_json, error_detail,
		  started_at, completed_at)
		 VALUES (?, ?, 'failed', ?, ?, ?, ?, ?, '{}', ?, ?, ?)
		 ON CONFLICT(operation_type, idempotency_key) DO UPDATE SET
		  state = 'failed', payload_digest = excluded.payload_digest,
		  error_detail = excluded.error_detail, completed_at = excluded.completed_at`),
		uuid.NewString(), operationType, command.Actor, command.Reason,
		command.IdempotencyKey, payloadDigest, scannerrelease.BackupFormatVersion,
		detail, started, completed)
	return err
}

func quoteBackupIdentifier(value string) string {
	// Every caller supplies a name from the compiled allowlist or from columns
	// read directly from that allowlisted table. Double-quoting preserves names
	// across SQLite and PostgreSQL.
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
