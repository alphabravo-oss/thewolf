package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

const (
	maxAlertSummaryBytes  = 500
	maxAlertEvidenceBytes = 4 << 10
)

type scannerAlertCondition struct {
	Fingerprint  string
	Kind         scannerrelease.AlertKind
	Severity     scannerrelease.AlertSeverity
	ScopeType    string
	ScopeID      string
	Summary      string
	EvidenceJSON string
}

type scannerAlertSignals struct {
	Evaluated  map[scannerrelease.AlertKind]bool
	Conditions map[string]scannerAlertCondition
}

func (r *scannerReleaseRepository) GetAlert(
	ctx context.Context,
	id string,
) (*scannerrelease.Alert, error) {
	var alert scannerrelease.Alert
	if err := r.get(ctx, &alert,
		`SELECT * FROM scanner_release_alerts WHERE id = ?`, id); err != nil {
		return nil, err
	}
	return &alert, nil
}

func (r *scannerReleaseRepository) ListAlerts(
	ctx context.Context,
	filter scannerrelease.AlertFilter,
	page scannerrelease.PageRequest,
) (scannerrelease.AlertPage, error) {
	var query strings.Builder
	query.WriteString(`SELECT * FROM scanner_release_alerts WHERE 1 = 1`)
	var args []any
	if filter.State != "" {
		query.WriteString(` AND state = ?`)
		args = append(args, filter.State)
	}
	if filter.Kind != "" {
		query.WriteString(` AND kind = ?`)
		args = append(args, filter.Kind)
	}
	if filter.Severity != "" {
		query.WriteString(` AND severity = ?`)
		args = append(args, filter.Severity)
	}
	if err := appendCursorCondition(&query, &args, page.Cursor); err != nil {
		return scannerrelease.AlertPage{}, err
	}
	limit := pageLimit(page.Limit)
	query.WriteString(` ORDER BY created_at DESC, id DESC LIMIT ?`)
	args = append(args, limit+1)
	var items []scannerrelease.Alert
	if err := r.selectRows(ctx, &items, query.String(), args...); err != nil {
		return scannerrelease.AlertPage{}, err
	}
	items, next := pageCursor(items, limit, func(item scannerrelease.Alert) (time.Time, string) {
		return item.CreatedAt, item.ID
	})
	return scannerrelease.AlertPage{Items: items, NextCursor: next}, nil
}

func (r *scannerReleaseRepository) AlertCounts(
	ctx context.Context,
) (scannerrelease.AlertCounts, error) {
	return r.alertCountsQuery(ctx, r.db)
}

type alertCountQueryer interface {
	SelectContext(context.Context, any, string, ...any) error
}

func (r *scannerReleaseRepository) alertCountsQuery(
	ctx context.Context,
	queryer alertCountQueryer,
) (scannerrelease.AlertCounts, error) {
	var rows []struct {
		State    string `db:"state"`
		Severity string `db:"severity"`
		Count    int    `db:"count"`
	}
	if err := queryer.SelectContext(
		ctx, &rows, r.db.Rebind(
			`SELECT state, severity, COUNT(*) AS count
			 FROM scanner_release_alerts GROUP BY state, severity`,
		),
	); err != nil {
		return scannerrelease.AlertCounts{}, err
	}
	var counts scannerrelease.AlertCounts
	for _, row := range rows {
		if scannerrelease.AlertState(row.State) == scannerrelease.AlertResolved {
			counts.Resolved += row.Count
			continue
		}
		switch scannerrelease.AlertSeverity(row.Severity) {
		case scannerrelease.AlertWarning:
			counts.OpenWarning += row.Count
		case scannerrelease.AlertCritical:
			counts.OpenCritical += row.Count
		}
	}
	return counts, nil
}

