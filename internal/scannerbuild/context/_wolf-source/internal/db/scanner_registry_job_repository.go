package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

const registryJobColumns = `id, registry_target_id,
	COALESCE(source_registry_target_id, '') AS source_registry_target_id,
	COALESCE(release_id, '') AS release_id, job_kind, re_sign_policy, state,
	actor, reason, idempotency_key, attempt, max_attempts, available_at,
	worker_id, lease_token, lease_expires_at, heartbeat_at, summary_json,
	error_class, error_detail, version, started_at, completed_at,
	dead_lettered_at, created_at, updated_at`

func validateRegistryJob(job *scannerrelease.RegistryJob) error {
	if job == nil || strings.TrimSpace(job.RegistryTargetID) == "" ||
		strings.TrimSpace(job.Actor) == "" || strings.TrimSpace(job.Reason) == "" ||
		strings.TrimSpace(job.IdempotencyKey) == "" {
		return errors.New("registry job requires target, actor, reason, and idempotency key")
	}
	if len(job.Actor) > 320 || len(job.Reason) > 2048 || len(job.IdempotencyKey) > 200 {
		return errors.New("registry job actor, reason, or idempotency key exceeds its bounded size")
	}
	if job.MaxAttempts > 20 {
		return errors.New("registry job max attempts cannot exceed 20")
	}
	switch job.Kind {
	case scannerrelease.RegistryJobReconcile:
		if job.ReleaseID == "" {
			return errors.New("registry reconciliation requires a release")
		}
	case scannerrelease.RegistryJobRepair:
		if job.ReleaseID == "" || job.SourceRegistryTargetID == "" {
			return errors.New("registry repair requires a release and source registry")
		}
		if job.SourceRegistryTargetID == job.RegistryTargetID {
			return errors.New("registry repair source and destination must differ")
		}
	case scannerrelease.RegistryJobCleanup:
		if job.ReleaseID != "" || job.SourceRegistryTargetID != "" {
			return errors.New("registry cleanup cannot be scoped to a release or source registry")
		}
	default:
		return errors.New("invalid registry job kind")
	}
	switch job.ReSignPolicy {
	case "", scannerrelease.RegistryReSignPreserve:
		job.ReSignPolicy = scannerrelease.RegistryReSignPreserve
	case scannerrelease.RegistryReSignRequired, scannerrelease.RegistryReSignForbidden:
	default:
		return errors.New("invalid registry re-sign policy")
	}
	return nil
}

