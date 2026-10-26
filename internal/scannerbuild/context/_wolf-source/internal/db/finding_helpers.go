package db

import (
	"context"
	"encoding/json"
	"github.com/jmoiron/sqlx"
	"time"

	"github.com/alphabravocompany/thewolf/internal/finding/identity"
	"github.com/alphabravocompany/thewolf/internal/models"
)

func prepareFindingForWrite(f *models.Finding) {
	if f.StableFingerprint == "" || f.LocationFingerprint == "" || f.SemanticFingerprint == "" || f.IdentityVersion == 0 {
		identity.Apply(f)
	}
	if f.Fingerprint == "" {
		f.Fingerprint = f.StableFingerprint
	}
	if f.CorroboratedByJSON == "" {
		tools := f.CorroboratedBy
		if len(tools) == 0 && f.ToolName != "" {
			tools = []string{f.ToolName}
		}
		if data, err := json.Marshal(tools); err == nil {
			f.CorroboratedByJSON = string(data)
		} else {
			f.CorroboratedByJSON = "[]"
		}
	}
}

func hydrateFindingAfterRead(f *models.Finding) {
	if f.StableFingerprint == "" {
		f.StableFingerprint = f.Fingerprint
	}
	if f.CorroboratedByJSON != "" {
		_ = json.Unmarshal([]byte(f.CorroboratedByJSON), &f.CorroboratedBy)
	}
}

func hydrateFindingsAfterRead(findings []models.Finding) {
	for i := range findings {
		hydrateFindingAfterRead(&findings[i])
	}
}

func updateFinding(ctx context.Context, database sqlx.ExtContext, f *models.Finding) error {
	f.UpdatedAt = time.Now().UTC()
	prepareFindingForWrite(f)
	_, err := sqlx.NamedExecContext(ctx, database, `UPDATE findings SET
 ai_context_score=:ai_context_score, ai_fix_suggestion=:ai_fix_suggestion, composite_score=:composite_score,
 tool_severity_score=:tool_severity_score, location_weight=:location_weight,
 module_name=:module_name, function_name=:function_name, symbol_kind=:symbol_kind, file_purpose=:file_purpose, dependents_json=:dependents_json,
 stable_fingerprint=:stable_fingerprint, location_fingerprint=:location_fingerprint, semantic_fingerprint=:semantic_fingerprint, evidence_fingerprint=:evidence_fingerprint,
 identity_version=:identity_version, fine_category=:fine_category, fix_strategy_id=:fix_strategy_id, confidence=:confidence, corroborated_by_json=:corroborated_by_json,
 suppressed=:suppressed, suppression_id=:suppression_id, suppressed_reason=:suppressed_reason,
 source_kind=:source_kind, source_ref=:source_ref, updated_at=:updated_at,
 package_name=:package_name, package_version=:package_version, package_ecosystem=:package_ecosystem, package_url=:package_url, advisory_id=:advisory_id
 WHERE id=:id`, f)
	return err
}