func (r *scannerReleaseRepository) EvaluateAlerts(
	ctx context.Context,
	request scannerrelease.AlertEvaluationRequest,
	now time.Time,
) (scannerrelease.AlertEvaluationSummary, error) {
	if strings.TrimSpace(request.PolicyID) == "" ||
		request.PolicyRevision <= 0 || now.IsZero() {
		return scannerrelease.AlertEvaluationSummary{},
			errors.New("alert evaluation requires policy identity, revision, and time")
	}
	request.PolicyScope = strings.TrimSpace(request.PolicyScope)
	if request.PolicyScope == "" {
		request.PolicyScope = "global"
	}
	if request.MissedDiscovery.Enabled && request.MissedDiscovery.After <= 0 {
		return scannerrelease.AlertEvaluationSummary{},
			errors.New("missed discovery alert requires a positive duration")
	}
	if request.StaleStableRelease.Enabled && request.StaleStableRelease.After <= 0 {
		return scannerrelease.AlertEvaluationSummary{},
			errors.New("stale stable release alert requires a positive duration")
	}
	if request.QueueBacklog.Enabled &&
		request.QueueBacklog.MaxDepth <= 0 && request.QueueBacklog.MaxAge <= 0 {
		return scannerrelease.AlertEvaluationSummary{},
			errors.New("queue backlog alert requires a positive depth or age")
	}
	if request.QueueBacklog.MaxDepth < 0 || request.QueueBacklog.MaxAge < 0 {
		return scannerrelease.AlertEvaluationSummary{},
			errors.New("queue backlog alert thresholds must not be negative")
	}
	if request.LeaseChurn.Enabled &&
		(request.LeaseChurn.Count <= 0 || request.LeaseChurn.Window <= 0) {
		return scannerrelease.AlertEvaluationSummary{},
			errors.New("lease churn alert requires a positive count and window")
	}
	if request.RepeatedGateFailure.Enabled &&
		(request.RepeatedGateFailure.Count <= 0 ||
			request.RepeatedGateFailure.Window <= 0) {
		return scannerrelease.AlertEvaluationSummary{},
			errors.New("repeated gate failure alert requires a positive count and window")
	}
	now = now.UTC()
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return scannerrelease.AlertEvaluationSummary{}, err
	}
	defer func() { _ = tx.Rollback() }()
	signals, err := r.collectScannerAlertSignals(ctx, tx, request, now)
	if err != nil {
		return scannerrelease.AlertEvaluationSummary{}, err
	}
	summary, err := r.reconcileScannerAlerts(ctx, tx, request, signals, now)
	if err != nil {
		return scannerrelease.AlertEvaluationSummary{}, err
	}
	if err := tx.Commit(); err != nil {
		return scannerrelease.AlertEvaluationSummary{}, err
	}
	return summary, nil
}

