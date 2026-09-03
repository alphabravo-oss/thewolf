package db

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func (s *SQLiteStore) UpsertVulnerability(ctx context.Context, v *models.Vulnerability) error {
	return upsertVulnerability(ctx, s.db, v)
}
func (s *SQLiteStore) GetVulnerabilityByID(ctx context.Context, id string) (*models.Vulnerability, error) {
	return getVulnerabilityByID(ctx, s.db, id)
}
func (s *SQLiteStore) GetVulnerabilityByRepoKey(ctx context.Context, repoID, canonicalKey string) (*models.Vulnerability, error) {
	return getVulnerabilityByRepoKey(ctx, s.db, repoID, canonicalKey)
}
func (s *SQLiteStore) ListVulnerabilitiesByRepo(ctx context.Context, repoID string) ([]models.Vulnerability, error) {
	return listVulnerabilities(ctx, s.db, "SELECT * FROM vulnerabilities WHERE repo_id = ? ORDER BY composite_score DESC", repoID)
}
func (s *SQLiteStore) ListVulnerabilitiesByScan(ctx context.Context, scanID string) ([]models.Vulnerability, error) {
	return listVulnerabilities(ctx, s.db, "SELECT * FROM vulnerabilities WHERE scan_id = ? ORDER BY composite_score DESC", scanID)
}
func (s *SQLiteStore) ListVulnerabilitiesForUser(ctx context.Context, userID string, fleetMode bool) ([]models.Vulnerability, error) {
	return listVulnerabilitiesForUser(ctx, s.db, userID, fleetMode)
}
func (s *SQLiteStore) InsertVulnerabilityEvidence(ctx context.Context, e *models.VulnerabilityEvidence) error {
	return insertVulnerabilityEvidence(ctx, s.db, e)
}
func (s *SQLiteStore) ListEvidenceByVulnerability(ctx context.Context, vulnerabilityID string) ([]models.VulnerabilityEvidence, error) {
	return listEvidenceByVulnerability(ctx, s.db, vulnerabilityID)
}
func (s *SQLiteStore) MoveVulnerabilityEvidence(ctx context.Context, evidenceIDs []string, toVulnerabilityID string) error {
	return moveVulnerabilityEvidence(ctx, s.db, evidenceIDs, toVulnerabilityID)
}
func (s *SQLiteStore) DeleteVulnerability(ctx context.Context, id string) error {
	return deleteVulnerability(ctx, s.db, id)
}
func (s *SQLiteStore) RefreshVulnerabilityEvidence(ctx context.Context, vulnerabilityID string) error {
	return refreshVulnerabilityEvidence(ctx, s.db, vulnerabilityID)
}

func (s *PostgresStore) UpsertVulnerability(ctx context.Context, v *models.Vulnerability) error {
	return upsertVulnerability(ctx, s.db, v)
}
func (s *PostgresStore) GetVulnerabilityByID(ctx context.Context, id string) (*models.Vulnerability, error) {
	return getVulnerabilityByID(ctx, s.db, id)
}
func (s *PostgresStore) GetVulnerabilityByRepoKey(ctx context.Context, repoID, canonicalKey string) (*models.Vulnerability, error) {
	return getVulnerabilityByRepoKey(ctx, s.db, repoID, canonicalKey)
}
func (s *PostgresStore) ListVulnerabilitiesByRepo(ctx context.Context, repoID string) ([]models.Vulnerability, error) {
	return listVulnerabilities(ctx, s.db, "SELECT * FROM vulnerabilities WHERE repo_id = ? ORDER BY composite_score DESC", repoID)
}
func (s *PostgresStore) ListVulnerabilitiesByScan(ctx context.Context, scanID string) ([]models.Vulnerability, error) {
	return listVulnerabilities(ctx, s.db, "SELECT * FROM vulnerabilities WHERE scan_id = ? ORDER BY composite_score DESC", scanID)
}
func (s *PostgresStore) ListVulnerabilitiesForUser(ctx context.Context, userID string, fleetMode bool) ([]models.Vulnerability, error) {
	return listVulnerabilitiesForUser(ctx, s.db, userID, fleetMode)
}
func (s *PostgresStore) InsertVulnerabilityEvidence(ctx context.Context, e *models.VulnerabilityEvidence) error {
	return insertVulnerabilityEvidence(ctx, s.db, e)
}
func (s *PostgresStore) ListEvidenceByVulnerability(ctx context.Context, vulnerabilityID string) ([]models.VulnerabilityEvidence, error) {
	return listEvidenceByVulnerability(ctx, s.db, vulnerabilityID)
}
func (s *PostgresStore) MoveVulnerabilityEvidence(ctx context.Context, evidenceIDs []string, toVulnerabilityID string) error {
	return moveVulnerabilityEvidence(ctx, s.db, evidenceIDs, toVulnerabilityID)
}
func (s *PostgresStore) DeleteVulnerability(ctx context.Context, id string) error {
	return deleteVulnerability(ctx, s.db, id)
}
func (s *PostgresStore) RefreshVulnerabilityEvidence(ctx context.Context, vulnerabilityID string) error {
	return refreshVulnerabilityEvidence(ctx, s.db, vulnerabilityID)
}

