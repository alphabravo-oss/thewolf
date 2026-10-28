package db

import (
	_ "embed"
	"fmt"
	"strings"

	"github.com/jmoiron/sqlx"
)

//go:embed migrations/001_initial.sql
var migrationSQL string

//go:embed migrations/002_collection_scan_config.sql
var migration002SQL string

//go:embed migrations/003_repo_detection_cache.sql
var migration003SQL string

//go:embed migrations/004_ai_logs.sql
var migration004SQL string

//go:embed migrations/005_enrichment_and_reports.sql
var migration005SQL string

//go:embed migrations/006_scan_ai_enabled.sql
var migration006SQL string

//go:embed migrations/008_ai_log_cost.sql
var migration008SQL string

//go:embed migrations/009_scan_tools_errors.sql
var migration009SQL string

//go:embed migrations/010_api_tokens_and_audit.sql
var migration010SQL string

//go:embed migrations/011_auth_sessions.sql
var migration011SQL string

//go:embed migrations/012_remote_nodes_and_scan_targets.sql
var migration012SQL string

//go:embed migrations/013_scanner_version_checks.sql
var migration013SQL string

//go:embed migrations/014_scan_quality_foundation.sql
var migration014SQL string

//go:embed migrations/015_finding_suppressions.sql
var migration015SQL string

//go:embed migrations/016_quality_gates.sql
var migration016SQL string

//go:embed migrations/017_scan_artifact_metadata.sql
var migration017SQL string

//go:embed migrations/018_sarif_imports.sql
var migration018SQL string

//go:embed migrations/019_scanner_run_records.sql
var migration019SQL string

//go:embed migrations/020_fleet_mode.sql
var migration020SQL string

//go:embed migrations/021_autofix.sql
var migration021SQL string

//go:embed migrations/022_user_roles.sql
var migration022SQL string

//go:embed migrations/023_mfa.sql
var migration023SQL string

//go:embed migrations/024_user_display_name.sql
var migration024SQL string

//go:embed migrations/025_audit_classification.sql
var migration025SQL string

//go:embed migrations/026_remote_scan_workers.sql
var migration026SQL string

//go:embed migrations/027_credential_metadata.sql
var migration027SQL string

//go:embed migrations/028_artifact_storage_keys.sql
var migration028SQL string

//go:embed migrations/029_scan_tree_digest.sql
var migration029SQL string

//go:embed migrations/031_scanner_run_release_provenance.sql
var migration031SQL string

//go:embed migrations/032_scan_release_assignment.sql
var migration032SQL string

//go:embed migrations/033_scanner_registry_observations.sql
var migration033SQL string

//go:embed migrations/034_scanner_discovery_execution.sql
var migration034SQL string

//go:embed migrations/035_scanner_rollout_controller.sql
var migration035SQL string

//go:embed migrations/036_scanner_proposal_execution.sql
var migration036SQL string

//go:embed migrations/037_scanner_release_sequence.sql
var migration037SQL string

//go:embed migrations/038_scanner_worker_assignment_evidence.sql
var migration038SQL string

//go:embed migrations/039_scanner_release_notifications.sql
var migration039SQL string

//go:embed migrations/040_scanner_release_alerts.sql
var migration040SQL string

//go:embed migrations/041_scan_release_reproducibility.sql
var migration041SQL string

//go:embed migrations/042_scanner_signer_profiles.sql
var migration042SQL string

//go:embed migrations/043_scanner_registry_reconciliation.sql
var migration043SQL string

//go:embed migrations/044_scanner_release_backup_restore.sql
var migration044SQL string

//go:embed migrations/045_scanner_operation_correlation.sql
var migration045SQL string

//go:embed migrations/046_scanner_custom_builds.sql
var migration046SQL string

//go:embed migrations/047_scanner_release_image_kind.sql
var migration047SQL string

//go:embed migrations/048_scanner_release_exceptions.sql
var migration048SQL string

//go:embed migrations/049_user_scanner_supply_chain_access.sql
var migration049SQL string

//go:embed migrations/050_scanner_release_signature_identity.sql
var migration050SQL string

//go:embed migrations/051_autofix_loop_push.sql
var migration051SQL string

//go:embed migrations/052_fixer_model_effort.sql
var migration052SQL string

//go:embed migrations/053_fixer_console.sql
var migration053SQL string

//go:embed migrations/054_scan_remediation.sql
var migration054SQL string

//go:embed migrations/055_fix_job_planned_runs.sql
var migration055SQL string

//go:embed migrations/056_scan_schedules.sql
var migration056SQL string

//go:embed migrations/057_vulnerabilities.sql
var migration057SQL string

//go:embed migrations/058_vulnerability_evidence.sql
var migration058SQL string

//go:embed migrations/059_enterprise_records.sql
var migration059SQL string

//go:embed migrations/060_test_coverage.sql
var migration060SQL string

//go:embed migrations/061_finding_dependency_identity.sql
var migration061SQL string

//go:embed migrations/062_finding_clusters.sql
var migration062SQL string

//go:embed migrations/063_finding_revisions.sql
var migration063SQL string

//go:embed migrations/064_artifact_finding_revision.sql
var migration064SQL string

//go:embed migrations/065_finding_baseline_snapshot.sql
var migration065SQL string

//go:embed migrations/066_baseline_identity.sql
var migration066SQL string

//go:embed migrations/067_gate_scan_status.sql
var migration067SQL string

//go:embed migrations/068_mfa_replay.sql
var migration068SQL string

//go:embed migrations/069_scan_ignore_policy.sql
var migration069SQL string

//go:embed migrations/070_finding_pages.sql
var migration070SQL string

//go:embed migrations/071_fix_creation.sql
var migration071SQL string

//go:embed migrations/072_fix_job_revision.sql
var migration072SQL string

//go:embed migrations/073_repo_dast.sql
var migration073SQL string

// execColumnMigration atomically adds a column and initializes old rows once.
// An existing column means the data rewrite already ran; replaying it can undo
// later user decisions (roles) or invalidate current scan results.
func execColumnMigration(database *sqlx.DB, migration string) error {
	tx, err := database.Beginx()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(migration); err != nil {
		message := strings.ToLower(err.Error())
		if strings.Contains(message, "duplicate column") || (strings.Contains(message, "column") && strings.Contains(message, "already exists")) {
			return nil
		}
		return err
	}
	return tx.Commit()
}

// execAdditiveMigration executes additive DDL one statement at a time. This
// makes migrations containing several ALTER TABLE statements safely
// restartable after an interrupted upgrade.
func execAdditiveMigration(db *sqlx.DB, migration string) error {
	// Full-line comments may contain semicolons. They are documentation,
	// not statement separators (notably in the older user/MFA migrations).
	lines := strings.Split(migration, "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			lines[i] = ""
		}
	}
	migration = strings.Join(lines, "\n")
	for _, statement := range strings.Split(migration, ";") {
		statement = strings.TrimSpace(statement)
		if statement == "" {
			continue
		}
		if _, err := db.Exec(statement); err != nil {
			msg := strings.ToLower(err.Error())
			if strings.Contains(msg, "duplicate column") ||
				strings.Contains(msg, "already exists") ||
				strings.Contains(msg, "duplicate key") {
				continue
			}
			return fmt.Errorf("additive migration statement failed: %w", err)
		}
	}
	return nil
}