func (r *scannerReleaseRepository) collectScannerAlertSignals(
	ctx context.Context,
	tx *sqlx.Tx,
	request scannerrelease.AlertEvaluationRequest,
	now time.Time,
) (scannerAlertSignals, error) {
	signals := scannerAlertSignals{
		Evaluated:  make(map[scannerrelease.AlertKind]bool),
		Conditions: make(map[string]scannerAlertCondition),
	}
	add := func(
		kind scannerrelease.AlertKind,
		severity scannerrelease.AlertSeverity,
		scopeType, scopeID, summary string,
		evidence map[string]any,
	) error {
		condition, err := newScannerAlertCondition(
			request.PolicyScope, kind, severity, scopeType, scopeID, summary, evidence,
		)
		if err != nil {
			return err
		}
		signals.Conditions[condition.Fingerprint] = condition
		return nil
	}

	if request.MissedDiscovery.Enabled {
		var completedAt time.Time
		err := tx.GetContext(ctx, &completedAt, r.db.Rebind(
			`SELECT completed_at FROM scanner_discovery_runs
			 WHERE state = ? AND completed_at IS NOT NULL
			 ORDER BY completed_at DESC, id DESC LIMIT 1`),
			scannerrelease.DiscoveryCompleted)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return signals, err
		}
		if err == nil {
			signals.Evaluated[scannerrelease.AlertMissedDiscovery] = true
			age := nonNegativeDuration(now.Sub(completedAt.UTC()))
			if age > request.MissedDiscovery.After {
				if err := add(
					scannerrelease.AlertMissedDiscovery, scannerrelease.AlertWarning,
					"policy_scope", "global",
					"Scanner update discovery is older than the configured maximum age",
					map[string]any{
						"age_seconds":       int64(age.Seconds()),
						"threshold_seconds": int64(request.MissedDiscovery.After.Seconds()),
						"last_completed_at": completedAt.UTC(),
					},
				); err != nil {
					return signals, err
				}
			}
		}
	}

	if request.StaleStableRelease.Enabled {
		var stable struct {
			ID          string    `db:"id"`
			PublishedAt time.Time `db:"published_at"`
		}
		err := tx.GetContext(ctx, &stable, r.db.Rebind(
			`SELECT id, published_at FROM scanner_releases
			 WHERE state = ? ORDER BY published_at DESC, id DESC LIMIT 1`),
			scannerrelease.ReleaseStable)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return signals, err
		}
		if err == nil {
			signals.Evaluated[scannerrelease.AlertStaleStableRelease] = true
			age := nonNegativeDuration(now.Sub(stable.PublishedAt.UTC()))
			if age > request.StaleStableRelease.After {
				if err := add(
					scannerrelease.AlertStaleStableRelease, scannerrelease.AlertWarning,
					"release_channel", "stable",
					"The stable scanner release is older than the configured maximum age",
					map[string]any{
						"age_seconds":       int64(age.Seconds()),
						"threshold_seconds": int64(request.StaleStableRelease.After.Seconds()),
						"release_id":        stable.ID,
					},
				); err != nil {
					return signals, err
				}
			}
		}
	}

	if request.QueueBacklog.Enabled {
		signals.Evaluated[scannerrelease.AlertQueueBacklog] = true
		queues := []struct {
			name        string
			countQuery  string
			oldestQuery string
			args        []any
		}{
			{
				name:       "discovery",
				countQuery: `SELECT COUNT(*) FROM scanner_discovery_runs WHERE state = ?`,
				oldestQuery: `SELECT created_at FROM scanner_discovery_runs
					WHERE state = ? ORDER BY created_at ASC, id ASC LIMIT 1`,
				args: []any{scannerrelease.DiscoveryQueued},
			},
			{
				name: "candidate",
				countQuery: `SELECT COUNT(*) FROM scanner_release_candidates
					WHERE state IN (?, ?)`,
				oldestQuery: `SELECT created_at FROM scanner_release_candidates
					WHERE state IN (?, ?) ORDER BY created_at ASC, id ASC LIMIT 1`,
				args: []any{
					scannerrelease.CandidateAwaitingDefinition,
					scannerrelease.CandidateQueued,
				},
			},
			{
				name:       "build",
				countQuery: `SELECT COUNT(*) FROM scanner_build_runs WHERE state = ?`,
				oldestQuery: `SELECT created_at FROM scanner_build_runs
					WHERE state = ? ORDER BY created_at ASC, id ASC LIMIT 1`,
				args: []any{scannerrelease.BuildQueued},
			},
			{
				name:       "rollout",
				countQuery: `SELECT COUNT(*) FROM scanner_rollouts WHERE state = ?`,
				oldestQuery: `SELECT created_at FROM scanner_rollouts
					WHERE state = ? ORDER BY created_at ASC, id ASC LIMIT 1`,
				args: []any{scannerrelease.RolloutPending},
			},
			{
				name: "notification",
				countQuery: `SELECT COUNT(*) FROM scanner_release_notifications
					WHERE destination_type <> ? AND state IN (?, ?)`,
				oldestQuery: `SELECT created_at FROM scanner_release_notifications
					WHERE destination_type <> ? AND state IN (?, ?)
					ORDER BY created_at ASC, id ASC LIMIT 1`,
				args: []any{
					scannerrelease.NotificationDestinationUI,
					scannerrelease.NotificationPending,
					scannerrelease.NotificationRetry,
				},
			},
		}
		for _, queue := range queues {
			var row struct {
				Count    int
				OldestAt sql.NullTime
			}
			if err := tx.GetContext(
				ctx, &row.Count, r.db.Rebind(queue.countQuery), queue.args...,
			); err != nil {
				return signals, err
			}
			if row.Count > 0 {
				var oldest time.Time
				if err := tx.GetContext(
					ctx, &oldest, r.db.Rebind(queue.oldestQuery), queue.args...,
				); err != nil {
					return signals, err
				}
				row.OldestAt = sql.NullTime{Time: oldest, Valid: true}
			}
			age := time.Duration(0)
			if row.OldestAt.Valid {
				age = nonNegativeDuration(now.Sub(row.OldestAt.Time.UTC()))
			}
			depthExceeded := request.QueueBacklog.MaxDepth > 0 &&
				row.Count > request.QueueBacklog.MaxDepth
			ageExceeded := request.QueueBacklog.MaxAge > 0 &&
				row.OldestAt.Valid && age > request.QueueBacklog.MaxAge
			if !depthExceeded && !ageExceeded {
				continue
			}
			if err := add(
				scannerrelease.AlertQueueBacklog, scannerrelease.AlertWarning,
				"queue", queue.name,
				"Scanner release work queue exceeds its configured backlog threshold",
				map[string]any{
					"queue": queue.name, "depth": row.Count,
					"max_depth":          request.QueueBacklog.MaxDepth,
					"oldest_age_seconds": int64(age.Seconds()),
					"max_age_seconds":    int64(request.QueueBacklog.MaxAge.Seconds()),
				},
			); err != nil {
				return signals, err
			}
		}
	}

	if request.LeaseChurn.Enabled {
		signals.Evaluated[scannerrelease.AlertLeaseChurn] = true
		windowStart := now.Add(-request.LeaseChurn.Window)
		var churn int
		if err := tx.GetContext(ctx, &churn, r.db.Rebind(
			`SELECT COUNT(*) FROM scanner_release_events
			 WHERE created_at >= ? AND (
			   event_type IN (?, ?, ?, ?, ?, ?, ?)
			   OR (
			     aggregate_type = ? AND event_type IN (?, ?)
			     AND payload_json LIKE ?
			   )
			 )`),
			windowStart,
			"discovery.requeued_after_worker_loss",
			"discovery.failed_after_worker_loss",
			"candidate.proposal_requeued_after_worker_loss",
			"candidate.proposal_failed_after_worker_loss",
			"build.requeued_after_worker_loss",
			"build.failed_after_worker_loss",
			"rollout.reclaimed",
			"notification", "notification.retry", "notification.dead_letter",
			`%"error_class":"worker_lost"%`,
		); err != nil {
			return signals, err
		}
		stuck, err := r.countExpiredScannerLeases(ctx, tx, now)
		if err != nil {
			return signals, err
		}
		if churn >= request.LeaseChurn.Count || stuck > 0 {
			severity := scannerrelease.AlertWarning
			if stuck > 0 {
				severity = scannerrelease.AlertCritical
			}
			if err := add(
				scannerrelease.AlertLeaseChurn, severity,
				"control_plane", "scanner_release",
				"Scanner release lease churn or expired owned work exceeds policy",
				map[string]any{
					"events": churn, "threshold": request.LeaseChurn.Count,
					"window_seconds": int64(request.LeaseChurn.Window.Seconds()),
					"expired_leases": stuck,
				},
			); err != nil {
				return signals, err
			}
		}
	}

	if request.RepeatedGateFailure.Enabled {
		signals.Evaluated[scannerrelease.AlertRepeatedGateFailure] = true
		var failures int
		if err := tx.GetContext(ctx, &failures, r.db.Rebind(
			`SELECT COUNT(*) FROM scanner_release_events
			 WHERE created_at >= ? AND event_type IN (?, ?)`),
			now.Add(-request.RepeatedGateFailure.Window),
			"candidate.blocked", "build.failed",
		); err != nil {
			return signals, err
		}
		if failures >= request.RepeatedGateFailure.Count {
			if err := add(
				scannerrelease.AlertRepeatedGateFailure, scannerrelease.AlertWarning,
				"control_plane", "release_gates",
				"Scanner release gates failed repeatedly within the configured window",
				map[string]any{
					"failures":       failures,
					"threshold":      request.RepeatedGateFailure.Count,
					"window_seconds": int64(request.RepeatedGateFailure.Window.Seconds()),
				},
			); err != nil {
				return signals, err
			}
		}
	}

	if request.MirrorDrift {
		signals.Evaluated[scannerrelease.AlertMirrorDrift] = true
		var mirrors []struct {
			ID              string `db:"id"`
			Parity          string `db:"digest_parity_status"`
			DeadLetterJobs  int    `db:"dead_letter_jobs"`
			CleanupFailures int    `db:"cleanup_failures"`
		}
		if err := tx.SelectContext(ctx, &mirrors, r.db.Rebind(
			`SELECT target.id, target.digest_parity_status,
			        (SELECT COUNT(*) FROM scanner_registry_jobs jobs
			         WHERE jobs.registry_target_id = target.id AND jobs.state = ?) AS dead_letter_jobs,
			        (SELECT COUNT(*) FROM scanner_registry_quarantine_objects objects
			         WHERE objects.registry_target_id = target.id AND objects.state = 'delete_failed') AS cleanup_failures
			 FROM scanner_registry_targets target
			 WHERE target.enabled = ? AND target.registry_type = ?
			   AND (target.digest_parity_status = ?
			        OR EXISTS (SELECT 1 FROM scanner_registry_jobs jobs
			                   WHERE jobs.registry_target_id = target.id AND jobs.state = ?)
			        OR EXISTS (SELECT 1 FROM scanner_registry_quarantine_objects objects
			                   WHERE objects.registry_target_id = target.id AND objects.state = 'delete_failed'))
			 ORDER BY id`),
			scannerrelease.RegistryJobDeadLetter, true, scannerrelease.RegistryMirror,
			"mismatched", scannerrelease.RegistryJobDeadLetter,
		); err != nil {
			return signals, err
		}
		for _, mirror := range mirrors {
			if err := add(
				scannerrelease.AlertMirrorDrift, scannerrelease.AlertCritical,
				"registry", mirror.ID,
				"Scanner registry mirror has drift or failed repair/cleanup work",
				map[string]any{
					"digest_parity_status": mirror.Parity,
					"dead_letter_jobs":     mirror.DeadLetterJobs,
					"cleanup_failures":     mirror.CleanupFailures,
				},
			); err != nil {
				return signals, err
			}
		}
	}

	if request.RolloutFailure {
		signals.Evaluated[scannerrelease.AlertRolloutFailure] = true
		var rollouts []struct {
			ID     string                      `db:"id"`
			Target string                      `db:"target"`
			State  scannerrelease.RolloutState `db:"state"`
		}
		if err := tx.SelectContext(ctx, &rollouts,
			`SELECT id, target, state FROM scanner_rollouts
			 ORDER BY updated_at DESC, id DESC`); err != nil {
			return signals, err
		}
		seenTargets := make(map[string]struct{})
		for _, rollout := range rollouts {
			scopeID := rollout.Target
			if scopeID == "" {
				scopeID = rollout.ID
			}
			if _, exists := seenTargets[scopeID]; exists {
				continue
			}
			seenTargets[scopeID] = struct{}{}
			if rollout.State != scannerrelease.RolloutFailed &&
				rollout.State != scannerrelease.RolloutRolledBack {
				continue
			}
			if err := add(
				scannerrelease.AlertRolloutFailure, scannerrelease.AlertCritical,
				"rollout_target", scopeID,
				"The latest scanner release rollout for this target failed or rolled back",
				map[string]any{
					"rollout_id": rollout.ID, "state": rollout.State,
				},
			); err != nil {
				return signals, err
			}
		}
	}

	if request.SignatureHealth {
		signals.Evaluated[scannerrelease.AlertSignatureHealth] = true
		var releases []struct {
			ID    string                      `db:"id"`
			State scannerrelease.ReleaseState `db:"state"`
		}
		if err := tx.SelectContext(ctx, &releases, r.db.Rebind(
			`SELECT DISTINCT release.id, release.state
			 FROM scanner_releases AS release
			 LEFT JOIN scanner_release_images AS image ON image.release_id = release.id
			 WHERE release.state = ?
			   AND (image.id IS NULL OR image.signature_status <> ?)
			 UNION
			 SELECT id, state FROM scanner_releases WHERE state = ?
			 ORDER BY id`),
			scannerrelease.ReleaseStable, "verified", scannerrelease.ReleaseRevoked,
		); err != nil {
			return signals, err
		}
		for _, release := range releases {
			if err := add(
				scannerrelease.AlertSignatureHealth, scannerrelease.AlertCritical,
				"release", release.ID,
				"A stable or revoked scanner release has unhealthy signature status",
				map[string]any{"release_state": release.State},
			); err != nil {
				return signals, err
			}
		}
	}
	return signals, nil
}

