package db

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"

	"github.com/alphabravocompany/thewolf/internal/models"
)

// PostgresStore implements Store using PostgreSQL.
type PostgresStore struct {
	db *sqlx.DB
}

// NewPostgres creates a new Postgres store.
func NewPostgres(dsn string) (*PostgresStore, error) {
	db, err := sqlx.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	store := &PostgresStore{db: db}
	if err := store.Migrate(); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return store, nil
}

func (s *PostgresStore) Close() error { return s.db.Close() }

func (s *PostgresStore) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

func (s *PostgresStore) Migrate() error {
	if _, err := s.db.Exec(migrationSQL); err != nil {
		return err
	}
	if _, err := s.db.Exec(migration002SQL); err != nil {
		if !strings.Contains(err.Error(), "duplicate column") && !strings.Contains(err.Error(), "already exists") {
			return err
		}
	}
	if _, err := s.db.Exec(migration003SQL); err != nil {
		if !strings.Contains(err.Error(), "duplicate column") && !strings.Contains(err.Error(), "already exists") {
			return err
		}
	}
	if _, err := s.db.Exec(migration004SQL); err != nil {
		if !strings.Contains(err.Error(), "already exists") {
			return err
		}
	}
	if _, err := s.db.Exec(migration005SQL); err != nil {
		if !strings.Contains(err.Error(), "duplicate column") && !strings.Contains(err.Error(), "already exists") {
			return err
		}
	}
	if _, err := s.db.Exec(migration006SQL); err != nil {
		if !strings.Contains(err.Error(), "duplicate column") && !strings.Contains(err.Error(), "already exists") {
			return err
		}
	}
	if _, err := s.db.Exec(migration007SQL); err != nil {
		if !strings.Contains(err.Error(), "already exists") && !strings.Contains(err.Error(), "syntax error") {
			return err
		}
	}
	if _, err := s.db.Exec(migration008SQL); err != nil {
		if !strings.Contains(err.Error(), "duplicate column") && !strings.Contains(err.Error(), "already exists") {
			return err
		}
	}
	if _, err := s.db.Exec(migration009SQL); err != nil {
		if !strings.Contains(err.Error(), "duplicate column") && !strings.Contains(err.Error(), "already exists") {
			return err
		}
	}
	// Seed default setting using Postgres-compatible syntax.
	if _, err := s.db.Exec(`INSERT INTO settings (key, value) VALUES ('ai_enabled', 'true') ON CONFLICT(key) DO NOTHING`); err != nil {
		if !strings.Contains(err.Error(), "already exists") && !strings.Contains(err.Error(), "does not exist") {
			return err
		}
	}
	if _, err := s.db.Exec(`INSERT INTO settings (key, value) VALUES ('registration_enabled', 'true') ON CONFLICT(key) DO NOTHING`); err != nil {
		if !strings.Contains(err.Error(), "already exists") && !strings.Contains(err.Error(), "does not exist") {
			return err
		}
	}
	if _, err := s.db.Exec(migration010SQL); err != nil {
		if !strings.Contains(err.Error(), "duplicate column") && !strings.Contains(err.Error(), "already exists") {
			return err
		}
	}
	if _, err := s.db.Exec(migration011SQL); err != nil {
		if !strings.Contains(err.Error(), "duplicate column") && !strings.Contains(err.Error(), "already exists") {
			return err
		}
	}
	if _, err := s.db.Exec(migration012SQL); err != nil {
		if !strings.Contains(err.Error(), "duplicate column") && !strings.Contains(err.Error(), "already exists") {
			return err
		}
	}
	return nil
}

// --- Users ---

func (s *PostgresStore) CreateUser(ctx context.Context, user *models.User) error {
	now := time.Now().UTC()
	user.CreatedAt = now
	user.UpdatedAt = now
	_, err := s.db.NamedExecContext(ctx,
		`INSERT INTO users (id, email, password_hash, created_at, updated_at)
		 VALUES (:id, :email, :password_hash, :created_at, :updated_at)`, user)
	return err
}

func (s *PostgresStore) GetUserByID(ctx context.Context, id string) (*models.User, error) {
	var u models.User
	err := s.db.GetContext(ctx, &u, "SELECT * FROM users WHERE id = $1", id)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *PostgresStore) GetUserByEmail(ctx context.Context, email string) (*models.User, error) {
	var u models.User
	err := s.db.GetContext(ctx, &u, "SELECT * FROM users WHERE email = $1", email)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *PostgresStore) UpdateUser(ctx context.Context, user *models.User) error {
	user.UpdatedAt = time.Now().UTC()
	_, err := s.db.NamedExecContext(ctx,
		`UPDATE users SET email=:email, password_hash=:password_hash, updated_at=:updated_at WHERE id=:id`, user)
	return err
}