func (r *scannerReleaseRepository) CreateRegistryJob(
	ctx context.Context,
	job *scannerrelease.RegistryJob,
	command scannerrelease.TransitionCommand,
) error {
	if err := validateRegistryJob(job); err != nil {
		return err
	}
	initializeIDAndTimes(&job.ID, &job.CreatedAt, &job.UpdatedAt)
	if job.State == "" {
		job.State = scannerrelease.RegistryJobQueued
	}
	if job.State != scannerrelease.RegistryJobQueued {
		return errors.New("new registry jobs must be queued")
	}
	if job.MaxAttempts <= 0 {
		job.MaxAttempts = 5
	}
	if job.AvailableAt.IsZero() {
		job.AvailableAt = job.CreatedAt
	}
	job.AvailableAt = job.AvailableAt.UTC()
	job.SummaryJSON = jsonDefault(job.SummaryJSON, "{}")
	if !json.Valid([]byte(job.SummaryJSON)) {
		return errors.New("registry job summary must be valid JSON")
	}
	job.Version = 1
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var existing scannerrelease.RegistryJob
	err = tx.GetContext(ctx, &existing, r.db.Rebind(
		`SELECT `+registryJobColumns+` FROM scanner_registry_jobs WHERE idempotency_key = ?`),
		job.IdempotencyKey)
	if err == nil {
		if existing.RegistryTargetID != job.RegistryTargetID ||
			existing.SourceRegistryTargetID != job.SourceRegistryTargetID ||
			existing.ReleaseID != job.ReleaseID || existing.Kind != job.Kind ||
			existing.ReSignPolicy != job.ReSignPolicy {
			return scannerrelease.ErrIdempotencyConflict
		}
		*job = existing
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = r.execTx(ctx, tx,
		`INSERT INTO scanner_registry_jobs
		 (id, registry_target_id, source_registry_target_id, release_id, job_kind,
		  re_sign_policy, state, actor, reason, idempotency_key, attempt,
		  max_attempts, available_at, worker_id, lease_token, summary_json,
		  error_class, error_detail, version, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', '', ?, '', '', 1, ?, ?)`,
		job.ID, job.RegistryTargetID, nullableString(job.SourceRegistryTargetID),
		nullableString(job.ReleaseID), job.Kind, job.ReSignPolicy, job.State,
		job.Actor, job.Reason, job.IdempotencyKey, 0, job.MaxAttempts,
		job.AvailableAt, job.SummaryJSON, job.CreatedAt, job.UpdatedAt)
	if err != nil {
		return err
	}
	command.Actor = job.Actor
	command.Reason = job.Reason
	command.IdempotencyKey = "registry-job-created:" + job.IdempotencyKey
	command.PayloadJSON = fmt.Sprintf(
		`{"kind":%q,"registry_target_id":%q,"release_id":%q}`,
		job.Kind, job.RegistryTargetID, job.ReleaseID,
	)
	if _, err := r.appendEventTx(
		ctx, tx, "registry_job", job.ID, "registry_job.queued", "",
		string(job.State), command, job.CreatedAt,
	); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *scannerReleaseRepository) GetRegistryJob(
	ctx context.Context,
	id string,
) (*scannerrelease.RegistryJob, error) {
	var job scannerrelease.RegistryJob
	if err := r.get(ctx, &job,
		`SELECT `+registryJobColumns+` FROM scanner_registry_jobs WHERE id = ?`, id); err != nil {
		return nil, err
	}
	return &job, nil
}

func (r *scannerReleaseRepository) ListRegistryJobs(
	ctx context.Context,
	filter scannerrelease.RegistryJobFilter,
	limit int,
) ([]scannerrelease.RegistryJob, error) {
	var query strings.Builder
	query.WriteString(`SELECT ` + registryJobColumns + ` FROM scanner_registry_jobs WHERE 1 = 1`)
	var args []any
	if filter.RegistryTargetID != "" {
		query.WriteString(` AND registry_target_id = ?`)
		args = append(args, filter.RegistryTargetID)
	}
	if filter.ReleaseID != "" {
		query.WriteString(` AND release_id = ?`)
		args = append(args, filter.ReleaseID)
	}
	if filter.State != "" {
		query.WriteString(` AND state = ?`)
		args = append(args, filter.State)
	}
	if filter.Kind != "" {
		query.WriteString(` AND job_kind = ?`)
		args = append(args, filter.Kind)
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	query.WriteString(` ORDER BY created_at DESC, id DESC LIMIT ?`)
	args = append(args, limit)
	var jobs []scannerrelease.RegistryJob
	return jobs, r.selectRows(ctx, &jobs, query.String(), args...)
}

func (r *scannerReleaseRepository) ClaimNextRegistryJob(
	ctx context.Context,
	workerID string,
	now, leaseUntil time.Time,
) (*scannerrelease.RegistryJob, error) {
	if workerID == "" || now.IsZero() || !leaseUntil.After(now) {
		return nil, errors.New("invalid registry job claim")
	}
	now, leaseUntil = now.UTC(), leaseUntil.UTC()
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var ids []string
	if err := tx.SelectContext(ctx, &ids, r.db.Rebind(
		`SELECT id FROM scanner_registry_jobs
		 WHERE state IN (?, ?) AND available_at <= ? AND attempt < max_attempts
		 ORDER BY available_at ASC, created_at ASC, id ASC LIMIT 100`),
		scannerrelease.RegistryJobQueued, scannerrelease.RegistryJobRetry, now); err != nil {
		return nil, err
	}
	for _, id := range ids {
		var current scannerrelease.RegistryJob
		if err := tx.GetContext(ctx, &current, r.db.Rebind(
			`SELECT `+registryJobColumns+` FROM scanner_registry_jobs WHERE id = ?`), id); err != nil {
			return nil, err
		}
		token := uuid.NewString()
		result, err := r.execTx(ctx, tx,
			`UPDATE scanner_registry_jobs
			 SET state = ?, worker_id = ?, lease_token = ?, lease_expires_at = ?,
			     heartbeat_at = ?, attempt = attempt + 1,
			     started_at = COALESCE(started_at, ?), error_class = '',
			     error_detail = '', version = version + 1, updated_at = ?
			 WHERE id = ? AND version = ? AND state IN (?, ?)
			   AND available_at <= ? AND attempt < max_attempts`,
			scannerrelease.RegistryJobClaimed, workerID, token, leaseUntil, now,
			now, now, id, current.Version, scannerrelease.RegistryJobQueued,
			scannerrelease.RegistryJobRetry, now)
		if err != nil {
			return nil, err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			continue
		}
		var claimed scannerrelease.RegistryJob
		if err := tx.GetContext(ctx, &claimed, r.db.Rebind(
			`SELECT `+registryJobColumns+` FROM scanner_registry_jobs WHERE id = ?`), id); err != nil {
			return nil, err
		}
		if _, err := r.appendEventTx(
			ctx, tx, "registry_job", id, "registry_job.claimed",
			string(current.State), string(claimed.State),
			scannerrelease.TransitionCommand{
				Actor: workerID, Reason: "registry job claimed",
				IdempotencyKey: "registry-job-claim:" + token,
				PayloadJSON:    fmt.Sprintf(`{"attempt":%d}`, claimed.Attempt),
			}, now,
		); err != nil {
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

func (r *scannerReleaseRepository) HeartbeatRegistryJob(
	ctx context.Context,
	id, workerID, token string,
	now, leaseUntil time.Time,
) (scannerrelease.RegistryJobLeaseStatus, error) {
	if id == "" || workerID == "" || token == "" || now.IsZero() ||
		!leaseUntil.After(now) {
		return scannerrelease.RegistryJobLeaseStatus{}, errors.New("invalid registry job heartbeat")
	}
	now, leaseUntil = now.UTC(), leaseUntil.UTC()
	result, err := r.db.ExecContext(ctx, r.db.Rebind(
		`UPDATE scanner_registry_jobs
		 SET heartbeat_at = ?, lease_expires_at = ?, version = version + 1, updated_at = ?
		 WHERE id = ? AND worker_id = ? AND lease_token = ? AND state = ?
		   AND lease_expires_at > ?`),
		now, leaseUntil, now, id, workerID, token, scannerrelease.RegistryJobClaimed, now)
	if err != nil {
		return scannerrelease.RegistryJobLeaseStatus{}, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return scannerrelease.RegistryJobLeaseStatus{}, nil
	}
	var status scannerrelease.RegistryJobLeaseStatus
	if err := r.get(ctx, &status,
		`SELECT 1 AS current, state, version FROM scanner_registry_jobs WHERE id = ?`,
		id); err != nil {
		return scannerrelease.RegistryJobLeaseStatus{}, err
	}
	return status, nil
}

func (r *scannerReleaseRepository) FinalizeRegistryJob(
	ctx context.Context,
	id, workerID, token string,
	target scannerrelease.RegistryJobState,
	availableAt time.Time,
	summaryJSON, errorClass, errorDetail string,
	now time.Time,
) (*scannerrelease.RegistryJob, error) {
	if id == "" || workerID == "" || token == "" || now.IsZero() {
		return nil, errors.New("invalid registry job finalization")
	}
	switch target {
	case scannerrelease.RegistryJobCompleted, scannerrelease.RegistryJobRetry,
		scannerrelease.RegistryJobDeadLetter, scannerrelease.RegistryJobCancelled:
	default:
		return nil, errors.New("invalid registry job final state")
	}
	now = now.UTC()
	if target == scannerrelease.RegistryJobRetry {
		if availableAt.Before(now) {
			return nil, errors.New("registry job retry cannot be available in the past")
		}
		availableAt = availableAt.UTC()
	} else {
		availableAt = now
	}
	summaryJSON = jsonDefault(summaryJSON, "{}")
	if !json.Valid([]byte(summaryJSON)) {
		return nil, errors.New("registry job summary must be valid JSON")
	}
	errorClass = boundedRegistryJobError(errorClass)
	errorDetail = boundedRegistryJobError(errorDetail)
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var current scannerrelease.RegistryJob
	if err := tx.GetContext(ctx, &current, r.db.Rebind(
		`SELECT `+registryJobColumns+` FROM scanner_registry_jobs WHERE id = ?`), id); err != nil {
		return nil, err
	}
	if current.State != scannerrelease.RegistryJobClaimed ||
		current.WorkerID != workerID || current.LeaseToken != token ||
		current.LeaseExpiresAt == nil || !current.LeaseExpiresAt.After(now) {
		return nil, scannerrelease.ErrLeaseNotOwned
	}
	var completedAt, deadLetteredAt *time.Time
	if target == scannerrelease.RegistryJobCompleted || target == scannerrelease.RegistryJobCancelled {
		completedAt = &now
	}
	if target == scannerrelease.RegistryJobDeadLetter {
		deadLetteredAt = &now
	}
	result, err := r.execTx(ctx, tx,
		`UPDATE scanner_registry_jobs
		 SET state = ?, available_at = ?, worker_id = '', lease_token = '',
		     lease_expires_at = NULL, heartbeat_at = NULL, summary_json = ?,
		     error_class = ?, error_detail = ?, completed_at = ?,
		     dead_lettered_at = ?, version = version + 1, updated_at = ?
		 WHERE id = ? AND version = ? AND worker_id = ? AND lease_token = ?
		   AND state = ? AND lease_expires_at > ?`,
		target, availableAt, summaryJSON, errorClass, errorDetail, completedAt,
		deadLetteredAt, now, id, current.Version, workerID, token,
		scannerrelease.RegistryJobClaimed, now)
	if err != nil {
		return nil, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return nil, scannerrelease.ErrLeaseNotOwned
	}
	if _, err := r.appendEventTx(
		ctx, tx, "registry_job", id, "registry_job."+string(target),
		string(current.State), string(target),
		scannerrelease.TransitionCommand{
			Actor: workerID, Reason: "registry job " + string(target),
			IdempotencyKey: "registry-job-" + string(target) + ":" + token,
			PayloadJSON:    summaryJSON,
		}, now,
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return r.GetRegistryJob(ctx, id)
}

func (r *scannerReleaseRepository) ReclaimStaleRegistryJobs(
	ctx context.Context,
	now time.Time,
) (scannerrelease.RegistryJobReclaimSummary, error) {
	if now.IsZero() {
		return scannerrelease.RegistryJobReclaimSummary{}, errors.New("registry job reclaim time is required")
	}
	now = now.UTC()
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return scannerrelease.RegistryJobReclaimSummary{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var stale []scannerrelease.RegistryJob
	if err := tx.SelectContext(ctx, &stale, r.db.Rebind(
		`SELECT `+registryJobColumns+` FROM scanner_registry_jobs
		 WHERE state = ? AND lease_expires_at <= ?
		 ORDER BY lease_expires_at ASC, id ASC LIMIT 100`),
		scannerrelease.RegistryJobClaimed, now); err != nil {
		return scannerrelease.RegistryJobReclaimSummary{}, err
	}
	var summary scannerrelease.RegistryJobReclaimSummary
	for _, current := range stale {
		target := scannerrelease.RegistryJobRetry
		var deadLetteredAt *time.Time
		if current.Attempt >= current.MaxAttempts {
			target = scannerrelease.RegistryJobDeadLetter
			deadLetteredAt = &now
		}
		result, err := r.execTx(ctx, tx,
			`UPDATE scanner_registry_jobs
			 SET state = ?, available_at = ?, worker_id = '', lease_token = '',
			     lease_expires_at = NULL, heartbeat_at = NULL,
			     dead_lettered_at = ?, error_class = 'worker_lost',
			     error_detail = 'registry worker lease expired',
			     version = version + 1, updated_at = ?
			 WHERE id = ? AND version = ? AND state = ? AND lease_expires_at <= ?`,
			target, now, deadLetteredAt, now, current.ID, current.Version,
			scannerrelease.RegistryJobClaimed, now)
		if err != nil {
			return scannerrelease.RegistryJobReclaimSummary{}, err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			continue
		}
		if target == scannerrelease.RegistryJobDeadLetter {
			summary.DeadLettered++
		} else {
			summary.Retried++
		}
		if _, err := r.appendEventTx(
			ctx, tx, "registry_job", current.ID, "registry_job."+string(target),
			string(current.State), string(target),
			scannerrelease.TransitionCommand{
				Actor:          "registry-job-reclaimer",
				Reason:         "expired registry job lease recovered",
				IdempotencyKey: fmt.Sprintf("registry-job-reclaim:%s:%d", current.ID, current.Version),
				PayloadJSON:    `{"error_class":"worker_lost"}`,
			}, now,
		); err != nil {
			return scannerrelease.RegistryJobReclaimSummary{}, err
		}
	}
	return summary, tx.Commit()
}

func (r *scannerReleaseRepository) RetryDeadLetterRegistryJob(
	ctx context.Context,
	id string,
	expectedVersion int64,
	command scannerrelease.TransitionCommand,
	now time.Time,
) (*scannerrelease.RegistryJob, error) {
	if id == "" || expectedVersion <= 0 || now.IsZero() ||
		command.Actor == "" || command.Reason == "" || command.IdempotencyKey == "" {
		return nil, errors.New("registry job retry requires ID, version, actor, reason, and idempotency key")
	}
	now = now.UTC()
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var current scannerrelease.RegistryJob
	if err := tx.GetContext(ctx, &current, r.db.Rebind(
		`SELECT `+registryJobColumns+` FROM scanner_registry_jobs WHERE id = ?`), id); err != nil {
		return nil, err
	}
	if current.Version != expectedVersion {
		return nil, scannerrelease.ErrVersionConflict
	}
	if current.State != scannerrelease.RegistryJobDeadLetter {
		return nil, errors.New("only dead-letter registry jobs can be retried")
	}
	result, err := r.execTx(ctx, tx,
		`UPDATE scanner_registry_jobs
		 SET state = ?, attempt = 0, available_at = ?, dead_lettered_at = NULL,
		     error_class = '', error_detail = '', version = version + 1, updated_at = ?
		 WHERE id = ? AND version = ? AND state = ?`,
		scannerrelease.RegistryJobRetry, now, now, id, expectedVersion,
		scannerrelease.RegistryJobDeadLetter)
	if err != nil {
		return nil, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return nil, scannerrelease.ErrVersionConflict
	}
	if _, err := r.appendEventTx(
		ctx, tx, "registry_job", id, "registry_job.retry",
		string(current.State), string(scannerrelease.RegistryJobRetry), command, now,
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return r.GetRegistryJob(ctx, id)
}

func boundedRegistryJobError(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 2048 {
		return value[:2048]
	}
	return value
}

func (r *scannerReleaseRepository) UpsertRegistryImageObservation(
	ctx context.Context,
	observation *scannerrelease.RegistryImageObservation,
) error {
	if observation == nil || observation.JobID == "" || observation.ImageKey == "" ||
		observation.DestinationReference == "" || observation.ExpectedDigest == "" ||
		observation.CheckedAt.IsZero() {
		return errors.New("registry image observation is incomplete")
	}
	initializeIDAndTimes(&observation.ID, &observation.CreatedAt, &observation.UpdatedAt)
	observation.DetailJSON = jsonDefault(observation.DetailJSON, "{}")
	if !json.Valid([]byte(observation.DetailJSON)) {
		return errors.New("registry image observation detail must be valid JSON")
	}
	_, err := r.db.NamedExecContext(ctx, r.db.Rebind(
		`INSERT INTO scanner_registry_image_observations
		 (id, job_id, image_key, source_reference, destination_reference,
		  expected_digest, source_digest, destination_digest,
		  expected_signature_digest, destination_signature_digest,
		  expected_provenance_digest, destination_provenance_digest,
		  expected_sbom_digest, destination_sbom_digest, state, detail_json,
		  checked_at, created_at, updated_at)
		 VALUES
		 (:id, :job_id, :image_key, :source_reference, :destination_reference,
		  :expected_digest, :source_digest, :destination_digest,
		  :expected_signature_digest, :destination_signature_digest,
		  :expected_provenance_digest, :destination_provenance_digest,
		  :expected_sbom_digest, :destination_sbom_digest, :state, :detail_json,
		  :checked_at, :created_at, :updated_at)
		 ON CONFLICT(job_id, image_key) DO UPDATE SET
		  source_reference = excluded.source_reference,
		  destination_reference = excluded.destination_reference,
		  expected_digest = excluded.expected_digest,
		  source_digest = excluded.source_digest,
		  destination_digest = excluded.destination_digest,
		  expected_signature_digest = excluded.expected_signature_digest,
		  destination_signature_digest = excluded.destination_signature_digest,
		  expected_provenance_digest = excluded.expected_provenance_digest,
		  destination_provenance_digest = excluded.destination_provenance_digest,
		  expected_sbom_digest = excluded.expected_sbom_digest,
		  destination_sbom_digest = excluded.destination_sbom_digest,
		  state = excluded.state, detail_json = excluded.detail_json,
		  checked_at = excluded.checked_at, updated_at = excluded.updated_at`),
		observation)
	return err
}

func (r *scannerReleaseRepository) ListRegistryImageObservations(
	ctx context.Context,
	jobID string,
) ([]scannerrelease.RegistryImageObservation, error) {
	var observations []scannerrelease.RegistryImageObservation
	return observations, r.selectRows(ctx, &observations,
		`SELECT * FROM scanner_registry_image_observations
		 WHERE job_id = ? ORDER BY image_key ASC`, jobID)
}

func (r *scannerReleaseRepository) UpsertRegistryQuarantineObject(
	ctx context.Context,
	object *scannerrelease.RegistryQuarantineObject,
) error {
	if object == nil || object.RegistryTargetID == "" || object.Repository == "" ||
		object.Digest == "" || object.ObjectKind == "" || object.DiscoveredAt.IsZero() {
		return errors.New("registry quarantine object is incomplete")
	}
	if object.State == "" {
		object.State = "quarantined"
	}
	if object.RetentionClass == "" {
		object.RetentionClass = "quarantine"
	}
	initializeIDAndTimes(&object.ID, &object.CreatedAt, &object.UpdatedAt)
	object.MetadataJSON = jsonDefault(object.MetadataJSON, "{}")
	if !json.Valid([]byte(object.MetadataJSON)) {
		return errors.New("registry quarantine metadata must be valid JSON")
	}
	_, err := r.db.ExecContext(ctx, r.db.Rebind(
		`INSERT INTO scanner_registry_quarantine_objects
		 (id, registry_target_id, candidate_id, repository, digest, object_kind,
		  state, protected, retention_class, retain_until, discovered_at,
		  last_referenced_at, error_detail, metadata_json, version, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
		 ON CONFLICT(registry_target_id, repository, digest, object_kind) DO UPDATE SET
		  candidate_id = COALESCE(scanner_registry_quarantine_objects.candidate_id, excluded.candidate_id),
		  state = CASE
		    WHEN scanner_registry_quarantine_objects.state IN ('deleting', 'promoted', 'retained')
		      THEN scanner_registry_quarantine_objects.state
		    WHEN excluded.state = 'quarantined' THEN excluded.state
		    ELSE scanner_registry_quarantine_objects.state END,
		  protected = scanner_registry_quarantine_objects.protected OR excluded.protected,
		  retain_until = CASE
		    WHEN scanner_registry_quarantine_objects.retain_until IS NULL THEN excluded.retain_until
		    WHEN excluded.retain_until IS NULL THEN scanner_registry_quarantine_objects.retain_until
		    WHEN scanner_registry_quarantine_objects.retain_until > excluded.retain_until
		      THEN scanner_registry_quarantine_objects.retain_until
		    ELSE excluded.retain_until END,
		  metadata_json = excluded.metadata_json, version = scanner_registry_quarantine_objects.version + 1,
		  updated_at = excluded.updated_at`),
		object.ID, object.RegistryTargetID, nullableString(object.CandidateID),
		object.Repository, object.Digest, object.ObjectKind, object.State,
		object.Protected, object.RetentionClass, object.RetainUntil,
		object.DiscoveredAt, object.LastReferencedAt, object.ErrorDetail,
		object.MetadataJSON, object.CreatedAt, object.UpdatedAt)
	return err
}

const quarantineColumns = `id, registry_target_id,
	COALESCE(candidate_id, '') AS candidate_id, repository, digest, object_kind,
	state, protected, retention_class, retain_until, discovered_at,
	last_referenced_at, deletion_worker_id, deletion_lease_token,
	deletion_lease_expires_at, deletion_verified_at, error_detail, metadata_json,
	version, created_at, updated_at`

func (r *scannerReleaseRepository) ListRegistryQuarantineObjects(
	ctx context.Context,
	registryTargetID, state string,
	limit int,
) ([]scannerrelease.RegistryQuarantineObject, error) {
	var query strings.Builder
	query.WriteString(`SELECT ` + quarantineColumns + ` FROM scanner_registry_quarantine_objects WHERE 1 = 1`)
	var args []any
	if registryTargetID != "" {
		query.WriteString(` AND registry_target_id = ?`)
		args = append(args, registryTargetID)
	}
	if state != "" {
		query.WriteString(` AND state = ?`)
		args = append(args, state)
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query.WriteString(` ORDER BY discovered_at ASC, id ASC LIMIT ?`)
	args = append(args, limit)
	var objects []scannerrelease.RegistryQuarantineObject
	return objects, r.selectRows(ctx, &objects, query.String(), args...)
}

// AuthorizeRegistryQuarantineDeletion is deliberately conservative. Any
// immutable release image or evidence/build reference blocks deletion,
// regardless of current release state. This is stronger than checking only
// protected/rollback releases and closes accidental cleanup of historical
// rollback material.
func (r *scannerReleaseRepository) AuthorizeRegistryQuarantineDeletion(
	ctx context.Context,
	id, workerID string,
	now, leaseUntil time.Time,
) (*scannerrelease.RegistryQuarantineObject, scannerrelease.RegistryQuarantineDecision, error) {
	if id == "" || workerID == "" || now.IsZero() || !leaseUntil.After(now) {
		return nil, scannerrelease.RegistryQuarantineDecision{}, errors.New("invalid quarantine deletion authorization")
	}
	now, leaseUntil = now.UTC(), leaseUntil.UTC()
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, scannerrelease.RegistryQuarantineDecision{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var object scannerrelease.RegistryQuarantineObject
	if err := tx.GetContext(ctx, &object, r.db.Rebind(
		`SELECT `+quarantineColumns+` FROM scanner_registry_quarantine_objects WHERE id = ?`),
		id); err != nil {
		return nil, scannerrelease.RegistryQuarantineDecision{}, err
	}
	var reasons []string
	if object.Protected {
		reasons = append(reasons, "object_is_protected")
	}
	if object.RetainUntil == nil || object.RetainUntil.After(now) {
		reasons = append(reasons, "retention_not_expired")
	}
	switch object.State {
	case "quarantined", "orphaned", "delete_failed":
	default:
		reasons = append(reasons, "state_not_deletable")
	}
	referenceChecks := []struct {
		reason string
		query  string
		args   []any
	}{
		{"release_image_reference", `SELECT COUNT(*) FROM scanner_release_images WHERE digest = ?`, []any{object.Digest}},
		{"release_artifact_reference", `SELECT COUNT(*) FROM scanner_release_artifacts WHERE digest = ?`, []any{object.Digest}},
		{"release_manifest_reference", `SELECT COUNT(*) FROM scanner_releases WHERE manifest_digest = ? OR lock_digest = ?`, []any{object.Digest, object.Digest}},
		{"release_tool_reference", `SELECT COUNT(*) FROM scanner_release_tools WHERE source_digest = ? OR checksum = ?`, []any{object.Digest, object.Digest}},
		{"approval_evidence_reference", `SELECT COUNT(*) FROM scanner_release_approvals WHERE evidence_digest = ?`, []any{object.Digest}},
		{"candidate_lock_reference", `SELECT COUNT(*) FROM scanner_release_candidates WHERE lock_digest = ?`, []any{object.Digest}},
		{"build_step_reference", `SELECT COUNT(*) FROM scanner_build_steps WHERE output_digest = ?`, []any{object.Digest}},
	}
	if object.CandidateID != "" {
		referenceChecks = append(referenceChecks, struct {
			reason string
			query  string
			args   []any
		}{
			"published_candidate_reference",
			`SELECT COUNT(*) FROM scanner_releases WHERE candidate_id = ?`,
			[]any{object.CandidateID},
		})
	}
	for _, check := range referenceChecks {
		var count int
		if err := tx.GetContext(ctx, &count, r.db.Rebind(check.query), check.args...); err != nil {
			return nil, scannerrelease.RegistryQuarantineDecision{}, err
		}
		if count > 0 {
			reasons = append(reasons, check.reason)
		}
	}
	decision := scannerrelease.RegistryQuarantineDecision{
		Eligible: len(reasons) == 0,
		Reasons:  reasons,
	}
	if !decision.Eligible {
		return &object, decision, tx.Commit()
	}
	token := uuid.NewString()
	result, err := r.execTx(ctx, tx,
		`UPDATE scanner_registry_quarantine_objects
		 SET state = 'deleting', deletion_worker_id = ?,
		     deletion_lease_token = ?, deletion_lease_expires_at = ?,
		     error_detail = '', version = version + 1, updated_at = ?
		 WHERE id = ? AND version = ? AND state IN ('quarantined', 'orphaned', 'delete_failed')
		   AND protected = ? AND (retain_until IS NOT NULL AND retain_until <= ?)`,
		workerID, token, leaseUntil, now, id, object.Version, false, now)
	if err != nil {
		return nil, scannerrelease.RegistryQuarantineDecision{}, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return nil, scannerrelease.RegistryQuarantineDecision{}, scannerrelease.ErrVersionConflict
	}
	if err := tx.GetContext(ctx, &object, r.db.Rebind(
		`SELECT `+quarantineColumns+` FROM scanner_registry_quarantine_objects WHERE id = ?`),
		id); err != nil {
		return nil, scannerrelease.RegistryQuarantineDecision{}, err
	}
	if err := tx.Commit(); err != nil {
		return nil, scannerrelease.RegistryQuarantineDecision{}, err
	}
	return &object, decision, nil
}

func (r *scannerReleaseRepository) CompleteRegistryQuarantineDeletion(
	ctx context.Context,
	id, workerID, token string,
	deleted bool,
	errorDetail string,
	now time.Time,
) error {
	if id == "" || workerID == "" || token == "" || now.IsZero() {
		return errors.New("invalid quarantine deletion completion")
	}
	now = now.UTC()
	state := "delete_failed"
	var verifiedAt *time.Time
	if deleted {
		state = "deleted"
		verifiedAt = &now
		errorDetail = ""
	}
	result, err := r.db.ExecContext(ctx, r.db.Rebind(
		`UPDATE scanner_registry_quarantine_objects
		 SET state = ?, deletion_verified_at = ?, deletion_worker_id = '',
		     deletion_lease_token = '', deletion_lease_expires_at = NULL,
		     error_detail = ?, version = version + 1, updated_at = ?
		 WHERE id = ? AND state = 'deleting' AND deletion_worker_id = ?
		   AND deletion_lease_token = ? AND deletion_lease_expires_at > ?`),
		state, verifiedAt, boundedRegistryJobError(errorDetail), now,
		id, workerID, token, now)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return scannerrelease.ErrLeaseNotOwned
	}
	return nil
}
