package db

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
	"github.com/alphabravocompany/thewolf/internal/scannersigning"
	"github.com/alphabravocompany/thewolf/internal/scannertrace"
)

// ScannerReleasePersistence is exported as an alias so application services
// can depend on the domain-owned grouped interfaces without importing a
// database implementation.
type ScannerReleasePersistence = scannerrelease.Persistence

type scannerReleaseRepository struct {
	db *sqlx.DB
}

var _ scannerrelease.Persistence = (*scannerReleaseRepository)(nil)

func newScannerReleaseRepository(db *sqlx.DB) *scannerReleaseRepository {
	return &scannerReleaseRepository{db: db}
}

func utcNow() time.Time { return time.Now().UTC() }

func initializeIDAndTimes(id *string, createdAt, updatedAt *time.Time) {
	if *id == "" {
		*id = uuid.NewString()
	}
	now := utcNow()
	if createdAt.IsZero() {
		*createdAt = now
	}
	if updatedAt.IsZero() {
		*updatedAt = *createdAt
	}
}

func jsonDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func (r *scannerReleaseRepository) namedExecTx(ctx context.Context, tx *sqlx.Tx, query string, arg any) error {
	_, err := r.namedExecResultTx(ctx, tx, query, arg)
	return err
}

func (r *scannerReleaseRepository) namedExecResultTx(ctx context.Context, tx *sqlx.Tx, query string, arg any) (sql.Result, error) {
	query, args, err := sqlx.Named(query, arg)
	if err != nil {
		return nil, err
	}
	return tx.ExecContext(ctx, r.db.Rebind(query), args...)
}

func (r *scannerReleaseRepository) execTx(ctx context.Context, tx *sqlx.Tx, query string, args ...any) (sql.Result, error) {
	return tx.ExecContext(ctx, r.db.Rebind(query), args...)
}

func (r *scannerReleaseRepository) get(ctx context.Context, dest any, query string, args ...any) error {
	return r.db.GetContext(ctx, dest, r.db.Rebind(query), args...)
}

func (r *scannerReleaseRepository) selectRows(ctx context.Context, dest any, query string, args ...any) error {
	return r.db.SelectContext(ctx, dest, r.db.Rebind(query), args...)
}

func normalizeCommand(command TransitionCommandAlias) scannerrelease.TransitionCommand {
	if command.IdempotencyKey == "" {
		command.IdempotencyKey = "event:" + uuid.NewString()
	}
	command.PayloadJSON = jsonDefault(command.PayloadJSON, "{}")
	if command.Actor == "" {
		command.Actor = "system"
	}
	return scannerrelease.TransitionCommand(command)
}

// TransitionCommandAlias avoids repeating a long qualified type in helper
// signatures while preserving the exact domain representation.
type TransitionCommandAlias scannerrelease.TransitionCommand

func (r *scannerReleaseRepository) appendEventTx(
	ctx context.Context,
	tx *sqlx.Tx,
	aggregateType, aggregateID, eventType, priorState, newState string,
	command scannerrelease.TransitionCommand,
	now time.Time,
) (*scannerrelease.Event, error) {
	command = normalizeCommand(TransitionCommandAlias(command))
	correlation, err := r.ensureOperationCorrelationTx(
		ctx, tx, aggregateType, aggregateID, command, now,
	)
	if err != nil {
		return nil, err
	}
	var sequence int64
	if err := tx.GetContext(ctx, &sequence, r.db.Rebind(
		`SELECT COALESCE(MAX(sequence), 0) + 1
		 FROM scanner_release_events
		 WHERE aggregate_type = ? AND aggregate_id = ?`),
		aggregateType, aggregateID); err != nil {
		return nil, err
	}
	event := &scannerrelease.Event{
		ID:                uuid.NewString(),
		AggregateType:     aggregateType,
		AggregateID:       aggregateID,
		Sequence:          sequence,
		EventType:         eventType,
		PriorState:        priorState,
		NewState:          newState,
		Actor:             command.Actor,
		Reason:            command.Reason,
		PolicyRevision:    command.PolicyRevision,
		IdempotencyKey:    command.IdempotencyKey,
		PayloadJSON:       command.PayloadJSON,
		TraceID:           correlation.TraceID,
		OperationID:       correlation.OperationID,
		ParentOperationID: correlation.ParentOperationID,
		CreatedAt:         now,
	}
	if err := r.namedExecTx(ctx, tx,
		`INSERT INTO scanner_release_events
		 (id, aggregate_type, aggregate_id, sequence, event_type, prior_state,
		  new_state, actor, reason, policy_revision, idempotency_key, payload_json,
		  trace_id, operation_id, parent_operation_id, created_at)
		 VALUES
		 (:id, :aggregate_type, :aggregate_id, :sequence, :event_type, :prior_state,
		  :new_state, :actor, :reason, :policy_revision, :idempotency_key, :payload_json,
		  :trace_id, :operation_id, :parent_operation_id, :created_at)`,
		event); err != nil {
		return nil, err
	}
	if err := r.appendNotificationsTx(ctx, tx, event); err != nil {
		return nil, err
	}
	return event, nil
}

func (r *scannerReleaseRepository) ensureOperationCorrelationTx(
	ctx context.Context,
	tx *sqlx.Tx,
	aggregateType, aggregateID string,
	command scannerrelease.TransitionCommand,
	now time.Time,
) (*scannerrelease.OperationCorrelation, error) {
	value, hasContext := scannertrace.FromContext(ctx)
	if scannertrace.ValidTraceID(command.TraceID) {
		value.TraceID = command.TraceID
	}
	if scannertrace.ValidOperationID(command.OperationID) {
		value.OperationID = command.OperationID
	}
	if scannertrace.ValidOperationID(command.ParentOperationID) {
		value.ParentOperationID = command.ParentOperationID
	}
	component := command.OriginComponent
	if component == "" && hasContext {
		component = value.Component
	}
	value = scannertrace.Normalize(value, component)
	correlation := &scannerrelease.OperationCorrelation{
		AggregateType: aggregateType, AggregateID: aggregateID,
		TraceID: value.TraceID, OperationID: value.OperationID,
		ParentOperationID: value.ParentOperationID,
		OriginComponent:   value.Component, CreatedAt: now,
	}
	if _, err := r.execTx(ctx, tx,
		`INSERT INTO scanner_operation_correlations
		 (aggregate_type, aggregate_id, trace_id, operation_id,
		  parent_operation_id, origin_component, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(aggregate_type, aggregate_id) DO NOTHING`,
		correlation.AggregateType, correlation.AggregateID, correlation.TraceID,
		correlation.OperationID, correlation.ParentOperationID,
		correlation.OriginComponent, correlation.CreatedAt); err != nil {
		return nil, err
	}
	if err := tx.GetContext(ctx, correlation, r.db.Rebind(
		`SELECT * FROM scanner_operation_correlations
		 WHERE aggregate_type = ? AND aggregate_id = ?`),
		aggregateType, aggregateID); err != nil {
		return nil, err
	}
	return correlation, nil
}

func (r *scannerReleaseRepository) existingCommandTx(
	ctx context.Context,
	tx *sqlx.Tx,
	aggregateType, aggregateID, idempotencyKey, intendedState string,
) (bool, error) {
	if idempotencyKey == "" {
		return false, nil
	}
	var existing scannerrelease.Event
	err := tx.GetContext(ctx, &existing, r.db.Rebind(
		`SELECT * FROM scanner_release_events
		 WHERE aggregate_type = ? AND aggregate_id = ? AND idempotency_key = ?`),
		aggregateType, aggregateID, idempotencyKey)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if existing.NewState != intendedState {
		return false, fmt.Errorf("%w: key %q was used for state %q", scannerrelease.ErrIdempotencyConflict, idempotencyKey, existing.NewState)
	}
	return true, nil
}

type transitionValidator func(string, string) error

func (r *scannerReleaseRepository) transitionState(
	ctx context.Context,
	table, aggregateType, id string,
	expectedVersion int64,
	to string,
	command scannerrelease.TransitionCommand,
	validate transitionValidator,
) (bool, error) {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	if exists, err := r.existingCommandTx(ctx, tx, aggregateType, id, command.IdempotencyKey, to); err != nil {
		return false, err
	} else if exists {
		return false, nil
	}

	var current struct {
		State             string     `db:"state"`
		Version           int64      `db:"version"`
		CancelRequestedAt *time.Time `db:"cancel_requested_at"`
	}
	currentColumns := `state, version, NULL AS cancel_requested_at`
	if table == "scanner_build_runs" {
		currentColumns = `state, version, cancel_requested_at`
	}
	if err := tx.GetContext(ctx, &current, r.db.Rebind(
		`SELECT `+currentColumns+` FROM `+table+` WHERE id = ?`), id); err != nil {
		return false, err
	}
	if current.Version != expectedVersion {
		return false, fmt.Errorf("%w: expected %d, current %d", scannerrelease.ErrVersionConflict, expectedVersion, current.Version)
	}
	if err := validate(current.State, to); err != nil {
		return false, err
	}
	if table == "scanner_build_runs" && current.CancelRequestedAt != nil &&
		to != string(scannerrelease.BuildCancelled) {
		return false, fmt.Errorf("%w: build cancellation has been requested", scannerrelease.ErrInvalidTransition)
	}
	now := utcNow()
	updateQuery := `UPDATE ` + table + `
		 SET state = ?, version = version + 1, updated_at = ?`
	updateArgs := []any{to, now}
	switch table {
	case "scanner_discovery_runs":
		updateQuery += `,
		     started_at = CASE WHEN ? = 'resolving' THEN COALESCE(started_at, ?) ELSE started_at END,
		     completed_at = CASE WHEN ? IN ('completed', 'failed', 'cancelled') THEN ? ELSE completed_at END`
		updateArgs = append(updateArgs, to, now, to, now)
	case "scanner_build_runs":
		updateQuery += `,
		     started_at = CASE WHEN ? IN ('claimed', 'running') THEN COALESCE(started_at, ?) ELSE started_at END,
		     completed_at = CASE WHEN ? IN ('completed', 'failed', 'cancelled') THEN ? ELSE completed_at END,
		     worker_id = CASE WHEN ? IN ('completed', 'failed', 'cancelled') THEN '' ELSE worker_id END,
		     lease_token = CASE WHEN ? IN ('completed', 'failed', 'cancelled') THEN '' ELSE lease_token END,
		     lease_expires_at = CASE WHEN ? IN ('completed', 'failed', 'cancelled') THEN NULL ELSE lease_expires_at END,
		     heartbeat_at = CASE WHEN ? IN ('completed', 'failed', 'cancelled') THEN NULL ELSE heartbeat_at END`
		updateArgs = append(updateArgs, to, now, to, now, to, to, to, to)
	case "scanner_build_steps":
		updateQuery += `,
		     started_at = CASE WHEN ? IN ('claimed', 'running') THEN COALESCE(started_at, ?) ELSE started_at END,
		     completed_at = CASE WHEN ? IN ('completed', 'failed', 'cancelled') THEN ? ELSE completed_at END`
		updateArgs = append(updateArgs, to, now, to, now)
	case "scanner_releases":
		updateQuery += `,
		     deprecated_at = CASE WHEN ? = 'deprecated' THEN ? ELSE deprecated_at END,
		     revoked_at = CASE WHEN ? = 'revoked' THEN ? ELSE revoked_at END,
		     rollback_eligible = CASE WHEN ? = 'revoked' THEN ? ELSE rollback_eligible END`
		updateArgs = append(updateArgs, to, now, to, now, to, false)
	case "scanner_rollouts":
		updateQuery += `,
		     started_at = CASE WHEN ? = 'preparing' THEN COALESCE(started_at, ?) ELSE started_at END,
		     completed_at = CASE WHEN ? IN ('completed', 'rolled_back') THEN ? ELSE completed_at END`
		updateArgs = append(updateArgs, to, now, to, now)
	}
	updateQuery += ` WHERE id = ? AND version = ?`
	updateArgs = append(updateArgs, id, expectedVersion)
	result, err := r.execTx(ctx, tx, updateQuery, updateArgs...)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected != 1 {
		return false, scannerrelease.ErrVersionConflict
	}
	if _, err := r.appendEventTx(ctx, tx, aggregateType, id, aggregateType+"."+to, current.State, to, command, now); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func pageLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	if limit > 200 {
		return 200
	}
	return limit
}

func encodeCursor(createdAt time.Time, id string) string {
	raw := createdAt.UTC().Format(time.RFC3339Nano) + "\x00" + id
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(cursor string) (time.Time, string, error) {
	if cursor == "" {
		return time.Time{}, "", nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("decode cursor: %w", err)
	}
	parts := strings.SplitN(string(raw), "\x00", 2)
	if len(parts) != 2 || parts[1] == "" {
		return time.Time{}, "", errors.New("invalid scanner release cursor")
	}
	at, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, "", fmt.Errorf("parse cursor timestamp: %w", err)
	}
	return at, parts[1], nil
}

func appendCursorCondition(query *strings.Builder, args *[]any, cursor string) error {
	at, id, err := decodeCursor(cursor)
	if err != nil {
		return err
	}
	if id != "" {
		query.WriteString(" AND (created_at < ? OR (created_at = ? AND id < ?))")
		*args = append(*args, at, at, id)
	}
	return nil
}

func pageCursor[T any](items []T, limit int, values func(T) (time.Time, string)) ([]T, string) {
	if len(items) <= limit {
		return items, ""
	}
	items = items[:limit]
	at, id := values(items[len(items)-1])
	return items, encodeCursor(at, id)
}

// --- Policies and registries -------------------------------------------------

func (r *scannerReleaseRepository) CreatePolicy(ctx context.Context, policy *scannerrelease.Policy) error {
	initializeIDAndTimes(&policy.ID, &policy.CreatedAt, &policy.UpdatedAt)
	if policy.Revision <= 0 {
		policy.Revision = 1
	}
	policy.ScheduleJSON = jsonDefault(policy.ScheduleJSON, "{}")
	policy.RulesJSON = jsonDefault(policy.RulesJSON, "{}")
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if policy.Enabled {
		if _, err := r.execTx(ctx, tx,
			`UPDATE scanner_update_policies SET enabled = ?, updated_at = ? WHERE scope = ? AND enabled = ?`,
			false, policy.CreatedAt, policy.Scope, true); err != nil {
			return err
		}
	}
	if err := r.namedExecTx(ctx, tx,
		`INSERT INTO scanner_update_policies
		 (id, scope, revision, enabled, schedule_json, rules_json, created_by, created_at, updated_at)
		 VALUES (:id, :scope, :revision, :enabled, :schedule_json, :rules_json, :created_by, :created_at, :updated_at)`,
		policy); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *scannerReleaseRepository) GetPolicy(ctx context.Context, id string) (*scannerrelease.Policy, error) {
	var policy scannerrelease.Policy
	if err := r.get(ctx, &policy, `SELECT * FROM scanner_update_policies WHERE id = ?`, id); err != nil {
		return nil, err
	}
	return &policy, nil
}

func (r *scannerReleaseRepository) ListPolicies(ctx context.Context, scope string, enabledOnly bool) ([]scannerrelease.Policy, error) {
	query := `SELECT * FROM scanner_update_policies WHERE 1 = 1`
	var args []any
	if scope != "" {
		query += ` AND scope = ?`
		args = append(args, scope)
	}
	if enabledOnly {
		query += ` AND enabled = ?`
		args = append(args, true)
	}
	query += ` ORDER BY scope ASC, revision DESC`
	var policies []scannerrelease.Policy
	return policies, r.selectRows(ctx, &policies, query, args...)
}

func (r *scannerReleaseRepository) CreateRegistryTarget(ctx context.Context, target *scannerrelease.RegistryTarget) error {
	initializeIDAndTimes(&target.ID, &target.CreatedAt, &target.UpdatedAt)
	if target.Version <= 0 {
		target.Version = 1
	}
	target.PlatformPolicyJSON = jsonDefault(target.PlatformPolicyJSON, "{}")
	if target.HealthStatus == "" {
		target.HealthStatus = "unknown"
	}
	if target.DigestParityStatus == "" {
		target.DigestParityStatus = "unknown"
	}
	target.HealthDetailJSON = jsonDefault(target.HealthDetailJSON, "{}")
	_, err := r.db.NamedExecContext(ctx, r.db.Rebind(
		`INSERT INTO scanner_registry_targets
		 (id, name, registry_type, host, namespace, secret_reference,
		  trust_policy_reference, platform_policy_json, enabled, version,
		  created_by, health_status, digest_parity_status, health_detail_json,
		  created_at, updated_at)
		 VALUES (:id, :name, :registry_type, :host, :namespace, :secret_reference,
		  :trust_policy_reference, :platform_policy_json, :enabled, :version,
		  :created_by, :health_status, :digest_parity_status, :health_detail_json,
		  :created_at, :updated_at)`), target)
	return err
}

func (r *scannerReleaseRepository) GetRegistryTarget(ctx context.Context, id string) (*scannerrelease.RegistryTarget, error) {
	var target scannerrelease.RegistryTarget
	if err := r.get(ctx, &target, `SELECT * FROM scanner_registry_targets WHERE id = ?`, id); err != nil {
		return nil, err
	}
	return &target, nil
}

func (r *scannerReleaseRepository) ListRegistryTargets(ctx context.Context, enabledOnly bool) ([]scannerrelease.RegistryTarget, error) {
	query := `SELECT * FROM scanner_registry_targets`
	var args []any
	if enabledOnly {
		query += ` WHERE enabled = ?`
		args = append(args, true)
	}
	query += ` ORDER BY name ASC, id ASC`
	var targets []scannerrelease.RegistryTarget
	return targets, r.selectRows(ctx, &targets, query, args...)
}

func (r *scannerReleaseRepository) UpdateRegistryTarget(ctx context.Context, target *scannerrelease.RegistryTarget, expectedVersion int64) error {
	target.UpdatedAt = utcNow()
	target.PlatformPolicyJSON = jsonDefault(target.PlatformPolicyJSON, "{}")
	result, err := r.db.ExecContext(ctx, r.db.Rebind(
		`UPDATE scanner_registry_targets
		 SET name = ?, registry_type = ?, host = ?, namespace = ?,
		     secret_reference = ?, trust_policy_reference = ?,
		     platform_policy_json = ?, enabled = ?, version = version + 1, updated_at = ?
		 WHERE id = ? AND version = ?`),
		target.Name, target.Type, target.Host, target.Namespace,
		target.SecretReference, target.TrustPolicyRef, target.PlatformPolicyJSON,
		target.Enabled, target.UpdatedAt, target.ID, expectedVersion)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return scannerrelease.ErrVersionConflict
	}
	target.Version = expectedVersion + 1
	return nil
}

