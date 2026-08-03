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
	"github.com/jmoiron/sqlx"

	"github.com/alphabravocompany/thewolf/internal/scannerpolicy"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

const (
	maxNotificationPayloadBytes = 8 << 10
	maxNotificationErrorBytes   = 1 << 10
	defaultNotificationAttempts = 8
)

type notificationPolicyRouting struct {
	ID           string
	Revision     int64
	Destinations []string
}

func (r *scannerReleaseRepository) appendNotificationsTx(
	ctx context.Context,
	tx *sqlx.Tx,
	event *scannerrelease.Event,
) error {
	notificationType, details, err := r.notificationTypeTx(ctx, tx, event)
	if err != nil {
		return err
	}
	if notificationType == "" {
		return nil
	}
	routing, err := r.notificationPolicyTx(ctx, tx, event)
	if err != nil {
		return err
	}
	payload := map[string]any{
		"schema_version":    "wolf.scanner-notification/v1",
		"notification_type": notificationType,
		"event": map[string]any{
			"id":             event.ID,
			"type":           event.EventType,
			"aggregate_type": event.AggregateType,
			"aggregate_id":   event.AggregateID,
			"sequence":       event.Sequence,
			"prior_state":    event.PriorState,
			"new_state":      event.NewState,
			"created_at":     event.CreatedAt,
		},
		"policy_revision": event.PolicyRevision,
	}
	if len(details) != 0 {
		payload["details"] = details
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if len(encoded) > maxNotificationPayloadBytes {
		return errors.New("scanner notification payload exceeds the bounded outbox limit")
	}

	destinations := []struct {
		kind      scannerrelease.NotificationDestinationType
		reference string
		state     scannerrelease.NotificationState
	}{
		{
			kind: scannerrelease.NotificationDestinationUI, reference: "administrators",
			state: scannerrelease.NotificationDelivered,
		},
	}
	for _, configured := range routing.Destinations {
		kind, reference, parseErr := scannerpolicy.ParseNotificationDestination(configured)
		if parseErr != nil {
			// Policy writes validate these references. A legacy malformed
			// entry must not roll back an otherwise valid domain transition.
			continue
		}
		destinations = append(destinations, struct {
			kind      scannerrelease.NotificationDestinationType
			reference string
			state     scannerrelease.NotificationState
		}{
			kind: scannerrelease.NotificationDestinationType(kind), reference: reference,
			state: scannerrelease.NotificationPending,
		})
	}
	for _, destination := range destinations {
		now := event.CreatedAt.UTC()
		notification := scannerrelease.Notification{
			ID: uuid.NewString(), EventID: event.ID,
			AggregateType: event.AggregateType, AggregateID: event.AggregateID,
			EventType: event.EventType, NotificationType: notificationType,
			DestinationType: destination.kind, DestinationRef: destination.reference,
			PolicyID: routing.ID, PolicyRevision: routing.Revision,
			State: destination.state, PayloadJSON: string(encoded),
			MaxAttempts: defaultNotificationAttempts, AvailableAt: now,
			Version: 1, CreatedAt: now, UpdatedAt: now,
		}
		if destination.state == scannerrelease.NotificationDelivered {
			notification.DeliveredAt = &now
		}
		if err := r.namedExecTx(ctx, tx,
			`INSERT INTO scanner_release_notifications
			 (id, event_id, aggregate_type, aggregate_id, event_type,
			  notification_type, destination_type, destination_ref, policy_id,
			  policy_revision, state, payload_json, attempt, max_attempts,
			  available_at, worker_id, lease_token, lease_expires_at, heartbeat_at,
			  delivered_at, dead_lettered_at, error_class, error_detail, version,
			  created_at, updated_at)
			 VALUES
			 (:id, :event_id, :aggregate_type, :aggregate_id, :event_type,
			  :notification_type, :destination_type, :destination_ref, :policy_id,
			  :policy_revision, :state, :payload_json, :attempt, :max_attempts,
			  :available_at, :worker_id, :lease_token, :lease_expires_at, :heartbeat_at,
			  :delivered_at, :dead_lettered_at, :error_class, :error_detail, :version,
			  :created_at, :updated_at)
			 ON CONFLICT(event_id, destination_type, destination_ref) DO NOTHING`,
			&notification); err != nil {
			return err
		}
	}
	return nil
}

func (r *scannerReleaseRepository) notificationTypeTx(
	ctx context.Context,
	tx *sqlx.Tx,
	event *scannerrelease.Event,
) (string, map[string]any, error) {
	switch event.EventType {
	case "discovery.completed":
		var critical int
		if err := tx.GetContext(ctx, &critical, r.db.Rebind(
			`SELECT COUNT(*) FROM scanner_update_items
			 WHERE discovery_run_id = ? AND risk_class = ? AND status = ?`),
			event.AggregateID, scannerrelease.RiskCritical, "update_available"); err != nil {
			return "", nil, err
		}
		if critical == 0 {
			return "", nil, nil
		}
		return "critical_update_discovered", map[string]any{"critical_updates": critical}, nil
	case "candidate.awaiting_approval":
		return "candidate_ready_for_approval", nil, nil
	case "candidate.blocked", "build.failed":
		return "gate_failure", nil, nil
	case "release.published":
		return "release_published", nil, nil
	case "release.revoked":
		return "stable_release_health_issue", map[string]any{"issue": "revoked"}, nil
	case "rollout.canary":
		return "canary_started", nil, nil
	case "rollout.paused":
		return "rollout_paused", nil, nil
	case "rollout.rolled_back":
		return "rollout_rolled_back", nil, nil
	case "rollout.completed":
		return "rollout_completed", nil, nil
	case "alert.opened", "alert.reopened", "alert.resolved":
		var alert struct {
			Kind     scannerrelease.AlertKind     `db:"kind"`
			Severity scannerrelease.AlertSeverity `db:"severity"`
		}
		if err := tx.GetContext(ctx, &alert, r.db.Rebind(
			`SELECT kind, severity FROM scanner_release_alerts WHERE id = ?`),
			event.AggregateID); err != nil {
			return "", nil, err
		}
		details := map[string]any{
			"kind": alert.Kind, "severity": alert.Severity,
		}
		if event.EventType == "alert.resolved" {
			return "scanner_release_alert_resolved", details, nil
		}
		switch alert.Kind {
		case scannerrelease.AlertMirrorDrift:
			return "mirror_drift", details, nil
		case scannerrelease.AlertRepeatedGateFailure:
			return "gate_failure", details, nil
		case scannerrelease.AlertSignatureHealth:
			return "stable_release_health_issue", details, nil
		default:
			return "scanner_release_alert", details, nil
		}
	case "rollout_cohort.updated":
		var cohort struct {
			Name  string `db:"cohort_name"`
			State string `db:"state"`
		}
		if err := tx.GetContext(ctx, &cohort, r.db.Rebind(
			`SELECT cohort_name, state FROM scanner_rollout_cohorts WHERE id = ?`),
			event.AggregateID); err != nil {
			return "", nil, err
		}
		if cohort.Name != "canary" {
			return "", nil, nil
		}
		switch cohort.State {
		case "completed":
			return "canary_passed", nil, nil
		case "failed":
			return "canary_failed", nil, nil
		}
	}
	return "", nil, nil
}

func (r *scannerReleaseRepository) notificationPolicyTx(
	ctx context.Context,
	tx *sqlx.Tx,
	event *scannerrelease.Event,
) (notificationPolicyRouting, error) {
	policyID, snapshotRules, err := r.notificationPolicyIdentityTx(ctx, tx, event)
	if err != nil {
		return notificationPolicyRouting{}, err
	}
	var policy scannerrelease.Policy
	switch {
	case policyID != "":
		if err := tx.GetContext(ctx, &policy, r.db.Rebind(
			`SELECT * FROM scanner_update_policies WHERE id = ?`), policyID); err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				return notificationPolicyRouting{}, err
			}
			policy = scannerrelease.Policy{}
		}
	case snapshotRules != "":
		policy.ID = ""
		policy.Revision = event.PolicyRevision
		policy.RulesJSON = snapshotRules
	case event.PolicyRevision > 0:
		err := tx.GetContext(ctx, &policy, r.db.Rebind(
			`SELECT * FROM scanner_update_policies
			 WHERE scope = ? AND revision = ? ORDER BY created_at DESC LIMIT 1`),
			"global", event.PolicyRevision)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return notificationPolicyRouting{}, err
		}
	}
	routing := notificationPolicyRouting{ID: policy.ID, Revision: policy.Revision}
	if routing.Revision == 0 {
		routing.Revision = event.PolicyRevision
	}
	var rules struct {
		Notifications scannerpolicy.NotificationPolicy `json:"notifications"`
	}
	if json.Unmarshal([]byte(policy.RulesJSON), &rules) == nil {
		routing.Destinations = append([]string(nil), rules.Notifications.Destinations...)
	}
	return routing, nil
}

