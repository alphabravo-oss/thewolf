package db

import (
	"context"
	"errors"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
)

// ErrHasScanRecords is returned when a repo cannot be deleted without
// also purging its scan/finding history.
var ErrHasScanRecords = errors.New("scan records exist")

// OrphanSummary is leftover scan/finding rows after their repo was deleted.
type OrphanSummary struct {
	ScanIDs      []string `json:"scan_ids"`
	ScanCount    int      `json:"scan_count"`
	FindingCount int      `json:"finding_count"`
}

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
	Key       string   `json:"key"`
	Repos     int      `json:"repos"`
	Findings  int      `json:"findings"`
	Tool      string   `json:"tool,omitempty"`
	Title     string   `json:"title,omitempty"`
	Severity  string   `json:"severity,omitempty"`
	RepoIDs   []string `json:"repo_ids,omitempty"`
	RepoNames []string `json:"repo_names,omitempty"`
}

// FindingsByRepoRow is current-open findings rolled up to one product.
type FindingsByRepoRow struct {
	RepoID   string `json:"repo_id"`
	Name     string `json:"name"`
	Total    int    `json:"total"`
	Critical int    `json:"critical"`
	High     int    `json:"high"`
	Medium   int    `json:"medium"`
	Low      int    `json:"low"`
	Info     int    `json:"info"`
}

// AuditQuery filters, sorts, and paginates the audit log.
type AuditQuery struct {
	Search   string // case-insensitive substring on path / action / method / event
	Method   string // exact HTTP method filter (empty = all)
	Category string // exact category filter (empty = all)
	Severity string // exact severity filter (empty = all)
	SortBy   string // "time" (default) | "status"
	Desc     bool   // sort descending (newest / highest first)
	Limit    int    // page size (1..1000; defaults applied in the store)
	Offset   int    // rows to skip
}

