package db

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/alphabravocompany/thewolf/internal/models"
)

// remediationRepository persists agentic remediation sessions, the triage
// plans and patches they produce, and the redacted agent event stream used
// for SSE replay. SQL is written with `?` placeholders and rebound per
// dialect via sqlx.DB.Rebind, so one implementation serves both SQLite and
// Postgres — the same shape as scannerReleaseRepository.
type remediationRepository struct {
	db *sqlx.DB
}

func newRemediationRepository(db *sqlx.DB) *remediationRepository {
	return &remediationRepository{db: db}
}

func (r *remediationRepository) CreateRemediationSession(ctx context.Context, s *models.RemediationSession) error {
	if s.ID == "" {
		s.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	if s.CreatedAt.IsZero() {
		s.CreatedAt = now
	}
	if s.UpdatedAt.IsZero() {
		s.UpdatedAt = s.CreatedAt
	}
	if s.Status == "" {
		s.Status = models.RemediationPending
	}
	_, err := r.db.NamedExecContext(ctx,
		`INSERT INTO remediation_sessions
		 (id, user_id, repo_id, scan_id, loop_id, status, plan_gate_enabled, patch_gate_enabled,
		  max_turns, turns_used_plan, turns_used_execute, tokens_used, cost_used, provider, model,
		  branch_name, worktree_path, pr_url, failure_reason, created_at, updated_at, started_at, completed_at)
		 VALUES
		 (:id, :user_id, :repo_id, :scan_id, :loop_id, :status, :plan_gate_enabled, :patch_gate_enabled,
		  :max_turns, :turns_used_plan, :turns_used_execute, :tokens_used, :cost_used, :provider, :model,
		  :branch_name, :worktree_path, :pr_url, :failure_reason, :created_at, :updated_at, :started_at, :completed_at)`,
		s)
	return err
}

func (r *remediationRepository) GetRemediationSession(ctx context.Context, id string) (*models.RemediationSession, error) {
	var s models.RemediationSession
	if err := r.db.GetContext(ctx, &s, r.db.Rebind(
		`SELECT * FROM remediation_sessions WHERE id = ?`), id); err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *remediationRepository) ListRemediationSessions(ctx context.Context, userID string) ([]models.RemediationSession, error) {
	var sessions []models.RemediationSession
	err := r.db.SelectContext(ctx, &sessions, r.db.Rebind(
		`SELECT * FROM remediation_sessions WHERE user_id = ? ORDER BY created_at DESC, id DESC`), userID)
	return sessions, err
}

func (r *remediationRepository) UpdateRemediationSession(ctx context.Context, s *models.RemediationSession) error {
	s.UpdatedAt = time.Now().UTC()
	_, err := r.db.NamedExecContext(ctx,
		`UPDATE remediation_sessions SET
		  status = :status, plan_gate_enabled = :plan_gate_enabled, patch_gate_enabled = :patch_gate_enabled,
		  max_turns = :max_turns, turns_used_plan = :turns_used_plan, turns_used_execute = :turns_used_execute,
		  tokens_used = :tokens_used, cost_used = :cost_used, provider = :provider, model = :model,
		  branch_name = :branch_name, worktree_path = :worktree_path, pr_url = :pr_url,
		  failure_reason = :failure_reason, updated_at = :updated_at, started_at = :started_at,
		  completed_at = :completed_at
		 WHERE id = :id`, s)
	return err
}

func (r *remediationRepository) SaveRemediationPlan(ctx context.Context, p *models.RemediationPlan) error {
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now().UTC()
	}
	_, err := r.db.NamedExecContext(ctx,
		`INSERT INTO remediation_plans
		 (id, session_id, plan_json, created_at, approved_by, approved_at, rejected_reason)
		 VALUES
		 (:id, :session_id, :plan_json, :created_at, :approved_by, :approved_at, :rejected_reason)`,
		p)
	return err
}