func prepareVulnerabilityForWrite(v *models.Vulnerability) {
	if v.FindingIDsJSON == "" {
		b, _ := json.Marshal(v.FindingIDs)
		if len(b) == 0 {
			b = []byte("[]")
		}
		v.FindingIDsJSON = string(b)
	}
	if v.CorroboratedByJSON == "" {
		b, _ := json.Marshal(v.CorroboratedBy)
		if len(b) == 0 {
			b = []byte("[]")
		}
		v.CorroboratedByJSON = string(b)
	}
}

func hydrateVulnerability(v *models.Vulnerability) {
	if v == nil {
		return
	}
	if v.FindingIDsJSON != "" {
		_ = json.Unmarshal([]byte(v.FindingIDsJSON), &v.FindingIDs)
	}
	if v.CorroboratedByJSON != "" {
		_ = json.Unmarshal([]byte(v.CorroboratedByJSON), &v.CorroboratedBy)
	}
}

func upsertVulnerability(ctx context.Context, database *sqlx.DB, v *models.Vulnerability) error {
	now := time.Now().UTC()
	if v.CreatedAt.IsZero() {
		v.CreatedAt = now
	}
	v.UpdatedAt = now
	prepareVulnerabilityForWrite(v)
	_, err := database.NamedExecContext(ctx,
		`INSERT INTO vulnerabilities (id, repo_id, scan_id, canonical_key, title, severity, category,
		 fine_category, confidence, baseline_state, composite_score, evidence_count,
		 finding_ids_json, corroborated_by_json, suppressed, created_at, updated_at)
		 VALUES (:id, :repo_id, :scan_id, :canonical_key, :title, :severity, :category,
		 :fine_category, :confidence, :baseline_state, :composite_score, :evidence_count,
		 :finding_ids_json, :corroborated_by_json, :suppressed, :created_at, :updated_at)
		 ON CONFLICT (repo_id, canonical_key) DO UPDATE SET
		   scan_id = excluded.scan_id,
		   title = excluded.title,
		   severity = excluded.severity,
		   category = excluded.category,
		   fine_category = excluded.fine_category,
		   confidence = excluded.confidence,
		   baseline_state = excluded.baseline_state,
		   composite_score = excluded.composite_score,
		   suppressed = excluded.suppressed,
		   updated_at = excluded.updated_at`, v)
	return err
}

func getVulnerabilityByID(ctx context.Context, database *sqlx.DB, id string) (*models.Vulnerability, error) {
	q := database.Rebind("SELECT * FROM vulnerabilities WHERE id = ?")
	var v models.Vulnerability
	if err := database.GetContext(ctx, &v, q, id); err != nil {
		return nil, err
	}
	hydrateVulnerability(&v)
	return &v, nil
}

func listVulnerabilities(ctx context.Context, database *sqlx.DB, query string, args ...any) ([]models.Vulnerability, error) {
	q := database.Rebind(query)
	var out []models.Vulnerability
	if err := database.SelectContext(ctx, &out, q, args...); err != nil {
		return nil, err
	}
	for i := range out {
		hydrateVulnerability(&out[i])
	}
	return out, nil
}

