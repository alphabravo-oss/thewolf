package db

import (
	"context"
	"time"

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
	FleetPosture(ctx context.Context, userID string, fleetMode bool, collectionID string) (*FleetPostureResult, error)
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

	// Remediation (agentic triage/fix sessions driven by the OpenCode CLI)
	CreateRemediationSession(ctx context.Context, session *models.RemediationSession) error
	GetRemediationSession(ctx context.Context, id string) (*models.RemediationSession, error)
	ListRemediationSessions(ctx context.Context, userID string) ([]models.RemediationSession, error)
	UpdateRemediationSession(ctx context.Context, session *models.RemediationSession) error
	// TransitionRemediationSession is UpdateRemediationSession's compare-
	// and-swap counterpart: the write only lands if the session's current
	// status still matches fromStatus, so two callers who both observed the
	// same review state cannot both advance it. A mismatch surfaces as
	// sql.ErrNoRows.
	TransitionRemediationSession(ctx context.Context, session *models.RemediationSession, fromStatus models.RemediationStatus) error
	SaveRemediationPlan(ctx context.Context, plan *models.RemediationPlan) error
	// GetRemediationPlan returns the most recently saved plan for a session.
	GetRemediationPlan(ctx context.Context, sessionID string) (*models.RemediationPlan, error)
	ApproveRemediationPlan(ctx context.Context, sessionID, approverID string) error
	// RejectRemediationPlan records a plan-gate rejection on the latest plan
	// row (approved_by, approved_at, rejected_reason) — the write
	// ApproveRemediationPlan has no counterpart for, so a rejection has
	// somewhere to land beside the plan a human actually reviewed.
	RejectRemediationPlan(ctx context.Context, sessionID, approverID, reason string) error
	SaveRemediationPatches(ctx context.Context, sessionID string, patches []models.RemediationPatch) error
	// ApproveRemediationPatches records who acted (approved or rejected) on
	// a session's whole patch set, across every patch row belonging to it —
	// the write ApprovePatches/RejectPatches otherwise silently discard.
	ApproveRemediationPatches(ctx context.Context, sessionID, approverID string) error
	ListRemediationPatches(ctx context.Context, sessionID string) ([]models.RemediationPatch, error)
	// AppendRemediationEvent and ListRemediationEvents back the SSE replay
	// stream: events are ordered by seq, never mutated once written.
	AppendRemediationEvent(ctx context.Context, event *models.RemediationEvent) error
	ListRemediationEvents(ctx context.Context, sessionID string, afterSeq int) ([]models.RemediationEvent, error)
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