func (r *scannerReleaseRepository) countExpiredScannerLeases(
	ctx context.Context,
	tx *sqlx.Tx,
	now time.Time,
) (int, error) {
	queries := []struct {
		query string
		args  []any
	}{
		{
			`SELECT COUNT(*) FROM scanner_discovery_runs
			 WHERE state IN (?, ?, ?) AND lease_expires_at IS NOT NULL
			   AND lease_expires_at <= ?`,
			[]any{
				scannerrelease.DiscoveryResolving,
				scannerrelease.DiscoveryComparing,
				scannerrelease.DiscoveryProposing, now,
			},
		},
		{
			`SELECT COUNT(*) FROM scanner_release_candidates
			 WHERE proposal_lease_token <> '' AND proposal_lease_expires_at IS NOT NULL
			   AND proposal_lease_expires_at <= ?`,
			[]any{now},
		},
		{
			`SELECT COUNT(*) FROM scanner_build_runs
			 WHERE state IN (?, ?) AND lease_expires_at IS NOT NULL
			   AND lease_expires_at <= ?`,
			[]any{scannerrelease.BuildClaimed, scannerrelease.BuildRunning, now},
		},
		{
			`SELECT COUNT(*) FROM scanner_rollout_claims
			 WHERE state = ? AND lease_expires_at <= ?`,
			[]any{scannerrelease.LeaseActive, now},
		},
		{
			`SELECT COUNT(*) FROM scanner_release_notifications
			 WHERE state = ? AND lease_expires_at IS NOT NULL
			   AND lease_expires_at <= ?`,
			[]any{scannerrelease.NotificationDelivering, now},
		},
		{
			`SELECT COUNT(*) FROM scanner_schedule_leases
			 WHERE state = ? AND lease_expires_at <= ?`,
			[]any{scannerrelease.LeaseActive, now},
		},
	}
	total := 0
	for _, item := range queries {
		var count int
		if err := tx.GetContext(
			ctx, &count, r.db.Rebind(item.query), item.args...,
		); err != nil {
			return 0, err
		}
		total += count
	}
	return total, nil
}