func (s *PostgresStore) ListUsers(ctx context.Context) ([]models.User, error) {
	var users []models.User
	err := s.db.SelectContext(ctx, &users,
		`SELECT id, email, password_hash, created_at, updated_at FROM users ORDER BY created_at ASC`)
	return users, err
}

func (s *PostgresStore) DeleteUser(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, id)
	return err
}

// --- Repos ---

func (s *PostgresStore) CreateRepo(ctx context.Context, repo *models.Repo) error {
	now := time.Now().UTC()
	repo.CreatedAt = now
	repo.UpdatedAt = now
	_, err := s.db.NamedExecContext(ctx,
		`INSERT INTO repos (id, user_id, name, source_type, source_path, remote_node_id, remote_path,
		 last_commit_sha, last_dirty_state, default_branch, created_at, updated_at)
		 VALUES (:id, :user_id, :name, :source_type, :source_path, :remote_node_id, :remote_path,
		 :last_commit_sha, :last_dirty_state, :default_branch, :created_at, :updated_at)`, repo)
	return err
}

func (s *PostgresStore) GetRepoByID(ctx context.Context, id string) (*models.Repo, error) {
	var r models.Repo
	err := s.db.GetContext(ctx, &r, "SELECT * FROM repos WHERE id = $1", id)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *PostgresStore) ListReposByUser(ctx context.Context, userID string) ([]models.Repo, error) {
	var repos []models.Repo
	// No RBAC yet — all authenticated users see all repos.
	err := s.db.SelectContext(ctx, &repos, "SELECT * FROM repos ORDER BY created_at DESC")
	return repos, err
}

func (s *PostgresStore) UpdateRepo(ctx context.Context, repo *models.Repo) error {
	repo.UpdatedAt = time.Now().UTC()
	_, err := s.db.NamedExecContext(ctx,
		`UPDATE repos SET name=:name, source_type=:source_type, source_path=:source_path,
		 remote_node_id=:remote_node_id, remote_path=:remote_path, last_commit_sha=:last_commit_sha,
		 last_dirty_state=:last_dirty_state, default_branch=:default_branch, updated_at=:updated_at WHERE id=:id`, repo)
	return err
}

func (s *PostgresStore) UpdateRepoDetection(ctx context.Context, repoID, languages, frameworks string) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		`UPDATE repos SET detected_languages = $1, detected_frameworks = $2, detected_at = $3, updated_at = $4 WHERE id = $5`,
		languages, frameworks, now, now, repoID)
	return err
}

func (s *PostgresStore) DeleteRepo(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM repos WHERE id = $1", id)
	return err
}

// --- Collections ---

func (s *PostgresStore) CreateCollection(ctx context.Context, col *models.Collection) error {
	now := time.Now().UTC()
	col.CreatedAt = now
	col.UpdatedAt = now
	_, err := s.db.NamedExecContext(ctx,
		`INSERT INTO collections (id, user_id, name, description, scan_config, created_at, updated_at)
		 VALUES (:id, :user_id, :name, :description, :scan_config, :created_at, :updated_at)`, col)
	return err
}

func (s *PostgresStore) GetCollectionByID(ctx context.Context, id string) (*models.Collection, error) {
	var c models.Collection
	err := s.db.GetContext(ctx, &c, "SELECT * FROM collections WHERE id = $1", id)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *PostgresStore) GetCollectionByName(ctx context.Context, name string) (*models.Collection, error) {
	var c models.Collection
	err := s.db.GetContext(ctx, &c, "SELECT * FROM collections WHERE name = $1", name)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *PostgresStore) ListCollectionsByUser(ctx context.Context, userID string) ([]models.Collection, error) {
	var cols []models.Collection
	// No RBAC yet — all authenticated users see all collections.
	err := s.db.SelectContext(ctx, &cols,
		`SELECT c.*, COUNT(cr.repo_id) AS repo_count
		 FROM collections c
		 LEFT JOIN collection_repos cr ON cr.collection_id = c.id
		 GROUP BY c.id
		 ORDER BY c.created_at DESC`)
	return cols, err
}

func (s *PostgresStore) UpdateCollection(ctx context.Context, col *models.Collection) error {
	col.UpdatedAt = time.Now().UTC()
	_, err := s.db.NamedExecContext(ctx,
		`UPDATE collections SET name=:name, description=:description, scan_config=:scan_config, updated_at=:updated_at WHERE id=:id`, col)
	return err
}

func (s *PostgresStore) DeleteCollection(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM collections WHERE id = $1", id)
	return err
}

func (s *PostgresStore) AddRepoToCollection(ctx context.Context, collectionID, repoID string) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO collection_repos (collection_id, repo_id) VALUES ($1, $2) ON CONFLICT DO NOTHING",
		collectionID, repoID)
	return err
}