func (r *scannerReleaseRepository) notificationPolicyIdentityTx(
	ctx context.Context,
	tx *sqlx.Tx,
	event *scannerrelease.Event,
) (string, string, error) {
	var policyID string
	switch event.AggregateType {
	case "discovery":
		err := tx.GetContext(ctx, &policyID, r.db.Rebind(
			`SELECT policy_id FROM scanner_discovery_runs WHERE id = ?`), event.AggregateID)
		return policyID, "", err
	case "candidate":
		err := tx.GetContext(ctx, &policyID, r.db.Rebind(
			`SELECT policy_id FROM scanner_release_candidates WHERE id = ?`), event.AggregateID)
		return policyID, "", err
	case "build":
		err := tx.GetContext(ctx, &policyID, r.db.Rebind(
			`SELECT candidate.policy_id
			 FROM scanner_build_runs AS build
			 JOIN scanner_release_candidates AS candidate ON candidate.id = build.candidate_id
			 WHERE build.id = ?`), event.AggregateID)
		return policyID, "", err
	case "build_step":
		err := tx.GetContext(ctx, &policyID, r.db.Rebind(
			`SELECT candidate.policy_id
			 FROM scanner_build_steps AS step
			 JOIN scanner_build_runs AS build ON build.id = step.build_run_id
			 JOIN scanner_release_candidates AS candidate ON candidate.id = build.candidate_id
			 WHERE step.id = ?`), event.AggregateID)
		return policyID, "", err
	case "release":
		err := tx.GetContext(ctx, &policyID, r.db.Rebind(
			`SELECT policy_id FROM scanner_releases WHERE id = ?`), event.AggregateID)
		return policyID, "", err
	case "alert":
		err := tx.GetContext(ctx, &policyID, r.db.Rebind(
			`SELECT policy_id FROM scanner_release_alerts WHERE id = ?`), event.AggregateID)
		return policyID, "", err
	case "rollout", "rollout_cohort":
		query := `SELECT policy_snapshot_json FROM scanner_rollouts WHERE id = ?`
		identifier := event.AggregateID
		if event.AggregateType == "rollout_cohort" {
			query = `SELECT rollout.policy_snapshot_json
				FROM scanner_rollout_cohorts AS cohort
				JOIN scanner_rollouts AS rollout ON rollout.id = cohort.rollout_id
				WHERE cohort.id = ?`
		}
		var snapshotJSON string
		if err := tx.GetContext(ctx, &snapshotJSON, r.db.Rebind(query), identifier); err != nil {
			return "", "", err
		}
		var snapshot scannerrelease.Policy
		if err := json.Unmarshal([]byte(snapshotJSON), &snapshot); err != nil {
			return "", "", nil
		}
		return snapshot.ID, snapshot.RulesJSON, nil
	}
	return "", "", nil
}

