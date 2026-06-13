package db

import (
	"context"

	"github.com/alphabravocompany/thewolf/internal/models"
)

// FleetPostureResult is the aggregate posture summary returned by
// Store.FleetPosture.
type FleetPostureResult struct {
	OpenFindingsBySeverity map[string]int `json:"open_findings_by_severity"`
	WeekOverWeekDelta      map[string]int `json:"week_over_week_delta"`
	RepoCount              int            `json:"repo_count"`
	GatesFailing           int            `json:"gates_failing"`
}

// FleetInventoryResult is the inventory breakdown returned by
// Store.FleetInventory.
type FleetInventoryResult struct {
	BySourceType map[string]int `json:"by_source_type"`
	ByCollection map[string]int `json:"by_collection"`
	ByLanguage   map[string]int `json:"by_language"`
}

// NeedsAttentionRow is a single repository in the needs-attention list,
// scored by Store.FleetNeedsAttention.
type NeedsAttentionRow struct {
	RepoID string `json:"repo_id"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
	Detail string `json:"detail"`
	Score  int    `json:"score"`
}

// FindingsAggregateRow is a single (rule_id, repos, findings) tuple returned
// by Store.FindingsAggregateByRule.
type FindingsAggregateRow struct {
	Key      string `json:"key"`
	Repos    int    `json:"repos"`
	Findings int    `json:"findings"`
}

// Store defines the interface for all database operations.
type Store interface {
	// Lifecycle
	Close() error
	Migrate() error
	Ping(ctx context.Context) error

	// Users
	CreateUser(ctx context.Context, user *models.User) error
	GetUserByID(ctx context.Context, id string) (*models.User, error)
	GetUserByEmail(ctx context.Context, email string) (*models.User, error)
	UpdateUser(ctx context.Context, user *models.User) error
	ListUsers(ctx context.Context) ([]models.User, error)
	DeleteUser(ctx context.Context, id string) error

	// Repos
	CreateRepo(ctx context.Context, repo *models.Repo) error
	GetRepoByID(ctx context.Context, id string) (*models.Repo, error)
	ListReposByUser(ctx context.Context, userID string) ([]models.Repo, error)
	UpdateRepo(ctx context.Context, repo *models.Repo) error
	UpdateRepoDetection(ctx context.Context, repoID, languages, frameworks string) error
	DeleteRepo(ctx context.Context, id string) error

	// Remote Nodes
	CreateRemoteNode(ctx context.Context, node *models.RemoteNode) error
	GetRemoteNodeByID(ctx context.Context, id string) (*models.RemoteNode, error)
	ListRemoteNodesByUser(ctx context.Context, userID string) ([]models.RemoteNode, error)
	UpdateRemoteNode(ctx context.Context, node *models.RemoteNode) error
	DeleteRemoteNode(ctx context.Context, id string) error
	TouchRemoteNodeCheck(ctx context.Context, id, status, checkError string) error

	// Collections
	CreateCollection(ctx context.Context, col *models.Collection) error
	GetCollectionByID(ctx context.Context, id string) (*models.Collection, error)
	GetCollectionByName(ctx context.Context, name string) (*models.Collection, error)
	ListCollectionsByUser(ctx context.Context, userID string) ([]models.Collection, error)
	UpdateCollection(ctx context.Context, col *models.Collection) error
	DeleteCollection(ctx context.Context, id string) error
	AddRepoToCollection(ctx context.Context, collectionID, repoID string) error
	RemoveRepoFromCollection(ctx context.Context, collectionID, repoID string) error
	ListReposInCollection(ctx context.Context, collectionID string) ([]models.Repo, error)

	// Secrets
	CreateSecret(ctx context.Context, secret *models.Secret) error
	GetSecretByID(ctx context.Context, id string) (*models.Secret, error)
	ListSecretsByUser(ctx context.Context, userID string) ([]models.Secret, error)
	DeleteSecret(ctx context.Context, id string) error

	// RepoMaps
	CreateRepoMap(ctx context.Context, rm *models.RepoMap) error
	GetRepoMap(ctx context.Context, repoID, branch string) (*models.RepoMap, error)
	UpdateRepoMap(ctx context.Context, rm *models.RepoMap) error

	// Scans
	CreateScan(ctx context.Context, scan *models.Scan) error
	GetScanByID(ctx context.Context, id string) (*models.Scan, error)
	ListAllScans(ctx context.Context) ([]models.Scan, error)
	ListScansByUser(ctx context.Context, userID string) ([]models.Scan, error)
	ListScansByRepo(ctx context.Context, repoID string) ([]models.Scan, error)
	ListScansByCollection(ctx context.Context, collectionID string) ([]models.Scan, error)
	UpdateScan(ctx context.Context, scan *models.Scan) error
	DeleteScan(ctx context.Context, id string) error

	// Findings
	CreateFinding(ctx context.Context, f *models.Finding) error
	CreateFindings(ctx context.Context, findings []models.Finding) error
	GetFindingByID(ctx context.Context, id string) (*models.Finding, error)
	ListFindingsByScan(ctx context.Context, scanID string) ([]models.Finding, error)
	ListFindingsByRepo(ctx context.Context, repoID string) ([]models.Finding, error)
	UpdateFinding(ctx context.Context, f *models.Finding) error
	UpdateFindingStatus(ctx context.Context, id string, status models.Status) error

	// Baselines and Comparisons
	CreateScanBaseline(ctx context.Context, baseline *models.ScanBaseline) error
	ListScanBaselines(ctx context.Context, repoID, branch string) ([]models.ScanBaseline, error)
	GetScanBaselineByName(ctx context.Context, repoID, branch, name string) (*models.ScanBaseline, error)
	UpsertScanComparison(ctx context.Context, comparison *models.ScanComparison) error
	GetScanComparison(ctx context.Context, baselineScanID, currentScanID string) (*models.ScanComparison, error)

	// Durable finding suppressions
	CreateFindingSuppression(ctx context.Context, suppression *models.FindingSuppression) error
	GetFindingSuppressionByID(ctx context.Context, id string) (*models.FindingSuppression, error)
	ListFindingSuppressions(ctx context.Context, repoID string, includeInactive bool) ([]models.FindingSuppression, error)
	RevokeFindingSuppression(ctx context.Context, id string) error
	CreateFindingSuppressionAudit(ctx context.Context, entry *models.FindingSuppressionAudit) error

	// Quality policies and gate results
	UpsertQualityPolicy(ctx context.Context, policy *models.QualityPolicy) error
	GetQualityPolicyByID(ctx context.Context, id string) (*models.QualityPolicy, error)
	ListQualityPolicies(ctx context.Context, scope, scopeID string) ([]models.QualityPolicy, error)
	UpsertQualityGateResult(ctx context.Context, result *models.QualityGateResult) error
	GetQualityGateResult(ctx context.Context, scanID, policyID string) (*models.QualityGateResult, error)
	ListQualityGateResults(ctx context.Context, scanID string) ([]models.QualityGateResult, error)

	// Fixes
	CreateFix(ctx context.Context, fix *models.Fix) error
	GetFixByID(ctx context.Context, id string) (*models.Fix, error)
	ListFixesByUser(ctx context.Context, userID string) ([]models.Fix, error)
	UpdateFix(ctx context.Context, fix *models.Fix) error

	// FixItems
	CreateFixItem(ctx context.Context, item *models.FixItem) error
	ListFixItemsByFix(ctx context.Context, fixID string) ([]models.FixItem, error)
	UpdateFixItem(ctx context.Context, item *models.FixItem) error

	// Loops
	CreateLoop(ctx context.Context, loop *models.Loop) error
	GetLoopByID(ctx context.Context, id string) (*models.Loop, error)
	ListLoopsByUser(ctx context.Context, userID string) ([]models.Loop, error)
	UpdateLoop(ctx context.Context, loop *models.Loop) error

	// ScanArtifacts
	CreateScanArtifact(ctx context.Context, artifact *models.ScanArtifact) error
	ListScanArtifacts(ctx context.Context, scanID string) ([]models.ScanArtifact, error)

	// SARIF imports
	CreateSARIFImport(ctx context.Context, imp *models.SARIFImport) error
	ListSARIFImportsByRepo(ctx context.Context, repoID string) ([]models.SARIFImport, error)

	// Scanner run records
	UpsertScannerRunRecord(ctx context.Context, record *models.ScannerRunRecord) error
	ListScannerRunRecords(ctx context.Context, scanID string) ([]models.ScannerRunRecord, error)

	// AILogs
	CreateAILog(ctx context.Context, log *models.AILog) error
	ListAILogsByScan(ctx context.Context, scanID string) ([]models.AILog, error)

	// ToolSummaries
	CreateToolSummary(ctx context.Context, ts *models.ToolSummary) error
	ListToolSummariesByScan(ctx context.Context, scanID string) ([]models.ToolSummary, error)

	// ScanRecommendations
	CreateScanRecommendation(ctx context.Context, rec *models.ScanRecommendation) error
	ListScanRecommendations(ctx context.Context, scanID string) ([]models.ScanRecommendation, error)

	// Settings
	GetSetting(ctx context.Context, key string) (string, error)
	SetSetting(ctx context.Context, key, value string) error
	ListSettings(ctx context.Context) (map[string]string, error)

	// Cascade Deletes
	// ListScanIDsByCollection returns all scan IDs for a collection.
	ListScanIDsByCollection(ctx context.Context, collectionID string) ([]string, error)
	// ListScanIDsByRepo returns all scan IDs for a repo.
	ListScanIDsByRepo(ctx context.Context, repoID string) ([]string, error)
	// DeleteScanCascade deletes a scan and all related data (findings, artifacts, fixes).
	DeleteScanCascade(ctx context.Context, scanID string) error
	// DeleteCollectionCascade deletes a collection, its scans, and all related data.
	DeleteCollectionCascade(ctx context.Context, collectionID string) ([]string, error)
	// DeleteRepoCascade deletes a repo, its scans, and all related data. Returns scan IDs for artifact cleanup.
	DeleteRepoCascade(ctx context.Context, repoID string) ([]string, error)

	// API Tokens
	CreateAPIToken(ctx context.Context, token *models.APIToken) error
	GetAPITokenByHash(ctx context.Context, hash string) (*models.APIToken, error)
	GetAPITokenByID(ctx context.Context, id string) (*models.APIToken, error)
	ListAPITokensByUser(ctx context.Context, userID string) ([]models.APIToken, error)
	RevokeAPIToken(ctx context.Context, id string) error
	TouchAPIToken(ctx context.Context, id string) error

	// Browser Sessions
	CreateAuthSession(ctx context.Context, session *models.AuthSession) error
	GetAuthSessionByHash(ctx context.Context, hash string) (*models.AuthSession, error)
	RevokeAuthSessionByHash(ctx context.Context, hash string) error
	TouchAuthSession(ctx context.Context, id string) error

	// Audit Log
	AppendAuditLog(ctx context.Context, entry *models.AuditLogEntry) error
	ListAuditLog(ctx context.Context, limit int) ([]models.AuditLogEntry, error)

	// Fleet aggregates
	FleetPosture(ctx context.Context, userID string, fleetMode bool) (*FleetPostureResult, error)
	FleetInventory(ctx context.Context, userID string, fleetMode bool) (*FleetInventoryResult, error)
	FleetNeedsAttention(ctx context.Context, userID string, fleetMode bool, limit int) ([]NeedsAttentionRow, error)
	FindingsAggregateByRule(ctx context.Context, userID string, fleetMode bool, limit int) ([]FindingsAggregateRow, error)

	// AI Prompt Templates
	CreatePromptTemplate(ctx context.Context, tmpl *models.AIPromptTemplate) error
	GetPromptTemplate(ctx context.Context, id string) (*models.AIPromptTemplate, error)
	ListPromptTemplates(ctx context.Context, scope, scopeID string) ([]models.AIPromptTemplate, error)
	UpdatePromptTemplate(ctx context.Context, tmpl *models.AIPromptTemplate) error
	DeletePromptTemplate(ctx context.Context, id string) error
	ResolvePromptSection(ctx context.Context, promptType, section, collectionID string) (string, error)
}