func (s *PostgresStore) RemoveRepoFromCollection(ctx context.Context, collectionID, repoID string) error {
	_, err := s.db.ExecContext(ctx,
		"DELETE FROM collection_repos WHERE collection_id = $1 AND repo_id = $2",
		collectionID, repoID)
	return err
}

func (s *PostgresStore) ListReposInCollection(ctx context.Context, collectionID string) ([]models.Repo, error) {
	var repos []models.Repo
	err := s.db.SelectContext(ctx, &repos,
		`SELECT r.* FROM repos r
		 JOIN collection_repos cr ON cr.repo_id = r.id
		 WHERE cr.collection_id = $1`, collectionID)
	return repos, err
}

// --- Secrets ---

func (s *PostgresStore) CreateSecret(ctx context.Context, secret *models.Secret) error {
	now := time.Now().UTC()
	secret.CreatedAt = now
	secret.UpdatedAt = now
	_, err := s.db.NamedExecContext(ctx,
		`INSERT INTO secrets (id, user_id, key_type, key_name, encrypted_value, created_at, updated_at)
		 VALUES (:id, :user_id, :key_type, :key_name, :encrypted_value, :created_at, :updated_at)`, secret)
	return err
}

func (s *PostgresStore) GetSecretByID(ctx context.Context, id string) (*models.Secret, error) {
	var sec models.Secret
	err := s.db.GetContext(ctx, &sec, "SELECT * FROM secrets WHERE id = $1", id)
	if err != nil {
		return nil, err
	}
	return &sec, nil
}

func (s *PostgresStore) ListSecretsByUser(ctx context.Context, userID string) ([]models.Secret, error) {
	var secs []models.Secret
	// No RBAC yet — all authenticated users see all secrets.
	err := s.db.SelectContext(ctx, &secs, "SELECT * FROM secrets ORDER BY created_at DESC")
	return secs, err
}

func (s *PostgresStore) DeleteSecret(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM secrets WHERE id = $1", id)
	return err
}

// --- RepoMaps ---

func (s *PostgresStore) CreateRepoMap(ctx context.Context, rm *models.RepoMap) error {
	now := time.Now().UTC()
	rm.CreatedAt = now
	rm.UpdatedAt = now
	_, err := s.db.NamedExecContext(ctx,
		`INSERT INTO repo_maps (id, repo_id, branch, structural_data, semantic_data, file_hashes, created_at, updated_at)
		 VALUES (:id, :repo_id, :branch, :structural_data, :semantic_data, :file_hashes, :created_at, :updated_at)`, rm)
	return err
}

func (s *PostgresStore) GetRepoMap(ctx context.Context, repoID, branch string) (*models.RepoMap, error) {
	var rm models.RepoMap
	err := s.db.GetContext(ctx, &rm,
		"SELECT * FROM repo_maps WHERE repo_id = $1 AND branch = $2 ORDER BY created_at DESC LIMIT 1",
		repoID, branch)
	if err != nil {
		return nil, err
	}
	return &rm, nil
}

func (s *PostgresStore) UpdateRepoMap(ctx context.Context, rm *models.RepoMap) error {
	rm.UpdatedAt = time.Now().UTC()
	_, err := s.db.NamedExecContext(ctx,
		`UPDATE repo_maps SET structural_data=:structural_data, semantic_data=:semantic_data, file_hashes=:file_hashes, updated_at=:updated_at WHERE id=:id`, rm)
	return err
}

// --- Scans ---

func (s *PostgresStore) CreateScan(ctx context.Context, scan *models.Scan) error {
	now := time.Now().UTC()
	scan.CreatedAt = now
	scan.UpdatedAt = now
	if scan.ToolsErrors == "" {
		scan.ToolsErrors = "{}"
	}
	_, err := s.db.NamedExecContext(ctx,
		`INSERT INTO scans (id, user_id, repo_id, collection_id, loop_id, iteration, branch, status,
		 source_type, remote_node_id, source_path, commit_sha, dirty_state, prepared_workspace,
		 tools_selected, tools_completed, tools_failed, tools_errors, finding_count, coverage_summary, ai_enabled, ai_summary,
		 started_at, completed_at, created_at, updated_at)
		 VALUES (:id, :user_id, :repo_id, :collection_id, :loop_id, :iteration, :branch, :status,
		 :source_type, :remote_node_id, :source_path, :commit_sha, :dirty_state, :prepared_workspace,
		 :tools_selected, :tools_completed, :tools_failed, :tools_errors, :finding_count, :coverage_summary, :ai_enabled, :ai_summary,
		 :started_at, :completed_at, :created_at, :updated_at)`, scan)
	return err
}