func (r *scannerReleaseRepository) UpdateRegistryObservation(
	ctx context.Context,
	id string,
	observation scannerrelease.RegistryObservation,
) error {
	if strings.TrimSpace(id) == "" || observation.CheckedAt.IsZero() {
		return errors.New("registry observation ID and checked time are required")
	}
	if observation.HealthStatus == "" {
		observation.HealthStatus = "unknown"
	}
	if observation.DigestParityStatus == "" {
		observation.DigestParityStatus = "unknown"
	}
	observation.DetailJSON = jsonDefault(observation.DetailJSON, "{}")
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var current scannerrelease.RegistryTarget
	if err := tx.GetContext(ctx, &current, r.db.Rebind(
		`SELECT * FROM scanner_registry_targets WHERE id = ?`), id); err != nil {
		return err
	}
	result, err := r.execTx(ctx, tx,
		`UPDATE scanner_registry_targets
		 SET health_status = ?, last_checked_at = ?, last_error = ?, latency_ms = ?,
		     digest_parity_status = ?, mirror_lag_seconds = ?,
		     health_detail_json = ?, updated_at = ?
		 WHERE id = ?`,
		observation.HealthStatus, observation.CheckedAt, observation.Error, observation.LatencyMS,
		observation.DigestParityStatus, observation.MirrorLagSeconds,
		observation.DetailJSON, observation.CheckedAt, id)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return sql.ErrNoRows
	}
	if current.Type == scannerrelease.RegistryMirror &&
		current.DigestParityStatus != "mismatched" &&
		observation.DigestParityStatus == "mismatched" {
		var policyRevision int64
		queryErr := tx.GetContext(ctx, &policyRevision, r.db.Rebind(
			`SELECT revision FROM scanner_update_policies
			 WHERE scope = ? AND enabled = ? ORDER BY revision DESC LIMIT 1`),
			"global", true)
		if queryErr != nil && !errors.Is(queryErr, sql.ErrNoRows) {
			return queryErr
		}
		if _, err := r.appendEventTx(
			ctx, tx, "registry", id, "registry.mirror_drift",
			current.DigestParityStatus, observation.DigestParityStatus,
			scannerrelease.TransitionCommand{
				Actor:          "registry-reconciler",
				Reason:         "mirror digest parity changed to mismatched",
				PolicyRevision: policyRevision,
				IdempotencyKey: fmt.Sprintf(
					"registry-mirror-drift:%s:%d", id,
					observation.CheckedAt.UTC().UnixNano(),
				),
				PayloadJSON: `{"digest_parity_status":"mismatched"}`,
			},
			observation.CheckedAt.UTC(),
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *scannerReleaseRepository) CreateSignerProfile(
	ctx context.Context,
	profile *scannerrelease.SignerProfile,
) error {
	initializeIDAndTimes(&profile.ID, &profile.CreatedAt, &profile.UpdatedAt)
	if profile.State == "" {
		profile.State = scannerrelease.SignerActive
	}
	if profile.Revision <= 0 {
		profile.Revision = 1
	}
	if err := scannersigning.ValidateProfile(*profile); err != nil {
		return err
	}
	_, err := r.db.NamedExecContext(ctx, r.db.Rebind(
		`INSERT INTO scanner_signer_profiles
		 (id, name, provider, algorithm, key_reference, secret_reference,
		  workload_identity, identity, issuer, subject, trust_root_reference,
		  state, revision, rotated_from_id, revocation_reason, revoked_by,
		  revoked_at, created_by, created_at, updated_at)
		 VALUES
		 (:id, :name, :provider, :algorithm, :key_reference, :secret_reference,
		  :workload_identity, :identity, :issuer, :subject, :trust_root_reference,
		  :state, :revision, :rotated_from_id, :revocation_reason, :revoked_by,
		  :revoked_at, :created_by, :created_at, :updated_at)`),
		profile,
	)
	return err
}

func (r *scannerReleaseRepository) GetSignerProfile(
	ctx context.Context,
	id string,
) (*scannerrelease.SignerProfile, error) {
	var profile scannerrelease.SignerProfile
	if err := r.get(
		ctx, &profile, `SELECT * FROM scanner_signer_profiles WHERE id = ?`, id,
	); err != nil {
		return nil, err
	}
	return &profile, nil
}

func (r *scannerReleaseRepository) ListSignerProfiles(
	ctx context.Context,
	activeOnly bool,
) ([]scannerrelease.SignerProfile, error) {
	query := `SELECT * FROM scanner_signer_profiles`
	var args []any
	if activeOnly {
		query += ` WHERE state = ?`
		args = append(args, scannerrelease.SignerActive)
	}
	query += ` ORDER BY name ASC, revision DESC, id ASC`
	var profiles []scannerrelease.SignerProfile
	return profiles, r.selectRows(ctx, &profiles, query, args...)
}

func (r *scannerReleaseRepository) RotateSignerProfile(
	ctx context.Context,
	id string,
	expectedVersion int64,
	replacement *scannerrelease.SignerProfile,
) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current scannerrelease.SignerProfile
	if err := tx.GetContext(
		ctx, &current, r.db.Rebind(
			`SELECT * FROM scanner_signer_profiles WHERE id = ?`,
		), id,
	); err != nil {
		return err
	}
	if current.Revision != expectedVersion ||
		current.State != scannerrelease.SignerActive {
		return scannerrelease.ErrVersionConflict
	}
	initializeIDAndTimes(
		&replacement.ID, &replacement.CreatedAt, &replacement.UpdatedAt,
	)
	replacement.Name = current.Name
	replacement.Revision = current.Revision + 1
	replacement.RotatedFromID = current.ID
	replacement.State = scannerrelease.SignerActive
	if err := scannersigning.ValidateProfile(*replacement); err != nil {
		return err
	}
	if _, err := tx.NamedExecContext(ctx, r.db.Rebind(
		`INSERT INTO scanner_signer_profiles
		 (id, name, provider, algorithm, key_reference, secret_reference,
		  workload_identity, identity, issuer, subject, trust_root_reference,
		  state, revision, rotated_from_id, revocation_reason, revoked_by,
		  revoked_at, created_by, created_at, updated_at)
		 VALUES
		 (:id, :name, :provider, :algorithm, :key_reference, :secret_reference,
		  :workload_identity, :identity, :issuer, :subject, :trust_root_reference,
		  :state, :revision, :rotated_from_id, :revocation_reason, :revoked_by,
		  :revoked_at, :created_by, :created_at, :updated_at)`),
		replacement,
	); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, r.db.Rebind(
		`UPDATE scanner_signer_profiles
		 SET state = ?, updated_at = ?
		 WHERE id = ? AND revision = ? AND state = ?`,
	), scannerrelease.SignerDisabled, replacement.CreatedAt,
		current.ID, expectedVersion, scannerrelease.SignerActive)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return scannerrelease.ErrVersionConflict
	}
	return tx.Commit()
}

func (r *scannerReleaseRepository) RevokeSignerProfile(
	ctx context.Context,
	id string,
	expectedVersion int64,
	reason, actor string,
	at time.Time,
) error {
	if strings.TrimSpace(reason) == "" || strings.TrimSpace(actor) == "" ||
		at.IsZero() {
		return errors.New("signer revocation reason, actor, and time are required")
	}
	result, err := r.db.ExecContext(ctx, r.db.Rebind(
		`UPDATE scanner_signer_profiles
		 SET state = ?, revocation_reason = ?, revoked_by = ?, revoked_at = ?,
		     updated_at = ?
		 WHERE id = ? AND revision = ? AND state <> ?`,
	), scannerrelease.SignerRevoked, reason, actor, at, at, id,
		expectedVersion, scannerrelease.SignerRevoked)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return scannerrelease.ErrVersionConflict
	}
	return nil
}

// --- Discovery ---------------------------------------------------------------

func (r *scannerReleaseRepository) CreateDiscoveryRun(ctx context.Context, run *scannerrelease.DiscoveryRun, command scannerrelease.TransitionCommand) error {
	initializeIDAndTimes(&run.ID, &run.CreatedAt, &run.UpdatedAt)
	if run.State == "" {
		run.State = scannerrelease.DiscoveryQueued
	}
	run.ScopeJSON = jsonDefault(run.ScopeJSON, `{"mode":"complete"}`)
	if run.MaxAttempts <= 0 {
		run.MaxAttempts = 3
	}
	if run.Version <= 0 {
		run.Version = 1
	}
	if run.IdempotencyKey == "" {
		run.IdempotencyKey = command.IdempotencyKey
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := r.namedExecResultTx(ctx, tx,
		`INSERT INTO scanner_discovery_runs
		 (id, trigger, schedule_period, definition_commit, definition_digest, lock_digest,
		  policy_id, policy_revision, scope_json, state, coverage, total_count,
		  covered_count, current_count, available_count, unreachable_count,
		  unsupported_count, held_count, yanked_count, unknown_count, selected_count,
		  worker_id, lease_token, lease_expires_at, heartbeat_at, cancel_requested_at,
		  attempt, max_attempts, error_class, error_detail, actor, idempotency_key,
		  version, started_at, completed_at, created_at, updated_at)
		 VALUES
		 (:id, :trigger, :schedule_period, :definition_commit, :definition_digest, :lock_digest,
		  :policy_id, :policy_revision, :scope_json, :state, :coverage, :total_count,
		  :covered_count, :current_count, :available_count, :unreachable_count,
		  :unsupported_count, :held_count, :yanked_count, :unknown_count, :selected_count,
		  :worker_id, :lease_token, :lease_expires_at, :heartbeat_at, :cancel_requested_at,
		  :attempt, :max_attempts, :error_class, :error_detail, :actor, :idempotency_key,
		  :version, :started_at, :completed_at, :created_at, :updated_at)
		 ON CONFLICT(idempotency_key) DO NOTHING`, run)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		var existing scannerrelease.DiscoveryRun
		if err := tx.GetContext(ctx, &existing, r.db.Rebind(
			`SELECT * FROM scanner_discovery_runs WHERE idempotency_key = ?`),
			run.IdempotencyKey); err != nil {
			return err
		}
		if existing.Trigger != run.Trigger ||
			existing.DefinitionCommit != run.DefinitionCommit ||
			existing.PolicyID != run.PolicyID ||
			existing.PolicyRevision != run.PolicyRevision ||
			existing.ScopeJSON != run.ScopeJSON {
			return scannerrelease.ErrIdempotencyConflict
		}
		*run = existing
		return tx.Commit()
	}
	if _, err := r.appendEventTx(ctx, tx, "discovery", run.ID, "discovery.created", "", string(run.State), command, run.CreatedAt); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *scannerReleaseRepository) GetDiscoveryRun(ctx context.Context, id string) (*scannerrelease.DiscoveryRun, error) {
	var run scannerrelease.DiscoveryRun
	if err := r.get(ctx, &run, `SELECT * FROM scanner_discovery_runs WHERE id = ?`, id); err != nil {
		return nil, err
	}
	return &run, nil
}

func (r *scannerReleaseRepository) GetLatestCompletedDiscovery(
	ctx context.Context,
	definitionCommit, policyID string,
	policyRevision int64,
	scopeJSON string,
) (*scannerrelease.DiscoveryRun, error) {
	var run scannerrelease.DiscoveryRun
	err := r.get(ctx, &run,
		`SELECT * FROM scanner_discovery_runs
		 WHERE state = ? AND definition_commit = ? AND policy_id = ?
		   AND policy_revision = ? AND scope_json = ?
		   AND COALESCE(error_class, '') = ''
		   AND unreachable_count = 0 AND unsupported_count = 0 AND unknown_count = 0
		   AND (total_count = 0 OR (coverage >= 1 AND covered_count = total_count))
		 ORDER BY completed_at DESC, created_at DESC, id DESC
		 LIMIT 1`,
		scannerrelease.DiscoveryCompleted, definitionCommit, policyID,
		policyRevision, jsonDefault(scopeJSON, `{"mode":"complete"}`))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &run, nil
}

func (r *scannerReleaseRepository) ListDiscoveryRuns(ctx context.Context, filter scannerrelease.DiscoveryFilter, page scannerrelease.PageRequest) (scannerrelease.DiscoveryPage, error) {
	var query strings.Builder
	query.WriteString(`SELECT * FROM scanner_discovery_runs WHERE 1 = 1`)
	var args []any
	if filter.State != "" {
		query.WriteString(` AND state = ?`)
		args = append(args, filter.State)
	}
	if filter.Trigger != "" {
		query.WriteString(` AND trigger = ?`)
		args = append(args, filter.Trigger)
	}
	if err := appendCursorCondition(&query, &args, page.Cursor); err != nil {
		return scannerrelease.DiscoveryPage{}, err
	}
	limit := pageLimit(page.Limit)
	query.WriteString(` ORDER BY created_at DESC, id DESC LIMIT ?`)
	args = append(args, limit+1)
	var items []scannerrelease.DiscoveryRun
	if err := r.selectRows(ctx, &items, query.String(), args...); err != nil {
		return scannerrelease.DiscoveryPage{}, err
	}
	items, next := pageCursor(items, limit, func(item scannerrelease.DiscoveryRun) (time.Time, string) {
		return item.CreatedAt, item.ID
	})
	return scannerrelease.DiscoveryPage{Items: items, NextCursor: next}, nil
}