func listVulnerabilitiesForUser(ctx context.Context, database *sqlx.DB, userID string, fleetMode bool) ([]models.Vulnerability, error) {
	q := `SELECT v.* FROM vulnerabilities v`
	args := []any{}
	if !fleetMode {
		q += ` WHERE v.repo_id IN (SELECT id FROM repos WHERE user_id = ?)`
		args = append(args, userID)
	}
	q += ` ORDER BY v.composite_score DESC`
	return listVulnerabilities(ctx, database, q, args...)
}

func getVulnerabilityByRepoKey(ctx context.Context, database *sqlx.DB, repoID, canonicalKey string) (*models.Vulnerability, error) {
	q := database.Rebind("SELECT * FROM vulnerabilities WHERE repo_id = ? AND canonical_key = ?")
	var v models.Vulnerability
	if err := database.GetContext(ctx, &v, q, repoID, canonicalKey); err != nil {
		return nil, err
	}
	hydrateVulnerability(&v)
	return &v, nil
}

func insertVulnerabilityEvidence(ctx context.Context, database *sqlx.DB, e *models.VulnerabilityEvidence) error {
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	_, err := database.NamedExecContext(ctx,
		`INSERT INTO vulnerability_evidence (id, vulnerability_id, finding_id, tool_name, title, file_path, line_start, rule_id, reason, created_at)
		 VALUES (:id, :vulnerability_id, :finding_id, :tool_name, :title, :file_path, :line_start, :rule_id, :reason, :created_at)
		 ON CONFLICT (finding_id) DO NOTHING`, e)
	return err
}

func listEvidenceByVulnerability(ctx context.Context, database *sqlx.DB, vulnerabilityID string) ([]models.VulnerabilityEvidence, error) {
	q := database.Rebind("SELECT * FROM vulnerability_evidence WHERE vulnerability_id = ? ORDER BY created_at")
	var out []models.VulnerabilityEvidence
	err := database.SelectContext(ctx, &out, q, vulnerabilityID)
	return out, err
}

func moveVulnerabilityEvidence(ctx context.Context, database *sqlx.DB, evidenceIDs []string, toVulnerabilityID string) error {
	if len(evidenceIDs) == 0 {
		return nil
	}
	q, args, err := sqlx.In(
		"UPDATE vulnerability_evidence SET vulnerability_id = ? WHERE id IN (?)",
		toVulnerabilityID, evidenceIDs,
	)
	if err != nil {
		return err
	}
	_, err = database.ExecContext(ctx, database.Rebind(q), args...)
	return err
}

func deleteVulnerability(ctx context.Context, database *sqlx.DB, id string) error {
	q := database.Rebind("DELETE FROM vulnerabilities WHERE id = ?")
	_, err := database.ExecContext(ctx, q, id)
	return err
}

func refreshVulnerabilityEvidence(ctx context.Context, database *sqlx.DB, vulnerabilityID string) error {
	ev, err := listEvidenceByVulnerability(ctx, database, vulnerabilityID)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(ev))
	tools := make([]string, 0, len(ev))
	seenTool := map[string]struct{}{}
	for _, e := range ev {
		ids = append(ids, e.FindingID)
		if e.ToolName == "" {
			continue
		}
		if _, ok := seenTool[e.ToolName]; ok {
			continue
		}
		seenTool[e.ToolName] = struct{}{}
		tools = append(tools, e.ToolName)
	}
	idJSON, _ := json.Marshal(ids)
	if len(idJSON) == 0 {
		idJSON = []byte("[]")
	}
	toolJSON, _ := json.Marshal(tools)
	if len(toolJSON) == 0 {
		toolJSON = []byte("[]")
	}
	q := database.Rebind(`UPDATE vulnerabilities SET finding_ids_json = ?, corroborated_by_json = ?, evidence_count = ?, updated_at = ? WHERE id = ?`)
	_, err = database.ExecContext(ctx, q, string(idJSON), string(toolJSON), len(ev), time.Now().UTC(), vulnerabilityID)
	return err
}