func (s *PostgresStore) GetScanByID(ctx context.Context, id string) (*models.Scan, error) {
	var scan models.Scan
	err := s.db.GetContext(ctx, &scan, "SELECT * FROM scans WHERE id = $1", id)
	if err != nil {
		return nil, err
	}
	return &scan, nil
}

func (s *PostgresStore) ListAllScans(ctx context.Context) ([]models.Scan, error) {
	var scans []models.Scan
	err := s.db.SelectContext(ctx, &scans, "SELECT * FROM scans ORDER BY created_at DESC")
	return scans, err
}

func (s *PostgresStore) ListScansByUser(ctx context.Context, userID string) ([]models.Scan, error) {
	var scans []models.Scan
	// No RBAC yet — all authenticated users see all scans.
	err := s.db.SelectContext(ctx, &scans, "SELECT * FROM scans ORDER BY created_at DESC")
	return scans, err
}

func (s *PostgresStore) ListScansByRepo(ctx context.Context, repoID string) ([]models.Scan, error) {
	var scans []models.Scan
	err := s.db.SelectContext(ctx, &scans, "SELECT * FROM scans WHERE repo_id = $1 ORDER BY created_at DESC", repoID)
	return scans, err
}

func (s *PostgresStore) ListScansByCollection(ctx context.Context, collectionID string) ([]models.Scan, error) {
	var scans []models.Scan
	err := s.db.SelectContext(ctx, &scans, "SELECT * FROM scans WHERE collection_id = $1 ORDER BY created_at DESC", collectionID)
	return scans, err
}

func (s *PostgresStore) UpdateScan(ctx context.Context, scan *models.Scan) error {
	scan.UpdatedAt = time.Now().UTC()
	_, err := s.db.NamedExecContext(ctx,
		`UPDATE scans SET status=:status, tools_selected=:tools_selected, tools_completed=:tools_completed,
		 tools_failed=:tools_failed, tools_errors=:tools_errors, finding_count=:finding_count, coverage_summary=:coverage_summary,
		 ai_enabled=:ai_enabled, ai_summary=:ai_summary, source_type=:source_type, remote_node_id=:remote_node_id,
		 source_path=:source_path, commit_sha=:commit_sha, dirty_state=:dirty_state, prepared_workspace=:prepared_workspace,
		 started_at=:started_at, completed_at=:completed_at, updated_at=:updated_at
		 WHERE id=:id`, scan)
	return err
}

func (s *PostgresStore) DeleteScan(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM scans WHERE id = $1", id)
	return err
}

// --- Findings ---

func (s *PostgresStore) CreateFinding(ctx context.Context, f *models.Finding) error {
	now := time.Now().UTC()
	f.CreatedAt = now
	f.UpdatedAt = now
	_, err := s.db.NamedExecContext(ctx,
		`INSERT INTO findings (id, scan_id, repo_id, fingerprint, tool_name, category, severity,
		 title, description, file_path, line_start, line_end, code_snippet, cwe_id, rule_id,
		 tool_severity_score, location_weight, ai_context_score, composite_score,
		 ai_fix_suggestion, status, sarif_data, module_name, function_name, symbol_kind, file_purpose, dependents_json, created_at, updated_at)
		 VALUES (:id, :scan_id, :repo_id, :fingerprint, :tool_name, :category, :severity,
		 :title, :description, :file_path, :line_start, :line_end, :code_snippet, :cwe_id, :rule_id,
		 :tool_severity_score, :location_weight, :ai_context_score, :composite_score,
		 :ai_fix_suggestion, :status, :sarif_data, :module_name, :function_name, :symbol_kind, :file_purpose, :dependents_json, :created_at, :updated_at)`, f)
	return err
}