func (r *scannerReleaseRepository) GetNotification(
	ctx context.Context,
	id string,
) (*scannerrelease.Notification, error) {
	var notification scannerrelease.Notification
	if err := r.get(ctx, &notification,
		`SELECT * FROM scanner_release_notifications WHERE id = ?`, id); err != nil {
		return nil, err
	}
	return &notification, nil
}

func (r *scannerReleaseRepository) ListNotifications(
	ctx context.Context,
	filter scannerrelease.NotificationFilter,
	page scannerrelease.PageRequest,
) (scannerrelease.NotificationPage, error) {
	var query strings.Builder
	query.WriteString(`SELECT * FROM scanner_release_notifications WHERE 1 = 1`)
	var args []any
	if filter.State != "" {
		query.WriteString(` AND state = ?`)
		args = append(args, filter.State)
	}
	if filter.DestinationType != "" {
		query.WriteString(` AND destination_type = ?`)
		args = append(args, filter.DestinationType)
	}
	if filter.NotificationType != "" {
		query.WriteString(` AND notification_type = ?`)
		args = append(args, filter.NotificationType)
	}
	if err := appendCursorCondition(&query, &args, page.Cursor); err != nil {
		return scannerrelease.NotificationPage{}, err
	}
	limit := pageLimit(page.Limit)
	query.WriteString(` ORDER BY created_at DESC, id DESC LIMIT ?`)
	args = append(args, limit+1)
	var items []scannerrelease.Notification
	if err := r.selectRows(ctx, &items, query.String(), args...); err != nil {
		return scannerrelease.NotificationPage{}, err
	}
	items, next := pageCursor(items, limit, func(item scannerrelease.Notification) (time.Time, string) {
		return item.CreatedAt, item.ID
	})
	return scannerrelease.NotificationPage{Items: items, NextCursor: next}, nil
}