// Store defines the interface for all database operations.
type Store interface {
	// Lifecycle
	Close() error
	Migrate() error
	Ping(ctx context.Context) error
	ScannerReleases() ScannerReleasePersistence

	// Users
	CreateUser(ctx context.Context, user *models.User) error
	GetUserByID(ctx context.Context, id string) (*models.User, error)
	GetUserByEmail(ctx context.Context, email string) (*models.User, error)
	UpdateUser(ctx context.Context, user *models.User) error
	UpdateUserScannerSupplyChainPersonas(ctx context.Context, userID, encodedPersonas string) error
	UpdateUserMFA(ctx context.Context, user *models.User) error
	UpdateUserProfile(ctx context.Context, user *models.User) error
	ListUsers(ctx context.Context) ([]models.User, error)
	DeleteUser(ctx context.Context, id string) error

	// Repos
	CreateRepo(ctx context.Context, repo *models.Repo) error
	GetRepoByID(ctx context.Context, id string) (*models.Repo, error)
	ListReposByUser(ctx context.Context, userID string) ([]models.Repo, error)
	ListAllRepos(ctx context.Context) ([]models.Repo, error)
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
	ListAllCollections(ctx context.Context) ([]models.Collection, error)
	UpdateCollection(ctx context.Context, col *models.Collection) error
	DeleteCollection(ctx context.Context, id string) error
	AddRepoToCollection(ctx context.Context, collectionID, repoID string) error
	RemoveRepoFromCollection(ctx context.Context, collectionID, repoID string) error
	// SetRepoCollection moves a repo into exactly one collection: it clears any
	// existing membership for the repo, then adds it to collectionID. This is
	// the "folder" model where every repo belongs to a single collection.
	SetRepoCollection(ctx context.Context, repoID, collectionID string) error
	ListReposInCollection(ctx context.Context, collectionID string) ([]models.Repo, error)

	// Secrets
	CreateSecret(ctx context.Context, secret *models.Secret) error
	GetSecretByID(ctx context.Context, id string) (*models.Secret, error)
	GetSecretMetadataByID(ctx context.Context, id string) (*models.Secret, error)
	ListSecretsByUser(ctx context.Context, userID string) ([]models.Secret, error)
	ListSecretMetadataByUser(ctx context.Context, userID string) ([]models.Secret, error)
	ListAllSecrets(ctx context.Context) ([]models.Secret, error)
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
	FindScanByIdempotencyKey(ctx context.Context, userID, key string) (*models.Scan, error)
	ClaimNextScan(ctx context.Context, workerID, backend string, leaseUntil time.Time) (*models.Scan, error)
	StartScanExecution(ctx context.Context, scanID, leaseToken string, startedAt time.Time) (bool, error)
	HeartbeatScanLease(ctx context.Context, scanID, leaseToken string, leaseUntil time.Time) (bool, error)
	FinalizeScan(ctx context.Context, scan *models.Scan, leaseToken string) (bool, error)
	ReclaimStaleScans(ctx context.Context, now time.Time) (int, error)
	RequestScanCancellation(ctx context.Context, scanID string, at time.Time) error
	RequestScannerRunCancellation(ctx context.Context, scanID, toolName string, at time.Time) error
	DeleteFindingsByScanTool(ctx context.Context, scanID, toolName string) error
	CreateFindingsForScanLease(ctx context.Context, findings []models.Finding, scanID, leaseToken string) (bool, error)

	// Durable scan progress and worker capacity.
	AppendScanEvent(ctx context.Context, event *models.ScanEvent) error
	ListScanEvents(ctx context.Context, scanID string, afterSequence int64, limit int) ([]models.ScanEvent, error)
	UpsertScanWorker(ctx context.Context, worker *models.ScanWorker) error
	ListScanWorkers(ctx context.Context, activeAfter time.Time) ([]models.ScanWorker, error)

	// Findings
	CreateFinding(ctx context.Context, f *models.Finding) error
	CreateFindings(ctx context.Context, findings []models.Finding) error
	GetFindingByID(ctx context.Context, id string) (*models.Finding, error)
	ListFindingsByScan(ctx context.Context, scanID string) ([]models.Finding, error)
	ListFindingsByRepo(ctx context.Context, repoID string) ([]models.Finding, error)
	UpdateFinding(ctx context.Context, f *models.Finding) error
	UpdateFindingStatus(ctx context.Context, id string, status models.Status) error

	// Canonical vulnerabilities (Phase 7 dual-write of finding clusters)
	UpsertVulnerability(ctx context.Context, v *models.Vulnerability) error
	GetVulnerabilityByID(ctx context.Context, id string) (*models.Vulnerability, error)
	GetVulnerabilityByRepoKey(ctx context.Context, repoID, canonicalKey string) (*models.Vulnerability, error)
	ListVulnerabilitiesByRepo(ctx context.Context, repoID string) ([]models.Vulnerability, error)
	ListVulnerabilitiesByScan(ctx context.Context, scanID string) ([]models.Vulnerability, error)
	ListVulnerabilitiesForUser(ctx context.Context, userID string, fleetMode bool) ([]models.Vulnerability, error)
	InsertVulnerabilityEvidence(ctx context.Context, e *models.VulnerabilityEvidence) error
	ListEvidenceByVulnerability(ctx context.Context, vulnerabilityID string) ([]models.VulnerabilityEvidence, error)
	MoveVulnerabilityEvidence(ctx context.Context, evidenceIDs []string, toVulnerabilityID string) error
	DeleteVulnerability(ctx context.Context, id string) error
	RefreshVulnerabilityEvidence(ctx context.Context, vulnerabilityID string) error

	// Baselines and Comparisons
	CreateScanSchedule(ctx context.Context, schedule *models.ScanSchedule) error
	GetScanScheduleByID(ctx context.Context, id string) (*models.ScanSchedule, error)
	ListScanSchedulesByUser(ctx context.Context, userID string) ([]models.ScanSchedule, error)
	ListEnabledScanSchedules(ctx context.Context) ([]models.ScanSchedule, error)
	UpdateScanSchedule(ctx context.Context, schedule *models.ScanSchedule) error
	DeleteScanSchedule(ctx context.Context, id string) error

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
	// DeleteCollectionKeepHistory deletes a collection and memberships but
	// leaves scan/finding/loop rows (collection_id is cleared).
	DeleteCollectionKeepHistory(ctx context.Context, collectionID string) error
	// DeleteRepoCascade deletes a repo, its scans, and all related data. Returns scan IDs for artifact cleanup.
	DeleteRepoCascade(ctx context.Context, repoID string) ([]string, error)
	// DeleteRepoKeepHistory deletes a repo only when it has no scan records.
	// Returns ErrHasScanRecords otherwise.
	DeleteRepoKeepHistory(ctx context.Context, repoID string) error
	// ListOrphanScanIDs returns scans whose repo row is gone.
	ListOrphanScanIDs(ctx context.Context) ([]string, error)
	// OrphanSummary counts leftover scans and findings after a repo delete.
	OrphanSummary(ctx context.Context) (*OrphanSummary, error)
	// PurgeOrphanedRecords deletes scans/findings/fix jobs whose repo
	// no longer exists. Returns the orphan scan IDs that were removed.
	PurgeOrphanedRecords(ctx context.Context) ([]string, error)

	// Fix jobs (autonomous fix engine — gated by autofix_enabled)
	EnqueueFixJob(ctx context.Context, job *models.FixJob) error
	GetFixJobByID(ctx context.Context, id string) (*models.FixJob, error)
	ListFixJobs(ctx context.Context, repoID string) ([]models.FixJob, error)
	// ListFixJobsByUser is the tenant-scoped list: only the caller's jobs,
	// optionally narrowed to one repo. Used by the API to prevent IDOR.
	ListFixJobsByUser(ctx context.Context, userID, repoID string) ([]models.FixJob, error)
	// ClaimNextFixJob atomically claims the oldest queued job for a worker.
	// Returns (nil, nil) when the queue is empty — never double-claims.
	ClaimNextFixJob(ctx context.Context, workerID string) (*models.FixJob, error)
	UpdateFixJob(ctx context.Context, job *models.FixJob) error
	// ReclaimStaleJobs requeues claimed/running jobs whose worker stopped
	// heartbeating before the cutoff (crashed/killed worker recovery).
	ReclaimStaleJobs(ctx context.Context, cutoff time.Time) (int, error)
	CreateFixAttempt(ctx context.Context, attempt *models.FixAttempt) error
	ListFixAttempts(ctx context.Context, jobID string) ([]models.FixAttempt, error)

	CreateRemediation(ctx context.Context, rem *models.Remediation) error
	GetRemediationByID(ctx context.Context, id string) (*models.Remediation, error)
	GetOpenRemediationByOrigin(ctx context.Context, originScanID string) (*models.Remediation, error)
	GetLatestRemediationByOrigin(ctx context.Context, originScanID string) (*models.Remediation, error)
	UpdateRemediation(ctx context.Context, rem *models.Remediation) error
	ListScansByOrigin(ctx context.Context, originScanID string) ([]models.Scan, error)
	ListFixJobsByRemediation(ctx context.Context, remediationID string) ([]models.FixJob, error)

	EnqueueFixerConsole(ctx context.Context, cons *models.FixerConsole) error
	GetFixerConsoleByID(ctx context.Context, id string) (*models.FixerConsole, error)
	ClaimNextFixerConsole(ctx context.Context, workerID string) (*models.FixerConsole, error)
	UpdateFixerConsole(ctx context.Context, cons *models.FixerConsole) error
	ReclaimStaleConsoles(ctx context.Context, cutoff time.Time) (int, error)
	AppendFixerConsoleStdin(ctx context.Context, consoleID, data string) error
	DrainFixerConsoleStdin(ctx context.Context, consoleID string) ([]string, error)
	ListActiveFixerConsoles(ctx context.Context) ([]models.FixerConsole, error)

	// API Tokens
	CreateAPIToken(ctx context.Context, token *models.APIToken) error
	GetAPITokenByHash(ctx context.Context, hash string) (*models.APIToken, error)
	GetAPITokenByID(ctx context.Context, id string) (*models.APIToken, error)
	ListAPITokensByUser(ctx context.Context, userID string) ([]models.APIToken, error)
	ListAllAPITokens(ctx context.Context) ([]models.APIToken, error)
	RevokeAPIToken(ctx context.Context, id string) error
	RevokeAPITokensByUser(ctx context.Context, userID string, exceptTokenID string) error
	TouchAPIToken(ctx context.Context, id string) error

	// Browser Sessions
	CreateAuthSession(ctx context.Context, session *models.AuthSession) error
	GetAuthSessionByHash(ctx context.Context, hash string) (*models.AuthSession, error)
	RevokeAuthSessionByHash(ctx context.Context, hash string) error
	RevokeAuthSessionsByUser(ctx context.Context, userID string, exceptSessionID string) error
	TouchAuthSession(ctx context.Context, id string) error

	// Audit Log
	AppendAuditLog(ctx context.Context, entry *models.AuditLogEntry) error
	ListAuditLog(ctx context.Context, limit int) ([]models.AuditLogEntry, error)
	// QueryAuditLog returns a filtered/sorted/paginated page of audit entries
	// plus the total count matching the filter (for pagination).
	QueryAuditLog(ctx context.Context, q AuditQuery) ([]models.AuditLogEntry, int, error)

	// Fleet aggregates
	ListCurrentOpenFindings(ctx context.Context, userID string, fleetMode bool, collectionID string) ([]models.Finding, error)
	FleetPosture(ctx context.Context, userID string, fleetMode bool, collectionID string) (*FleetPostureResult, error)
	FleetInventory(ctx context.Context, userID string, fleetMode bool) (*FleetInventoryResult, error)
	FleetNeedsAttention(ctx context.Context, userID string, fleetMode bool, limit int) ([]NeedsAttentionRow, error)
	FindingsAggregateByRule(ctx context.Context, userID string, fleetMode bool, limit int) ([]FindingsAggregateRow, error)
	FindingsByRepo(ctx context.Context, userID string, fleetMode bool, collectionID string) ([]FindingsByRepoRow, error)

	// AI Prompt Templates
	CreatePromptTemplate(ctx context.Context, tmpl *models.AIPromptTemplate) error
	GetPromptTemplate(ctx context.Context, id string) (*models.AIPromptTemplate, error)
	ListPromptTemplates(ctx context.Context, scope, scopeID string) ([]models.AIPromptTemplate, error)
	UpdatePromptTemplate(ctx context.Context, tmpl *models.AIPromptTemplate) error
	DeletePromptTemplate(ctx context.Context, id string) error
	ResolvePromptSection(ctx context.Context, promptType, section, collectionID string) (string, error)
}

// prepareScanForWrite applies the durable queue defaults to explicit INSERTs.
// Database column defaults do not apply when a named INSERT supplies the zero
// value, and legacy callers intentionally construct only the original fields.
func prepareScanForWrite(scan *models.Scan) {
	if scan.RequestJSON == "" {
		scan.RequestJSON = "{}"
	}
	if scan.Phase == "" {
		scan.Phase = "queued"
	}
	if scan.MaxAttempts <= 0 {
		scan.MaxAttempts = 2
	}
	if scan.Categories == "" {
		scan.Categories = "[]"
	}
	if scan.IncludePaths == "" {
		scan.IncludePaths = "[]"
	}
	if scan.ExcludePaths == "" {
		scan.ExcludePaths = "[]"
	}
	if scan.ToolsSelected == "" {
		scan.ToolsSelected = "[]"
	}
	if scan.ToolsCompleted == "" {
		scan.ToolsCompleted = "[]"
	}
	if scan.ToolsFailed == "" {
		scan.ToolsFailed = "[]"
	}
	if scan.ToolsErrors == "" {
		scan.ToolsErrors = "{}"
	}
}
