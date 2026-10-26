package db

import (
	"context"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func (s *PostgresStore) UpsertQualityPolicy(ctx context.Context, policy *models.QualityPolicy) error {
	now := time.Now().UTC()
	if policy.CreatedAt.IsZero() {
		policy.CreatedAt = now
	}
	policy.UpdatedAt = now
	if policy.Scope == "" {
		policy.Scope = "global"
	}
	if policy.Mode == "" {
		policy.Mode = "warn"
	}
	if policy.RulesJSON == "" {
		policy.RulesJSON = "[]"
	}
	_, err := s.db.NamedExecContext(ctx,
		`INSERT INTO quality_policies (id, name, scope, scope_id, mode, rules_json, enabled, created_by, created_at, updated_at)
		 VALUES (:id, :name, :scope, :scope_id, :mode, :rules_json, :enabled, :created_by, :created_at, :updated_at)
		 ON CONFLICT (scope, scope_id, name) DO UPDATE SET
		   mode = excluded.mode,
		   rules_json = excluded.rules_json,
		   enabled = excluded.enabled,
		   updated_at = excluded.updated_at`,
		policy)
	return err
}

func (s *PostgresStore) GetQualityPolicyByID(ctx context.Context, id string) (*models.QualityPolicy, error) {
	var policy models.QualityPolicy
	err := s.db.GetContext(ctx, &policy, "SELECT * FROM quality_policies WHERE id = $1", id)
	if err != nil {
		return nil, err
	}
	return &policy, nil
}

func (s *PostgresStore) ListQualityPolicies(ctx context.Context, scope, scopeID string) ([]models.QualityPolicy, error) {
	var policies []models.QualityPolicy
	var err error
	if scope == "" {
		err = s.db.SelectContext(ctx, &policies, "SELECT * FROM quality_policies ORDER BY scope, scope_id, name")
	} else {
		err = s.db.SelectContext(ctx, &policies,
			"SELECT * FROM quality_policies WHERE scope = $1 AND scope_id = $2 ORDER BY name", scope, scopeID)
	}
	return policies, err
}

func (s *PostgresStore) UpsertQualityGateResult(ctx context.Context, result *models.QualityGateResult) error {
	return upsertQualityGateResult(ctx, s.db, result)
}

func (s *PostgresStore) GetQualityGateResult(ctx context.Context, scanID, policyID string) (*models.QualityGateResult, error) {
	var result models.QualityGateResult
	err := s.db.GetContext(ctx, &result,
		"SELECT g.* FROM quality_gate_results g JOIN scans s ON s.id = g.scan_id AND s.findings_revision = g.findings_revision AND s.status = g.scan_status WHERE g.scan_id = $1 AND policy_id = $2", scanID, policyID)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (s *PostgresStore) ListQualityGateResults(ctx context.Context, scanID string) ([]models.QualityGateResult, error) {
	var results []models.QualityGateResult
	err := s.db.SelectContext(ctx, &results,
		"SELECT g.* FROM quality_gate_results g JOIN scans s ON s.id = g.scan_id AND s.findings_revision = g.findings_revision AND s.status = g.scan_status WHERE g.scan_id = $1 ORDER BY evaluated_at DESC", scanID)
	return results, err
}