func (r *scannerReleaseRepository) NotificationQueueCounts(
	ctx context.Context,
) (scannerrelease.NotificationCounts, error) {
	var rows []struct {
		State string `db:"state"`
		Count int    `db:"count"`
	}
	if err := r.selectRows(ctx, &rows,
		`SELECT state, COUNT(*) AS count FROM scanner_release_notifications
		 GROUP BY state`); err != nil {
		return scannerrelease.NotificationCounts{}, err
	}
	var counts scannerrelease.NotificationCounts
	for _, row := range rows {
		switch scannerrelease.NotificationState(row.State) {
		case scannerrelease.NotificationPending:
			counts.Pending = row.Count
		case scannerrelease.NotificationDelivering:
			counts.Delivering = row.Count
		case scannerrelease.NotificationRetry:
			counts.Retry = row.Count
		case scannerrelease.NotificationDelivered:
			counts.Delivered = row.Count
		case scannerrelease.NotificationDeadLetter:
			counts.DeadLetter = row.Count
		}
	}
	return counts, nil
}

func (r *scannerReleaseRepository) ClaimNextNotification(
	ctx context.Context,
	workerID string,
	now, leaseUntil time.Time,
) (*scannerrelease.Notification, error) {
	if strings.TrimSpace(workerID) == "" || now.IsZero() || !leaseUntil.After(now) {
		return nil, errors.New("invalid scanner notification claim request")
	}
	now = now.UTC()
	leaseUntil = leaseUntil.UTC()
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var identifiers []string
	if err := tx.SelectContext(ctx, &identifiers, r.db.Rebind(
		`SELECT id FROM scanner_release_notifications
		 WHERE destination_type <> ?
		   AND state IN (?, ?)
		   AND available_at <= ?
		   AND attempt < max_attempts
		 ORDER BY available_at ASC, created_at ASC, id ASC
		 LIMIT 100`),
		scannerrelease.NotificationDestinationUI,
		scannerrelease.NotificationPending, scannerrelease.NotificationRetry, now); err != nil {
		return nil, err
	}
	for _, identifier := range identifiers {
		var current scannerrelease.Notification
		if err := tx.GetContext(ctx, &current, r.db.Rebind(
			`SELECT * FROM scanner_release_notifications WHERE id = ?`), identifier); err != nil {
			return nil, err
		}
		token := uuid.NewString()
		result, err := r.execTx(ctx, tx,
			`UPDATE scanner_release_notifications
			 SET state = ?, worker_id = ?, lease_token = ?, lease_expires_at = ?,
			     heartbeat_at = ?, attempt = attempt + 1, version = version + 1,
			     error_class = '', error_detail = '', updated_at = ?
			 WHERE id = ? AND version = ? AND state IN (?, ?)
			   AND available_at <= ? AND attempt < max_attempts`,
			scannerrelease.NotificationDelivering, workerID, token, leaseUntil,
			now, now, identifier, current.Version,
			scannerrelease.NotificationPending, scannerrelease.NotificationRetry, now)
		if err != nil {
			return nil, err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			continue
		}
		var claimed scannerrelease.Notification
		if err := tx.GetContext(ctx, &claimed, r.db.Rebind(
			`SELECT * FROM scanner_release_notifications WHERE id = ?`), identifier); err != nil {
			return nil, err
		}
		if _, err := r.appendEventTx(
			ctx, tx, "notification", claimed.ID, "notification.claimed",
			string(current.State), string(claimed.State),
			notificationCommand(
				workerID, "notification delivery claimed",
				"notification-claim:"+token, claimed.PolicyRevision,
				claimed.State, claimed.Attempt, "",
			), now,
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

func (r *scannerReleaseRepository) HeartbeatNotification(
	ctx context.Context,
	id, workerID, leaseToken string,
	now, leaseUntil time.Time,
) (scannerrelease.NotificationLeaseStatus, error) {
	if id == "" || workerID == "" || leaseToken == "" || now.IsZero() ||
		!leaseUntil.After(now) {
		return scannerrelease.NotificationLeaseStatus{}, errors.New("invalid scanner notification heartbeat")
	}
	now = now.UTC()
	leaseUntil = leaseUntil.UTC()
	result, err := r.db.ExecContext(ctx, r.db.Rebind(
		`UPDATE scanner_release_notifications
		 SET heartbeat_at = ?, lease_expires_at = ?, version = version + 1, updated_at = ?
		 WHERE id = ? AND worker_id = ? AND lease_token = ?
		   AND state = ? AND lease_expires_at > ?`),
		now, leaseUntil, now, id, workerID, leaseToken,
		scannerrelease.NotificationDelivering, now)
	if err != nil {
		return scannerrelease.NotificationLeaseStatus{}, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return scannerrelease.NotificationLeaseStatus{}, nil
	}
	var status scannerrelease.NotificationLeaseStatus
	if err := r.get(ctx, &status,
		`SELECT 1 AS current, state, version
		 FROM scanner_release_notifications WHERE id = ?`, id); err != nil {
		return scannerrelease.NotificationLeaseStatus{}, err
	}
	return status, nil
}

func (r *scannerReleaseRepository) FinalizeNotification(
	ctx context.Context,
	id, workerID, leaseToken string,
	target scannerrelease.NotificationState,
	availableAt time.Time,
	errorClass, errorDetail string,
	now time.Time,
) (*scannerrelease.Notification, error) {
	if id == "" || workerID == "" || leaseToken == "" || now.IsZero() {
		return nil, errors.New("invalid scanner notification finalization")
	}
	switch target {
	case scannerrelease.NotificationDelivered,
		scannerrelease.NotificationRetry,
		scannerrelease.NotificationDeadLetter:
	default:
		return nil, invalidNotificationState(target)
	}
	now = now.UTC()
	if target == scannerrelease.NotificationRetry {
		if availableAt.Before(now) {
			return nil, errors.New("scanner notification retry cannot be available in the past")
		}
		availableAt = availableAt.UTC()
	} else {
		availableAt = now
	}
	errorClass = boundedNotificationError(errorClass)
	errorDetail = boundedNotificationError(errorDetail)
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var current scannerrelease.Notification
	if err := tx.GetContext(ctx, &current, r.db.Rebind(
		`SELECT * FROM scanner_release_notifications WHERE id = ?`), id); err != nil {
		return nil, err
	}
	if current.State != scannerrelease.NotificationDelivering ||
		current.WorkerID != workerID || current.LeaseToken != leaseToken ||
		current.LeaseExpiresAt == nil || !current.LeaseExpiresAt.After(now) {
		return nil, scannerrelease.ErrLeaseNotOwned
	}
	var deliveredAt, deadLetteredAt *time.Time
	if target == scannerrelease.NotificationDelivered {
		deliveredAt = &now
		errorClass = ""
		errorDetail = ""
	}
	if target == scannerrelease.NotificationDeadLetter {
		deadLetteredAt = &now
	}
	result, err := r.execTx(ctx, tx,
		`UPDATE scanner_release_notifications
		 SET state = ?, available_at = ?, worker_id = '', lease_token = '',
		     lease_expires_at = NULL, heartbeat_at = NULL, delivered_at = ?,
		     dead_lettered_at = ?, error_class = ?, error_detail = ?,
		     version = version + 1, updated_at = ?
		 WHERE id = ? AND version = ? AND worker_id = ? AND lease_token = ?
		   AND state = ? AND lease_expires_at > ?`,
		target, availableAt, deliveredAt, deadLetteredAt, errorClass, errorDetail,
		now, id, current.Version, workerID, leaseToken,
		scannerrelease.NotificationDelivering, now)
	if err != nil {
		return nil, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return nil, scannerrelease.ErrLeaseNotOwned
	}
	eventType := "notification." + string(target)
	reason := "notification delivery completed"
	if target == scannerrelease.NotificationRetry {
		reason = "notification delivery scheduled for retry"
	} else if target == scannerrelease.NotificationDeadLetter {
		reason = "notification delivery exhausted attempts"
	}
	if _, err := r.appendEventTx(
		ctx, tx, "notification", id, eventType,
		string(current.State), string(target),
		notificationCommand(
			workerID, reason, eventType+":"+leaseToken,
			current.PolicyRevision, target, current.Attempt, errorClass,
		), now,
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return r.GetNotification(ctx, id)
}

func (r *scannerReleaseRepository) ReclaimStaleNotifications(
	ctx context.Context,
	now time.Time,
) (scannerrelease.NotificationReclaimSummary, error) {
	if now.IsZero() {
		return scannerrelease.NotificationReclaimSummary{}, errors.New("notification reclaim time is required")
	}
	now = now.UTC()
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return scannerrelease.NotificationReclaimSummary{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var stale []scannerrelease.Notification
	if err := tx.SelectContext(ctx, &stale, r.db.Rebind(
		`SELECT * FROM scanner_release_notifications
		 WHERE state = ? AND lease_expires_at <= ?
		 ORDER BY lease_expires_at ASC, id ASC LIMIT 100`),
		scannerrelease.NotificationDelivering, now); err != nil {
		return scannerrelease.NotificationReclaimSummary{}, err
	}
	var summary scannerrelease.NotificationReclaimSummary
	for _, current := range stale {
		target := scannerrelease.NotificationRetry
		errorClass := "worker_lost"
		reason := "expired notification delivery lease was requeued"
		var deadLetteredAt *time.Time
		if current.Attempt >= current.MaxAttempts {
			target = scannerrelease.NotificationDeadLetter
			deadLetteredAt = &now
			reason = "expired notification delivery lease exhausted attempts"
		}
		result, err := r.execTx(ctx, tx,
			`UPDATE scanner_release_notifications
			 SET state = ?, available_at = ?, worker_id = '', lease_token = '',
			     lease_expires_at = NULL, heartbeat_at = NULL,
			     dead_lettered_at = ?, error_class = ?, error_detail = ?,
			     version = version + 1, updated_at = ?
			 WHERE id = ? AND version = ? AND state = ? AND lease_expires_at <= ?`,
			target, now, deadLetteredAt, errorClass,
			"notification delivery worker lease expired",
			now, current.ID, current.Version, scannerrelease.NotificationDelivering, now)
		if err != nil {
			return scannerrelease.NotificationReclaimSummary{}, err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			continue
		}
		if target == scannerrelease.NotificationDeadLetter {
			summary.DeadLettered++
		} else {
			summary.Retried++
		}
		if _, err := r.appendEventTx(
			ctx, tx, "notification", current.ID, "notification."+string(target),
			string(current.State), string(target),
			notificationCommand(
				"notification-reclaimer", reason,
				fmt.Sprintf("notification-reclaim:%s:%d", current.ID, current.Version),
				current.PolicyRevision, target, current.Attempt, errorClass,
			), now,
		); err != nil {
			return scannerrelease.NotificationReclaimSummary{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return scannerrelease.NotificationReclaimSummary{}, err
	}
	return summary, nil
}

func (r *scannerReleaseRepository) RetryDeadLetterNotification(
	ctx context.Context,
	id string,
	expectedVersion int64,
	command scannerrelease.TransitionCommand,
	now time.Time,
) (*scannerrelease.Notification, error) {
	if id == "" || expectedVersion <= 0 || now.IsZero() ||
		strings.TrimSpace(command.Actor) == "" ||
		strings.TrimSpace(command.Reason) == "" ||
		strings.TrimSpace(command.IdempotencyKey) == "" {
		return nil, errors.New("notification retry requires ID, version, actor, reason, and idempotency key")
	}
	now = now.UTC()
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var current scannerrelease.Notification
	if err := tx.GetContext(ctx, &current, r.db.Rebind(
		`SELECT * FROM scanner_release_notifications WHERE id = ?`), id); err != nil {
		return nil, err
	}
	var existing int
	err = tx.GetContext(ctx, &existing, r.db.Rebind(
		`SELECT COUNT(*) FROM scanner_release_events
		 WHERE aggregate_type = ? AND aggregate_id = ? AND idempotency_key = ?`),
		"notification", id, command.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	if existing > 0 {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return &current, nil
	}
	if current.Version != expectedVersion {
		return nil, scannerrelease.ErrVersionConflict
	}
	if current.State != scannerrelease.NotificationDeadLetter {
		return nil, invalidNotificationState(current.State)
	}
	result, err := r.execTx(ctx, tx,
		`UPDATE scanner_release_notifications
		 SET state = ?, attempt = 0, available_at = ?, worker_id = '',
		     lease_token = '', lease_expires_at = NULL, heartbeat_at = NULL,
		     dead_lettered_at = NULL, error_class = '', error_detail = '',
		     version = version + 1, updated_at = ?
		 WHERE id = ? AND version = ? AND state = ?`,
		scannerrelease.NotificationRetry, now, now, id, expectedVersion,
		scannerrelease.NotificationDeadLetter)
	if err != nil {
		return nil, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return nil, scannerrelease.ErrVersionConflict
	}
	command.PolicyRevision = current.PolicyRevision
	command.PayloadJSON = notificationAuditPayload(
		scannerrelease.NotificationRetry, 0, "operator_retry",
	)
	if _, err := r.appendEventTx(
		ctx, tx, "notification", id, "notification.operator_retry",
		string(current.State), string(scannerrelease.NotificationRetry), command, now,
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return r.GetNotification(ctx, id)
}

func boundedNotificationError(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > maxNotificationErrorBytes {
		value = value[:maxNotificationErrorBytes]
	}
	return value
}

func notificationAuditPayload(state scannerrelease.NotificationState, attempt int, errorClass string) string {
	payload, _ := json.Marshal(map[string]any{
		"state": state, "attempt": attempt, "error_class": errorClass,
	})
	return string(payload)
}

func notificationCommand(
	actor, reason, key string,
	policyRevision int64,
	state scannerrelease.NotificationState,
	attempt int,
	errorClass string,
) scannerrelease.TransitionCommand {
	return scannerrelease.TransitionCommand{
		Actor: actor, Reason: reason, PolicyRevision: policyRevision,
		IdempotencyKey: key,
		PayloadJSON:    notificationAuditPayload(state, attempt, errorClass),
	}
}

func invalidNotificationState(state scannerrelease.NotificationState) error {
	return fmt.Errorf("%w: notification state %q", scannerrelease.ErrInvalidTransition, state)
}