func (r *scannerReleaseRepository) ClaimNextDiscoveryRun(
	ctx context.Context,
	workerID string,
	leaseUntil time.Time,
) (*scannerrelease.DiscoveryRun, error) {
	now := utcNow()
	if strings.TrimSpace(workerID) == "" || !leaseUntil.After(now) {
		return nil, errors.New("invalid scanner discovery claim request")
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var queued []scannerrelease.DiscoveryRun
	if err := tx.SelectContext(ctx, &queued, r.db.Rebind(
		`SELECT * FROM scanner_discovery_runs
		 WHERE state = ? AND cancel_requested_at IS NULL
		 ORDER BY created_at ASC, id ASC`), scannerrelease.DiscoveryQueued); err != nil {
		return nil, err
	}
	for i := range queued {
		token := uuid.NewString()
		result, err := r.execTx(ctx, tx,
			`UPDATE scanner_discovery_runs
			 SET state = ?, worker_id = ?, lease_token = ?, lease_expires_at = ?,
			     heartbeat_at = ?, started_at = COALESCE(started_at, ?),
			     attempt = attempt + 1, error_class = '', error_detail = '',
			     version = version + 1, updated_at = ?
			 WHERE id = ? AND state = ? AND version = ? AND cancel_requested_at IS NULL`,
			scannerrelease.DiscoveryResolving, workerID, token, leaseUntil,
			now, now, now, queued[i].ID, scannerrelease.DiscoveryQueued, queued[i].Version)
		if err != nil {
			return nil, err
		}
		affected, _ := result.RowsAffected()
		if affected != 1 {
			continue
		}
		command := scannerrelease.TransitionCommand{
			Actor:          workerID,
			Reason:         "discovery worker claimed queued run",
			IdempotencyKey: "claim:" + token,
			PayloadJSON:    `{"lease":"acquired"}`,
		}
		if _, err := r.appendEventTx(
			ctx, tx, "discovery", queued[i].ID, "discovery.claimed",
			string(scannerrelease.DiscoveryQueued), string(scannerrelease.DiscoveryResolving),
			command, now,
		); err != nil {
			return nil, err
		}
		var claimed scannerrelease.DiscoveryRun
		if err := tx.GetContext(ctx, &claimed, r.db.Rebind(
			`SELECT * FROM scanner_discovery_runs WHERE id = ?`), queued[i].ID); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return &claimed, nil
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return nil, nil
}

func (r *scannerReleaseRepository) HeartbeatDiscoveryRun(
	ctx context.Context,
	id, workerID, leaseToken string,
	leaseUntil time.Time,
) (scannerrelease.DiscoveryLeaseStatus, error) {
	now := utcNow()
	if id == "" || workerID == "" || leaseToken == "" || !leaseUntil.After(now) {
		return scannerrelease.DiscoveryLeaseStatus{}, errors.New("invalid scanner discovery heartbeat")
	}
	var status scannerrelease.DiscoveryLeaseStatus
	err := r.get(ctx, &status,
		`UPDATE scanner_discovery_runs
		 SET heartbeat_at = ?, lease_expires_at = ?, updated_at = ?
		 WHERE id = ? AND worker_id = ? AND lease_token = ?
		   AND state IN (?, ?, ?) AND lease_expires_at > ?
		 RETURNING 1 AS current,
		   CASE WHEN cancel_requested_at IS NULL THEN 0 ELSE 1 END AS cancel_requested,
		   version`,
		now, leaseUntil, now, id, workerID, leaseToken,
		scannerrelease.DiscoveryResolving, scannerrelease.DiscoveryComparing,
		scannerrelease.DiscoveryProposing, now)
	if errors.Is(err, sql.ErrNoRows) {
		return scannerrelease.DiscoveryLeaseStatus{}, nil
	}
	if err != nil {
		return scannerrelease.DiscoveryLeaseStatus{}, err
	}
	return status, nil
}

func (r *scannerReleaseRepository) RequestDiscoveryCancellation(
	ctx context.Context,
	id string,
	command scannerrelease.TransitionCommand,
	at time.Time,
) (bool, error) {
	if at.IsZero() {
		at = utcNow()
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	var current struct {
		State             scannerrelease.DiscoveryState `db:"state"`
		Version           int64                         `db:"version"`
		CancelRequestedAt *time.Time                    `db:"cancel_requested_at"`
	}
	if err := tx.GetContext(ctx, &current, r.db.Rebind(
		`SELECT state, version, cancel_requested_at
		 FROM scanner_discovery_runs WHERE id = ?`), id); err != nil {
		return false, err
	}
	if command.IdempotencyKey != "" {
		var existing scannerrelease.Event
		err := tx.GetContext(ctx, &existing, r.db.Rebind(
			`SELECT * FROM scanner_release_events
			 WHERE aggregate_type = ? AND aggregate_id = ? AND idempotency_key = ?`),
			"discovery", id, command.IdempotencyKey)
		if err == nil {
			if existing.EventType != "discovery.cancellation_requested" &&
				existing.EventType != "discovery.cancelled" {
				return false, scannerrelease.ErrIdempotencyConflict
			}
			return false, tx.Commit()
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return false, err
		}
	}
	if scannerrelease.IsTerminalDiscoveryState(current.State) ||
		current.CancelRequestedAt != nil {
		return false, tx.Commit()
	}
	next := current.State
	eventType := "discovery.cancellation_requested"
	var completedAt any
	if current.State == scannerrelease.DiscoveryQueued {
		next = scannerrelease.DiscoveryCancelled
		eventType = "discovery.cancelled"
		completedAt = at
	}
	result, err := r.execTx(ctx, tx,
		`UPDATE scanner_discovery_runs
		 SET state = ?, cancel_requested_at = COALESCE(cancel_requested_at, ?),
		     completed_at = COALESCE(completed_at, ?),
		     version = version + 1, updated_at = ?
		 WHERE id = ? AND version = ?`,
		next, at, completedAt, at, id, current.Version)
	if err != nil {
		return false, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return false, scannerrelease.ErrVersionConflict
	}
	if _, err := r.appendEventTx(
		ctx, tx, "discovery", id, eventType,
		string(current.State), string(next), command, at,
	); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (r *scannerReleaseRepository) ReclaimStaleDiscoveryRuns(ctx context.Context, now time.Time) (int, error) {
	if now.IsZero() {
		now = utcNow()
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	var stale []scannerrelease.DiscoveryRun
	if err := tx.SelectContext(ctx, &stale, r.db.Rebind(
		`SELECT * FROM scanner_discovery_runs
		 WHERE state IN (?, ?, ?) AND lease_expires_at IS NOT NULL
		   AND lease_expires_at <= ?
		 ORDER BY lease_expires_at ASC, id ASC`),
		scannerrelease.DiscoveryResolving, scannerrelease.DiscoveryComparing,
		scannerrelease.DiscoveryProposing, now); err != nil {
		return 0, err
	}
	reclaimed := 0
	for i := range stale {
		next := scannerrelease.DiscoveryQueued
		eventType := "discovery.requeued_after_worker_loss"
		errorClass := "worker_lost"
		errorDetail := "discovery worker lease expired; run requeued"
		var completedAt any
		if stale[i].CancelRequestedAt != nil {
			next = scannerrelease.DiscoveryCancelled
			eventType = "discovery.cancelled_after_worker_loss"
			errorClass = "cancelled"
			errorDetail = "discovery cancellation completed after worker lease expired"
			completedAt = now
		} else if stale[i].Attempt >= stale[i].MaxAttempts {
			next = scannerrelease.DiscoveryFailed
			eventType = "discovery.failed_after_worker_loss"
			errorDetail = "discovery worker lease expired and retry budget was exhausted"
			completedAt = now
		}
		result, err := r.execTx(ctx, tx,
			`UPDATE scanner_discovery_runs
			 SET state = ?, worker_id = '', lease_token = '', lease_expires_at = NULL,
			     heartbeat_at = NULL, error_class = ?, error_detail = ?,
			     completed_at = ?, version = version + 1, updated_at = ?
			 WHERE id = ? AND version = ? AND lease_token = ?`,
			next, errorClass, errorDetail, completedAt, now,
			stale[i].ID, stale[i].Version, stale[i].LeaseToken)
		if err != nil {
			return reclaimed, err
		}
		affected, _ := result.RowsAffected()
		if affected != 1 {
			continue
		}
		command := scannerrelease.TransitionCommand{
			Actor:          "scheduler",
			Reason:         "discovery worker lease expired",
			IdempotencyKey: "reclaim:" + stale[i].LeaseToken,
			PayloadJSON:    `{"errorClass":"worker_lost"}`,
		}
		if _, err := r.appendEventTx(
			ctx, tx, "discovery", stale[i].ID, eventType,
			string(stale[i].State), string(next), command, now,
		); err != nil {
			return reclaimed, err
		}
		reclaimed++
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return reclaimed, nil
}

func prepareUpdateItem(item *scannerrelease.UpdateItem, runID string) {
	item.DiscoveryRunID = runID
	initializeIDAndTimes(&item.ID, &item.CreatedAt, &item.UpdatedAt)
	item.SourceEvidenceJSON = jsonDefault(item.SourceEvidenceJSON, "{}")
	item.CompatibilityJSON = jsonDefault(item.CompatibilityJSON, "{}")
	if item.RiskClass == "" {
		item.RiskClass = scannerrelease.RiskNone
	}
	if item.Status == "" {
		item.Status = "unknown"
	}
	if item.SelectionState == "" {
		item.SelectionState = "unselected"
	}
}

func (r *scannerReleaseRepository) addUpdateItemsTx(
	ctx context.Context,
	tx *sqlx.Tx,
	runID string,
	items []scannerrelease.UpdateItem,
) error {
	for i := range items {
		prepareUpdateItem(&items[i], runID)
		if err := r.namedExecTx(ctx, tx,
			`INSERT INTO scanner_update_items
			 (id, discovery_run_id, component_type, component_name, current_value,
			  available_value, available_digest, status, source_evidence_json,
			  risk_class, compatibility_json, selection_state, error_class,
			  error_detail, resolver, attempts, retry_at, checked_at, created_at, updated_at)
			 VALUES
			 (:id, :discovery_run_id, :component_type, :component_name, :current_value,
			  :available_value, :available_digest, :status, :source_evidence_json,
			  :risk_class, :compatibility_json, :selection_state, :error_class,
			  :error_detail, :resolver, :attempts, :retry_at, :checked_at, :created_at, :updated_at)
			 ON CONFLICT(discovery_run_id, component_type, component_name) DO UPDATE SET
			  current_value = excluded.current_value,
			  available_value = excluded.available_value,
			  available_digest = excluded.available_digest,
			  status = excluded.status,
			  source_evidence_json = excluded.source_evidence_json,
			  risk_class = excluded.risk_class,
			  compatibility_json = excluded.compatibility_json,
			  selection_state = excluded.selection_state,
			  error_class = excluded.error_class,
			  error_detail = excluded.error_detail,
			  resolver = excluded.resolver,
			  attempts = excluded.attempts,
			  retry_at = excluded.retry_at,
			  checked_at = excluded.checked_at,
			  updated_at = excluded.updated_at`, &items[i]); err != nil {
			return err
		}
	}
	return nil
}

func (r *scannerReleaseRepository) FinalizeDiscoveryRun(
	ctx context.Context,
	run *scannerrelease.DiscoveryRun,
	expectedVersion int64,
	leaseToken string,
	items []scannerrelease.UpdateItem,
	command scannerrelease.TransitionCommand,
) (*scannerrelease.DiscoveryRun, error) {
	if run == nil || run.ID == "" || leaseToken == "" {
		return nil, errors.New("invalid scanner discovery finalization")
	}
	switch run.State {
	case scannerrelease.DiscoveryCompleted, scannerrelease.DiscoveryFailed, scannerrelease.DiscoveryCancelled:
	default:
		return nil, fmt.Errorf("invalid scanner discovery terminal state %q", run.State)
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var current scannerrelease.DiscoveryRun
	if err := tx.GetContext(ctx, &current, r.db.Rebind(
		`SELECT * FROM scanner_discovery_runs WHERE id = ?`), run.ID); err != nil {
		return nil, err
	}
	now := utcNow()
	if current.Version != expectedVersion {
		return nil, scannerrelease.ErrVersionConflict
	}
	if current.LeaseToken != leaseToken || current.WorkerID != run.WorkerID ||
		current.LeaseExpiresAt == nil || !current.LeaseExpiresAt.After(now) {
		return nil, scannerrelease.ErrLeaseNotOwned
	}
	switch current.State {
	case scannerrelease.DiscoveryResolving, scannerrelease.DiscoveryComparing, scannerrelease.DiscoveryProposing:
	default:
		return nil, scannerrelease.ErrLeaseNotOwned
	}
	if current.CancelRequestedAt != nil {
		run.State = scannerrelease.DiscoveryCancelled
		run.ErrorClass = "cancelled"
		if run.ErrorDetail == "" {
			run.ErrorDetail = "discovery cancelled by operator request"
		}
	}
	run.ScopeJSON = jsonDefault(run.ScopeJSON, current.ScopeJSON)
	if run.CompletedAt == nil {
		completedAt := now
		run.CompletedAt = &completedAt
	}
	result, err := r.execTx(ctx, tx,
		`UPDATE scanner_discovery_runs
		 SET definition_digest = ?, lock_digest = ?, scope_json = ?, state = ?,
		     coverage = ?, total_count = ?, covered_count = ?, current_count = ?,
		     available_count = ?, unreachable_count = ?, unsupported_count = ?,
		     held_count = ?, yanked_count = ?, unknown_count = ?, selected_count = ?,
		     worker_id = '', lease_token = '', lease_expires_at = NULL,
		     heartbeat_at = NULL, error_class = ?, error_detail = ?,
		     completed_at = ?, version = version + 1, updated_at = ?
		 WHERE id = ? AND version = ? AND lease_token = ?`,
		run.DefinitionDigest, run.LockDigest, run.ScopeJSON, run.State,
		run.Coverage, run.TotalCount, run.CoveredCount, run.CurrentCount,
		run.AvailableCount, run.UnreachableCount, run.UnsupportedCount,
		run.HeldCount, run.YankedCount, run.UnknownCount, run.SelectedCount,
		run.ErrorClass, run.ErrorDetail, run.CompletedAt, now,
		run.ID, expectedVersion, leaseToken)
	if err != nil {
		return nil, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return nil, scannerrelease.ErrLeaseNotOwned
	}
	if err := r.addUpdateItemsTx(ctx, tx, run.ID, items); err != nil {
		return nil, err
	}
	eventType := "discovery." + string(run.State)
	if _, err := r.appendEventTx(
		ctx, tx, "discovery", run.ID, eventType,
		string(current.State), string(run.State), command, now,
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return r.GetDiscoveryRun(ctx, run.ID)
}

func (r *scannerReleaseRepository) AddUpdateItems(ctx context.Context, runID string, items []scannerrelease.UpdateItem) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := r.addUpdateItemsTx(ctx, tx, runID, items); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *scannerReleaseRepository) ListUpdateItems(ctx context.Context, runID string) ([]scannerrelease.UpdateItem, error) {
	var items []scannerrelease.UpdateItem
	return items, r.selectRows(ctx, &items,
		`SELECT * FROM scanner_update_items
		 WHERE discovery_run_id = ?
		 ORDER BY risk_class DESC, component_type ASC, component_name ASC`, runID)
}

func (r *scannerReleaseRepository) UpdateDiscoverySummary(
	ctx context.Context,
	run *scannerrelease.DiscoveryRun,
	expectedVersion int64,
	command scannerrelease.TransitionCommand,
) (*scannerrelease.DiscoveryRun, error) {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var current struct {
		State   string `db:"state"`
		Version int64  `db:"version"`
	}
	if err := tx.GetContext(ctx, &current, r.db.Rebind(
		`SELECT state, version FROM scanner_discovery_runs WHERE id = ?`), run.ID); err != nil {
		return nil, err
	}
	if current.Version != expectedVersion {
		return nil, scannerrelease.ErrVersionConflict
	}
	now := utcNow()
	result, err := r.execTx(ctx, tx,
		`UPDATE scanner_discovery_runs
		 SET definition_digest = ?, lock_digest = ?, scope_json = ?, coverage = ?,
		     total_count = ?, covered_count = ?, current_count = ?, available_count = ?,
		     unreachable_count = ?, unsupported_count = ?, held_count = ?,
		     yanked_count = ?, unknown_count = ?, selected_count = ?,
		     error_class = ?, error_detail = ?,
		     version = version + 1, updated_at = ?
		 WHERE id = ? AND version = ?`,
		run.DefinitionDigest, run.LockDigest, jsonDefault(run.ScopeJSON, `{"mode":"complete"}`),
		run.Coverage, run.TotalCount, run.CoveredCount, run.CurrentCount,
		run.AvailableCount, run.UnreachableCount, run.UnsupportedCount,
		run.HeldCount, run.YankedCount, run.UnknownCount, run.SelectedCount,
		run.ErrorClass, run.ErrorDetail,
		now, run.ID, expectedVersion)
	if err != nil {
		return nil, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return nil, scannerrelease.ErrVersionConflict
	}
	if _, err := r.appendEventTx(
		ctx, tx, "discovery", run.ID, "discovery.summary_updated",
		current.State, current.State, command, now,
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return r.GetDiscoveryRun(ctx, run.ID)
}

func (r *scannerReleaseRepository) TransitionDiscovery(ctx context.Context, id string, expectedVersion int64, to scannerrelease.DiscoveryState, command scannerrelease.TransitionCommand) (*scannerrelease.DiscoveryRun, error) {
	_, err := r.transitionState(ctx, "scanner_discovery_runs", "discovery", id, expectedVersion, string(to), command,
		func(from, to string) error {
			return scannerrelease.ValidateDiscoveryTransition(scannerrelease.DiscoveryState(from), scannerrelease.DiscoveryState(to))
		})
	if err != nil {
		return nil, err
	}
	return r.GetDiscoveryRun(ctx, id)
}

// --- Candidates --------------------------------------------------------------

func (r *scannerReleaseRepository) CreateCandidate(ctx context.Context, candidate *scannerrelease.Candidate, command scannerrelease.TransitionCommand) error {
	initializeIDAndTimes(&candidate.ID, &candidate.CreatedAt, &candidate.UpdatedAt)
	if candidate.State == "" {
		candidate.State = scannerrelease.CandidateDraft
	}
	if candidate.Version <= 0 {
		candidate.Version = 1
	}
	if candidate.IdempotencyKey == "" {
		candidate.IdempotencyKey = command.IdempotencyKey
	}
	candidate.RiskSummaryJSON = jsonDefault(candidate.RiskSummaryJSON, "{}")
	candidate.RequiredGatesJSON = jsonDefault(candidate.RequiredGatesJSON, "[]")
	candidate.SelectionJSON = jsonDefault(candidate.SelectionJSON, "{}")
	if candidate.ProposalMaxAttempts <= 0 {
		candidate.ProposalMaxAttempts = 3
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := r.execTx(ctx, tx,
		`INSERT INTO scanner_release_candidates
		 (id, discovery_run_id, selection_json, definition_commit, proposed_commit, proposal_url,
		  lock_digest, lock_uri, risk_summary_json, state, required_gates_json,
		  policy_decision, policy_id, policy_revision, proposal_worker_id,
		  proposal_lease_token, proposal_lease_expires_at, proposal_heartbeat_at,
		  proposal_attempt, proposal_max_attempts, proposal_error_class,
		  proposal_error_detail, proposal_started_at, proposal_completed_at,
		  actor, idempotency_key,
		  error_class, error_detail, version, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
		         ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(idempotency_key) DO NOTHING`,
		candidate.ID, nullableString(candidate.DiscoveryRunID), candidate.SelectionJSON, candidate.DefinitionCommit,
		candidate.ProposedCommit, candidate.ProposalURL, candidate.LockDigest, candidate.LockURI,
		candidate.RiskSummaryJSON, candidate.State, candidate.RequiredGatesJSON,
		candidate.PolicyDecision, candidate.PolicyID, candidate.PolicyRevision,
		candidate.ProposalWorkerID, candidate.ProposalLeaseToken,
		candidate.ProposalLeaseExpiresAt, candidate.ProposalHeartbeatAt,
		candidate.ProposalAttempt, candidate.ProposalMaxAttempts,
		candidate.ProposalErrorClass, candidate.ProposalErrorDetail,
		candidate.ProposalStartedAt, candidate.ProposalCompletedAt, candidate.Actor,
		candidate.IdempotencyKey, candidate.ErrorClass, candidate.ErrorDetail, candidate.Version,
		candidate.CreatedAt, candidate.UpdatedAt)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		var existing scannerrelease.Candidate
		if err := tx.GetContext(ctx, &existing, r.db.Rebind(
			`SELECT `+candidateColumns+` FROM scanner_release_candidates WHERE idempotency_key = ?`),
			candidate.IdempotencyKey); err != nil {
			return err
		}
		if existing.DefinitionCommit != candidate.DefinitionCommit ||
			existing.PolicyID != candidate.PolicyID ||
			existing.PolicyRevision != candidate.PolicyRevision ||
			existing.DiscoveryRunID != candidate.DiscoveryRunID ||
			existing.SelectionJSON != candidate.SelectionJSON {
			return scannerrelease.ErrIdempotencyConflict
		}
		*candidate = existing
		return tx.Commit()
	}
	if _, err := r.appendEventTx(ctx, tx, "candidate", candidate.ID, "candidate.created", "", string(candidate.State), command, candidate.CreatedAt); err != nil {
		return err
	}
	return tx.Commit()
}

const candidateColumns = `id, COALESCE(discovery_run_id, '') AS discovery_run_id,
	selection_json, definition_commit, proposed_commit, proposal_url, lock_digest, lock_uri,
	risk_summary_json, state, required_gates_json, policy_decision, policy_id,
	policy_revision, proposal_worker_id, proposal_lease_token,
	proposal_lease_expires_at, proposal_heartbeat_at, proposal_attempt,
	proposal_max_attempts, proposal_error_class, proposal_error_detail,
	proposal_started_at, proposal_completed_at, actor, idempotency_key,
	error_class, error_detail, version,
	created_at, updated_at`

func (r *scannerReleaseRepository) GetCandidate(ctx context.Context, id string) (*scannerrelease.Candidate, error) {
	var candidate scannerrelease.Candidate
	if err := r.get(ctx, &candidate, `SELECT `+candidateColumns+` FROM scanner_release_candidates WHERE id = ?`, id); err != nil {
		return nil, err
	}
	return &candidate, nil
}

func (r *scannerReleaseRepository) ListCandidates(ctx context.Context, filter scannerrelease.CandidateFilter, page scannerrelease.PageRequest) (scannerrelease.CandidatePage, error) {
	var query strings.Builder
	query.WriteString(`SELECT ` + candidateColumns + ` FROM scanner_release_candidates WHERE 1 = 1`)
	var args []any
	if filter.State != "" {
		query.WriteString(` AND state = ?`)
		args = append(args, filter.State)
	}
	if err := appendCursorCondition(&query, &args, page.Cursor); err != nil {
		return scannerrelease.CandidatePage{}, err
	}
	limit := pageLimit(page.Limit)
	query.WriteString(` ORDER BY created_at DESC, id DESC LIMIT ?`)
	args = append(args, limit+1)
	var items []scannerrelease.Candidate
	if err := r.selectRows(ctx, &items, query.String(), args...); err != nil {
		return scannerrelease.CandidatePage{}, err
	}
	items, next := pageCursor(items, limit, func(item scannerrelease.Candidate) (time.Time, string) {
		return item.CreatedAt, item.ID
	})
	return scannerrelease.CandidatePage{Items: items, NextCursor: next}, nil
}

func (r *scannerReleaseRepository) ClaimNextCandidateProposal(
	ctx context.Context,
	workerID string,
	leaseUntil time.Time,
) (*scannerrelease.Candidate, error) {
	now := utcNow()
	if strings.TrimSpace(workerID) == "" || !leaseUntil.After(now) {
		return nil, errors.New("invalid scanner proposal claim request")
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var candidates []scannerrelease.Candidate
	if err := tx.SelectContext(ctx, &candidates, r.db.Rebind(
		`SELECT `+candidateColumns+` FROM scanner_release_candidates
		 WHERE state = ? AND proposal_lease_token = ''
		   AND proposal_attempt < proposal_max_attempts
		 ORDER BY created_at ASC, id ASC`),
		scannerrelease.CandidateAwaitingDefinition); err != nil {
		return nil, err
	}
	for i := range candidates {
		token := uuid.NewString()
		result, err := r.execTx(ctx, tx,
			`UPDATE scanner_release_candidates
			 SET proposal_worker_id = ?, proposal_lease_token = ?,
			     proposal_lease_expires_at = ?, proposal_heartbeat_at = ?,
			     proposal_attempt = proposal_attempt + 1,
			     proposal_error_class = '', proposal_error_detail = '',
			     proposal_started_at = COALESCE(proposal_started_at, ?),
			     proposal_completed_at = NULL,
			     version = version + 1, updated_at = ?
			 WHERE id = ? AND state = ? AND version = ?
			   AND proposal_lease_token = ''
			   AND proposal_attempt < proposal_max_attempts`,
			workerID, token, leaseUntil, now, now, now,
			candidates[i].ID, scannerrelease.CandidateAwaitingDefinition,
			candidates[i].Version)
		if err != nil {
			return nil, err
		}
		affected, _ := result.RowsAffected()
		if affected != 1 {
			continue
		}
		command := scannerrelease.TransitionCommand{
			Actor:          workerID,
			Reason:         "proposal worker claimed candidate definition work",
			PolicyRevision: candidates[i].PolicyRevision,
			IdempotencyKey: "proposal-claim:" + token,
			PayloadJSON:    `{"lease":"acquired"}`,
		}
		if _, err := r.appendEventTx(
			ctx, tx, "candidate", candidates[i].ID, "candidate.proposal_claimed",
			string(candidates[i].State), string(candidates[i].State), command, now,
		); err != nil {
			return nil, err
		}
		var claimed scannerrelease.Candidate
		if err := tx.GetContext(ctx, &claimed, r.db.Rebind(
			`SELECT `+candidateColumns+` FROM scanner_release_candidates WHERE id = ?`),
			candidates[i].ID); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return &claimed, nil
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return nil, nil
}

func (r *scannerReleaseRepository) HeartbeatCandidateProposal(
	ctx context.Context,
	id, workerID, leaseToken string,
	leaseUntil time.Time,
) (scannerrelease.CandidateProposalLeaseStatus, error) {
	now := utcNow()
	if id == "" || workerID == "" || leaseToken == "" || !leaseUntil.After(now) {
		return scannerrelease.CandidateProposalLeaseStatus{}, errors.New("invalid scanner proposal heartbeat")
	}
	var status scannerrelease.CandidateProposalLeaseStatus
	err := r.get(ctx, &status,
		`UPDATE scanner_release_candidates
		 SET proposal_heartbeat_at = ?, proposal_lease_expires_at = ?, updated_at = ?
		 WHERE id = ? AND state = ? AND proposal_worker_id = ?
		   AND proposal_lease_token = ? AND proposal_lease_expires_at > ?
		 RETURNING 1 AS current, version, state`,
		now, leaseUntil, now, id, scannerrelease.CandidateAwaitingDefinition,
		workerID, leaseToken, now)
	if errors.Is(err, sql.ErrNoRows) {
		err = r.get(ctx, &status,
			`SELECT 0 AS current, version, state
			 FROM scanner_release_candidates WHERE id = ?`, id)
		if errors.Is(err, sql.ErrNoRows) {
			return scannerrelease.CandidateProposalLeaseStatus{}, nil
		}
	}
	if err != nil {
		return scannerrelease.CandidateProposalLeaseStatus{}, err
	}
	return status, nil
}

func (r *scannerReleaseRepository) ReleaseCandidateProposal(
	ctx context.Context,
	id, workerID, leaseToken, errorClass, errorDetail string,
	command scannerrelease.TransitionCommand,
) (*scannerrelease.Candidate, error) {
	now := utcNow()
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var current scannerrelease.Candidate
	if err := tx.GetContext(ctx, &current, r.db.Rebind(
		`SELECT `+candidateColumns+` FROM scanner_release_candidates WHERE id = ?`), id); err != nil {
		return nil, err
	}
	if current.State != scannerrelease.CandidateAwaitingDefinition ||
		current.ProposalWorkerID != workerID ||
		current.ProposalLeaseToken != leaseToken {
		return nil, scannerrelease.ErrLeaseNotOwned
	}
	// A worker may report a failed attempt just after its lease deadline. The
	// token-and-version guarded update below is still safe: if another worker has
	// reclaimed or acquired the proposal, either value changes and this write
	// loses. Allowing the release avoids leaving otherwise unowned work stuck
	// until the next stale-lease sweep.
	next := scannerrelease.CandidateAwaitingDefinition
	eventType := "candidate.proposal_released"
	candidateErrorClass := ""
	candidateErrorDetail := ""
	var completedAt any
	if current.ProposalAttempt >= current.ProposalMaxAttempts {
		next = scannerrelease.CandidateFailed
		eventType = "candidate.proposal_failed"
		candidateErrorClass = errorClass
		candidateErrorDetail = errorDetail
		completedAt = now
	}
	result, err := r.execTx(ctx, tx,
		`UPDATE scanner_release_candidates
		 SET state = ?, proposal_worker_id = '', proposal_lease_token = '',
		     proposal_lease_expires_at = NULL, proposal_heartbeat_at = NULL,
		     proposal_error_class = ?, proposal_error_detail = ?,
		     proposal_completed_at = ?, error_class = ?, error_detail = ?,
		     version = version + 1, updated_at = ?
		 WHERE id = ? AND state = ? AND version = ?
		   AND proposal_worker_id = ? AND proposal_lease_token = ?`,
		next, errorClass, errorDetail, completedAt,
		candidateErrorClass, candidateErrorDetail, now, id,
		scannerrelease.CandidateAwaitingDefinition, current.Version,
		workerID, leaseToken)
	if err != nil {
		return nil, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return nil, scannerrelease.ErrLeaseNotOwned
	}
	if _, err := r.appendEventTx(
		ctx, tx, "candidate", id, eventType,
		string(current.State), string(next), command, now,
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return r.GetCandidate(ctx, id)
}

// FinalizeCandidateProposalNoOp records a successful terminal proposal
// outcome when scheduled discovery proves that the immutable definition is
// already current. The existing rejected terminal state is used for schema
// compatibility, while the dedicated candidate.noop event and no_changes
// outcome fields distinguish this from an operator rejection or failure.
func (r *scannerReleaseRepository) FinalizeCandidateProposalNoOp(
	ctx context.Context,
	id, workerID, leaseToken, outcomeClass, outcomeDetail string,
	command scannerrelease.TransitionCommand,
) (*scannerrelease.Candidate, error) {
	now := utcNow()
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var current scannerrelease.Candidate
	if err := tx.GetContext(ctx, &current, r.db.Rebind(
		`SELECT `+candidateColumns+` FROM scanner_release_candidates WHERE id = ?`), id); err != nil {
		return nil, err
	}
	if current.State != scannerrelease.CandidateAwaitingDefinition ||
		current.ProposalWorkerID != workerID || current.ProposalLeaseToken != leaseToken {
		return nil, scannerrelease.ErrLeaseNotOwned
	}
	result, err := r.execTx(ctx, tx,
		`UPDATE scanner_release_candidates
		 SET state = ?, proposal_worker_id = '', proposal_lease_token = '',
		     proposal_lease_expires_at = NULL, proposal_heartbeat_at = NULL,
		     proposal_error_class = '', proposal_error_detail = '',
		     proposal_completed_at = ?, error_class = ?, error_detail = ?,
		     version = version + 1, updated_at = ?
		 WHERE id = ? AND state = ? AND version = ?
		   AND proposal_worker_id = ? AND proposal_lease_token = ?`,
		scannerrelease.CandidateRejected, now, outcomeClass, outcomeDetail, now,
		id, scannerrelease.CandidateAwaitingDefinition, current.Version,
		workerID, leaseToken)
	if err != nil {
		return nil, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return nil, scannerrelease.ErrLeaseNotOwned
	}
	if _, err := r.appendEventTx(
		ctx, tx, "candidate", id, "candidate.noop",
		string(current.State), string(scannerrelease.CandidateRejected), command, now,
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return r.GetCandidate(ctx, id)
}

func (r *scannerReleaseRepository) FinalizeCandidateProposal(
	ctx context.Context,
	candidate *scannerrelease.Candidate,
	expectedVersion int64,
	leaseToken string,
	command scannerrelease.TransitionCommand,
) (*scannerrelease.Candidate, error) {
	if candidate == nil || candidate.ID == "" || candidate.ProposalWorkerID == "" || leaseToken == "" {
		return nil, errors.New("invalid scanner proposal finalization")
	}
	candidate.RiskSummaryJSON = jsonDefault(candidate.RiskSummaryJSON, "{}")
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var current scannerrelease.Candidate
	if err := tx.GetContext(ctx, &current, r.db.Rebind(
		`SELECT `+candidateColumns+` FROM scanner_release_candidates WHERE id = ?`),
		candidate.ID); err != nil {
		return nil, err
	}
	now := utcNow()
	if current.Version != expectedVersion {
		return nil, scannerrelease.ErrVersionConflict
	}
	if current.State != scannerrelease.CandidateAwaitingDefinition ||
		current.ProposalWorkerID != candidate.ProposalWorkerID ||
		current.ProposalLeaseToken != leaseToken ||
		current.ProposalLeaseExpiresAt == nil ||
		!current.ProposalLeaseExpiresAt.After(now) {
		return nil, scannerrelease.ErrLeaseNotOwned
	}
	result, err := r.execTx(ctx, tx,
		`UPDATE scanner_release_candidates
		 SET proposed_commit = ?, proposal_url = ?, lock_digest = ?, lock_uri = ?,
		     risk_summary_json = ?, state = ?,
		     proposal_worker_id = '', proposal_lease_token = '',
		     proposal_lease_expires_at = NULL, proposal_heartbeat_at = NULL,
		     proposal_error_class = '', proposal_error_detail = '',
		     proposal_completed_at = ?, error_class = '', error_detail = '',
		     version = version + 1, updated_at = ?
		 WHERE id = ? AND state = ? AND version = ?
		   AND proposal_worker_id = ? AND proposal_lease_token = ?`,
		candidate.ProposedCommit, candidate.ProposalURL, candidate.LockDigest,
		candidate.LockURI, candidate.RiskSummaryJSON, scannerrelease.CandidateQueued,
		now, now, candidate.ID, scannerrelease.CandidateAwaitingDefinition,
		expectedVersion, candidate.ProposalWorkerID, leaseToken)
	if err != nil {
		return nil, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return nil, scannerrelease.ErrLeaseNotOwned
	}
	if _, err := r.appendEventTx(
		ctx, tx, "candidate", candidate.ID, "candidate.proposal_finalized",
		string(current.State), string(scannerrelease.CandidateQueued), command, now,
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return r.GetCandidate(ctx, candidate.ID)
}

func (r *scannerReleaseRepository) ReclaimStaleCandidateProposals(
	ctx context.Context,
	now time.Time,
) (int, error) {
	if now.IsZero() {
		now = utcNow()
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	var stale []scannerrelease.Candidate
	if err := tx.SelectContext(ctx, &stale, r.db.Rebind(
		`SELECT `+candidateColumns+` FROM scanner_release_candidates
		 WHERE state = ? AND proposal_lease_token <> ''
		   AND proposal_lease_expires_at IS NOT NULL
		   AND proposal_lease_expires_at <= ?
		 ORDER BY proposal_lease_expires_at ASC, id ASC`),
		scannerrelease.CandidateAwaitingDefinition, now); err != nil {
		return 0, err
	}
	reclaimed := 0
	for i := range stale {
		next := scannerrelease.CandidateAwaitingDefinition
		eventType := "candidate.proposal_requeued_after_worker_loss"
		detail := "proposal worker lease expired; candidate requeued"
		candidateErrorClass := ""
		candidateErrorDetail := ""
		var completedAt any
		if stale[i].ProposalAttempt >= stale[i].ProposalMaxAttempts {
			next = scannerrelease.CandidateFailed
			eventType = "candidate.proposal_failed_after_worker_loss"
			detail = "proposal worker lease expired and retry budget was exhausted"
			candidateErrorClass = "proposal_worker_lost"
			candidateErrorDetail = detail
			completedAt = now
		}
		result, err := r.execTx(ctx, tx,
			`UPDATE scanner_release_candidates
			 SET state = ?, proposal_worker_id = '', proposal_lease_token = '',
			     proposal_lease_expires_at = NULL, proposal_heartbeat_at = NULL,
			     proposal_error_class = 'proposal_worker_lost',
			     proposal_error_detail = ?, proposal_completed_at = ?,
			     error_class = ?, error_detail = ?,
			     version = version + 1, updated_at = ?
			 WHERE id = ? AND state = ? AND version = ?
			   AND proposal_lease_token = ?`,
			next, detail, completedAt, candidateErrorClass, candidateErrorDetail,
			now, stale[i].ID, scannerrelease.CandidateAwaitingDefinition,
			stale[i].Version, stale[i].ProposalLeaseToken)
		if err != nil {
			return reclaimed, err
		}
		affected, _ := result.RowsAffected()
		if affected != 1 {
			continue
		}
		command := scannerrelease.TransitionCommand{
			Actor:          "scheduler",
			Reason:         "proposal worker lease expired",
			PolicyRevision: stale[i].PolicyRevision,
			IdempotencyKey: "proposal-reclaim:" + stale[i].ProposalLeaseToken,
			PayloadJSON:    `{"errorClass":"proposal_worker_lost"}`,
		}
		if _, err := r.appendEventTx(
			ctx, tx, "candidate", stale[i].ID, eventType,
			string(stale[i].State), string(next), command, now,
		); err != nil {
			return reclaimed, err
		}
		reclaimed++
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return reclaimed, nil
}

func (r *scannerReleaseRepository) UpdateCandidateProposal(
	ctx context.Context,
	candidate *scannerrelease.Candidate,
	expectedVersion int64,
	command scannerrelease.TransitionCommand,
) (*scannerrelease.Candidate, error) {
	candidate.RiskSummaryJSON = jsonDefault(candidate.RiskSummaryJSON, "{}")
	candidate.RequiredGatesJSON = jsonDefault(candidate.RequiredGatesJSON, "[]")
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var current struct {
		State   string `db:"state"`
		Version int64  `db:"version"`
	}
	if err := tx.GetContext(ctx, &current, r.db.Rebind(
		`SELECT state, version FROM scanner_release_candidates WHERE id = ?`), candidate.ID); err != nil {
		return nil, err
	}
	if current.Version != expectedVersion {
		return nil, scannerrelease.ErrVersionConflict
	}
	now := utcNow()
	result, err := r.execTx(ctx, tx,
		`UPDATE scanner_release_candidates
		 SET proposed_commit = ?, proposal_url = ?, lock_digest = ?, lock_uri = ?,
		     risk_summary_json = ?, required_gates_json = ?, policy_decision = ?,
		     error_class = ?, error_detail = ?, version = version + 1, updated_at = ?
		 WHERE id = ? AND version = ?`,
		candidate.ProposedCommit, candidate.ProposalURL, candidate.LockDigest,
		candidate.LockURI, candidate.RiskSummaryJSON, candidate.RequiredGatesJSON,
		candidate.PolicyDecision, candidate.ErrorClass, candidate.ErrorDetail,
		now, candidate.ID, expectedVersion)
	if err != nil {
		return nil, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return nil, scannerrelease.ErrVersionConflict
	}
	if _, err := r.appendEventTx(
		ctx, tx, "candidate", candidate.ID, "candidate.proposal_updated",
		current.State, current.State, command, now,
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return r.GetCandidate(ctx, candidate.ID)
}

func (r *scannerReleaseRepository) TransitionCandidate(ctx context.Context, id string, expectedVersion int64, to scannerrelease.CandidateState, command scannerrelease.TransitionCommand) (*scannerrelease.Candidate, error) {
	_, err := r.transitionState(ctx, "scanner_release_candidates", "candidate", id, expectedVersion, string(to), command,
		func(from, to string) error {
			return scannerrelease.ValidateCandidateTransition(scannerrelease.CandidateState(from), scannerrelease.CandidateState(to))
		})
	if err != nil {
		return nil, err
	}
	return r.GetCandidate(ctx, id)
}

// --- Builds ------------------------------------------------------------------

func (r *scannerReleaseRepository) CreateBuildRun(ctx context.Context, run *scannerrelease.BuildRun, command scannerrelease.TransitionCommand) error {
	initializeIDAndTimes(&run.ID, &run.CreatedAt, &run.UpdatedAt)
	if run.Attempt <= 0 {
		run.Attempt = 1
	}
	if run.State == "" {
		run.State = scannerrelease.BuildQueued
	}
	if run.Version <= 0 {
		run.Version = 1
	}
	run.PlatformsJSON = jsonDefault(run.PlatformsJSON, "[]")
	platforms, err := requestedBuildPlatforms(run.PlatformsJSON)
	if err != nil {
		return err
	}
	if len(platforms) == 0 {
		return errors.New("scanner build requires at least one platform")
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := r.namedExecResultTx(ctx, tx,
		`INSERT INTO scanner_build_runs
		 (id, candidate_id, attempt, worker_id, state, platforms_json, lease_token,
		  lease_expires_at, heartbeat_at, cancel_requested_at, error_class,
		  error_detail, version, started_at, completed_at, created_at, updated_at)
		 VALUES
		 (:id, :candidate_id, :attempt, :worker_id, :state, :platforms_json, :lease_token,
		  :lease_expires_at, :heartbeat_at, :cancel_requested_at, :error_class,
		  :error_detail, :version, :started_at, :completed_at, :created_at, :updated_at)
		 ON CONFLICT(candidate_id, attempt) DO NOTHING`, run)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		var existing scannerrelease.BuildRun
		if err := tx.GetContext(ctx, &existing, r.db.Rebind(
			`SELECT * FROM scanner_build_runs WHERE candidate_id = ? AND attempt = ?`),
			run.CandidateID, run.Attempt); err != nil {
			return err
		}
		if existing.PlatformsJSON != run.PlatformsJSON {
			return scannerrelease.ErrIdempotencyConflict
		}
		*run = existing
		return tx.Commit()
	}
	if _, err := r.appendEventTx(ctx, tx, "build", run.ID, "build.created", "", string(run.State), command, run.CreatedAt); err != nil {
		return err
	}
	return tx.Commit()
}

// CreateBuildPlan atomically persists a build run and its complete DAG. It is
// safe to replay after an ambiguous client failure; conflicting platform or
// step definitions fail closed.
func (r *scannerReleaseRepository) CreateBuildPlan(
	ctx context.Context,
	run *scannerrelease.BuildRun,
	steps []scannerrelease.BuildStep,
	command scannerrelease.TransitionCommand,
) error {
	initializeIDAndTimes(&run.ID, &run.CreatedAt, &run.UpdatedAt)
	if run.Attempt <= 0 {
		run.Attempt = 1
	}
	if run.State == "" {
		run.State = scannerrelease.BuildQueued
	}
	if run.Version <= 0 {
		run.Version = 1
	}
	run.PlatformsJSON = jsonDefault(run.PlatformsJSON, "[]")
	platforms, err := requestedBuildPlatforms(run.PlatformsJSON)
	if err != nil {
		return err
	}
	if len(platforms) == 0 || len(steps) == 0 {
		return errors.New("scanner build plan requires platforms and steps")
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := r.namedExecResultTx(ctx, tx,
		`INSERT INTO scanner_build_runs
		 (id, candidate_id, attempt, worker_id, state, platforms_json, lease_token,
		  lease_expires_at, heartbeat_at, cancel_requested_at, error_class,
		  error_detail, version, started_at, completed_at, created_at, updated_at)
		 VALUES
		 (:id, :candidate_id, :attempt, :worker_id, :state, :platforms_json, :lease_token,
		  :lease_expires_at, :heartbeat_at, :cancel_requested_at, :error_class,
		  :error_detail, :version, :started_at, :completed_at, :created_at, :updated_at)
		 ON CONFLICT(candidate_id, attempt) DO NOTHING`, run)
	if err != nil {
		return err
	}
	createdRun := false
	if affected, _ := result.RowsAffected(); affected == 0 {
		var existing scannerrelease.BuildRun
		if err := tx.GetContext(ctx, &existing, r.db.Rebind(
			`SELECT * FROM scanner_build_runs WHERE candidate_id = ? AND attempt = ?`),
			run.CandidateID, run.Attempt); err != nil {
			return err
		}
		if existing.PlatformsJSON != run.PlatformsJSON {
			return scannerrelease.ErrIdempotencyConflict
		}
		*run = existing
	} else {
		createdRun = true
		if _, err := r.appendEventTx(
			ctx, tx, "build", run.ID, "build.created", "", string(run.State),
			command, run.CreatedAt,
		); err != nil {
			return err
		}
	}
	seen := make(map[string]struct{}, len(steps))
	for index := range steps {
		step := &steps[index]
		if step.BuildRunID != "" && step.BuildRunID != run.ID {
			return scannerrelease.ErrIdempotencyConflict
		}
		step.BuildRunID = run.ID
		initializeIDAndTimes(&step.ID, &step.CreatedAt, &step.UpdatedAt)
		if step.Attempt <= 0 {
			step.Attempt = 1
		}
		if step.State == "" {
			step.State = scannerrelease.BuildQueued
		}
		if step.Version <= 0 {
			step.Version = 1
		}
		step.SummaryJSON = jsonDefault(step.SummaryJSON, "{}")
		if step.RetentionClass == "" {
			step.RetentionClass = "transient"
		}
		logicalKey := fmt.Sprintf("%s/%d", step.StepKey, step.Attempt)
		if _, duplicate := seen[logicalKey]; duplicate {
			return fmt.Errorf("duplicate scanner build plan step %s", logicalKey)
		}
		seen[logicalKey] = struct{}{}
		result, err := r.namedExecResultTx(ctx, tx,
			`INSERT INTO scanner_build_steps
			 (id, build_run_id, step_key, state, attempt, output_uri, output_digest,
			  summary_json, retention_class, retain_until, protected, error_class,
			  error_detail, version, started_at, completed_at, created_at, updated_at)
			 VALUES
			 (:id, :build_run_id, :step_key, :state, :attempt, :output_uri, :output_digest,
			  :summary_json, :retention_class, :retain_until, :protected, :error_class,
			  :error_detail, :version, :started_at, :completed_at, :created_at, :updated_at)
			 ON CONFLICT(build_run_id, step_key, attempt) DO NOTHING`, step)
		if err != nil {
			return err
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			var existing scannerrelease.BuildStep
			if err := tx.GetContext(ctx, &existing, r.db.Rebind(
				`SELECT * FROM scanner_build_steps
				 WHERE build_run_id = ? AND step_key = ? AND attempt = ?`),
				run.ID, step.StepKey, step.Attempt); err != nil {
				return err
			}
			if existing.RetentionClass != step.RetentionClass ||
				existing.SummaryJSON != step.SummaryJSON {
				return scannerrelease.ErrIdempotencyConflict
			}
			*step = existing
			continue
		}
		stepCommand := command
		stepCommand.IdempotencyKey = fmt.Sprintf(
			"%s/step/%03d/%s/%d",
			command.IdempotencyKey, index, step.StepKey, step.Attempt,
		)
		stepCommand.Reason = "candidate build step enqueued"
		if _, err := r.appendEventTx(
			ctx, tx, "build_step", step.ID, "build_step.created", "", string(step.State),
			stepCommand, step.CreatedAt,
		); err != nil {
			return err
		}
	}
	if !createdRun {
		var count int
		if err := tx.GetContext(ctx, &count, r.db.Rebind(
			`SELECT COUNT(*) FROM scanner_build_steps WHERE build_run_id = ?`),
			run.ID); err != nil {
			return err
		}
		if count != len(steps) {
			return scannerrelease.ErrIdempotencyConflict
		}
	}
	return tx.Commit()
}

func (r *scannerReleaseRepository) GetBuildRun(ctx context.Context, id string) (*scannerrelease.BuildRun, error) {
	var run scannerrelease.BuildRun
	if err := r.get(ctx, &run, `SELECT * FROM scanner_build_runs WHERE id = ?`, id); err != nil {
		return nil, err
	}
	return &run, nil
}

func (r *scannerReleaseRepository) ListBuildRuns(ctx context.Context, candidateID string) ([]scannerrelease.BuildRun, error) {
	var runs []scannerrelease.BuildRun
	return runs, r.selectRows(ctx, &runs,
		`SELECT * FROM scanner_build_runs WHERE candidate_id = ? ORDER BY attempt DESC`, candidateID)
}

func requestedBuildPlatforms(requestedJSON string) ([]string, error) {
	var requested []string
	encoded := []byte(jsonDefault(requestedJSON, "[]"))
	if err := json.Unmarshal(encoded, &requested); err != nil {
		requested = nil
		// Build plans persist the image/platform matrix so the same BuildRun
		// can represent every image variant. Accept that canonical shape in
		// addition to the compact []string form used by custom workers.
		var images []struct {
			Platforms []string `json:"platforms"`
		}
		if imageErr := json.Unmarshal(encoded, &images); imageErr != nil {
			return nil, fmt.Errorf("decode requested build platforms: %w", err)
		}
		seen := make(map[string]struct{})
		for _, image := range images {
			for _, platform := range image.Platforms {
				if _, duplicate := seen[platform]; duplicate {
					continue
				}
				seen[platform] = struct{}{}
				requested = append(requested, platform)
			}
		}
	}
	for _, platform := range requested {
		if strings.TrimSpace(platform) == "" {
			return nil, errors.New("scanner build platform cannot be empty")
		}
	}
	return requested, nil
}

func supportsBuildPlatforms(requestedJSON string, supported []string) (bool, error) {
	requested, err := requestedBuildPlatforms(requestedJSON)
	if err != nil {
		return false, err
	}
	if len(supported) == 0 {
		return true, nil
	}
	available := make(map[string]struct{}, len(supported))
	for _, platform := range supported {
		available[platform] = struct{}{}
	}
	for _, platform := range requested {
		if _, ok := available[platform]; !ok {
			return false, nil
		}
	}
	return true, nil
}

func (r *scannerReleaseRepository) ClaimNextBuildRun(
	ctx context.Context,
	workerID string,
	supportedPlatforms []string,
	leaseUntil time.Time,
) (*scannerrelease.BuildRun, error) {
	now := utcNow()
	if workerID == "" || !leaseUntil.After(now) {
		return nil, errors.New("invalid scanner build claim request")
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	query := `SELECT * FROM scanner_build_runs
		WHERE state = ? AND cancel_requested_at IS NULL
		ORDER BY created_at ASC, id ASC`
	var queued []scannerrelease.BuildRun
	if err := tx.SelectContext(ctx, &queued, r.db.Rebind(query), scannerrelease.BuildQueued); err != nil {
		return nil, err
	}
	for i := range queued {
		compatible, err := supportsBuildPlatforms(queued[i].PlatformsJSON, supportedPlatforms)
		if err != nil {
			return nil, err
		}
		if !compatible {
			continue
		}
		token := uuid.NewString()
		result, err := r.execTx(ctx, tx,
			`UPDATE scanner_build_runs
			 SET state = ?, worker_id = ?, lease_token = ?, lease_expires_at = ?,
			     heartbeat_at = ?, started_at = COALESCE(started_at, ?),
			     version = version + 1, updated_at = ?
			 WHERE id = ? AND state = ? AND version = ? AND cancel_requested_at IS NULL`,
			scannerrelease.BuildClaimed, workerID, token, leaseUntil, now, now, now,
			queued[i].ID, scannerrelease.BuildQueued, queued[i].Version)
		if err != nil {
			return nil, err
		}
		affected, _ := result.RowsAffected()
		if affected != 1 {
			continue
		}
		command := scannerrelease.TransitionCommand{
			Actor:          workerID,
			Reason:         "build worker claimed queued release build",
			IdempotencyKey: "claim:" + token,
			PayloadJSON:    `{"lease":"acquired"}`,
		}
		if _, err := r.appendEventTx(
			ctx, tx, "build", queued[i].ID, "build.claimed",
			string(scannerrelease.BuildQueued), string(scannerrelease.BuildClaimed),
			command, now,
		); err != nil {
			return nil, err
		}
		var claimed scannerrelease.BuildRun
		if err := tx.GetContext(ctx, &claimed, r.db.Rebind(
			`SELECT * FROM scanner_build_runs WHERE id = ?`), queued[i].ID); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return &claimed, nil
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return nil, nil
}

func (r *scannerReleaseRepository) HeartbeatBuildRun(
	ctx context.Context,
	id, workerID, leaseToken string,
	leaseUntil time.Time,
) (scannerrelease.BuildLeaseStatus, error) {
	now := utcNow()
	if id == "" || workerID == "" || leaseToken == "" || !leaseUntil.After(now) {
		return scannerrelease.BuildLeaseStatus{}, errors.New("invalid scanner build heartbeat")
	}
	var status scannerrelease.BuildLeaseStatus
	err := r.get(ctx, &status,
		`UPDATE scanner_build_runs
		 SET heartbeat_at = ?, lease_expires_at = ?, updated_at = ?
		 WHERE id = ? AND worker_id = ? AND lease_token = ?
		   AND state IN (?, ?) AND lease_expires_at > ?
		 RETURNING 1 AS current,
		   CASE WHEN cancel_requested_at IS NULL THEN 0 ELSE 1 END AS cancel_requested,
		   version`,
		now, leaseUntil, now, id, workerID, leaseToken,
		scannerrelease.BuildClaimed, scannerrelease.BuildRunning, now)
	if errors.Is(err, sql.ErrNoRows) {
		return scannerrelease.BuildLeaseStatus{}, nil
	}
	if err != nil {
		return scannerrelease.BuildLeaseStatus{}, err
	}
	return status, nil
}

func (r *scannerReleaseRepository) RequestBuildCancellation(
	ctx context.Context,
	id string,
	command scannerrelease.TransitionCommand,
	at time.Time,
) (bool, error) {
	if at.IsZero() {
		at = utcNow()
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	var current struct {
		State             scannerrelease.BuildState `db:"state"`
		Version           int64                     `db:"version"`
		CancelRequestedAt *time.Time                `db:"cancel_requested_at"`
	}
	if err := tx.GetContext(ctx, &current, r.db.Rebind(
		`SELECT state, version, cancel_requested_at
		 FROM scanner_build_runs WHERE id = ?`), id); err != nil {
		return false, err
	}
	if command.IdempotencyKey != "" {
		var existing scannerrelease.Event
		err := tx.GetContext(ctx, &existing, r.db.Rebind(
			`SELECT * FROM scanner_release_events
			 WHERE aggregate_type = ? AND aggregate_id = ? AND idempotency_key = ?`),
			"build", id, command.IdempotencyKey)
		if err == nil {
			if existing.EventType != "build.cancellation_requested" {
				return false, scannerrelease.ErrIdempotencyConflict
			}
			return false, tx.Commit()
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return false, err
		}
	}
	if scannerrelease.IsTerminalBuildState(current.State) {
		return false, tx.Commit()
	}
	if current.CancelRequestedAt != nil {
		return false, tx.Commit()
	}
	result, err := r.execTx(ctx, tx,
		`UPDATE scanner_build_runs
		 SET cancel_requested_at = COALESCE(cancel_requested_at, ?),
		     version = version + 1, updated_at = ?
		 WHERE id = ? AND version = ?`,
		at, at, id, current.Version)
	if err != nil {
		return false, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return false, scannerrelease.ErrVersionConflict
	}
	if _, err := r.appendEventTx(
		ctx, tx, "build", id, "build.cancellation_requested",
		string(current.State), string(current.State), command, at,
	); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return current.CancelRequestedAt == nil, nil
}

func (r *scannerReleaseRepository) ReclaimStaleBuildRuns(ctx context.Context, now time.Time) (int, error) {
	if now.IsZero() {
		now = utcNow()
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	query := `SELECT * FROM scanner_build_runs
		WHERE state IN (?, ?) AND lease_expires_at IS NOT NULL AND lease_expires_at <= ?
		ORDER BY lease_expires_at ASC, id ASC`
	var stale []scannerrelease.BuildRun
	if err := tx.SelectContext(
		ctx, &stale, r.db.Rebind(query),
		scannerrelease.BuildClaimed, scannerrelease.BuildRunning, now,
	); err != nil {
		return 0, err
	}
	reclaimed := 0
	for i := range stale {
		next := scannerrelease.BuildQueued
		eventType := "build.requeued_after_worker_loss"
		errorClass := ""
		errorDetail := ""
		var completedAt any
		if stale[i].CancelRequestedAt != nil {
			next = scannerrelease.BuildCancelled
			eventType = "build.cancelled_after_worker_loss"
			completedAt = now
		}
		result, err := r.execTx(ctx, tx,
			`UPDATE scanner_build_runs
			 SET state = ?, worker_id = '', lease_token = '', lease_expires_at = NULL,
			     heartbeat_at = NULL, error_class = ?, error_detail = ?,
			     completed_at = ?, version = version + 1, updated_at = ?
			 WHERE id = ? AND version = ? AND lease_token = ?`,
			next, errorClass, errorDetail, completedAt, now,
			stale[i].ID, stale[i].Version, stale[i].LeaseToken)
		if err != nil {
			return reclaimed, err
		}
		affected, _ := result.RowsAffected()
		if affected != 1 {
			continue
		}
		command := scannerrelease.TransitionCommand{
			Actor:          "scheduler",
			Reason:         "build worker lease expired",
			IdempotencyKey: "reclaim:" + stale[i].LeaseToken,
			PayloadJSON:    `{"errorClass":"worker_lost"}`,
		}
		if _, err := r.appendEventTx(
			ctx, tx, "build", stale[i].ID, eventType,
			string(stale[i].State), string(next), command, now,
		); err != nil {
			return reclaimed, err
		}
		reclaimed++
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return reclaimed, nil
}

func (r *scannerReleaseRepository) TransitionBuildRun(ctx context.Context, id string, expectedVersion int64, to scannerrelease.BuildState, command scannerrelease.TransitionCommand) (*scannerrelease.BuildRun, error) {
	_, err := r.transitionState(ctx, "scanner_build_runs", "build", id, expectedVersion, string(to), command,
		func(from, to string) error {
			return scannerrelease.ValidateBuildTransition(scannerrelease.BuildState(from), scannerrelease.BuildState(to))
		})
	if err != nil {
		return nil, err
	}
	return r.GetBuildRun(ctx, id)
}

func (r *scannerReleaseRepository) CreateBuildStep(ctx context.Context, step *scannerrelease.BuildStep, command scannerrelease.TransitionCommand) error {
	initializeIDAndTimes(&step.ID, &step.CreatedAt, &step.UpdatedAt)
	if step.Attempt <= 0 {
		step.Attempt = 1
	}
	if step.State == "" {
		step.State = scannerrelease.BuildQueued
	}
	if step.Version <= 0 {
		step.Version = 1
	}
	step.SummaryJSON = jsonDefault(step.SummaryJSON, "{}")
	if step.RetentionClass == "" {
		step.RetentionClass = "transient"
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := r.namedExecResultTx(ctx, tx,
		`INSERT INTO scanner_build_steps
		 (id, build_run_id, step_key, state, attempt, output_uri, output_digest,
		  summary_json, retention_class, retain_until, protected, error_class,
		  error_detail, version, started_at, completed_at, created_at, updated_at)
		 VALUES
		 (:id, :build_run_id, :step_key, :state, :attempt, :output_uri, :output_digest,
		  :summary_json, :retention_class, :retain_until, :protected, :error_class,
		  :error_detail, :version, :started_at, :completed_at, :created_at, :updated_at)
		 ON CONFLICT(build_run_id, step_key, attempt) DO NOTHING`, step)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		var existing scannerrelease.BuildStep
		if err := tx.GetContext(ctx, &existing, r.db.Rebind(
			`SELECT * FROM scanner_build_steps
			 WHERE build_run_id = ? AND step_key = ? AND attempt = ?`),
			step.BuildRunID, step.StepKey, step.Attempt); err != nil {
			return err
		}
		if existing.RetentionClass != step.RetentionClass {
			return scannerrelease.ErrIdempotencyConflict
		}
		*step = existing
		return tx.Commit()
	}
	if _, err := r.appendEventTx(ctx, tx, "build_step", step.ID, "build_step.created", "", string(step.State), command, step.CreatedAt); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *scannerReleaseRepository) ListBuildSteps(ctx context.Context, buildRunID string) ([]scannerrelease.BuildStep, error) {
	var steps []scannerrelease.BuildStep
	return steps, r.selectRows(ctx, &steps,
		`SELECT * FROM scanner_build_steps
		 WHERE build_run_id = ? ORDER BY created_at ASC, step_key ASC, attempt ASC`, buildRunID)
}

func (r *scannerReleaseRepository) UpdateBuildStepEvidence(
	ctx context.Context,
	step *scannerrelease.BuildStep,
	expectedVersion int64,
	command scannerrelease.TransitionCommand,
) (*scannerrelease.BuildStep, error) {
	step.SummaryJSON = jsonDefault(step.SummaryJSON, "{}")
	if step.RetentionClass == "" {
		step.RetentionClass = "transient"
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var current struct {
		State   string `db:"state"`
		Version int64  `db:"version"`
	}
	if err := tx.GetContext(ctx, &current, r.db.Rebind(
		`SELECT state, version FROM scanner_build_steps WHERE id = ?`), step.ID); err != nil {
		return nil, err
	}
	if current.Version != expectedVersion {
		return nil, scannerrelease.ErrVersionConflict
	}
	if err := r.rejectQuarantineDeletionReferenceTx(ctx, tx, step.OutputDigest); err != nil {
		return nil, err
	}
	now := utcNow()
	result, err := r.execTx(ctx, tx,
		`UPDATE scanner_build_steps
		 SET output_uri = ?, output_digest = ?, summary_json = ?,
		     retention_class = ?, retain_until = ?, protected = ?,
		     error_class = ?, error_detail = ?, version = version + 1, updated_at = ?
		 WHERE id = ? AND version = ?`,
		step.OutputURI, step.OutputDigest, step.SummaryJSON, step.RetentionClass,
		step.RetainUntil, step.Protected, step.ErrorClass, step.ErrorDetail,
		now, step.ID, expectedVersion)
	if err != nil {
		return nil, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return nil, scannerrelease.ErrVersionConflict
	}
	if _, err := r.appendEventTx(
		ctx, tx, "build_step", step.ID, "build_step.evidence_recorded",
		current.State, current.State, command, now,
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	var updated scannerrelease.BuildStep
	if err := r.get(ctx, &updated, `SELECT * FROM scanner_build_steps WHERE id = ?`, step.ID); err != nil {
		return nil, err
	}
	return &updated, nil
}

func (r *scannerReleaseRepository) TransitionBuildStep(ctx context.Context, id string, expectedVersion int64, to scannerrelease.BuildState, command scannerrelease.TransitionCommand) (*scannerrelease.BuildStep, error) {
	_, err := r.transitionState(ctx, "scanner_build_steps", "build_step", id, expectedVersion, string(to), command,
		func(from, to string) error {
			return scannerrelease.ValidateBuildTransition(scannerrelease.BuildState(from), scannerrelease.BuildState(to))
		})
	if err != nil {
		return nil, err
	}
	var step scannerrelease.BuildStep
	if err := r.get(ctx, &step, `SELECT * FROM scanner_build_steps WHERE id = ?`, id); err != nil {
		return nil, err
	}
	return &step, nil
}

// --- Releases and immutable evidence ----------------------------------------

func prepareReleaseInventory(inventory *scannerrelease.ReleaseInventory) {
	release := &inventory.Release
	initializeIDAndTimes(&release.ID, &release.CreatedAt, &release.UpdatedAt)
	if release.State == "" {
		release.State = scannerrelease.ReleasePublished
	}
	if release.Version <= 0 {
		release.Version = 1
	}
	if release.PublishedAt.IsZero() {
		release.PublishedAt = release.CreatedAt
	}
	if release.RetentionClass == "" {
		release.RetentionClass = "release"
	}
}

func parseScannerReleaseName(name string) (string, int, bool) {
	const prefix = "scanner-set-"
	if !strings.HasPrefix(name, prefix) {
		return "", 0, false
	}
	parts := strings.Split(strings.TrimPrefix(name, prefix), ".")
	if len(parts) != 3 || len(parts[0]) != 4 || len(parts[1]) != 2 {
		return "", 0, false
	}
	year, yearErr := strconv.Atoi(parts[0])
	week, weekErr := strconv.Atoi(parts[1])
	sequence, sequenceErr := strconv.Atoi(parts[2])
	if yearErr != nil || weekErr != nil || sequenceErr != nil ||
		year < 1 || week < 1 || week > 53 || sequence < 1 {
		return "", 0, false
	}
	return fmt.Sprintf("%04d.%02d", year, week), sequence, true
}

// reserveReleaseNameTx binds one human-readable release sequence to the
// candidate inside the immutable-publication transaction. An empty name uses
// the ISO week containing PublishedAt. A supplied valid name is retained for
// compatibility with existing publication clients, but is still reserved and
// advances the server-side counter so subsequent generated names cannot
// collide with it.
func (r *scannerReleaseRepository) reserveReleaseNameTx(
	ctx context.Context,
	tx *sqlx.Tx,
	release *scannerrelease.Release,
) error {
	var existingName string
	err := tx.GetContext(ctx, &existingName, r.db.Rebind(
		`SELECT release_name
		 FROM scanner_release_name_reservations
		 WHERE candidate_id = ?`), release.CandidateID)
	switch {
	case err == nil:
		if release.Name != "" && release.Name != existingName {
			return scannerrelease.ErrIdempotencyConflict
		}
		release.Name = existingName
		return nil
	case !errors.Is(err, sql.ErrNoRows):
		return err
	}

	if release.Name != "" {
		period, sequence, ok := parseScannerReleaseName(release.Name)
		if !ok {
			return fmt.Errorf("invalid scanner release name %q", release.Name)
		}
		if _, err := r.execTx(ctx, tx,
			`INSERT INTO scanner_release_sequence_counters
			 (period_key, next_sequence, updated_at)
			 VALUES (?, ?, ?)
			 ON CONFLICT(period_key) DO UPDATE SET
			   next_sequence = CASE
			     WHEN scanner_release_sequence_counters.next_sequence < excluded.next_sequence
			       THEN excluded.next_sequence
			     ELSE scanner_release_sequence_counters.next_sequence
			   END,
			   updated_at = excluded.updated_at`,
			period, sequence+1, release.PublishedAt,
		); err != nil {
			return err
		}
		if _, err := r.execTx(ctx, tx,
			`INSERT INTO scanner_release_name_reservations
			 (candidate_id, period_key, sequence_number, release_name, created_at)
			 VALUES (?, ?, ?, ?, ?)`,
			release.CandidateID, period, sequence, release.Name, release.PublishedAt,
		); err != nil {
			return fmt.Errorf("reserve scanner release name: %w", err)
		}
		return nil
	}

	year, week := release.PublishedAt.UTC().ISOWeek()
	period := fmt.Sprintf("%04d.%02d", year, week)
	for {
		var sequence int
		if err := tx.GetContext(ctx, &sequence, r.db.Rebind(
			`INSERT INTO scanner_release_sequence_counters
			 (period_key, next_sequence, updated_at)
			 VALUES (?, 2, ?)
			 ON CONFLICT(period_key) DO UPDATE SET
			   next_sequence = scanner_release_sequence_counters.next_sequence + 1,
			   updated_at = excluded.updated_at
			 RETURNING next_sequence - 1`),
			period, release.PublishedAt,
		); err != nil {
			return fmt.Errorf("allocate scanner release sequence: %w", err)
		}
		name := fmt.Sprintf("scanner-set-%s.%d", period, sequence)

		var existingCandidateID string
		err := tx.GetContext(ctx, &existingCandidateID, r.db.Rebind(
			`SELECT candidate_id FROM scanner_releases WHERE release_name = ?`), name)
		if err == nil {
			// A release created before the allocator migration already consumed
			// this sequence. Advance again without exposing an avoidable
			// publication conflict.
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := r.execTx(ctx, tx,
			`INSERT INTO scanner_release_name_reservations
			 (candidate_id, period_key, sequence_number, release_name, created_at)
			 VALUES (?, ?, ?, ?, ?)`,
			release.CandidateID, period, sequence, name, release.PublishedAt,
		); err != nil {
			return fmt.Errorf("reserve generated scanner release name: %w", err)
		}
		release.Name = name
		return nil
	}
}

func (r *scannerReleaseRepository) CreateRelease(
	ctx context.Context,
	inventory *scannerrelease.ReleaseInventory,
	command scannerrelease.TransitionCommand,
) error {
	prepareReleaseInventory(inventory)
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := r.insertReleaseInventoryTx(ctx, tx, inventory, command); err != nil {
		return err
	}
	return tx.Commit()
}

// insertReleaseInventoryTx writes a complete immutable release inventory in
// the caller's transaction. It returns false for an idempotent pre-existing
// candidate publication and rejects any conflicting immutable identity.
func (r *scannerReleaseRepository) insertReleaseInventoryTx(
	ctx context.Context,
	tx *sqlx.Tx,
	inventory *scannerrelease.ReleaseInventory,
	command scannerrelease.TransitionCommand,
) (bool, error) {
	release := &inventory.Release
	if err := r.rejectQuarantineDeletionReferenceTx(ctx, tx, release.ManifestDigest); err != nil {
		return false, err
	}
	if err := r.rejectQuarantineDeletionReferenceTx(ctx, tx, release.LockDigest); err != nil {
		return false, err
	}
	result, err := r.namedExecResultTx(ctx, tx,
		`INSERT INTO scanner_releases
		 (id, release_name, candidate_id, lock_digest, manifest_digest, manifest_uri,
		  state, signer_identity, policy_id, policy_revision, definition_commit,
		  imported, legacy, protected, rollback_eligible, retention_class, retain_until,
		  version, published_at, deprecated_at, revoked_at, created_at, updated_at)
		 VALUES
		 (:id, :release_name, :candidate_id, :lock_digest, :manifest_digest, :manifest_uri,
		  :state, :signer_identity, :policy_id, :policy_revision, :definition_commit,
		  :imported, :legacy, :protected, :rollback_eligible, :retention_class, :retain_until,
		  :version, :published_at, :deprecated_at, :revoked_at, :created_at, :updated_at)
		 ON CONFLICT(candidate_id) DO NOTHING`, release)
	if err != nil {
		return false, err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		var existing scannerrelease.Release
		if err := tx.GetContext(ctx, &existing, r.db.Rebind(
			`SELECT * FROM scanner_releases WHERE candidate_id = ?`),
			release.CandidateID); err != nil {
			return false, err
		}
		if existing.Name != release.Name ||
			existing.LockDigest != release.LockDigest ||
			existing.ManifestDigest != release.ManifestDigest ||
			existing.ManifestURI != release.ManifestURI ||
			existing.SignerIdentity != release.SignerIdentity ||
			existing.PolicyID != release.PolicyID ||
			existing.PolicyRevision != release.PolicyRevision ||
			existing.DefinitionCommit != release.DefinitionCommit {
			return false, scannerrelease.ErrIdempotencyConflict
		}
		release.ID = existing.ID
		*release = existing
		return false, nil
	}
	for i := range inventory.Tools {
		tool := &inventory.Tools[i]
		if tool.ID == "" {
			tool.ID = uuid.NewString()
		}
		tool.ReleaseID = release.ID
		if tool.CreatedAt.IsZero() {
			tool.CreatedAt = release.CreatedAt
		}
		tool.MetadataJSON = jsonDefault(tool.MetadataJSON, "{}")
		if err := r.namedExecTx(ctx, tx,
			`INSERT INTO scanner_release_tools
			 (id, release_id, tool_key, tool_version, source_reference, source_digest,
			  checksum, parser_compatibility, metadata_json, created_at)
			 VALUES
			 (:id, :release_id, :tool_key, :tool_version, :source_reference, :source_digest,
			  :checksum, :parser_compatibility, :metadata_json, :created_at)`, tool); err != nil {
			return false, err
		}
	}
	for i := range inventory.Images {
		image := &inventory.Images[i]
		if image.ID == "" {
			image.ID = uuid.NewString()
		}
		image.ReleaseID = release.ID
		if image.CreatedAt.IsZero() {
			image.CreatedAt = release.CreatedAt
		}
		image.PlatformDigests = jsonDefault(image.PlatformDigests, "{}")
		image.ImageKind = scannerrelease.NormalizedImageKind(*image)
		var deleting int
		if err := tx.GetContext(ctx, &deleting, r.db.Rebind(
			`SELECT COUNT(*) FROM scanner_registry_quarantine_objects
			 WHERE digest = ? AND state = 'deleting'`), image.Digest); err != nil {
			return false, err
		}
		if deleting > 0 {
			return false, fmt.Errorf(
				"release image %s is being deleted from registry quarantine: %w",
				image.Digest, scannerrelease.ErrVersionConflict,
			)
		}
		if err := r.namedExecTx(ctx, tx,
			`INSERT INTO scanner_release_images
			 (id, release_id, image_key, image_kind, registry_target_id, repository, digest,
			  platform_digests_json, size_bytes, signature_status, signature_digest,
			  signature_artifact_uri, signature_artifact_digest, signature_media_type,
			  signature_artifact_size_bytes, signature_certificate_digest, signature_identity,
			  signature_issuer, signature_subject, signature_trust_root, signature_operation_id,
			  provenance_digest, sbom_digest, created_at)
			 VALUES
			 (:id, :release_id, :image_key, :image_kind, :registry_target_id, :repository, :digest,
			  :platform_digests_json, :size_bytes, :signature_status, :signature_digest,
			  :signature_artifact_uri, :signature_artifact_digest, :signature_media_type,
			  :signature_artifact_size_bytes, :signature_certificate_digest, :signature_identity,
			  :signature_issuer, :signature_subject, :signature_trust_root, :signature_operation_id,
			  :provenance_digest, :sbom_digest, :created_at)`, image); err != nil {
			return false, err
		}
	}
	for i := range inventory.Artifacts {
		artifact := &inventory.Artifacts[i]
		if artifact.ID == "" {
			artifact.ID = uuid.NewString()
		}
		artifact.ReleaseID = release.ID
		if artifact.CreatedAt.IsZero() {
			artifact.CreatedAt = release.CreatedAt
		}
		if err := r.insertArtifactTx(ctx, tx, artifact); err != nil {
			return false, err
		}
	}
	if _, err := r.appendEventTx(ctx, tx, "release", release.ID, "release.published", "", string(release.State), command, release.PublishedAt); err != nil {
		return false, err
	}
	return true, nil
}

func (r *scannerReleaseRepository) CommitCandidatePublication(
	ctx context.Context,
	candidateID string,
	expectedVersion int64,
	inventory *scannerrelease.ReleaseInventory,
	command scannerrelease.TransitionCommand,
) (*scannerrelease.Release, error) {
	if inventory == nil || candidateID == "" || inventory.Release.CandidateID != candidateID {
		return nil, scannerrelease.ErrIdempotencyConflict
	}
	prepareReleaseInventory(inventory)
	now := utcNow()
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var current struct {
		State          scannerrelease.CandidateState `db:"state"`
		Version        int64                         `db:"version"`
		PolicyRevision int64                         `db:"policy_revision"`
	}
	if err := tx.GetContext(ctx, &current, r.db.Rebind(
		`SELECT state, version, policy_revision
		 FROM scanner_release_candidates WHERE id = ?`), candidateID); err != nil {
		return nil, err
	}
	if current.State == scannerrelease.CandidatePublished {
		var existing scannerrelease.Release
		if err := tx.GetContext(ctx, &existing, r.db.Rebind(
			`SELECT * FROM scanner_releases WHERE candidate_id = ?`), candidateID); err != nil {
			return nil, err
		}
		if existing.ManifestDigest != inventory.Release.ManifestDigest ||
			existing.Name != inventory.Release.Name {
			return nil, scannerrelease.ErrIdempotencyConflict
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return &existing, nil
	}
	if current.State != scannerrelease.CandidateApproved &&
		current.State != scannerrelease.CandidatePublishing {
		return nil, fmt.Errorf("candidate publication state %s is not eligible", current.State)
	}
	if current.State == scannerrelease.CandidateApproved && current.Version != expectedVersion {
		return nil, scannerrelease.ErrVersionConflict
	}
	if current.State == scannerrelease.CandidateApproved {
		if err := scannerrelease.ValidateCandidateTransition(
			current.State, scannerrelease.CandidatePublishing,
		); err != nil {
			return nil, err
		}
		result, err := r.execTx(ctx, tx,
			`UPDATE scanner_release_candidates
			 SET state = ?, version = version + 1, updated_at = ?
			 WHERE id = ? AND version = ?`,
			scannerrelease.CandidatePublishing, now, candidateID, current.Version)
		if err != nil {
			return nil, err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return nil, scannerrelease.ErrVersionConflict
		}
		publishingCommand := command
		publishingCommand.IdempotencyKey += "/publishing"
		if _, err := r.appendEventTx(
			ctx, tx, "candidate", candidateID, "candidate."+string(scannerrelease.CandidatePublishing),
			string(current.State), string(scannerrelease.CandidatePublishing),
			publishingCommand, now,
		); err != nil {
			return nil, err
		}
		current.State = scannerrelease.CandidatePublishing
		current.Version++
	}
	releaseCommand := command
	releaseCommand.IdempotencyKey += "/release"
	if err := r.reserveReleaseNameTx(ctx, tx, &inventory.Release); err != nil {
		return nil, err
	}
	if _, err := r.insertReleaseInventoryTx(ctx, tx, inventory, releaseCommand); err != nil {
		return nil, err
	}
	if err := scannerrelease.ValidateCandidateTransition(
		current.State, scannerrelease.CandidatePublished,
	); err != nil {
		return nil, err
	}
	result, err := r.execTx(ctx, tx,
		`UPDATE scanner_release_candidates
		 SET state = ?, version = version + 1, updated_at = ?
		 WHERE id = ? AND version = ?`,
		scannerrelease.CandidatePublished, now, candidateID, current.Version)
	if err != nil {
		return nil, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return nil, scannerrelease.ErrVersionConflict
	}
	publishedCommand := command
	publishedCommand.IdempotencyKey += "/published"
	publishedCommand.PayloadJSON = fmt.Sprintf(`{"release_id":%q}`, inventory.Release.ID)
	if _, err := r.appendEventTx(
		ctx, tx, "candidate", candidateID, "candidate."+string(scannerrelease.CandidatePublished),
		string(current.State), string(scannerrelease.CandidatePublished),
		publishedCommand, now,
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	release := inventory.Release
	return &release, nil
}

func (r *scannerReleaseRepository) GetRelease(ctx context.Context, id string) (*scannerrelease.Release, error) {
	var release scannerrelease.Release
	if err := r.get(ctx, &release, `SELECT * FROM scanner_releases WHERE id = ?`, id); err != nil {
		return nil, err
	}
	return &release, nil
}

func (r *scannerReleaseRepository) GetReleaseInventory(ctx context.Context, id string) (*scannerrelease.ReleaseInventory, error) {
	release, err := r.GetRelease(ctx, id)
	if err != nil {
		return nil, err
	}
	inventory := &scannerrelease.ReleaseInventory{Release: *release}
	if err := r.selectRows(ctx, &inventory.Tools,
		`SELECT * FROM scanner_release_tools WHERE release_id = ? ORDER BY tool_key ASC`, id); err != nil {
		return nil, err
	}
	if err := r.selectRows(ctx, &inventory.Images,
		`SELECT * FROM scanner_release_images WHERE release_id = ? ORDER BY image_key ASC, registry_target_id ASC`, id); err != nil {
		return nil, err
	}
	artifacts, err := r.ListArtifacts(ctx, id, "")
	if err != nil {
		return nil, err
	}
	inventory.Artifacts = artifacts
	return inventory, nil
}

func (r *scannerReleaseRepository) ListReleases(ctx context.Context, filter scannerrelease.ReleaseFilter, page scannerrelease.PageRequest) (scannerrelease.ReleasePage, error) {
	var query strings.Builder
	query.WriteString(`SELECT * FROM scanner_releases WHERE 1 = 1`)
	var args []any
	if filter.State != "" {
		query.WriteString(` AND state = ?`)
		args = append(args, filter.State)
	}
	if filter.Protected != nil {
		query.WriteString(` AND protected = ?`)
		args = append(args, *filter.Protected)
	}
	if err := appendCursorCondition(&query, &args, page.Cursor); err != nil {
		return scannerrelease.ReleasePage{}, err
	}
	limit := pageLimit(page.Limit)
	query.WriteString(` ORDER BY created_at DESC, id DESC LIMIT ?`)
	args = append(args, limit+1)
	var items []scannerrelease.Release
	if err := r.selectRows(ctx, &items, query.String(), args...); err != nil {
		return scannerrelease.ReleasePage{}, err
	}
	items, next := pageCursor(items, limit, func(item scannerrelease.Release) (time.Time, string) {
		return item.CreatedAt, item.ID
	})
	return scannerrelease.ReleasePage{Items: items, NextCursor: next}, nil
}

func (r *scannerReleaseRepository) TransitionRelease(ctx context.Context, id string, expectedVersion int64, to scannerrelease.ReleaseState, command scannerrelease.TransitionCommand) (*scannerrelease.Release, error) {
	_, err := r.transitionState(ctx, "scanner_releases", "release", id, expectedVersion, string(to), command,
		func(from, to string) error {
			return scannerrelease.ValidateReleaseTransition(scannerrelease.ReleaseState(from), scannerrelease.ReleaseState(to))
		})
	if err != nil {
		return nil, err
	}
	return r.GetRelease(ctx, id)
}

func (r *scannerReleaseRepository) insertArtifactTx(ctx context.Context, tx *sqlx.Tx, artifact *scannerrelease.ReleaseArtifact) error {
	if artifact.ID == "" {
		artifact.ID = uuid.NewString()
	}
	if artifact.CreatedAt.IsZero() {
		artifact.CreatedAt = utcNow()
	}
	if artifact.RetentionClass == "" {
		artifact.RetentionClass = "evidence"
	}
	if err := r.rejectQuarantineDeletionReferenceTx(ctx, tx, artifact.Digest); err != nil {
		return err
	}
	_, err := r.execTx(ctx, tx,
		`INSERT INTO scanner_release_artifacts
		 (id, release_id, candidate_id, artifact_type, media_type, uri, digest,
		  size_bytes, retention_class, retain_until, protected, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		artifact.ID, nullableString(artifact.ReleaseID), nullableString(artifact.CandidateID),
		artifact.ArtifactType, artifact.MediaType, artifact.URI, artifact.Digest,
		artifact.SizeBytes, artifact.RetentionClass, artifact.RetainUntil,
		artifact.Protected, artifact.CreatedAt)
	return err
}

func (r *scannerReleaseRepository) rejectQuarantineDeletionReferenceTx(
	ctx context.Context,
	tx *sqlx.Tx,
	digest string,
) error {
	if strings.TrimSpace(digest) == "" {
		return nil
	}
	var deleting int
	if err := tx.GetContext(ctx, &deleting, r.db.Rebind(
		`SELECT COUNT(*) FROM scanner_registry_quarantine_objects
		 WHERE digest = ? AND state = 'deleting'`), digest); err != nil {
		return err
	}
	if deleting > 0 {
		return fmt.Errorf(
			"artifact %s is being deleted from registry quarantine: %w",
			digest, scannerrelease.ErrVersionConflict,
		)
	}
	return nil
}

func (r *scannerReleaseRepository) AddArtifact(ctx context.Context, artifact *scannerrelease.ReleaseArtifact) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := r.insertArtifactTx(ctx, tx, artifact); err != nil {
		return err
	}
	return tx.Commit()
}

const artifactColumns = `id, COALESCE(release_id, '') AS release_id,
	COALESCE(candidate_id, '') AS candidate_id, artifact_type, media_type, uri,
	digest, size_bytes, retention_class, retain_until, protected, created_at`

func (r *scannerReleaseRepository) ListArtifacts(ctx context.Context, releaseID, candidateID string) ([]scannerrelease.ReleaseArtifact, error) {
	query := `SELECT ` + artifactColumns + ` FROM scanner_release_artifacts WHERE 1 = 1`
	var args []any
	if releaseID != "" {
		query += ` AND release_id = ?`
		args = append(args, releaseID)
	}
	if candidateID != "" {
		query += ` AND candidate_id = ?`
		args = append(args, candidateID)
	}
	query += ` ORDER BY created_at ASC, id ASC`
	var artifacts []scannerrelease.ReleaseArtifact
	return artifacts, r.selectRows(ctx, &artifacts, query, args...)
}

func (r *scannerReleaseRepository) AddApproval(ctx context.Context, approval *scannerrelease.Approval) error {
	if approval.ID == "" {
		approval.ID = uuid.NewString()
	}
	if approval.IdempotencyKey == "" {
		approval.IdempotencyKey = "approval:" + approval.ID
	}
	if approval.CreatedAt.IsZero() {
		approval.CreatedAt = utcNow()
	}
	result, err := r.db.ExecContext(ctx, r.db.Rebind(
		`INSERT INTO scanner_release_approvals
		 (id, candidate_id, release_id, actor, action, reason, exception_scope,
		  exception_owner_id, compensating_control, evidence_digest,
		  policy_decision, expires_at, idempotency_key, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(idempotency_key) DO NOTHING`),
		approval.ID, nullableString(approval.CandidateID), nullableString(approval.ReleaseID),
		approval.Actor, approval.Action, approval.Reason, approval.ExceptionScope,
		approval.ExceptionOwner, approval.CompensatingControl, approval.EvidenceDigest,
		approval.PolicyDecision, approval.ExpiresAt, approval.IdempotencyKey, approval.CreatedAt)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 1 {
		return nil
	}
	var existing scannerrelease.Approval
	err = r.get(ctx, &existing,
		`SELECT `+approvalColumns+` FROM scanner_release_approvals WHERE idempotency_key = ?`,
		approval.IdempotencyKey)
	if err != nil {
		return err
	}
	if existing.EvidenceDigest != approval.EvidenceDigest ||
		existing.Action != approval.Action ||
		existing.Actor != approval.Actor ||
		existing.Reason != approval.Reason ||
		existing.CandidateID != approval.CandidateID ||
		existing.ReleaseID != approval.ReleaseID ||
		existing.ExceptionScope != approval.ExceptionScope ||
		existing.ExceptionOwner != approval.ExceptionOwner ||
		existing.CompensatingControl != approval.CompensatingControl ||
		existing.PolicyDecision != approval.PolicyDecision ||
		!sameOptionalTime(existing.ExpiresAt, approval.ExpiresAt) {
		return scannerrelease.ErrIdempotencyConflict
	}
	*approval = existing
	return nil
}

func sameOptionalTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

const approvalColumns = `id, COALESCE(candidate_id, '') AS candidate_id,
	COALESCE(release_id, '') AS release_id, actor, action, reason, exception_scope,
	exception_owner_id, compensating_control, evidence_digest, policy_decision,
	expires_at, idempotency_key, created_at`

func (r *scannerReleaseRepository) ListApprovals(ctx context.Context, candidateID, releaseID string) ([]scannerrelease.Approval, error) {
	query := `SELECT ` + approvalColumns + ` FROM scanner_release_approvals WHERE 1 = 1`
	var args []any
	if candidateID != "" {
		query += ` AND candidate_id = ?`
		args = append(args, candidateID)
	}
	if releaseID != "" {
		query += ` AND release_id = ?`
		args = append(args, releaseID)
	}
	query += ` ORDER BY created_at ASC, id ASC`
	var approvals []scannerrelease.Approval
	return approvals, r.selectRows(ctx, &approvals, query, args...)
}

// --- Rollouts and workers ----------------------------------------------------

func (r *scannerReleaseRepository) CreateRollout(ctx context.Context, rollout *scannerrelease.Rollout, cohorts []scannerrelease.RolloutCohort, command scannerrelease.TransitionCommand) error {
	initializeIDAndTimes(&rollout.ID, &rollout.CreatedAt, &rollout.UpdatedAt)
	if rollout.State == "" {
		rollout.State = scannerrelease.RolloutPending
	}
	if rollout.Version <= 0 {
		rollout.Version = 1
	}
	if rollout.IdempotencyKey == "" {
		rollout.IdempotencyKey = command.IdempotencyKey
	}
	rollout.PolicySnapshotJSON = jsonDefault(rollout.PolicySnapshotJSON, "{}")
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := r.execTx(ctx, tx,
		`INSERT INTO scanner_rollouts
		 (id, target, from_release_id, to_release_id, strategy, state,
		  policy_snapshot_json, actor, idempotency_key, rollback_of_rollout_id,
		  error_class, error_detail, version, started_at, completed_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(idempotency_key) DO NOTHING`,
		rollout.ID, rollout.Target, nullableString(rollout.FromReleaseID), rollout.ToReleaseID,
		rollout.Strategy, rollout.State, rollout.PolicySnapshotJSON, rollout.Actor,
		rollout.IdempotencyKey, nullableString(rollout.RollbackOfRolloutID), rollout.ErrorClass,
		rollout.ErrorDetail, rollout.Version, rollout.StartedAt, rollout.CompletedAt,
		rollout.CreatedAt, rollout.UpdatedAt)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		var existing scannerrelease.Rollout
		if err := tx.GetContext(ctx, &existing, r.db.Rebind(
			`SELECT `+rolloutColumns+` FROM scanner_rollouts WHERE idempotency_key = ?`),
			rollout.IdempotencyKey); err != nil {
			return err
		}
		if existing.Target != rollout.Target ||
			existing.ToReleaseID != rollout.ToReleaseID ||
			existing.Strategy != rollout.Strategy {
			return scannerrelease.ErrIdempotencyConflict
		}
		*rollout = existing
		return tx.Commit()
	}
	for i := range cohorts {
		cohort := &cohorts[i]
		initializeIDAndTimes(&cohort.ID, &cohort.CreatedAt, &cohort.UpdatedAt)
		cohort.RolloutID = rollout.ID
		if cohort.DesiredReleaseID == "" {
			cohort.DesiredReleaseID = rollout.ToReleaseID
		}
		if cohort.State == "" {
			cohort.State = "pending"
		}
		if cohort.Version <= 0 {
			cohort.Version = 1
		}
		cohort.HealthSummaryJSON = jsonDefault(cohort.HealthSummaryJSON, "{}")
		_, err := r.execTx(ctx, tx,
			`INSERT INTO scanner_rollout_cohorts
			 (id, rollout_id, cohort_name, ordinal, desired_release_id,
			  observed_release_id, state, total_workers, ready_workers, failed_workers,
			  health_summary_json, deadline, started_at, health_observed_at,
			  completed_at, version, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			cohort.ID, cohort.RolloutID, cohort.Name, cohort.Ordinal, cohort.DesiredReleaseID,
			nullableString(cohort.ObservedReleaseID), cohort.State, cohort.TotalWorkers,
			cohort.ReadyWorkers, cohort.FailedWorkers, cohort.HealthSummaryJSON,
			cohort.Deadline, cohort.StartedAt, cohort.HealthObservedAt,
			cohort.CompletedAt, cohort.Version, cohort.CreatedAt, cohort.UpdatedAt)
		if err != nil {
			return err
		}
	}
	if _, err := r.appendEventTx(ctx, tx, "rollout", rollout.ID, "rollout.created", "", string(rollout.State), command, rollout.CreatedAt); err != nil {
		return err
	}
	return tx.Commit()
}

const rolloutColumns = `id, target, COALESCE(from_release_id, '') AS from_release_id,
	to_release_id, strategy, state, policy_snapshot_json, actor, idempotency_key,
	COALESCE(rollback_of_rollout_id, '') AS rollback_of_rollout_id, error_class,
	error_detail, version, started_at, completed_at, created_at, updated_at`

func (r *scannerReleaseRepository) GetRollout(ctx context.Context, id string) (*scannerrelease.Rollout, error) {
	var rollout scannerrelease.Rollout
	if err := r.get(ctx, &rollout, `SELECT `+rolloutColumns+` FROM scanner_rollouts WHERE id = ?`, id); err != nil {
		return nil, err
	}
	return &rollout, nil
}

func (r *scannerReleaseRepository) ListRollouts(ctx context.Context, filter scannerrelease.RolloutFilter, page scannerrelease.PageRequest) (scannerrelease.RolloutPage, error) {
	var query strings.Builder
	query.WriteString(`SELECT ` + rolloutColumns + ` FROM scanner_rollouts WHERE 1 = 1`)
	var args []any
	if filter.State != "" {
		query.WriteString(` AND state = ?`)
		args = append(args, filter.State)
	}
	if filter.Target != "" {
		query.WriteString(` AND target = ?`)
		args = append(args, filter.Target)
	}
	if err := appendCursorCondition(&query, &args, page.Cursor); err != nil {
		return scannerrelease.RolloutPage{}, err
	}
	limit := pageLimit(page.Limit)
	query.WriteString(` ORDER BY created_at DESC, id DESC LIMIT ?`)
	args = append(args, limit+1)
	var items []scannerrelease.Rollout
	if err := r.selectRows(ctx, &items, query.String(), args...); err != nil {
		return scannerrelease.RolloutPage{}, err
	}
	items, next := pageCursor(items, limit, func(item scannerrelease.Rollout) (time.Time, string) {
		return item.CreatedAt, item.ID
	})
	return scannerrelease.RolloutPage{Items: items, NextCursor: next}, nil
}

func (r *scannerReleaseRepository) TransitionRollout(ctx context.Context, id string, expectedVersion int64, to scannerrelease.RolloutState, command scannerrelease.TransitionCommand) (*scannerrelease.Rollout, error) {
	_, err := r.transitionState(ctx, "scanner_rollouts", "rollout", id, expectedVersion, string(to), command,
		func(from, to string) error {
			return scannerrelease.ValidateRolloutTransition(scannerrelease.RolloutState(from), scannerrelease.RolloutState(to))
		})
	if err != nil {
		return nil, err
	}
	return r.GetRollout(ctx, id)
}

const cohortColumns = `id, rollout_id, cohort_name, ordinal, desired_release_id,
	COALESCE(observed_release_id, '') AS observed_release_id, state, total_workers,
	ready_workers, failed_workers, health_summary_json, deadline, version,
	started_at, health_observed_at, completed_at, created_at, updated_at`

func (r *scannerReleaseRepository) ListRolloutCohorts(ctx context.Context, rolloutID string) ([]scannerrelease.RolloutCohort, error) {
	var cohorts []scannerrelease.RolloutCohort
	return cohorts, r.selectRows(ctx, &cohorts,
		`SELECT `+cohortColumns+` FROM scanner_rollout_cohorts
		 WHERE rollout_id = ? ORDER BY ordinal ASC`, rolloutID)
}

func (r *scannerReleaseRepository) UpdateRolloutCohort(
	ctx context.Context,
	cohort *scannerrelease.RolloutCohort,
	expectedVersion int64,
	command scannerrelease.TransitionCommand,
) error {
	cohort.UpdatedAt = utcNow()
	cohort.HealthSummaryJSON = jsonDefault(cohort.HealthSummaryJSON, "{}")
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var current struct {
		State   string `db:"state"`
		Version int64  `db:"version"`
	}
	if err := tx.GetContext(ctx, &current, r.db.Rebind(
		`SELECT state, version FROM scanner_rollout_cohorts WHERE id = ?`), cohort.ID); err != nil {
		return err
	}
	if current.Version != expectedVersion {
		return scannerrelease.ErrVersionConflict
	}
	result, err := r.execTx(ctx, tx,
		`UPDATE scanner_rollout_cohorts
		 SET desired_release_id = ?, observed_release_id = ?, state = ?,
		     total_workers = ?, ready_workers = ?, failed_workers = ?,
		     health_summary_json = ?, deadline = ?, started_at = ?,
		     health_observed_at = ?, completed_at = ?,
		     version = version + 1, updated_at = ?
		 WHERE id = ? AND version = ?`,
		cohort.DesiredReleaseID, nullableString(cohort.ObservedReleaseID), cohort.State,
		cohort.TotalWorkers, cohort.ReadyWorkers, cohort.FailedWorkers,
		cohort.HealthSummaryJSON, cohort.Deadline, cohort.StartedAt,
		cohort.HealthObservedAt, cohort.CompletedAt, cohort.UpdatedAt,
		cohort.ID, expectedVersion)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return scannerrelease.ErrVersionConflict
	}
	if _, err := r.appendEventTx(
		ctx, tx, "rollout_cohort", cohort.ID, "rollout_cohort.updated",
		current.State, cohort.State, command, cohort.UpdatedAt,
	); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	cohort.Version = expectedVersion + 1
	return nil
}

func (r *scannerReleaseRepository) ClaimNextRollout(
	ctx context.Context,
	workerID string,
	now, leaseUntil time.Time,
) (*scannerrelease.RolloutClaim, error) {
	if workerID == "" || now.IsZero() || !leaseUntil.After(now) {
		return nil, errors.New("invalid scanner rollout claim request")
	}
	now = now.UTC()
	leaseUntil = leaseUntil.UTC()
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var rolloutIDs []string
	if err := tx.SelectContext(ctx, &rolloutIDs, r.db.Rebind(
		`SELECT rollout.id
		 FROM scanner_rollouts AS rollout
		 LEFT JOIN scanner_rollout_claims AS claim ON claim.rollout_id = rollout.id
		 WHERE rollout.state IN (?, ?, ?, ?, ?, ?, ?)
		   AND (
		     claim.rollout_id IS NULL OR
		     (claim.state = ? AND claim.available_at <= ?) OR
		     (claim.state = ? AND claim.lease_expires_at <= ?)
		   )
		 ORDER BY rollout.created_at ASC, rollout.id ASC`),
		scannerrelease.RolloutPending, scannerrelease.RolloutPreparing,
		scannerrelease.RolloutCanary, scannerrelease.RolloutVerifying,
		scannerrelease.RolloutRollingOut, scannerrelease.RolloutRollingBack,
		scannerrelease.RolloutPaused,
		scannerrelease.RolloutClaimReleased, now,
		scannerrelease.RolloutClaimActive, now,
	); err != nil {
		return nil, err
	}
	for _, rolloutID := range rolloutIDs {
		var previous scannerrelease.RolloutClaim
		previousErr := tx.GetContext(ctx, &previous, r.db.Rebind(
			`SELECT * FROM scanner_rollout_claims WHERE rollout_id = ?`), rolloutID)
		if previousErr != nil && !errors.Is(previousErr, sql.ErrNoRows) {
			return nil, previousErr
		}
		token := uuid.NewString()
		result, err := r.execTx(ctx, tx,
			`INSERT INTO scanner_rollout_claims
			 (rollout_id, worker_id, lease_token, state, lease_expires_at,
			  heartbeat_at, available_at, attempt, version, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, 1, 1, ?, ?)
			 ON CONFLICT(rollout_id) DO UPDATE SET
			  worker_id = excluded.worker_id,
			  lease_token = excluded.lease_token,
			  state = excluded.state,
			  lease_expires_at = excluded.lease_expires_at,
			  heartbeat_at = excluded.heartbeat_at,
			  available_at = excluded.available_at,
			  attempt = scanner_rollout_claims.attempt + 1,
			  version = scanner_rollout_claims.version + 1,
			  updated_at = excluded.updated_at
			 WHERE (scanner_rollout_claims.state = ? AND scanner_rollout_claims.available_at <= ?)
			    OR (scanner_rollout_claims.state = ? AND scanner_rollout_claims.lease_expires_at <= ?)`,
			rolloutID, workerID, token, scannerrelease.RolloutClaimActive,
			leaseUntil, now, now, now, now,
			scannerrelease.RolloutClaimReleased, now,
			scannerrelease.RolloutClaimActive, now)
		if err != nil {
			return nil, err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			continue
		}
		var claim scannerrelease.RolloutClaim
		if err := tx.GetContext(ctx, &claim, r.db.Rebind(
			`SELECT * FROM scanner_rollout_claims WHERE rollout_id = ?`), rolloutID); err != nil {
			return nil, err
		}
		eventType := "rollout.claimed"
		reason := "rollout controller claimed reconciliation"
		takeover := previousErr == nil &&
			previous.State == scannerrelease.RolloutClaimActive &&
			!previous.LeaseExpires.After(now)
		claim.Reclaimed = takeover
		if takeover {
			eventType = "rollout.reclaimed"
			reason = "stale rollout controller lease was reclaimed"
		}
		var currentState string
		if err := tx.GetContext(ctx, &currentState, r.db.Rebind(
			`SELECT state FROM scanner_rollouts WHERE id = ?`), rolloutID); err != nil {
			return nil, err
		}
		payload, _ := json.Marshal(map[string]any{
			"attempt":  claim.Attempt,
			"takeover": takeover,
		})
		if _, err := r.appendEventTx(
			ctx, tx, "rollout", rolloutID, eventType, currentState, currentState,
			scannerrelease.TransitionCommand{
				Actor: workerID, Reason: reason,
				IdempotencyKey: "rollout-claim:" + token,
				PayloadJSON:    string(payload),
			}, now,
		); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return &claim, nil
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return nil, nil
}

func (r *scannerReleaseRepository) HeartbeatRollout(
	ctx context.Context,
	rolloutID, workerID, leaseToken string,
	now, leaseUntil time.Time,
) (scannerrelease.RolloutLeaseStatus, error) {
	if rolloutID == "" || workerID == "" || leaseToken == "" ||
		now.IsZero() || !leaseUntil.After(now) {
		return scannerrelease.RolloutLeaseStatus{}, errors.New("invalid scanner rollout heartbeat")
	}
	now = now.UTC()
	leaseUntil = leaseUntil.UTC()
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return scannerrelease.RolloutLeaseStatus{}, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := r.execTx(ctx, tx,
		`UPDATE scanner_rollout_claims
		 SET heartbeat_at = ?, lease_expires_at = ?, version = version + 1, updated_at = ?
		 WHERE rollout_id = ? AND worker_id = ? AND lease_token = ?
		   AND state = ? AND lease_expires_at > ?`,
		now, leaseUntil, now, rolloutID, workerID, leaseToken,
		scannerrelease.RolloutClaimActive, now)
	if err != nil {
		return scannerrelease.RolloutLeaseStatus{}, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		if err := tx.Commit(); err != nil {
			return scannerrelease.RolloutLeaseStatus{}, err
		}
		return scannerrelease.RolloutLeaseStatus{}, nil
	}
	status := scannerrelease.RolloutLeaseStatus{Current: true}
	if err := tx.GetContext(ctx, &status, r.db.Rebind(
		`SELECT 1 AS current, version AS rollout_version, state AS rollout_state
		 FROM scanner_rollouts WHERE id = ?`), rolloutID); err != nil {
		return scannerrelease.RolloutLeaseStatus{}, err
	}
	if err := tx.Commit(); err != nil {
		return scannerrelease.RolloutLeaseStatus{}, err
	}
	return status, nil
}

func (r *scannerReleaseRepository) ReleaseRolloutClaim(
	ctx context.Context,
	rolloutID, workerID, leaseToken string,
	now, availableAt time.Time,
	command scannerrelease.TransitionCommand,
) (bool, error) {
	if rolloutID == "" || workerID == "" || leaseToken == "" || now.IsZero() ||
		availableAt.Before(now) {
		return false, errors.New("invalid scanner rollout claim release")
	}
	now = now.UTC()
	availableAt = availableAt.UTC()
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := r.execTx(ctx, tx,
		`UPDATE scanner_rollout_claims
		 SET state = ?, available_at = ?, lease_expires_at = ?,
		     heartbeat_at = ?, version = version + 1, updated_at = ?
		 WHERE rollout_id = ? AND worker_id = ? AND lease_token = ?
		   AND state = ? AND lease_expires_at > ?`,
		scannerrelease.RolloutClaimReleased, availableAt, now, now, now,
		rolloutID, workerID, leaseToken, scannerrelease.RolloutClaimActive, now)
	if err != nil {
		return false, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		if err := tx.Commit(); err != nil {
			return false, err
		}
		return false, nil
	}
	var currentState string
	if err := tx.GetContext(ctx, &currentState, r.db.Rebind(
		`SELECT state FROM scanner_rollouts WHERE id = ?`), rolloutID); err != nil {
		return false, err
	}
	command.Actor = workerID
	if command.Reason == "" {
		command.Reason = "rollout reconciliation claim released"
	}
	if command.IdempotencyKey == "" {
		command.IdempotencyKey = "rollout-release:" + leaseToken
	}
	payload, _ := json.Marshal(map[string]any{"available_at": availableAt})
	command.PayloadJSON = string(payload)
	if _, err := r.appendEventTx(
		ctx, tx, "rollout", rolloutID, "rollout.claim_released",
		currentState, currentState, command, now,
	); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (r *scannerReleaseRepository) AssignWorkerReleaseStatuses(
	ctx context.Context,
	cohort, desiredReleaseID, operationID string,
	activeAfter, assignedAt time.Time,
) (int64, error) {
	result, err := r.db.ExecContext(ctx, r.db.Rebind(
		`UPDATE scanner_worker_release_status
		 SET desired_release_id = ?,
		     observed_release_id = NULL,
		     cached_digests_json = '[]',
		     verification_state = 'pending',
		     verification_error = '',
		     capabilities_json = '{}',
		     assignment_operation_id = ?,
		     assigned_at = ?,
		     evidence_observed_at = NULL,
		     version = version + 1,
		     updated_at = ?
		 WHERE cohort = ?
		   AND last_heartbeat >= ?
		   AND assignment_operation_id <> ?`),
		desiredReleaseID, operationID, assignedAt, assignedAt,
		cohort, activeAfter, operationID)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (r *scannerReleaseRepository) UpsertWorkerReleaseStatus(ctx context.Context, status *scannerrelease.WorkerReleaseStatus) error {
	now := utcNow()
	if status.CreatedAt.IsZero() {
		status.CreatedAt = now
	}
	status.UpdatedAt = now
	if status.LastHeartbeat.IsZero() {
		status.LastHeartbeat = now
	}
	if status.Version <= 0 {
		status.Version = 1
	}
	status.CachedDigestsJSON = jsonDefault(status.CachedDigestsJSON, "[]")
	status.CapabilitiesJSON = jsonDefault(status.CapabilitiesJSON, "{}")
	_, err := r.db.ExecContext(ctx, r.db.Rebind(
		`INSERT INTO scanner_worker_release_status
		 (worker_id, cohort, desired_release_id, observed_release_id, cached_digests_json,
		  verification_state, verification_error, capabilities_json,
		  assignment_operation_id, assigned_at, evidence_observed_at, version,
		  last_heartbeat, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(worker_id) DO UPDATE SET
		  cohort = excluded.cohort,
		  desired_release_id = excluded.desired_release_id,
		  observed_release_id = excluded.observed_release_id,
		  cached_digests_json = excluded.cached_digests_json,
		  verification_state = excluded.verification_state,
		  verification_error = excluded.verification_error,
		  capabilities_json = excluded.capabilities_json,
		  assignment_operation_id = excluded.assignment_operation_id,
		  assigned_at = excluded.assigned_at,
		  evidence_observed_at = excluded.evidence_observed_at,
		  version = scanner_worker_release_status.version + 1,
		  last_heartbeat = excluded.last_heartbeat,
		  updated_at = excluded.updated_at`),
		status.WorkerID, status.Cohort, nullableString(status.DesiredReleaseID),
		nullableString(status.ObservedReleaseID), status.CachedDigestsJSON,
		status.VerificationState, status.VerificationError, status.CapabilitiesJSON,
		status.AssignmentOperationID, status.AssignedAt, status.EvidenceObservedAt,
		status.Version, status.LastHeartbeat, status.CreatedAt, status.UpdatedAt)
	return err
}

const workerStatusColumns = `worker_id, cohort,
	COALESCE(desired_release_id, '') AS desired_release_id,
	COALESCE(observed_release_id, '') AS observed_release_id,
	cached_digests_json, verification_state, verification_error, capabilities_json,
	assignment_operation_id, assigned_at, evidence_observed_at,
	version, last_heartbeat, created_at, updated_at`

func (r *scannerReleaseRepository) ListWorkerReleaseStatuses(ctx context.Context, cohort string, activeAfter time.Time) ([]scannerrelease.WorkerReleaseStatus, error) {
	query := `SELECT ` + workerStatusColumns + ` FROM scanner_worker_release_status WHERE last_heartbeat >= ?`
	args := []any{activeAfter}
	if cohort != "" {
		query += ` AND cohort = ?`
		args = append(args, cohort)
	}
	query += ` ORDER BY cohort ASC, worker_id ASC`
	var statuses []scannerrelease.WorkerReleaseStatus
	return statuses, r.selectRows(ctx, &statuses, query, args...)
}

// --- Event stream and schedule leases ---------------------------------------

func (r *scannerReleaseRepository) ListEvents(ctx context.Context, aggregateType, aggregateID string, afterSequence int64, limit int) ([]scannerrelease.Event, error) {
	limit = pageLimit(limit)
	var events []scannerrelease.Event
	return events, r.selectRows(ctx, &events,
		`SELECT * FROM scanner_release_events
		 WHERE aggregate_type = ? AND aggregate_id = ? AND sequence > ?
		 ORDER BY sequence ASC LIMIT ?`,
		aggregateType, aggregateID, afterSequence, limit)
}

func (r *scannerReleaseRepository) ListAllEvents(
	ctx context.Context,
	filter scannerrelease.EventFilter,
	page scannerrelease.PageRequest,
) (scannerrelease.EventPage, error) {
	if filter.TraceID != "" && !scannertrace.ValidTraceID(filter.TraceID) {
		return scannerrelease.EventPage{}, errors.New("invalid scanner event trace ID")
	}
	if filter.OperationID != "" && !scannertrace.ValidOperationID(filter.OperationID) {
		return scannerrelease.EventPage{}, errors.New("invalid scanner event operation ID")
	}
	var query strings.Builder
	query.WriteString(`SELECT * FROM scanner_release_events WHERE 1 = 1`)
	var args []any
	if filter.AggregateType != "" {
		query.WriteString(` AND aggregate_type = ?`)
		args = append(args, filter.AggregateType)
	}
	if filter.EventType != "" {
		query.WriteString(` AND event_type = ?`)
		args = append(args, filter.EventType)
	}
	if filter.Actor != "" {
		query.WriteString(` AND actor = ?`)
		args = append(args, filter.Actor)
	}
	if filter.TraceID != "" {
		query.WriteString(` AND trace_id = ?`)
		args = append(args, filter.TraceID)
	}
	if filter.OperationID != "" {
		query.WriteString(` AND operation_id = ?`)
		args = append(args, filter.OperationID)
	}
	if err := appendCursorCondition(&query, &args, page.Cursor); err != nil {
		return scannerrelease.EventPage{}, err
	}
	limit := pageLimit(page.Limit)
	query.WriteString(` ORDER BY created_at DESC, id DESC LIMIT ?`)
	args = append(args, limit+1)
	var items []scannerrelease.Event
	if err := r.selectRows(ctx, &items, query.String(), args...); err != nil {
		return scannerrelease.EventPage{}, err
	}
	items, next := pageCursor(items, limit, func(item scannerrelease.Event) (time.Time, string) {
		return item.CreatedAt, item.ID
	})
	return scannerrelease.EventPage{Items: items, NextCursor: next}, nil
}

func (r *scannerReleaseRepository) GetOperationCorrelation(
	ctx context.Context,
	aggregateType, aggregateID string,
) (*scannerrelease.OperationCorrelation, error) {
	if strings.TrimSpace(aggregateType) == "" || strings.TrimSpace(aggregateID) == "" {
		return nil, errors.New("scanner operation aggregate type and ID are required")
	}
	var correlation scannerrelease.OperationCorrelation
	if err := r.get(ctx, &correlation,
		`SELECT * FROM scanner_operation_correlations
		 WHERE aggregate_type = ? AND aggregate_id = ?`,
		aggregateType, aggregateID); err != nil {
		return nil, err
	}
	return &correlation, nil
}

func (r *scannerReleaseRepository) AcquireScheduleLease(
	ctx context.Context,
	scheduleKey, periodKey, owner string,
	now, leaseExpires time.Time,
) (*scannerrelease.ScheduleLease, bool, error) {
	if scheduleKey == "" || periodKey == "" || owner == "" || !leaseExpires.After(now) {
		return nil, false, errors.New("invalid scanner schedule lease request")
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback() }()
	token := uuid.NewString()
	result, err := r.execTx(ctx, tx,
		`INSERT INTO scanner_schedule_leases
		 (schedule_key, period_key, owner, lease_token, state, lease_expires_at,
		  heartbeat_at, version, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
		 ON CONFLICT(schedule_key, period_key) DO NOTHING`,
		scheduleKey, periodKey, owner, token, scannerrelease.LeaseActive,
		leaseExpires, now, now, now)
	if err != nil {
		return nil, false, err
	}
	affected, _ := result.RowsAffected()
	acquired := affected == 1
	if !acquired {
		result, err = r.execTx(ctx, tx,
			`UPDATE scanner_schedule_leases
			 SET owner = ?, lease_token = ?, state = ?, lease_expires_at = ?,
			     heartbeat_at = ?, completed_at = NULL, result_ref = '',
			     version = version + 1, updated_at = ?
			 WHERE schedule_key = ? AND period_key = ? AND state = ?
			   AND lease_expires_at <= ?`,
			owner, token, scannerrelease.LeaseActive, leaseExpires, now, now,
			scheduleKey, periodKey, scannerrelease.LeaseActive, now)
		if err != nil {
			return nil, false, err
		}
		affected, _ = result.RowsAffected()
		acquired = affected == 1
	}
	var lease scannerrelease.ScheduleLease
	if err := tx.GetContext(ctx, &lease, r.db.Rebind(
		`SELECT * FROM scanner_schedule_leases WHERE schedule_key = ? AND period_key = ?`),
		scheduleKey, periodKey); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return &lease, acquired, nil
}

func (r *scannerReleaseRepository) HeartbeatScheduleLease(
	ctx context.Context,
	scheduleKey, periodKey, owner, token string,
	now, leaseExpires time.Time,
) (bool, error) {
	if !leaseExpires.After(now) {
		return false, errors.New("lease expiration must be in the future")
	}
	result, err := r.db.ExecContext(ctx, r.db.Rebind(
		`UPDATE scanner_schedule_leases
		 SET heartbeat_at = ?, lease_expires_at = ?, version = version + 1, updated_at = ?
		 WHERE schedule_key = ? AND period_key = ? AND owner = ? AND lease_token = ?
		   AND state = ? AND lease_expires_at > ?`),
		now, leaseExpires, now, scheduleKey, periodKey, owner, token,
		scannerrelease.LeaseActive, now)
	if err != nil {
		return false, err
	}
	affected, _ := result.RowsAffected()
	return affected == 1, nil
}

func (r *scannerReleaseRepository) CompleteScheduleLease(
	ctx context.Context,
	scheduleKey, periodKey, owner, token string,
	state scannerrelease.LeaseState,
	resultRef string,
	now time.Time,
) (bool, error) {
	if state != scannerrelease.LeaseCompleted && state != scannerrelease.LeaseFailed {
		return false, errors.New("schedule lease completion state must be completed or failed")
	}
	result, err := r.db.ExecContext(ctx, r.db.Rebind(
		`UPDATE scanner_schedule_leases
		 SET state = ?, completed_at = ?, result_ref = ?, version = version + 1, updated_at = ?
		 WHERE schedule_key = ? AND period_key = ? AND owner = ? AND lease_token = ?
		   AND state = ? AND lease_expires_at > ?`),
		state, now, resultRef, now, scheduleKey, periodKey, owner, token,
		scannerrelease.LeaseActive, now)
	if err != nil {
		return false, err
	}
	affected, _ := result.RowsAffected()
	return affected == 1, nil
}

func (r *scannerReleaseRepository) GetScheduleLease(ctx context.Context, scheduleKey, periodKey string) (*scannerrelease.ScheduleLease, error) {
	var lease scannerrelease.ScheduleLease
	if err := r.get(ctx, &lease,
		`SELECT * FROM scanner_schedule_leases WHERE schedule_key = ? AND period_key = ?`,
		scheduleKey, periodKey); err != nil {
		return nil, err
	}
	return &lease, nil
}