func (r *scannerReleaseRepository) reconcileScannerAlerts(
	ctx context.Context,
	tx *sqlx.Tx,
	request scannerrelease.AlertEvaluationRequest,
	signals scannerAlertSignals,
	now time.Time,
) (scannerrelease.AlertEvaluationSummary, error) {
	var existing []scannerrelease.Alert
	if err := tx.SelectContext(ctx, &existing, r.db.Rebind(
		`SELECT * FROM scanner_release_alerts
		 WHERE policy_scope = ? ORDER BY created_at, id`),
		request.PolicyScope); err != nil {
		return scannerrelease.AlertEvaluationSummary{}, err
	}
	byFingerprint := make(map[string]*scannerrelease.Alert, len(existing))
	for index := range existing {
		byFingerprint[existing[index].Fingerprint] = &existing[index]
	}
	var summary scannerrelease.AlertEvaluationSummary
	for fingerprint, condition := range signals.Conditions {
		current := byFingerprint[fingerprint]
		if current == nil {
			alert := scannerrelease.Alert{
				ID: uuid.NewString(), Fingerprint: fingerprint,
				Kind: condition.Kind, Severity: condition.Severity,
				State:     scannerrelease.AlertOpen,
				ScopeType: condition.ScopeType, ScopeID: condition.ScopeID,
				Summary: condition.Summary, EvidenceJSON: condition.EvidenceJSON,
				PolicyID: request.PolicyID, PolicyScope: request.PolicyScope,
				PolicyRevision: request.PolicyRevision,
				TriggerCount:   1, Generation: 1, Version: 1,
				FirstTriggeredAt: now, LastTriggeredAt: now,
				CreatedAt: now, UpdatedAt: now,
			}
			if err := r.namedExecTx(ctx, tx,
				`INSERT INTO scanner_release_alerts
				 (id, fingerprint, kind, severity, state, scope_type, scope_id,
				  summary, evidence_json, policy_id, policy_scope, policy_revision, trigger_count,
				  generation, version, first_triggered_at, last_triggered_at,
				  resolved_at, created_at, updated_at)
				 VALUES
				 (:id, :fingerprint, :kind, :severity, :state, :scope_type, :scope_id,
				  :summary, :evidence_json, :policy_id, :policy_scope, :policy_revision, :trigger_count,
				  :generation, :version, :first_triggered_at, :last_triggered_at,
				  :resolved_at, :created_at, :updated_at)`,
				&alert); err != nil {
				return scannerrelease.AlertEvaluationSummary{}, err
			}
			if err := r.appendAlertEventTx(ctx, tx, &alert, "alert.opened", "", now); err != nil {
				return scannerrelease.AlertEvaluationSummary{}, err
			}
			summary.Opened++
			continue
		}
		reopened := current.State == scannerrelease.AlertResolved
		if reopened {
			current.State = scannerrelease.AlertOpen
			current.Generation++
			current.ResolvedAt = nil
			summary.Reopened++
		}
		current.Severity = condition.Severity
		current.Summary = condition.Summary
		current.EvidenceJSON = condition.EvidenceJSON
		current.PolicyID = request.PolicyID
		current.PolicyRevision = request.PolicyRevision
		current.TriggerCount++
		current.LastTriggeredAt = now
		current.UpdatedAt = now
		priorVersion := current.Version
		current.Version++
		result, err := r.execTx(ctx, tx,
			`UPDATE scanner_release_alerts
			 SET severity = ?, state = ?, summary = ?, evidence_json = ?,
			     policy_id = ?, policy_revision = ?, trigger_count = ?,
			     generation = ?, version = ?, last_triggered_at = ?,
			     resolved_at = NULL, updated_at = ?
			 WHERE id = ? AND version = ?`,
			current.Severity, current.State, current.Summary, current.EvidenceJSON,
			current.PolicyID, current.PolicyRevision, current.TriggerCount,
			current.Generation, current.Version, current.LastTriggeredAt,
			current.UpdatedAt, current.ID, priorVersion)
		if err != nil {
			return scannerrelease.AlertEvaluationSummary{}, err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return scannerrelease.AlertEvaluationSummary{}, scannerrelease.ErrVersionConflict
		}
		if reopened {
			if err := r.appendAlertEventTx(
				ctx, tx, current, "alert.reopened",
				string(scannerrelease.AlertResolved), now,
			); err != nil {
				return scannerrelease.AlertEvaluationSummary{}, err
			}
		}
	}
	for index := range existing {
		current := &existing[index]
		if current.State != scannerrelease.AlertOpen ||
			!signals.Evaluated[current.Kind] {
			continue
		}
		if _, active := signals.Conditions[current.Fingerprint]; active {
			continue
		}
		result, err := r.execTx(ctx, tx,
			`UPDATE scanner_release_alerts
			 SET state = ?, resolved_at = ?, version = version + 1, updated_at = ?
			 WHERE id = ? AND version = ? AND state = ?`,
			scannerrelease.AlertResolved, now, now,
			current.ID, current.Version, scannerrelease.AlertOpen)
		if err != nil {
			return scannerrelease.AlertEvaluationSummary{}, err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return scannerrelease.AlertEvaluationSummary{}, scannerrelease.ErrVersionConflict
		}
		current.State = scannerrelease.AlertResolved
		current.ResolvedAt = &now
		current.Version++
		current.UpdatedAt = now
		if err := r.appendAlertEventTx(
			ctx, tx, current, "alert.resolved",
			string(scannerrelease.AlertOpen), now,
		); err != nil {
			return scannerrelease.AlertEvaluationSummary{}, err
		}
		summary.Resolved++
	}
	counts, err := r.alertCountsQuery(ctx, tx)
	if err != nil {
		return scannerrelease.AlertEvaluationSummary{}, err
	}
	summary.Active = counts
	return summary, nil
}

