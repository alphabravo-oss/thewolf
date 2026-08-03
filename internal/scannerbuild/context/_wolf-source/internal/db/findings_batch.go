package db

import (
	"context"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/alphabravocompany/thewolf/internal/models"
)

const insertFindingQuery = `INSERT INTO findings (id, scan_id, repo_id, fingerprint, stable_fingerprint, location_fingerprint,
 semantic_fingerprint, evidence_fingerprint, identity_version, tool_name, category, severity,
 title, description, file_path, line_start, line_end, code_snippet, cwe_id, rule_id,
 tool_severity_score, location_weight, ai_context_score, composite_score,
 ai_fix_suggestion, status, sarif_data, module_name, function_name, symbol_kind, file_purpose, dependents_json,
 fine_category, fix_strategy_id, confidence, corroborated_by_json, suppressed, suppression_id,
 suppressed_reason, baseline_state, introduced_in_scan_id, resolved_in_scan_id, source_kind, source_ref,
 created_at, updated_at)
 VALUES (:id, :scan_id, :repo_id, :fingerprint, :stable_fingerprint, :location_fingerprint,
 :semantic_fingerprint, :evidence_fingerprint, :identity_version, :tool_name, :category, :severity,
 :title, :description, :file_path, :line_start, :line_end, :code_snippet, :cwe_id, :rule_id,
 :tool_severity_score, :location_weight, :ai_context_score, :composite_score,
 :ai_fix_suggestion, :status, :sarif_data, :module_name, :function_name, :symbol_kind, :file_purpose, :dependents_json,
 :fine_category, :fix_strategy_id, :confidence, :corroborated_by_json, :suppressed, :suppression_id,
 :suppressed_reason, :baseline_state, :introduced_in_scan_id, :resolved_in_scan_id, :source_kind, :source_ref,
 :created_at, :updated_at)`

func insertFindingsTx(ctx context.Context, tx *sqlx.Tx, findings []models.Finding) error {
	for i := range findings {
		now := time.Now().UTC()
		findings[i].CreatedAt = now
		findings[i].UpdatedAt = now
		prepareFindingForWrite(&findings[i])
		if _, err := tx.NamedExecContext(ctx, insertFindingQuery, &findings[i]); err != nil {
			return err
		}
	}
	return nil
}