func (s *PostgresStore) CreateFindings(ctx context.Context, findings []models.Finding) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i := range findings {
		now := time.Now().UTC()
		findings[i].CreatedAt = now
		findings[i].UpdatedAt = now
		_, err := tx.NamedExecContext(ctx,
			`INSERT INTO findings (id, scan_id, repo_id, fingerprint, tool_name, category, severity,
			 title, description, file_path, line_start, line_end, code_snippet, cwe_id, rule_id,
			 tool_severity_score, location_weight, ai_context_score, composite_score,
			 ai_fix_suggestion, status, sarif_data, module_name, function_name, symbol_kind, file_purpose, dependents_json, created_at, updated_at)
			 VALUES (:id, :scan_id, :repo_id, :fingerprint, :tool_name, :category, :severity,
			 :title, :description, :file_path, :line_start, :line_end, :code_snippet, :cwe_id, :rule_id,
			 :tool_severity_score, :location_weight, :ai_context_score, :composite_score,
			 :ai_fix_suggestion, :status, :sarif_data, :module_name, :function_name, :symbol_kind, :file_purpose, :dependents_json, :created_at, :updated_at)`, &findings[i])
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *PostgresStore) GetFindingByID(ctx context.Context, id string) (*models.Finding, error) {
	var f models.Finding
	err := s.db.GetContext(ctx, &f, "SELECT * FROM findings WHERE id = $1", id)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

func (s *PostgresStore) ListFindingsByScan(ctx context.Context, scanID string) ([]models.Finding, error) {
	var findings []models.Finding
	err := s.db.SelectContext(ctx, &findings,
		"SELECT * FROM findings WHERE scan_id = $1 ORDER BY composite_score DESC", scanID)
	return findings, err
}

func (s *PostgresStore) ListFindingsByRepo(ctx context.Context, repoID string) ([]models.Finding, error) {
	var findings []models.Finding
	err := s.db.SelectContext(ctx, &findings,
		"SELECT * FROM findings WHERE repo_id = $1 ORDER BY composite_score DESC", repoID)
	return findings, err
}

func (s *PostgresStore) UpdateFinding(ctx context.Context, f *models.Finding) error {
	f.UpdatedAt = time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		`UPDATE findings SET ai_context_score = $1, ai_fix_suggestion = $2, composite_score = $3,
		 tool_severity_score = $4, location_weight = $5,
		 module_name = $6, function_name = $7, symbol_kind = $8, file_purpose = $9, dependents_json = $10,
		 updated_at = $11 WHERE id = $12`,
		f.AIContextScore, f.AIFixSuggestion, f.CompositeScore,
		f.ToolSeverityScore, f.LocationWeight,
		f.ModuleName, f.FunctionName, f.SymbolKind, f.FilePurpose, f.DependentsJSON,
		f.UpdatedAt, f.ID)
	return err
}

func (s *PostgresStore) UpdateFindingStatus(ctx context.Context, id string, status models.Status) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE findings SET status = $1, updated_at = $2 WHERE id = $3",
		status, time.Now().UTC(), id)
	return err
}

// --- Fixes ---

func (s *PostgresStore) CreateFix(ctx context.Context, fix *models.Fix) error {
	now := time.Now().UTC()
	fix.CreatedAt = now
	fix.UpdatedAt = now
	_, err := s.db.NamedExecContext(ctx,
		`INSERT INTO fixes (id, user_id, scan_id, loop_id, status, severity_filter, branch_name,
		 worktree_path, findings_attempted, findings_fixed, findings_failed, pr_urls,
		 started_at, completed_at, created_at, updated_at)
		 VALUES (:id, :user_id, :scan_id, :loop_id, :status, :severity_filter, :branch_name,
		 :worktree_path, :findings_attempted, :findings_fixed, :findings_failed, :pr_urls,
		 :started_at, :completed_at, :created_at, :updated_at)`, fix)
	return err
}