func (r *scannerReleaseRepository) appendAlertEventTx(
	ctx context.Context,
	tx *sqlx.Tx,
	alert *scannerrelease.Alert,
	eventType, priorState string,
	now time.Time,
) error {
	payload, _ := json.Marshal(map[string]any{
		"kind": alert.Kind, "severity": alert.Severity,
		"generation": alert.Generation,
	})
	_, err := r.appendEventTx(
		ctx, tx, "alert", alert.ID, eventType, priorState, string(alert.State),
		scannerrelease.TransitionCommand{
			Actor:          "scanner-alert-evaluator",
			Reason:         "scanner release operational alert lifecycle",
			PolicyRevision: alert.PolicyRevision,
			IdempotencyKey: fmt.Sprintf(
				"scanner-alert:%s:%d:%s", alert.Fingerprint,
				alert.Generation, eventType,
			),
			PayloadJSON: string(payload),
		},
		now,
	)
	return err
}

func newScannerAlertCondition(
	policyScope string,
	kind scannerrelease.AlertKind,
	severity scannerrelease.AlertSeverity,
	scopeType, scopeID, summary string,
	evidence map[string]any,
) (scannerAlertCondition, error) {
	if strings.TrimSpace(policyScope) == "" ||
		!validScannerAlertKind(kind) ||
		(severity != scannerrelease.AlertWarning &&
			severity != scannerrelease.AlertCritical) ||
		strings.TrimSpace(scopeType) == "" ||
		strings.TrimSpace(scopeID) == "" {
		return scannerAlertCondition{}, errors.New("invalid scanner alert condition")
	}
	summary = strings.TrimSpace(summary)
	if summary == "" || len(summary) > maxAlertSummaryBytes {
		return scannerAlertCondition{}, errors.New("scanner alert summary is invalid")
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		return scannerAlertCondition{}, err
	}
	if len(encoded) > maxAlertEvidenceBytes {
		return scannerAlertCondition{}, errors.New("scanner alert evidence exceeds bounded limit")
	}
	raw := strings.Join([]string{
		strings.TrimSpace(policyScope), string(kind),
		strings.TrimSpace(scopeType), strings.TrimSpace(scopeID),
	}, "\x00")
	digest := sha256.Sum256([]byte(raw))
	return scannerAlertCondition{
		Fingerprint: "sha256:" + hex.EncodeToString(digest[:]),
		Kind:        kind, Severity: severity,
		ScopeType: strings.TrimSpace(scopeType),
		ScopeID:   strings.TrimSpace(scopeID),
		Summary:   summary, EvidenceJSON: string(encoded),
	}, nil
}

func validScannerAlertKind(kind scannerrelease.AlertKind) bool {
	switch kind {
	case scannerrelease.AlertMissedDiscovery,
		scannerrelease.AlertStaleStableRelease,
		scannerrelease.AlertQueueBacklog,
		scannerrelease.AlertLeaseChurn,
		scannerrelease.AlertRepeatedGateFailure,
		scannerrelease.AlertMirrorDrift,
		scannerrelease.AlertRolloutFailure,
		scannerrelease.AlertSignatureHealth:
		return true
	default:
		return false
	}
}

func nonNegativeDuration(value time.Duration) time.Duration {
	if value < 0 {
		return 0
	}
	return value
}