// GetRemediationPlan returns the most recently saved plan for a session. A
// session normally has exactly one, but re-triage after a repair attempt can
// leave more than one row, so callers get the latest.
func (r *remediationRepository) GetRemediationPlan(ctx context.Context, sessionID string) (*models.RemediationPlan, error) {
	var p models.RemediationPlan
	if err := r.db.GetContext(ctx, &p, r.db.Rebind(
		`SELECT * FROM remediation_plans WHERE session_id = ?
		 ORDER BY created_at DESC, id DESC LIMIT 1`), sessionID); err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *remediationRepository) ApproveRemediationPlan(ctx context.Context, sessionID, approverID string) error {
	now := time.Now().UTC()
	result, err := r.db.ExecContext(ctx, r.db.Rebind(
		`UPDATE remediation_plans SET approved_by = ?, approved_at = ?
		 WHERE id = (
		   SELECT id FROM remediation_plans WHERE session_id = ?
		   ORDER BY created_at DESC, id DESC LIMIT 1
		 )`), approverID, now, sessionID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (r *remediationRepository) SaveRemediationPatches(ctx context.Context, sessionID string, patches []models.RemediationPatch) error {
	if len(patches) == 0 {
		return nil
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().UTC()
	for i := range patches {
		p := &patches[i]
		p.SessionID = sessionID
		if p.ID == "" {
			p.ID = uuid.NewString()
		}
		if p.CreatedAt.IsZero() {
			p.CreatedAt = now
		}
		query, args, err := sqlx.Named(
			`INSERT INTO remediation_patches
			 (id, session_id, commit_sha, files_changed, finding_ids, message, created_at, approved_by, approved_at)
			 VALUES
			 (:id, :session_id, :commit_sha, :files_changed, :finding_ids, :message, :created_at, :approved_by, :approved_at)`,
			p)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, tx.Rebind(query), args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *remediationRepository) ListRemediationPatches(ctx context.Context, sessionID string) ([]models.RemediationPatch, error) {
	var patches []models.RemediationPatch
	err := r.db.SelectContext(ctx, &patches, r.db.Rebind(
		`SELECT * FROM remediation_patches WHERE session_id = ? ORDER BY created_at ASC, id ASC`), sessionID)
	return patches, err
}

func (r *remediationRepository) AppendRemediationEvent(ctx context.Context, e *models.RemediationEvent) error {
	if e.ID == "" {
		e.ID = uuid.NewString()
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	_, err := r.db.NamedExecContext(ctx,
		`INSERT INTO remediation_events (id, session_id, seq, type, payload_json, created_at)
		 VALUES (:id, :session_id, :seq, :type, :payload_json, :created_at)`, e)
	return err
}

func (r *remediationRepository) ListRemediationEvents(ctx context.Context, sessionID string, afterSeq int) ([]models.RemediationEvent, error) {
	var events []models.RemediationEvent
	err := r.db.SelectContext(ctx, &events, r.db.Rebind(
		`SELECT * FROM remediation_events WHERE session_id = ? AND seq > ? ORDER BY seq ASC`),
		sessionID, afterSeq)
	return events, err
}

// --- SQLiteStore / PostgresStore Store interface wiring -----------------
//
// Both stores share the dialect-agnostic implementation above; these are
// thin delegations that satisfy the Store interface for each concrete type.

func (s *SQLiteStore) CreateRemediationSession(ctx context.Context, session *models.RemediationSession) error {
	return newRemediationRepository(s.db).CreateRemediationSession(ctx, session)
}

func (s *SQLiteStore) GetRemediationSession(ctx context.Context, id string) (*models.RemediationSession, error) {
	return newRemediationRepository(s.db).GetRemediationSession(ctx, id)
}

func (s *SQLiteStore) ListRemediationSessions(ctx context.Context, userID string) ([]models.RemediationSession, error) {
	return newRemediationRepository(s.db).ListRemediationSessions(ctx, userID)
}

func (s *SQLiteStore) UpdateRemediationSession(ctx context.Context, session *models.RemediationSession) error {
	return newRemediationRepository(s.db).UpdateRemediationSession(ctx, session)
}

func (s *SQLiteStore) SaveRemediationPlan(ctx context.Context, plan *models.RemediationPlan) error {
	return newRemediationRepository(s.db).SaveRemediationPlan(ctx, plan)
}

func (s *SQLiteStore) GetRemediationPlan(ctx context.Context, sessionID string) (*models.RemediationPlan, error) {
	return newRemediationRepository(s.db).GetRemediationPlan(ctx, sessionID)
}

func (s *SQLiteStore) ApproveRemediationPlan(ctx context.Context, sessionID, approverID string) error {
	return newRemediationRepository(s.db).ApproveRemediationPlan(ctx, sessionID, approverID)
}

func (s *SQLiteStore) SaveRemediationPatches(ctx context.Context, sessionID string, patches []models.RemediationPatch) error {
	return newRemediationRepository(s.db).SaveRemediationPatches(ctx, sessionID, patches)
}

func (s *SQLiteStore) ListRemediationPatches(ctx context.Context, sessionID string) ([]models.RemediationPatch, error) {
	return newRemediationRepository(s.db).ListRemediationPatches(ctx, sessionID)
}

func (s *SQLiteStore) AppendRemediationEvent(ctx context.Context, event *models.RemediationEvent) error {
	return newRemediationRepository(s.db).AppendRemediationEvent(ctx, event)
}

func (s *SQLiteStore) ListRemediationEvents(ctx context.Context, sessionID string, afterSeq int) ([]models.RemediationEvent, error) {
	return newRemediationRepository(s.db).ListRemediationEvents(ctx, sessionID, afterSeq)
}

func (s *PostgresStore) CreateRemediationSession(ctx context.Context, session *models.RemediationSession) error {
	return newRemediationRepository(s.db).CreateRemediationSession(ctx, session)
}

func (s *PostgresStore) GetRemediationSession(ctx context.Context, id string) (*models.RemediationSession, error) {
	return newRemediationRepository(s.db).GetRemediationSession(ctx, id)
}

func (s *PostgresStore) ListRemediationSessions(ctx context.Context, userID string) ([]models.RemediationSession, error) {
	return newRemediationRepository(s.db).ListRemediationSessions(ctx, userID)
}

func (s *PostgresStore) UpdateRemediationSession(ctx context.Context, session *models.RemediationSession) error {
	return newRemediationRepository(s.db).UpdateRemediationSession(ctx, session)
}

func (s *PostgresStore) SaveRemediationPlan(ctx context.Context, plan *models.RemediationPlan) error {
	return newRemediationRepository(s.db).SaveRemediationPlan(ctx, plan)
}

func (s *PostgresStore) GetRemediationPlan(ctx context.Context, sessionID string) (*models.RemediationPlan, error) {
	return newRemediationRepository(s.db).GetRemediationPlan(ctx, sessionID)
}

func (s *PostgresStore) ApproveRemediationPlan(ctx context.Context, sessionID, approverID string) error {
	return newRemediationRepository(s.db).ApproveRemediationPlan(ctx, sessionID, approverID)
}

func (s *PostgresStore) SaveRemediationPatches(ctx context.Context, sessionID string, patches []models.RemediationPatch) error {
	return newRemediationRepository(s.db).SaveRemediationPatches(ctx, sessionID, patches)
}

func (s *PostgresStore) ListRemediationPatches(ctx context.Context, sessionID string) ([]models.RemediationPatch, error) {
	return newRemediationRepository(s.db).ListRemediationPatches(ctx, sessionID)
}

func (s *PostgresStore) AppendRemediationEvent(ctx context.Context, event *models.RemediationEvent) error {
	return newRemediationRepository(s.db).AppendRemediationEvent(ctx, event)
}

func (s *PostgresStore) ListRemediationEvents(ctx context.Context, sessionID string, afterSeq int) ([]models.RemediationEvent, error) {
	return newRemediationRepository(s.db).ListRemediationEvents(ctx, sessionID, afterSeq)
}