func (s *PostgresStore) GetFixByID(ctx context.Context, id string) (*models.Fix, error) {
	var f models.Fix
	err := s.db.GetContext(ctx, &f, "SELECT * FROM fixes WHERE id = $1", id)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

func (s *PostgresStore) ListFixesByUser(ctx context.Context, userID string) ([]models.Fix, error) {
	var fixes []models.Fix
	// No RBAC yet — all authenticated users see all fixes.
	err := s.db.SelectContext(ctx, &fixes, "SELECT * FROM fixes ORDER BY created_at DESC")
	return fixes, err
}

func (s *PostgresStore) UpdateFix(ctx context.Context, fix *models.Fix) error {
	fix.UpdatedAt = time.Now().UTC()
	_, err := s.db.NamedExecContext(ctx,
		`UPDATE fixes SET status=:status, severity_filter=:severity_filter, branch_name=:branch_name,
		 worktree_path=:worktree_path, findings_attempted=:findings_attempted, findings_fixed=:findings_fixed,
		 findings_failed=:findings_failed, pr_urls=:pr_urls, started_at=:started_at,
		 completed_at=:completed_at, updated_at=:updated_at WHERE id=:id`, fix)
	return err
}

// --- FixItems ---

func (s *PostgresStore) CreateFixItem(ctx context.Context, item *models.FixItem) error {
	now := time.Now().UTC()
	item.CreatedAt = now
	item.UpdatedAt = now
	_, err := s.db.NamedExecContext(ctx,
		`INSERT INTO fix_items (id, fix_id, finding_id, status, files_changed, diff,
		 validation_result, validation_output, error_message, created_at, updated_at)
		 VALUES (:id, :fix_id, :finding_id, :status, :files_changed, :diff,
		 :validation_result, :validation_output, :error_message, :created_at, :updated_at)`, item)
	return err
}

func (s *PostgresStore) ListFixItemsByFix(ctx context.Context, fixID string) ([]models.FixItem, error) {
	var items []models.FixItem
	err := s.db.SelectContext(ctx, &items, "SELECT * FROM fix_items WHERE fix_id = $1", fixID)
	return items, err
}

func (s *PostgresStore) UpdateFixItem(ctx context.Context, item *models.FixItem) error {
	item.UpdatedAt = time.Now().UTC()
	_, err := s.db.NamedExecContext(ctx,
		`UPDATE fix_items SET status=:status, files_changed=:files_changed, diff=:diff,
		 validation_result=:validation_result, validation_output=:validation_output,
		 error_message=:error_message, updated_at=:updated_at WHERE id=:id`, item)
	return err
}

// --- Loops ---

func (s *PostgresStore) CreateLoop(ctx context.Context, loop *models.Loop) error {
	now := time.Now().UTC()
	loop.CreatedAt = now
	loop.UpdatedAt = now
	_, err := s.db.NamedExecContext(ctx,
		`INSERT INTO loops (id, user_id, repo_id, collection_id, status, max_iterations,
		 current_iteration, severity_filter, rescan_strategy, total_findings_initial,
		 total_findings_fixed, total_findings_new, total_findings_remaining, guardrail_warnings,
		 started_at, completed_at, created_at, updated_at)
		 VALUES (:id, :user_id, :repo_id, :collection_id, :status, :max_iterations,
		 :current_iteration, :severity_filter, :rescan_strategy, :total_findings_initial,
		 :total_findings_fixed, :total_findings_new, :total_findings_remaining, :guardrail_warnings,
		 :started_at, :completed_at, :created_at, :updated_at)`, loop)
	return err
}

func (s *PostgresStore) GetLoopByID(ctx context.Context, id string) (*models.Loop, error) {
	var l models.Loop
	err := s.db.GetContext(ctx, &l, "SELECT * FROM loops WHERE id = $1", id)
	if err != nil {
		return nil, err
	}
	return &l, nil
}

func (s *PostgresStore) ListLoopsByUser(ctx context.Context, userID string) ([]models.Loop, error) {
	var loops []models.Loop
	// No RBAC yet — all authenticated users see all loops.
	err := s.db.SelectContext(ctx, &loops, "SELECT * FROM loops ORDER BY created_at DESC")
	return loops, err
}

func (s *PostgresStore) UpdateLoop(ctx context.Context, loop *models.Loop) error {
	loop.UpdatedAt = time.Now().UTC()
	_, err := s.db.NamedExecContext(ctx,
		`UPDATE loops SET status=:status, current_iteration=:current_iteration,
		 total_findings_fixed=:total_findings_fixed, total_findings_new=:total_findings_new,
		 total_findings_remaining=:total_findings_remaining, guardrail_warnings=:guardrail_warnings,
		 completed_at=:completed_at, updated_at=:updated_at WHERE id=:id`, loop)
	return err
}

// --- ScanArtifacts ---

func (s *PostgresStore) CreateScanArtifact(ctx context.Context, artifact *models.ScanArtifact) error {
	now := time.Now().UTC()
	artifact.CreatedAt = now
	artifact.UpdatedAt = now
	_, err := s.db.NamedExecContext(ctx,
		`INSERT INTO scan_artifacts (id, scan_id, artifact_type, file_path, file_size, created_at, updated_at)
		 VALUES (:id, :scan_id, :artifact_type, :file_path, :file_size, :created_at, :updated_at)`, artifact)
	return err
}

func (s *PostgresStore) ListScanArtifacts(ctx context.Context, scanID string) ([]models.ScanArtifact, error) {
	var artifacts []models.ScanArtifact
	err := s.db.SelectContext(ctx, &artifacts,
		"SELECT * FROM scan_artifacts WHERE scan_id = $1 ORDER BY created_at", scanID)
	return artifacts, err
}

// --- AILogs ---

func (s *PostgresStore) CreateAILog(ctx context.Context, log *models.AILog) error {
	if log.CreatedAt.IsZero() {
		log.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.NamedExecContext(ctx,
		`INSERT INTO ai_logs (id, scan_id, provider, model, phase, tool_name, prompt, response, error, prompt_tokens, response_tokens, duration_ms, created_at)
		 VALUES (:id, :scan_id, :provider, :model, :phase, :tool_name, :prompt, :response, :error, :prompt_tokens, :response_tokens, :duration_ms, :created_at)`, log)
	return err
}

func (s *PostgresStore) ListAILogsByScan(ctx context.Context, scanID string) ([]models.AILog, error) {
	var logs []models.AILog
	err := s.db.SelectContext(ctx, &logs,
		"SELECT * FROM ai_logs WHERE scan_id = $1 ORDER BY created_at", scanID)
	return logs, err
}

// --- ToolSummaries ---

func (s *PostgresStore) CreateToolSummary(ctx context.Context, ts *models.ToolSummary) error {
	ts.CreatedAt = time.Now().UTC()
	_, err := s.db.NamedExecContext(ctx,
		`INSERT INTO tool_summaries (id, scan_id, tool_name, summary_text, finding_count, severity_counts, critical_issues, created_at)
		 VALUES (:id, :scan_id, :tool_name, :summary_text, :finding_count, :severity_counts, :critical_issues, :created_at)
		 ON CONFLICT(scan_id, tool_name) DO UPDATE SET
		 summary_text=excluded.summary_text, finding_count=excluded.finding_count,
		 severity_counts=excluded.severity_counts, critical_issues=excluded.critical_issues`, ts)
	return err
}

func (s *PostgresStore) ListToolSummariesByScan(ctx context.Context, scanID string) ([]models.ToolSummary, error) {
	var summaries []models.ToolSummary
	err := s.db.SelectContext(ctx, &summaries,
		"SELECT * FROM tool_summaries WHERE scan_id = $1 ORDER BY tool_name", scanID)
	return summaries, err
}

// --- ScanRecommendations ---

func (s *PostgresStore) CreateScanRecommendation(ctx context.Context, rec *models.ScanRecommendation) error {
	rec.CreatedAt = time.Now().UTC()
	_, err := s.db.NamedExecContext(ctx,
		`INSERT INTO scan_recommendations (id, scan_id, priority, category, title, description, affected_tools, effort_estimate, created_at)
		 VALUES (:id, :scan_id, :priority, :category, :title, :description, :affected_tools, :effort_estimate, :created_at)`, rec)
	return err
}

func (s *PostgresStore) ListScanRecommendations(ctx context.Context, scanID string) ([]models.ScanRecommendation, error) {
	var recs []models.ScanRecommendation
	err := s.db.SelectContext(ctx, &recs,
		"SELECT * FROM scan_recommendations WHERE scan_id = $1 ORDER BY priority ASC", scanID)
	return recs, err
}

// --- Settings ---

func (s *PostgresStore) GetSetting(ctx context.Context, key string) (string, error) {
	var value string
	err := s.db.GetContext(ctx, &value, "SELECT value FROM settings WHERE key = $1", key)
	if err != nil {
		return "", err
	}
	return value, nil
}

func (s *PostgresStore) SetSetting(ctx context.Context, key, value string) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO settings (key, value, updated_at) VALUES ($1, $2, $3)
		 ON CONFLICT(key) DO UPDATE SET value = $4, updated_at = $5`,
		key, value, now, value, now)
	return err
}

func (s *PostgresStore) ListSettings(ctx context.Context) (map[string]string, error) {
	type kv struct {
		Key   string `db:"key"`
		Value string `db:"value"`
	}
	var rows []kv
	err := s.db.SelectContext(ctx, &rows, "SELECT key, value FROM settings ORDER BY key")
	if err != nil {
		return nil, err
	}
	result := make(map[string]string, len(rows))
	for _, r := range rows {
		result[r.Key] = r.Value
	}
	return result, nil
}

// --- Cascade Deletes ---

func (s *PostgresStore) ListScanIDsByCollection(ctx context.Context, collectionID string) ([]string, error) {
	var ids []string
	err := s.db.SelectContext(ctx, &ids, "SELECT id FROM scans WHERE collection_id = $1", collectionID)
	return ids, err
}

func (s *PostgresStore) ListScanIDsByRepo(ctx context.Context, repoID string) ([]string, error) {
	var ids []string
	err := s.db.SelectContext(ctx, &ids, "SELECT id FROM scans WHERE repo_id = $1", repoID)
	return ids, err
}

func (s *PostgresStore) DeleteScanCascade(ctx context.Context, scanID string) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, "DELETE FROM fix_items WHERE finding_id IN (SELECT id FROM findings WHERE scan_id = $1)", scanID); err != nil {
		return fmt.Errorf("delete fix_items: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM fixes WHERE scan_id = $1", scanID); err != nil {
		return fmt.Errorf("delete fixes: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM findings WHERE scan_id = $1", scanID); err != nil {
		return fmt.Errorf("delete findings: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM scan_artifacts WHERE scan_id = $1", scanID); err != nil {
		return fmt.Errorf("delete scan_artifacts: %w", err)
	}
	// ai_logs references scans(id) WITHOUT ON DELETE CASCADE, so it must be
	// deleted explicitly — otherwise the DELETE FROM scans below fails the
	// foreign-key check. tool_summaries and scan_recommendations do cascade.
	if _, err := tx.ExecContext(ctx, "DELETE FROM ai_logs WHERE scan_id = $1", scanID); err != nil {
		return fmt.Errorf("delete ai_logs: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM scans WHERE id = $1", scanID); err != nil {
		return fmt.Errorf("delete scan: %w", err)
	}

	return tx.Commit()
}

func (s *PostgresStore) DeleteCollectionCascade(ctx context.Context, collectionID string) ([]string, error) {
	scanIDs, err := s.ListScanIDsByCollection(ctx, collectionID)
	if err != nil {
		return nil, fmt.Errorf("list scans: %w", err)
	}

	for _, sid := range scanIDs {
		if err := s.DeleteScanCascade(ctx, sid); err != nil {
			return nil, fmt.Errorf("delete scan %s: %w", sid, err)
		}
	}

	if _, err := s.db.ExecContext(ctx, "DELETE FROM collection_repos WHERE collection_id = $1", collectionID); err != nil {
		return nil, fmt.Errorf("delete collection_repos: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, "DELETE FROM collections WHERE id = $1", collectionID); err != nil {
		return nil, fmt.Errorf("delete collection: %w", err)
	}

	return scanIDs, nil
}

func (s *PostgresStore) DeleteRepoCascade(ctx context.Context, repoID string) ([]string, error) {
	scanIDs, err := s.ListScanIDsByRepo(ctx, repoID)
	if err != nil {
		return nil, fmt.Errorf("list scans: %w", err)
	}

	for _, sid := range scanIDs {
		if err := s.DeleteScanCascade(ctx, sid); err != nil {
			return nil, fmt.Errorf("delete scan %s: %w", sid, err)
		}
	}

	if _, err := s.db.ExecContext(ctx, "DELETE FROM loops WHERE repo_id = $1", repoID); err != nil {
		return nil, fmt.Errorf("delete loops: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, "DELETE FROM repo_maps WHERE repo_id = $1", repoID); err != nil {
		return nil, fmt.Errorf("delete repo_maps: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, "DELETE FROM collection_repos WHERE repo_id = $1", repoID); err != nil {
		return nil, fmt.Errorf("delete collection_repos: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, "DELETE FROM repos WHERE id = $1", repoID); err != nil {
		return nil, fmt.Errorf("delete repo: %w", err)
	}

	return scanIDs, nil
}

// --- AI Prompt Templates ---

func (s *PostgresStore) CreatePromptTemplate(ctx context.Context, tmpl *models.AIPromptTemplate) error {
	tmpl.UpdatedAt = time.Now().UTC()
	_, err := s.db.NamedExecContext(ctx,
		`INSERT INTO ai_prompt_templates (id, scope, scope_id, prompt_type, section, content, updated_at)
		 VALUES (:id, :scope, :scope_id, :prompt_type, :section, :content, :updated_at)`, tmpl)
	return err
}

func (s *PostgresStore) GetPromptTemplate(ctx context.Context, id string) (*models.AIPromptTemplate, error) {
	var tmpl models.AIPromptTemplate
	err := s.db.GetContext(ctx, &tmpl, "SELECT * FROM ai_prompt_templates WHERE id = $1", id)
	if err != nil {
		return nil, err
	}
	return &tmpl, nil
}

func (s *PostgresStore) ListPromptTemplates(ctx context.Context, scope, scopeID string) ([]models.AIPromptTemplate, error) {
	var tmpls []models.AIPromptTemplate
	err := s.db.SelectContext(ctx, &tmpls,
		"SELECT * FROM ai_prompt_templates WHERE scope = $1 AND scope_id = $2 ORDER BY prompt_type, section",
		scope, scopeID)
	return tmpls, err
}

func (s *PostgresStore) UpdatePromptTemplate(ctx context.Context, tmpl *models.AIPromptTemplate) error {
	tmpl.UpdatedAt = time.Now().UTC()
	_, err := s.db.NamedExecContext(ctx,
		`UPDATE ai_prompt_templates SET scope=:scope, scope_id=:scope_id, prompt_type=:prompt_type,
		 section=:section, content=:content, updated_at=:updated_at WHERE id=:id`, tmpl)
	return err
}

func (s *PostgresStore) DeletePromptTemplate(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM ai_prompt_templates WHERE id = $1", id)
	return err
}

func (s *PostgresStore) ResolvePromptSection(ctx context.Context, promptType, section, collectionID string) (string, error) {
	// Try collection-scoped first.
	if collectionID != "" {
		var content string
		err := s.db.GetContext(ctx, &content,
			"SELECT content FROM ai_prompt_templates WHERE scope = $1 AND scope_id = $2 AND prompt_type = $3 AND section = $4",
			"collection", collectionID, promptType, section)
		if err == nil {
			return content, nil
		}
	}
	// Fall back to global.
	var content string
	err := s.db.GetContext(ctx, &content,
		"SELECT content FROM ai_prompt_templates WHERE scope = $1 AND scope_id = $2 AND prompt_type = $3 AND section = $4",
		"global", "", promptType, section)
	if err == nil {
		return content, nil
	}
	// Not found — caller will use hardcoded default.
	return "", nil
}
