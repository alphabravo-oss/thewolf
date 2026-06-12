package db

import (
	"context"
	_ "embed"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"

	"github.com/alphabravocompany/thewolf/internal/models"
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

//go:embed migrations/007_ai_prompts_and_settings.sql
var migration007SQL string

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

// SQLiteStore implements Store using SQLite.
type SQLiteStore struct {
	db *sqlx.DB
}

// NewSQLite creates a new SQLite store. Use ":memory:" for in-memory databases.
func NewSQLite(dsn string) (*SQLiteStore, error) {
	db, err := sqlx.Open("sqlite3", dsn+"?_journal_mode=WAL&_foreign_keys=on")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// For in-memory databases, restrict to a single connection so all
	// operations share the same database instance.
	if dsn == ":memory:" {
		db.SetMaxOpenConns(1)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	store := &SQLiteStore{db: db}
	if err := store.Migrate(); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return store, nil
}

func (s *SQLiteStore) Close() error { return s.db.Close() }

func (s *SQLiteStore) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

func (s *SQLiteStore) Migrate() error {
	if _, err := s.db.Exec(migrationSQL); err != nil {
		return err
	}
	// Ignore "duplicate column" errors for idempotent re-runs.
	if _, err := s.db.Exec(migration002SQL); err != nil {
		if !strings.Contains(err.Error(), "duplicate column") {
			return err
		}
	}
	if _, err := s.db.Exec(migration003SQL); err != nil {
		if !strings.Contains(err.Error(), "duplicate column") {
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
		if !strings.Contains(err.Error(), "duplicate column") {
			return err
		}
	}
	if _, err := s.db.Exec(migration007SQL); err != nil {
		if !strings.Contains(err.Error(), "already exists") {
			return err
		}
	}
	if _, err := s.db.Exec(migration008SQL); err != nil {
		if !strings.Contains(err.Error(), "duplicate column") {
			return err
		}
	}
	if _, err := s.db.Exec(migration009SQL); err != nil {
		if !strings.Contains(err.Error(), "duplicate column") {
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

func (s *SQLiteStore) CreateUser(ctx context.Context, user *models.User) error {
	now := time.Now().UTC()
	user.CreatedAt = now
	user.UpdatedAt = now
	_, err := s.db.NamedExecContext(ctx,
		`INSERT INTO users (id, email, password_hash, created_at, updated_at)
		 VALUES (:id, :email, :password_hash, :created_at, :updated_at)`, user)
	return err
}

func (s *SQLiteStore) GetUserByID(ctx context.Context, id string) (*models.User, error) {
	var u models.User
	err := s.db.GetContext(ctx, &u, "SELECT * FROM users WHERE id = ?", id)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *SQLiteStore) GetUserByEmail(ctx context.Context, email string) (*models.User, error) {
	var u models.User
	err := s.db.GetContext(ctx, &u, "SELECT * FROM users WHERE email = ?", email)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *SQLiteStore) UpdateUser(ctx context.Context, user *models.User) error {
	user.UpdatedAt = time.Now().UTC()
	_, err := s.db.NamedExecContext(ctx,
		`UPDATE users SET email=:email, password_hash=:password_hash, updated_at=:updated_at WHERE id=:id`, user)
	return err
}

func (s *SQLiteStore) ListUsers(ctx context.Context) ([]models.User, error) {
	var users []models.User
	err := s.db.SelectContext(ctx, &users,
		`SELECT id, email, password_hash, created_at, updated_at FROM users ORDER BY created_at ASC`)
	return users, err
}

func (s *SQLiteStore) DeleteUser(ctx context.Context, id string) error {
	// Cascading deletes (scans, secrets, etc.) are handled by ON DELETE
	// CASCADE in the migration schema. If they're not, callers should
	// clean up dependents before invoking this. We don't try to be
	// clever here — a single DELETE against the users table is the
	// right primitive.
	_, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	return err
}

// --- Repos ---

func (s *SQLiteStore) CreateRepo(ctx context.Context, repo *models.Repo) error {
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

func (s *SQLiteStore) GetRepoByID(ctx context.Context, id string) (*models.Repo, error) {
	var r models.Repo
	err := s.db.GetContext(ctx, &r, "SELECT * FROM repos WHERE id = ?", id)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *SQLiteStore) ListReposByUser(ctx context.Context, userID string) ([]models.Repo, error) {
	var repos []models.Repo
	// No RBAC yet — all authenticated users see all repos.
	err := s.db.SelectContext(ctx, &repos, "SELECT * FROM repos ORDER BY created_at DESC")
	return repos, err
}

func (s *SQLiteStore) UpdateRepo(ctx context.Context, repo *models.Repo) error {
	repo.UpdatedAt = time.Now().UTC()
	_, err := s.db.NamedExecContext(ctx,
		`UPDATE repos SET name=:name, source_type=:source_type, source_path=:source_path,
		 remote_node_id=:remote_node_id, remote_path=:remote_path, last_commit_sha=:last_commit_sha,
		 last_dirty_state=:last_dirty_state, default_branch=:default_branch, updated_at=:updated_at WHERE id=:id`, repo)
	return err
}

func (s *SQLiteStore) UpdateRepoDetection(ctx context.Context, repoID, languages, frameworks string) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		`UPDATE repos SET detected_languages = ?, detected_frameworks = ?, detected_at = ?, updated_at = ? WHERE id = ?`,
		languages, frameworks, now, now, repoID)
	return err
}

func (s *SQLiteStore) DeleteRepo(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM repos WHERE id = ?", id)
	return err
}

// --- Collections ---

func (s *SQLiteStore) CreateCollection(ctx context.Context, col *models.Collection) error {
	now := time.Now().UTC()
	col.CreatedAt = now
	col.UpdatedAt = now
	_, err := s.db.NamedExecContext(ctx,
		`INSERT INTO collections (id, user_id, name, description, scan_config, created_at, updated_at)
		 VALUES (:id, :user_id, :name, :description, :scan_config, :created_at, :updated_at)`, col)
	return err
}

func (s *SQLiteStore) GetCollectionByID(ctx context.Context, id string) (*models.Collection, error) {
	var c models.Collection
	err := s.db.GetContext(ctx, &c, "SELECT * FROM collections WHERE id = ?", id)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *SQLiteStore) GetCollectionByName(ctx context.Context, name string) (*models.Collection, error) {
	var c models.Collection
	err := s.db.GetContext(ctx, &c, "SELECT * FROM collections WHERE name = ?", name)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *SQLiteStore) ListCollectionsByUser(ctx context.Context, userID string) ([]models.Collection, error) {
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

func (s *SQLiteStore) UpdateCollection(ctx context.Context, col *models.Collection) error {
	col.UpdatedAt = time.Now().UTC()
	_, err := s.db.NamedExecContext(ctx,
		`UPDATE collections SET name=:name, description=:description, scan_config=:scan_config, updated_at=:updated_at WHERE id=:id`, col)
	return err
}

func (s *SQLiteStore) DeleteCollection(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM collections WHERE id = ?", id)
	return err
}

func (s *SQLiteStore) AddRepoToCollection(ctx context.Context, collectionID, repoID string) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT OR IGNORE INTO collection_repos (collection_id, repo_id) VALUES (?, ?)",
		collectionID, repoID)
	return err
}

func (s *SQLiteStore) RemoveRepoFromCollection(ctx context.Context, collectionID, repoID string) error {
	_, err := s.db.ExecContext(ctx,
		"DELETE FROM collection_repos WHERE collection_id = ? AND repo_id = ?",
		collectionID, repoID)
	return err
}

func (s *SQLiteStore) ListReposInCollection(ctx context.Context, collectionID string) ([]models.Repo, error) {
	var repos []models.Repo
	err := s.db.SelectContext(ctx, &repos,
		`SELECT r.* FROM repos r
		 JOIN collection_repos cr ON cr.repo_id = r.id
		 WHERE cr.collection_id = ?`, collectionID)
	return repos, err
}

// --- Secrets ---

func (s *SQLiteStore) CreateSecret(ctx context.Context, secret *models.Secret) error {
	now := time.Now().UTC()
	secret.CreatedAt = now
	secret.UpdatedAt = now
	_, err := s.db.NamedExecContext(ctx,
		`INSERT INTO secrets (id, user_id, key_type, key_name, encrypted_value, created_at, updated_at)
		 VALUES (:id, :user_id, :key_type, :key_name, :encrypted_value, :created_at, :updated_at)`, secret)
	return err
}

func (s *SQLiteStore) GetSecretByID(ctx context.Context, id string) (*models.Secret, error) {
	var sec models.Secret
	err := s.db.GetContext(ctx, &sec, "SELECT * FROM secrets WHERE id = ?", id)
	if err != nil {
		return nil, err
	}
	return &sec, nil
}

func (s *SQLiteStore) ListSecretsByUser(ctx context.Context, userID string) ([]models.Secret, error) {
	var secs []models.Secret
	// No RBAC yet — all authenticated users see all secrets.
	err := s.db.SelectContext(ctx, &secs, "SELECT * FROM secrets ORDER BY created_at DESC")
	return secs, err
}

func (s *SQLiteStore) DeleteSecret(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM secrets WHERE id = ?", id)
	return err
}

// --- RepoMaps ---

func (s *SQLiteStore) CreateRepoMap(ctx context.Context, rm *models.RepoMap) error {
	now := time.Now().UTC()
	rm.CreatedAt = now
	rm.UpdatedAt = now
	_, err := s.db.NamedExecContext(ctx,
		`INSERT INTO repo_maps (id, repo_id, branch, structural_data, semantic_data, file_hashes, created_at, updated_at)
		 VALUES (:id, :repo_id, :branch, :structural_data, :semantic_data, :file_hashes, :created_at, :updated_at)`, rm)
	return err
}

func (s *SQLiteStore) GetRepoMap(ctx context.Context, repoID, branch string) (*models.RepoMap, error) {
	var rm models.RepoMap
	err := s.db.GetContext(ctx, &rm,
		"SELECT * FROM repo_maps WHERE repo_id = ? AND branch = ? ORDER BY created_at DESC LIMIT 1",
		repoID, branch)
	if err != nil {
		return nil, err
	}
	return &rm, nil
}

func (s *SQLiteStore) UpdateRepoMap(ctx context.Context, rm *models.RepoMap) error {
	rm.UpdatedAt = time.Now().UTC()
	_, err := s.db.NamedExecContext(ctx,
		`UPDATE repo_maps SET structural_data=:structural_data, semantic_data=:semantic_data, file_hashes=:file_hashes, updated_at=:updated_at WHERE id=:id`, rm)
	return err
}

// --- Scans ---

func (s *SQLiteStore) CreateScan(ctx context.Context, scan *models.Scan) error {
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

func (s *SQLiteStore) GetScanByID(ctx context.Context, id string) (*models.Scan, error) {
	var scan models.Scan
	err := s.db.GetContext(ctx, &scan, "SELECT * FROM scans WHERE id = ?", id)
	if err != nil {
		return nil, err
	}
	return &scan, nil
}

func (s *SQLiteStore) ListAllScans(ctx context.Context) ([]models.Scan, error) {
	var scans []models.Scan
	err := s.db.SelectContext(ctx, &scans, "SELECT * FROM scans ORDER BY created_at DESC")
	return scans, err
}

func (s *SQLiteStore) ListScansByUser(ctx context.Context, userID string) ([]models.Scan, error) {
	var scans []models.Scan
	// No RBAC yet — all authenticated users see all scans.
	err := s.db.SelectContext(ctx, &scans, "SELECT * FROM scans ORDER BY created_at DESC")
	return scans, err
}

func (s *SQLiteStore) ListScansByRepo(ctx context.Context, repoID string) ([]models.Scan, error) {
	var scans []models.Scan
	err := s.db.SelectContext(ctx, &scans, "SELECT * FROM scans WHERE repo_id = ? ORDER BY created_at DESC", repoID)
	return scans, err
}

func (s *SQLiteStore) ListScansByCollection(ctx context.Context, collectionID string) ([]models.Scan, error) {
	var scans []models.Scan
	err := s.db.SelectContext(ctx, &scans, "SELECT * FROM scans WHERE collection_id = ? ORDER BY created_at DESC", collectionID)
	return scans, err
}

func (s *SQLiteStore) UpdateScan(ctx context.Context, scan *models.Scan) error {
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

func (s *SQLiteStore) DeleteScan(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM scans WHERE id = ?", id)
	return err
}

// --- Findings ---

func (s *SQLiteStore) CreateFinding(ctx context.Context, f *models.Finding) error {
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

func (s *SQLiteStore) CreateFindings(ctx context.Context, findings []models.Finding) error {
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

func (s *SQLiteStore) GetFindingByID(ctx context.Context, id string) (*models.Finding, error) {
	var f models.Finding
	err := s.db.GetContext(ctx, &f, "SELECT * FROM findings WHERE id = ?", id)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

func (s *SQLiteStore) ListFindingsByScan(ctx context.Context, scanID string) ([]models.Finding, error) {
	var findings []models.Finding
	err := s.db.SelectContext(ctx, &findings,
		"SELECT * FROM findings WHERE scan_id = ? ORDER BY composite_score DESC", scanID)
	return findings, err
}

func (s *SQLiteStore) ListFindingsByRepo(ctx context.Context, repoID string) ([]models.Finding, error) {
	var findings []models.Finding
	err := s.db.SelectContext(ctx, &findings,
		"SELECT * FROM findings WHERE repo_id = ? ORDER BY composite_score DESC", repoID)
	return findings, err
}

func (s *SQLiteStore) UpdateFinding(ctx context.Context, f *models.Finding) error {
	f.UpdatedAt = time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		`UPDATE findings SET ai_context_score = ?, ai_fix_suggestion = ?, composite_score = ?,
		 tool_severity_score = ?, location_weight = ?,
		 module_name = ?, function_name = ?, symbol_kind = ?, file_purpose = ?, dependents_json = ?,
		 updated_at = ? WHERE id = ?`,
		f.AIContextScore, f.AIFixSuggestion, f.CompositeScore,
		f.ToolSeverityScore, f.LocationWeight,
		f.ModuleName, f.FunctionName, f.SymbolKind, f.FilePurpose, f.DependentsJSON,
		f.UpdatedAt, f.ID)
	return err
}

func (s *SQLiteStore) UpdateFindingStatus(ctx context.Context, id string, status models.Status) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE findings SET status = ?, updated_at = ? WHERE id = ?",
		status, time.Now().UTC(), id)
	return err
}

// --- Fixes ---

func (s *SQLiteStore) CreateFix(ctx context.Context, fix *models.Fix) error {
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

func (s *SQLiteStore) GetFixByID(ctx context.Context, id string) (*models.Fix, error) {
	var f models.Fix
	err := s.db.GetContext(ctx, &f, "SELECT * FROM fixes WHERE id = ?", id)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

func (s *SQLiteStore) ListFixesByUser(ctx context.Context, userID string) ([]models.Fix, error) {
	var fixes []models.Fix
	// No RBAC yet — all authenticated users see all fixes.
	err := s.db.SelectContext(ctx, &fixes, "SELECT * FROM fixes ORDER BY created_at DESC")
	return fixes, err
}

func (s *SQLiteStore) UpdateFix(ctx context.Context, fix *models.Fix) error {
	fix.UpdatedAt = time.Now().UTC()
	_, err := s.db.NamedExecContext(ctx,
		`UPDATE fixes SET status=:status, severity_filter=:severity_filter, branch_name=:branch_name,
		 worktree_path=:worktree_path, findings_attempted=:findings_attempted, findings_fixed=:findings_fixed,
		 findings_failed=:findings_failed, pr_urls=:pr_urls, started_at=:started_at,
		 completed_at=:completed_at, updated_at=:updated_at WHERE id=:id`, fix)
	return err
}

// --- FixItems ---

func (s *SQLiteStore) CreateFixItem(ctx context.Context, item *models.FixItem) error {
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

func (s *SQLiteStore) ListFixItemsByFix(ctx context.Context, fixID string) ([]models.FixItem, error) {
	var items []models.FixItem
	err := s.db.SelectContext(ctx, &items, "SELECT * FROM fix_items WHERE fix_id = ?", fixID)
	return items, err
}

func (s *SQLiteStore) UpdateFixItem(ctx context.Context, item *models.FixItem) error {
	item.UpdatedAt = time.Now().UTC()
	_, err := s.db.NamedExecContext(ctx,
		`UPDATE fix_items SET status=:status, files_changed=:files_changed, diff=:diff,
		 validation_result=:validation_result, validation_output=:validation_output,
		 error_message=:error_message, updated_at=:updated_at WHERE id=:id`, item)
	return err
}

// --- Loops ---

func (s *SQLiteStore) CreateLoop(ctx context.Context, loop *models.Loop) error {
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

func (s *SQLiteStore) GetLoopByID(ctx context.Context, id string) (*models.Loop, error) {
	var l models.Loop
	err := s.db.GetContext(ctx, &l, "SELECT * FROM loops WHERE id = ?", id)
	if err != nil {
		return nil, err
	}
	return &l, nil
}

func (s *SQLiteStore) ListLoopsByUser(ctx context.Context, userID string) ([]models.Loop, error) {
	var loops []models.Loop
	// No RBAC yet — all authenticated users see all loops.
	err := s.db.SelectContext(ctx, &loops, "SELECT * FROM loops ORDER BY created_at DESC")
	return loops, err
}

func (s *SQLiteStore) UpdateLoop(ctx context.Context, loop *models.Loop) error {
	loop.UpdatedAt = time.Now().UTC()
	_, err := s.db.NamedExecContext(ctx,
		`UPDATE loops SET status=:status, current_iteration=:current_iteration,
		 total_findings_fixed=:total_findings_fixed, total_findings_new=:total_findings_new,
		 total_findings_remaining=:total_findings_remaining, guardrail_warnings=:guardrail_warnings,
		 completed_at=:completed_at, updated_at=:updated_at WHERE id=:id`, loop)
	return err
}

// --- ScanArtifacts ---

func (s *SQLiteStore) CreateScanArtifact(ctx context.Context, artifact *models.ScanArtifact) error {
	now := time.Now().UTC()
	artifact.CreatedAt = now
	artifact.UpdatedAt = now
	_, err := s.db.NamedExecContext(ctx,
		`INSERT INTO scan_artifacts (id, scan_id, artifact_type, file_path, file_size, created_at, updated_at)
		 VALUES (:id, :scan_id, :artifact_type, :file_path, :file_size, :created_at, :updated_at)`, artifact)
	return err
}

func (s *SQLiteStore) ListScanArtifacts(ctx context.Context, scanID string) ([]models.ScanArtifact, error) {
	var artifacts []models.ScanArtifact
	err := s.db.SelectContext(ctx, &artifacts,
		"SELECT * FROM scan_artifacts WHERE scan_id = ? ORDER BY created_at", scanID)
	return artifacts, err
}

// --- AILogs ---

func (s *SQLiteStore) CreateAILog(ctx context.Context, log *models.AILog) error {
	if log.CreatedAt.IsZero() {
		log.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.NamedExecContext(ctx,
		`INSERT INTO ai_logs (id, scan_id, provider, model, phase, tool_name, prompt, response, error, prompt_tokens, response_tokens, duration_ms, created_at)
		 VALUES (:id, :scan_id, :provider, :model, :phase, :tool_name, :prompt, :response, :error, :prompt_tokens, :response_tokens, :duration_ms, :created_at)`, log)
	return err
}

func (s *SQLiteStore) ListAILogsByScan(ctx context.Context, scanID string) ([]models.AILog, error) {
	var logs []models.AILog
	err := s.db.SelectContext(ctx, &logs,
		"SELECT * FROM ai_logs WHERE scan_id = ? ORDER BY created_at", scanID)
	return logs, err
}

// --- ToolSummaries ---

func (s *SQLiteStore) CreateToolSummary(ctx context.Context, ts *models.ToolSummary) error {
	ts.CreatedAt = time.Now().UTC()
	_, err := s.db.NamedExecContext(ctx,
		`INSERT INTO tool_summaries (id, scan_id, tool_name, summary_text, finding_count, severity_counts, critical_issues, created_at)
		 VALUES (:id, :scan_id, :tool_name, :summary_text, :finding_count, :severity_counts, :critical_issues, :created_at)
		 ON CONFLICT(scan_id, tool_name) DO UPDATE SET
		 summary_text=excluded.summary_text, finding_count=excluded.finding_count,
		 severity_counts=excluded.severity_counts, critical_issues=excluded.critical_issues`, ts)
	return err
}

func (s *SQLiteStore) ListToolSummariesByScan(ctx context.Context, scanID string) ([]models.ToolSummary, error) {
	var summaries []models.ToolSummary
	err := s.db.SelectContext(ctx, &summaries,
		"SELECT * FROM tool_summaries WHERE scan_id = ? ORDER BY tool_name", scanID)
	return summaries, err
}

// --- ScanRecommendations ---

func (s *SQLiteStore) CreateScanRecommendation(ctx context.Context, rec *models.ScanRecommendation) error {
	rec.CreatedAt = time.Now().UTC()
	_, err := s.db.NamedExecContext(ctx,
		`INSERT INTO scan_recommendations (id, scan_id, priority, category, title, description, affected_tools, effort_estimate, created_at)
		 VALUES (:id, :scan_id, :priority, :category, :title, :description, :affected_tools, :effort_estimate, :created_at)`, rec)
	return err
}

func (s *SQLiteStore) ListScanRecommendations(ctx context.Context, scanID string) ([]models.ScanRecommendation, error) {
	var recs []models.ScanRecommendation
	err := s.db.SelectContext(ctx, &recs,
		"SELECT * FROM scan_recommendations WHERE scan_id = ? ORDER BY priority ASC", scanID)
	return recs, err
}

// --- Settings ---

func (s *SQLiteStore) GetSetting(ctx context.Context, key string) (string, error) {
	var value string
	err := s.db.GetContext(ctx, &value, "SELECT value FROM settings WHERE key = ?", key)
	if err != nil {
		return "", err
	}
	return value, nil
}

func (s *SQLiteStore) SetSetting(ctx context.Context, key, value string) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = ?, updated_at = ?`,
		key, value, now, value, now)
	return err
}

func (s *SQLiteStore) ListSettings(ctx context.Context) (map[string]string, error) {
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

func (s *SQLiteStore) ListScanIDsByCollection(ctx context.Context, collectionID string) ([]string, error) {
	var ids []string
	err := s.db.SelectContext(ctx, &ids, "SELECT id FROM scans WHERE collection_id = ?", collectionID)
	return ids, err
}

func (s *SQLiteStore) ListScanIDsByRepo(ctx context.Context, repoID string) ([]string, error) {
	var ids []string
	err := s.db.SelectContext(ctx, &ids, "SELECT id FROM scans WHERE repo_id = ?", repoID)
	return ids, err
}

func (s *SQLiteStore) DeleteScanCascade(ctx context.Context, scanID string) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, "DELETE FROM fix_items WHERE finding_id IN (SELECT id FROM findings WHERE scan_id = ?)", scanID); err != nil {
		return fmt.Errorf("delete fix_items: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM fixes WHERE scan_id = ?", scanID); err != nil {
		return fmt.Errorf("delete fixes: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM findings WHERE scan_id = ?", scanID); err != nil {
		return fmt.Errorf("delete findings: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM scan_artifacts WHERE scan_id = ?", scanID); err != nil {
		return fmt.Errorf("delete scan_artifacts: %w", err)
	}
	// ai_logs references scans(id) WITHOUT ON DELETE CASCADE, so it must be
	// deleted explicitly — otherwise the DELETE FROM scans below fails the
	// foreign-key check (_foreign_keys=on). tool_summaries and
	// scan_recommendations do cascade, so they need no explicit delete.
	if _, err := tx.ExecContext(ctx, "DELETE FROM ai_logs WHERE scan_id = ?", scanID); err != nil {
		return fmt.Errorf("delete ai_logs: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM scans WHERE id = ?", scanID); err != nil {
		return fmt.Errorf("delete scan: %w", err)
	}

	return tx.Commit()
}

func (s *SQLiteStore) DeleteCollectionCascade(ctx context.Context, collectionID string) ([]string, error) {
	scanIDs, err := s.ListScanIDsByCollection(ctx, collectionID)
	if err != nil {
		return nil, fmt.Errorf("list scans: %w", err)
	}

	for _, sid := range scanIDs {
		if err := s.DeleteScanCascade(ctx, sid); err != nil {
			return nil, fmt.Errorf("delete scan %s: %w", sid, err)
		}
	}

	if _, err := s.db.ExecContext(ctx, "DELETE FROM collection_repos WHERE collection_id = ?", collectionID); err != nil {
		return nil, fmt.Errorf("delete collection_repos: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, "DELETE FROM collections WHERE id = ?", collectionID); err != nil {
		return nil, fmt.Errorf("delete collection: %w", err)
	}

	return scanIDs, nil
}

func (s *SQLiteStore) DeleteRepoCascade(ctx context.Context, repoID string) ([]string, error) {
	scanIDs, err := s.ListScanIDsByRepo(ctx, repoID)
	if err != nil {
		return nil, fmt.Errorf("list scans: %w", err)
	}

	for _, sid := range scanIDs {
		if err := s.DeleteScanCascade(ctx, sid); err != nil {
			return nil, fmt.Errorf("delete scan %s: %w", sid, err)
		}
	}

	if _, err := s.db.ExecContext(ctx, "DELETE FROM loops WHERE repo_id = ?", repoID); err != nil {
		return nil, fmt.Errorf("delete loops: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, "DELETE FROM repo_maps WHERE repo_id = ?", repoID); err != nil {
		return nil, fmt.Errorf("delete repo_maps: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, "DELETE FROM collection_repos WHERE repo_id = ?", repoID); err != nil {
		return nil, fmt.Errorf("delete collection_repos: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, "DELETE FROM repos WHERE id = ?", repoID); err != nil {
		return nil, fmt.Errorf("delete repo: %w", err)
	}

	return scanIDs, nil
}

// --- AI Prompt Templates ---

func (s *SQLiteStore) CreatePromptTemplate(ctx context.Context, tmpl *models.AIPromptTemplate) error {
	tmpl.UpdatedAt = time.Now().UTC()
	_, err := s.db.NamedExecContext(ctx,
		`INSERT INTO ai_prompt_templates (id, scope, scope_id, prompt_type, section, content, updated_at)
		 VALUES (:id, :scope, :scope_id, :prompt_type, :section, :content, :updated_at)`, tmpl)
	return err
}

func (s *SQLiteStore) GetPromptTemplate(ctx context.Context, id string) (*models.AIPromptTemplate, error) {
	var tmpl models.AIPromptTemplate
	err := s.db.GetContext(ctx, &tmpl, "SELECT * FROM ai_prompt_templates WHERE id = ?", id)
	if err != nil {
		return nil, err
	}
	return &tmpl, nil
}

func (s *SQLiteStore) ListPromptTemplates(ctx context.Context, scope, scopeID string) ([]models.AIPromptTemplate, error) {
	var tmpls []models.AIPromptTemplate
	err := s.db.SelectContext(ctx, &tmpls,
		"SELECT * FROM ai_prompt_templates WHERE scope = ? AND scope_id = ? ORDER BY prompt_type, section",
		scope, scopeID)
	return tmpls, err
}

func (s *SQLiteStore) UpdatePromptTemplate(ctx context.Context, tmpl *models.AIPromptTemplate) error {
	tmpl.UpdatedAt = time.Now().UTC()
	_, err := s.db.NamedExecContext(ctx,
		`UPDATE ai_prompt_templates SET scope=:scope, scope_id=:scope_id, prompt_type=:prompt_type,
		 section=:section, content=:content, updated_at=:updated_at WHERE id=:id`, tmpl)
	return err
}

func (s *SQLiteStore) DeletePromptTemplate(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM ai_prompt_templates WHERE id = ?", id)
	return err
}

func (s *SQLiteStore) ResolvePromptSection(ctx context.Context, promptType, section, collectionID string) (string, error) {
	// Try collection-scoped first.
	if collectionID != "" {
		var content string
		err := s.db.GetContext(ctx, &content,
			"SELECT content FROM ai_prompt_templates WHERE scope = ? AND scope_id = ? AND prompt_type = ? AND section = ?",
			"collection", collectionID, promptType, section)
		if err == nil {
			return content, nil
		}
	}
	// Fall back to global.
	var content string
	err := s.db.GetContext(ctx, &content,
		"SELECT content FROM ai_prompt_templates WHERE scope = ? AND scope_id = ? AND prompt_type = ? AND section = ?",
		"global", "", promptType, section)
	if err == nil {
		return content, nil
	}
	// Not found — caller will use hardcoded default.
	return "", nil
}
