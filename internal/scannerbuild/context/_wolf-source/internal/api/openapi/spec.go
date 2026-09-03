package openapi

import (
	"encoding/json"
	"regexp"
	"strings"
)

// Version is the API version this spec describes.
const Version = "1.0.0"

const maxScannerArtifactDiffResponseCharacters = 256 << 10

// Endpoint describes one API operation. The table of all Endpoints is the
// single source of truth for both the OpenAPI spec and the route-coverage
// test that guards against undocumented routes.
type Endpoint struct {
	Method  string // GET, POST, PUT, DELETE
	Path    string // relative to /api/v1, e.g. "/repos/{id}"
	Tag     string // grouping in the UI
	Summary string
	// Scope is "" for public endpoints, "self" for authenticated endpoints
	// with no scope requirement, or a concrete scope string.
	Scope string
	// Body, when non-empty, names a request-body schema in components.
	Body string
	// List marks endpoints that return a paginated ListResponse.
	List bool
}

// Endpoints is the complete, ordered catalog of API operations.
func Endpoints() []Endpoint {
	return []Endpoint{
		// Health & meta (public).
		{"GET", "/health", "system", "Liveness probe", "", "", false},
		{"GET", "/ready", "system", "Readiness probe", "", "", false},
		{"GET", "/metrics", "system", "Prometheus metrics", "", "", false},
		{"GET", "/version", "system", "Build version info", "", "", false},
		{"GET", "/edition", "system", "Edition, modules, entitlements, and Community limits", "", "", false},
		{"GET", "/license", "system", "Commercial license status (Community has none)", "", "", false},
		{"GET", "/coverage", "system", "Honest scanner coverage matrix from tools.yaml", "", "", false},
		{"GET", "/scan-profiles", "scans", "Named scan profiles (fast/standard/release)", "", "", true},
		{"GET", "/capabilities/{name}", "system", "Capability status (Community 404s enterprise/cloud)", "", "", false},
		{"POST", "/license/validate", "system", "Validate a commercial license blob (Community always reports invalid)", "admin", "LicenseBlobRequest", false},
		{"POST", "/license/install", "system", "Install a commercial license (admin; Community rejects)", "admin", "LicenseBlobRequest", false},
		{"GET", "/mcp/status", "system", "Whether MCP is enabled", "", "", false},
		{"POST", "/mcp", "mcp", "JSON-RPC MCP over application services (default off)", "read:repos", "", false},

		// Auth (public).
		{"GET", "/auth/settings", "auth", "Get public authentication settings", "", "", false},
		{"GET", "/auth/providers", "auth", "List login providers (Community: local)", "", "", false},
		{"GET", "/auth/sso/{name}/start", "auth", "Start SSO for a registered redirect provider", "", "", false},
		{"GET", "/auth/sso/{name}/callback", "auth", "SSO callback; issues a local session", "", "", false},
		{"POST", "/auth/register", "auth", "Register a new user", "", "RegisterRequest", false},
		{"POST", "/auth/login", "auth", "Log in and receive a JWT", "", "LoginRequest", false},
		{"POST", "/auth/mfa/login", "auth", "Complete two-factor login (exchange challenge + code for a session)", "", "", false},

		// Auth / session (authenticated).
		{"POST", "/auth/logout", "auth", "Log out the current session", "self", "", false},
		{"GET", "/auth/me", "auth", "Get the current identity", "self", "", false},
		{"PUT", "/auth/profile", "auth", "Update the current user's display name / email", "self", "", false},
		{"PUT", "/auth/password", "auth", "Change the current user's password", "self", "ChangePasswordRequest", false},

		// Two-factor auth (authenticated, self-managed).
		{"GET", "/auth/mfa/status", "auth", "Get the caller's MFA status", "self", "", false},
		{"POST", "/auth/mfa/setup", "auth", "Begin MFA enrollment (returns a QR code + secret)", "self", "", false},
		{"POST", "/auth/mfa/activate", "auth", "Activate MFA by confirming a code (returns one-time recovery codes)", "self", "", false},
		{"POST", "/auth/mfa/disable", "auth", "Disable MFA after verifying a current code", "self", "", false},

		// API tokens (authenticated, self-managed).
		{"GET", "/auth/tokens", "tokens", "List the caller's API tokens", "self", "", true},
		{"POST", "/auth/tokens", "tokens", "Create an API token", "self", "CreateTokenRequest", false},
		{"DELETE", "/auth/tokens/{id}", "tokens", "Revoke an API token", "self", "", false},

		// Audit log (admin).
		{"GET", "/audit-log", "audit", "List mutating-request audit entries", "admin", "", true},

		// Admin oversight (admin).
		{"GET", "/admin/tokens", "admin", "List all users' API tokens (metadata only)", "admin", "", true},
		{"GET", "/admin/secrets", "admin", "List all users' secrets, masked (existence only)", "admin", "", true},
		{"GET", "/admin/disk", "admin", "Disk usage for artifacts, workspaces, and the database", "admin", "", false},
		{"POST", "/admin/workspaces/reap", "admin", "Remove stale scan workspaces older than a TTL", "admin", "", false},

		// Users (admin).
		{"GET", "/users", "users", "List users", "admin", "", true},
		{"POST", "/users", "users", "Create a user", "admin", "CreateUserRequest", false},
		{"PUT", "/users/{id}/role", "users", "Change a user's role (admin|user)", "admin", "", false},
		{"PUT", "/users/{id}/scanner-supply-chain-access", "users", "Assign predefined scanner supply-chain personas", "admin", "UserScannerSupplyChainAccessRequest", false},
		{"POST", "/users/{id}/mfa/reset", "users", "Reset a user's MFA (e.g. lost device)", "admin", "", false},
		{"DELETE", "/users/{id}", "users", "Delete a user", "admin", "", false},

		// Repos.
		{"GET", "/repos", "repos", "List repositories", "read:repos", "", true},
		{"POST", "/repos", "repos", "Add a repository", "write:repos", "CreateRepoRequest", false},
		{"GET", "/repos/{id}", "repos", "Get a repository", "read:repos", "", false},
		{"PUT", "/repos/{id}", "repos", "Update a repository", "write:repos", "UpdateRepoRequest", false},
		{"POST", "/repos/{id}/sync", "repos", "Clone or pull the latest commits without starting a scan", "write:repos", "", false},
		{"DELETE", "/repos/{id}", "repos", "Delete a repository (pass purge=true to also remove scan records)", "write:repos", "", false},
		{"GET", "/repos/{id}/branches", "repos", "List a repository's branches", "read:repos", "", true},
		{"GET", "/repos/{id}/fixable", "repos", "Writability preflight: can wolf write a fix branch to this repo?", "read:repos", "", false},
		{"GET", "/repos/{id}/baselines", "repos", "List repository scan baselines", "read:scans", "", true},
		{"POST", "/repos/{id}/baselines", "repos", "Create a repository scan baseline", "write:scans", "CreateBaselineRequest", false},

		// Source credentials.
		{"GET", "/credentials", "credentials", "List source credentials (masked metadata only)", "read:credentials", "", true},
		{"POST", "/credentials", "credentials", "Register an encrypted source credential", "write:credentials", "CreateCredentialRequest", false},
		{"GET", "/credentials/{id}", "credentials", "Get source credential metadata", "read:credentials", "", false},
		{"DELETE", "/credentials/{id}", "credentials", "Delete a source credential", "write:credentials", "", false},

		// Remote SSH nodes.
		{"GET", "/nodes", "nodes", "List remote SSH nodes", "read:config", "", true},
		{"POST", "/nodes", "nodes", "Create a remote SSH node", "write:config", "RemoteNodeRequest", false},
		{"GET", "/nodes/{id}", "nodes", "Get a remote SSH node", "read:config", "", false},
		{"PUT", "/nodes/{id}", "nodes", "Update a remote SSH node", "write:config", "RemoteNodeRequest", false},
		{"DELETE", "/nodes/{id}", "nodes", "Delete a remote SSH node", "write:config", "", false},
		{"POST", "/nodes/{id}/check", "nodes", "Check SSH connectivity", "write:config", "", false},
		{"GET", "/nodes/{id}/browse", "nodes", "Browse directories on a remote SSH node", "read:config", "", false},
		{"GET", "/nodes/{id}/git-info", "nodes", "Inspect a remote git working tree", "read:config", "", false},
		{"POST", "/nodes/{id}/discover-repos", "nodes", "Discover git repositories on a remote SSH node", "write:config", "DiscoverReposRequest", false},

		// Collections.
		{"GET", "/collections", "collections", "List collections", "read:repos", "", true},
		{"POST", "/collections", "collections", "Create a collection", "write:repos", "CreateCollectionRequest", false},
		{"GET", "/collections/{id}", "collections", "Get a collection", "read:repos", "", false},
		{"PUT", "/collections/{id}", "collections", "Update a collection", "write:repos", "CreateCollectionRequest", false},
		{"DELETE", "/collections/{id}", "collections", "Delete a collection (pass purge=true to also remove scan records)", "write:repos", "", false},
		{"POST", "/collections/{id}/repos", "collections", "Add a repo to a collection", "write:repos", "CollectionRepoRequest", false},
		{"DELETE", "/collections/{id}/repos/{repoId}", "collections", "Remove a repo from a collection", "write:repos", "", false},
		{"GET", "/collections/{id}/tools", "collections", "List a collection's tools", "read:repos", "", true},
		{"GET", "/collections/{id}/metrics", "collections", "Get a collection's metrics", "read:repos", "", false},

		// Schedules.
		{"GET", "/schedules", "schedules", "List scan schedules", "read:scans", "", true},
		{"POST", "/schedules", "schedules", "Create a scan schedule", "write:scans", "", false},
		{"PUT", "/schedules/{id}", "schedules", "Update a scan schedule", "write:scans", "", false},
		{"DELETE", "/schedules/{id}", "schedules", "Delete a scan schedule", "write:scans", "", false},

		// Setup and notifications.
		{"GET", "/setup/status", "setup", "First-run setup counts", "read:repos", "", false},
		{"POST", "/setup/sample-repo", "setup", "Create the sample repository", "write:repos", "", false},
		{"GET", "/notifications", "notifications", "In-app notification bell", "read:scans", "", false},

		// Webhooks.
		{"POST", "/webhooks/github", "webhooks", "Inbound GitHub webhook", "", "", false},
		{"GET", "/webhooks/events", "webhooks", "Named application event catalog for outbound webhooks", "", "", true},
		{"POST", "/webhooks/outbound/test", "webhooks", "Send a test outbound webhook", "write:config", "", false},

		// Scans.
		{"GET", "/scans", "scans", "List scans", "read:scans", "", true},
		{"GET", "/scans/trends", "scans", "Scan trends over time", "read:scans", "", false},
		{"GET", "/scans/orphans", "scans", "List leftover scans whose repo was deleted", "read:scans", "", false},
		{"DELETE", "/scans/orphans", "scans", "Delete leftover scans/findings for missing repos", "write:scans", "", false},
		{"POST", "/scans", "scans", "Start a scan", "write:scans", "CreateScanRequest", false},
		{"POST", "/scans/{id}/release-rescans", "scans", "Create a distinct scan pinned to an explicitly selected scanner release", "write:scans + operate:scanner-supply-chain", "ReleaseRescanRequest", false},
		{"POST", "/scans/{id}/retry", "scans", "Retry a failed or cancelled scan with the original request", "write:scans", "", false},
		{"POST", "/scans/preflight", "scans", "Check which selected scanners are missing their image before scanning", "read:scans", "PreflightScanRequest", false},
		{"GET", "/scans/{id}", "scans", "Get a scan", "read:scans", "", false},
		{"GET", "/scans/{id}/lineage", "scans", "Get origin-scan lineage (children and agents)", "read:scans", "", false},
		{"GET", "/scans/{id}/result", "scans", "Get a compact automation-oriented scan result", "read:scans", "", false},
		{"GET", "/scans/{id}/findings", "scans", "List a scan's findings", "read:scans", "", true},
		{"GET", "/scans/{id}/findings/stats", "scans", "Finding statistics for a scan", "read:scans", "", false},
		{"GET", "/scans/{id}/stream", "scans", "Stream scan progress (SSE)", "read:scans", "", false},
		{"GET", "/scans/{id}/report", "scans", "Get a scan's report", "read:scans", "", false},
		{"GET", "/scans/{id}/manifest", "scans", "Get a scan's manifest", "read:scans", "", false},
		{"GET", "/scans/{id}/sarif", "scans", "Get a scan's SARIF output", "read:scans", "", false},
		{"GET", "/scans/{id}/coverage", "scans", "Get a scan's coverage", "read:scans", "", false},
		{"GET", "/scans/{id}/gate", "scans", "Get a scan's quality gate result", "read:scans", "", false},
		{"GET", "/scans/{id}/diff", "scans", "Get a scan's baseline diff", "read:scans", "", false},
		{"POST", "/scans/{id}/compare", "scans", "Compare a scan to a baseline scan", "read:scans", "CompareScanRequest", false},
		{"GET", "/scans/{id}/compare/{compareId}", "scans", "Compare two scans", "read:scans", "", false},
		{"GET", "/scans/{id}/tools", "scans", "List a scan's tools", "read:scans", "", true},
		{"GET", "/scans/{id}/scanner-runs", "scans", "List scanner run records", "read:scans", "", true},
		{"GET", "/scans/{id}/tools/{toolName}/output", "scans", "Get a tool's raw output", "read:scans", "", false},
		{"GET", "/scans/{id}/artifacts/{artifactId}/download", "scans", "Download a scan artifact", "read:scans", "", false},
		{"GET", "/scans/{id}/ai-logs", "scans", "List a scan's AI logs", "read:scans", "", true},
		{"GET", "/scans/{id}/tool-summaries", "scans", "List a scan's tool summaries", "read:scans", "", true},
		{"GET", "/scans/{id}/recommendations", "scans", "List a scan's recommendations", "read:scans", "", true},
		{"DELETE", "/scans/{id}", "scans", "Cancel a scan", "write:scans", "", false},
		{"DELETE", "/scans/{id}/tools/{toolName}", "scans", "Cancel one tool of a scan", "write:scans", "", false},

		// Source discovery.
		{"POST", "/sources/github/list-org-repos", "sources", "List GitHub repos the PAT can read (all accessible, or one org/user)", "write:repos", "ListOrgReposRequest", true},

		// Fleet aggregates.
		{"GET", "/fleet/posture", "fleet", "Fleet-wide posture summary", "read:scans", "", false},
		{"GET", "/fleet/inventory", "fleet", "Fleet inventory grouped by source / collection / language", "read:repos", "", false},
		{"GET", "/fleet/needs-attention", "fleet", "Top repos that need attention, scored", "read:scans", "", true},

		// Findings.
		{"GET", "/findings", "findings", "List findings", "read:findings", "", true},
		{"GET", "/findings/export", "findings", "Export findings", "read:findings", "", false},
		{"GET", "/findings/trends", "findings", "Finding trends over time", "read:findings", "", false},
		{"GET", "/findings/trends/export", "findings", "Export finding trends", "read:findings", "", false},
		{"GET", "/findings/aggregate", "findings", "Aggregate findings (e.g. top vulnerable rules)", "read:findings", "", true},
		{"GET", "/findings/by-repo", "findings", "Current-open findings grouped by repository", "read:findings", "", true},
		{"POST", "/findings/bulk", "findings", "Bulk-update finding status or suppress findings", "write:findings", "BulkUpdateFindingsRequest", false},
		{"GET", "/findings/{id}", "findings", "Get a finding", "read:findings", "", false},
		{"PUT", "/findings/{id}/status", "findings", "Change a finding's status", "write:findings", "FindingStatusRequest", false},

		// Canonical vulnerabilities (dual-write of finding clusters).
		{"GET", "/vulnerabilities", "vulnerabilities", "List canonical vulnerabilities (finding compatibility layer)", "read:findings", "", true},
		{"GET", "/evidence", "vulnerabilities", "List evidence for a vulnerability (query vulnerability_id)", "read:findings", "", true},
		{"GET", "/vulnerabilities/{id}", "vulnerabilities", "Get a canonical vulnerability and its evidence", "read:findings", "", false},
		{"GET", "/vulnerabilities/{id}/evidence", "vulnerabilities", "List evidence members of a vulnerability", "read:findings", "", true},
		{"GET", "/vulnerabilities/{id}/attack-path", "intelligence", "Enterprise attack path from cited evidence (Community 404)", "read:findings", "", false},
		{"POST", "/vulnerabilities/{id}/investigate", "intelligence", "Evidence-grounded investigation with citations (Community 404)", "read:findings", "InvestigateRequest", false},
		{"POST", "/vulnerabilities/{id}/verify", "verification", "Governed runtime verification; production-deny default (Community 404)", "write:findings", "VerifyRequest", false},
		{"POST", "/vulnerabilities/{id}/split", "vulnerabilities", "Split selected finding evidence into a new vulnerability", "write:findings", "SplitVulnerabilityRequest", false},
		{"POST", "/vulnerabilities/{id}/merge", "vulnerabilities", "Merge another vulnerability's evidence into this one", "write:findings", "MergeVulnerabilityRequest", false},

		// SARIF.
		{"POST", "/sarif/import", "sarif", "Import SARIF findings", "write:scans", "SARIFImportRequest", false},

		// Suppressions.
		{"GET", "/suppressions", "suppressions", "List finding suppressions", "read:findings", "", true},
		{"POST", "/suppressions", "suppressions", "Create a finding suppression", "write:findings", "SuppressionRequest", false},
		{"POST", "/suppressions/preview", "suppressions", "Preview a finding suppression", "write:findings", "SuppressionRequest", false},
		{"DELETE", "/suppressions/{id}", "suppressions", "Revoke a finding suppression", "write:findings", "", false},

		// Policies.
		{"GET", "/policies", "policies", "List quality policies", "read:config", "", true},
		{"POST", "/policies", "policies", "Create a quality policy", "write:config", "PolicyRequest", false},
		{"PUT", "/policies/{id}", "policies", "Update a quality policy", "write:config", "PolicyRequest", false},

		// Fixes (autonomous fix engine — gated by the autofix_enabled setting).
		{"POST", "/remediations/{id}/accept", "fixes", "Accept a local remediation and freeze the origin scan", "write:fixes", "", false},
		{"GET", "/fixes", "fixes", "List fix jobs", "read:fixes", "", true},
		{"POST", "/fixes", "fixes", "Enqueue an autonomous fix job (multi-turn on a branch, optional push)", "write:fixes", "CreateFixRequest", false},
		{"GET", "/fixes/engines", "fixes", "List fixer engine auth status (OAuth session + API keys)", "read:fixes", "", false},
		{"POST", "/fixes/consoles", "fixes", "Start a fixer-worker login or operator console", "write:fixes", "CreateFixerConsoleRequest", false},
		{"GET", "/fixes/consoles/{id}", "fixes", "Get a fixer console session", "read:fixes", "", false},
		{"GET", "/fixes/consoles/{id}/stream", "fixes", "Stream fixer console output (SSE)", "read:fixes", "", false},
		{"POST", "/fixes/consoles/{id}/input", "fixes", "Send keystrokes to a fixer console", "write:fixes", "FixerConsoleInputRequest", false},
		{"DELETE", "/fixes/consoles/{id}", "fixes", "Cancel a fixer console session", "write:fixes", "", false},
		{"GET", "/fixes/{id}", "fixes", "Get a fix job and its attempts", "read:fixes", "", false},
		{"GET", "/fixes/{id}/diff", "fixes", "Get the proposed diff for a fix job", "read:fixes", "", false},
		{"GET", "/fixes/{id}/commits", "fixes", "List commits on a fix job's workspace branch", "read:fixes", "", false},
		{"GET", "/fixes/{id}/stream", "fixes", "Stream fix worker logs (SSE)", "read:fixes", "", false},
		{"POST", "/fixes/{id}/resume", "fixes", "Continue a paused fix job or push its branch", "write:fixes", "ResumeFixRequest", false},
		{"DELETE", "/fixes/{id}", "fixes", "Cancel a fix job", "write:fixes", "", false},

		// Filesystem helpers.
		{"GET", "/browse", "system", "Browse a local filesystem path", "read:repos", "", false},
		{"GET", "/git-info", "system", "Inspect a local git repository", "read:repos", "", false},

		// Config.
		{"GET", "/config/secrets", "config", "List secrets", "read:config", "", true},
		{"POST", "/config/secrets", "config", "Create a secret", "write:config", "CreateSecretRequest", false},
		{"DELETE", "/config/secrets/{id}", "config", "Delete a secret", "write:config", "", false},
		{"GET", "/config/plugins", "config", "List plugins", "read:config", "", true},
		{"POST", "/config/plugins/{name}/install", "config", "Install a plugin", "write:config", "", false},
		{"GET", "/config/setup", "config", "Get setup status", "read:config", "", false},

		// Scanners.
		{"GET", "/scanners/tools", "scanners", "List scanner tools", "read:config", "", true},
		{"GET", "/scanners/tools/{name}", "scanners", "Get scanner tool", "read:config", "", false},
		{"POST", "/scanners/tools/check-updates", "scanners", "Check scanner tool updates", "write:config", "", true},
		{"POST", "/scanners/tools/{name}/check-update", "scanners", "Check one scanner tool update", "write:config", "", false},
		{"GET", "/scanners/images", "scanners", "List scanner images", "read:config", "", true},
		{"POST", "/scanners/images/pull", "scanners", "Pull one scanner image", "write:config", "", false},
		{"POST", "/scanners/images/{variant}/build", "scanners", "Build one scanner image (SSE)", "write:config", "", false},
		{"POST", "/scanners/images/build-all", "scanners", "Build all scanner images (SSE)", "write:config", "", false},
		{"GET", "/scanners/custom-builds", "scanners", "List durable custom scanner-image builds", "read:config", "", true},
		{"POST", "/scanners/custom-builds", "scanners", "Enqueue a durable custom scanner-image build", "write:config", "ScannerCustomBuildRequest", false},
		{"GET", "/scanners/custom-builds/{id}", "scanners", "Get a durable custom scanner-image build", "read:config", "", false},
		{"GET", "/scanners/custom-builds/{id}/events", "scanners", "Stream persisted custom-build logs and terminal state (SSE)", "read:config", "", false},
		{"POST", "/scanners/custom-builds/{id}/cancel", "scanners", "Request custom-build cancellation", "write:config", "ScannerReasonRequest", false},
		{"POST", "/scanners/custom-builds/{id}/retry", "scanners", "Retry a terminal custom build", "write:config", "ScannerReasonRequest", false},
		{"GET", "/scanners/config", "scanners", "Get scanner config", "read:config", "", false},
		{"GET", "/scanners/runtime-capabilities", "scanners", "Get active scan-runtime capabilities", "read:config", "", false},
		{"GET", "/scanners/workers", "scanners", "List active scan workers", "read:config", "", true},
		{"GET", "/scanners/list", "scanners", "List scanners", "read:config", "", true},
		{"POST", "/scanners/plan", "scanners", "Explain scanner run and skip decisions", "read:config", "ScannerPlanRequest", false},
		{"POST", "/scanners/doctor", "scanners", "Run scanner diagnostics", "write:config", "", false},
		{"POST", "/scanners/pull", "scanners", "Pull all scanner images", "write:config", "", false},

		// Scanner release management.
		{"GET", "/scanner-supply-chain/overview", "scanner-supply-chain", "Get scanner release freshness and health", "read:scanner-supply-chain", "", false},
		{"GET", "/scanner-supply-chain/updates", "scanner-supply-chain", "List update items from a discovery run", "read:scanner-supply-chain", "", true},
		{"GET", "/scanner-supply-chain/policy", "scanner-supply-chain", "Get active scanner supply-chain policy", "read:scanner-supply-chain", "", false},
		{"PUT", "/scanner-supply-chain/policy", "scanner-supply-chain", "Create the next validated policy revision", "admin:scanner-supply-chain", "ScannerPolicyRequest", false},
		{"POST", "/scanner-supply-chain/policy/validate", "scanner-supply-chain", "Validate scanner policy without saving it", "read:scanner-supply-chain", "ScannerPolicyRequest", false},
		{"POST", "/scanner-supply-chain/policy/dry-run", "scanner-supply-chain", "Evaluate proposed policy against a historical candidate", "read:scanner-supply-chain", "ScannerPolicyDryRunRequest", false},
		{"GET", "/scanner-supply-chain/policy/revisions", "scanner-supply-chain", "List immutable scanner policy revisions", "read:scanner-supply-chain", "", true},
		{"POST", "/scanner-supply-chain/policy/revisions/{revision}/restore", "scanner-supply-chain", "Restore historical policy as a new revision", "admin:scanner-supply-chain", "ScannerReasonRequest", false},
		{"POST", "/scanner-supply-chain/discovery-runs", "scanner-supply-chain", "Enqueue scanner update discovery", "operate:scanner-supply-chain", "ScannerDiscoveryRequest", false},
		{"GET", "/scanner-supply-chain/discovery-runs", "scanner-supply-chain", "List scanner discovery history", "read:scanner-supply-chain", "", true},
		{"GET", "/scanner-supply-chain/discovery-runs/{id}", "scanner-supply-chain", "Get scanner discovery and update items", "read:scanner-supply-chain", "", false},
		{"POST", "/scanner-supply-chain/discovery-runs/{id}/cancel", "scanner-supply-chain", "Cancel scanner discovery", "operate:scanner-supply-chain", "", false},
		{"GET", "/scanner-supply-chain/discovery-runs/{id}/events", "scanner-supply-chain", "Stream durable discovery events", "read:scanner-supply-chain", "", false},
		{"POST", "/scanner-supply-chain/candidates", "scanner-supply-chain", "Create a scanner release candidate", "operate:scanner-supply-chain", "ScannerCandidateRequest", false},
		{"GET", "/scanner-supply-chain/candidates", "scanner-supply-chain", "List scanner release candidates", "read:scanner-supply-chain", "", true},
		{"GET", "/scanner-supply-chain/candidates/{id}", "scanner-supply-chain", "Get candidate builds, evidence, and approvals", "read:scanner-supply-chain", "", false},
		{"GET", "/scanner-supply-chain/candidates/{id}/diffs/{kind}", "scanner-supply-chain", "Get bounded candidate manifest or lock diff content", "read:scanner-supply-chain", "", false},
		{"GET", "/scanner-supply-chain/candidates/{id}/events", "scanner-supply-chain", "Stream durable candidate events", "read:scanner-supply-chain", "", false},
		{"POST", "/scanner-supply-chain/candidates/{id}/cancel", "scanner-supply-chain", "Cooperatively cancel candidate build work", "operate:scanner-supply-chain", "ScannerReasonRequest", false},
		{"POST", "/scanner-supply-chain/candidates/{id}/retry", "scanner-supply-chain", "Retry a safely resumable candidate", "operate:scanner-supply-chain", "", false},
		{"POST", "/scanner-supply-chain/candidates/{id}/approve", "scanner-supply-chain", "Approve an exact candidate evidence digest", "approve:scanner-releases", "ScannerApprovalRequest", false},
		{"POST", "/scanner-supply-chain/candidates/{id}/reject", "scanner-supply-chain", "Reject a scanner release candidate", "approve:scanner-releases", "ScannerApprovalRequest", false},
		{"POST", "/scanner-supply-chain/candidates/{id}/exceptions", "scanner-supply-chain", "Record an approved scoped and expiring candidate exception", "approve:scanner-releases", "ScannerExceptionRequest", false},
		{"POST", "/scanner-supply-chain/candidates/{id}/publish", "scanner-supply-chain", "Record verified publication of an approved candidate", "approve:scanner-releases", "ScannerPublicationRequest", false},
		{"GET", "/scanner-supply-chain/releases", "scanner-supply-chain", "List immutable scanner releases", "read:scanner-supply-chain", "", true},
		{"GET", "/scanner-supply-chain/releases/compare", "scanner-supply-chain", "Compare two immutable scanner release inventories", "read:scanner-supply-chain", "", false},
		{"GET", "/scanner-supply-chain/releases/{id}", "scanner-supply-chain", "Get immutable scanner release inventory", "read:scanner-supply-chain", "", false},
		{"GET", "/scanner-supply-chain/releases/{id}/diffs/{kind}", "scanner-supply-chain", "Get bounded release manifest or lock diff content", "read:scanner-supply-chain", "", false},
		{"GET", "/scanner-supply-chain/releases/{id}/events", "scanner-supply-chain", "Stream durable release events", "read:scanner-supply-chain", "", false},
		{"GET", "/scanner-supply-chain/releases/{id}/export", "scanner-supply-chain", "Stream a portable immutable scanner release bundle", "read:scanner-supply-chain", "", false},
		{"POST", "/scanner-supply-chain/releases/{id}/verify", "scanner-supply-chain", "Verify immutable release evidence", "read:scanner-supply-chain", "", false},
		{"POST", "/scanner-supply-chain/releases/{id}/promote", "scanner-supply-chain", "Create a canary-first release rollout", "operate:scanner-supply-chain", "ScannerPromotionRequest", false},
		{"POST", "/scanner-supply-chain/releases/{id}/deprecate", "scanner-supply-chain", "Deprecate a scanner release", "admin:scanner-supply-chain", "ScannerReasonRequest", false},
		{"POST", "/scanner-supply-chain/releases/{id}/revoke", "scanner-supply-chain", "Revoke a scanner release", "admin:scanner-supply-chain", "ScannerReasonRequest", false},
		{"POST", "/scanner-supply-chain/release-imports", "scanner-supply-chain", "Verify and import a portable scanner release bundle", "admin:scanner-supply-chain", "", false},
		{"POST", "/scanner-supply-chain/legacy-release-imports", "scanner-supply-chain", "Snapshot configured legacy scanner references as immutable, unverified historical evidence without changing runtime assignments", "admin:scanner-supply-chain", "LegacyReleaseImportRequest", false},
		{"GET", "/scanner-supply-chain/rollouts", "scanner-supply-chain", "List scanner release rollouts", "read:scanner-supply-chain", "", true},
		{"GET", "/scanner-supply-chain/rollouts/{id}", "scanner-supply-chain", "Get rollout cohorts and health", "read:scanner-supply-chain", "", false},
		{"GET", "/scanner-supply-chain/rollouts/{id}/events", "scanner-supply-chain", "Stream durable rollout events", "read:scanner-supply-chain", "", false},
		{"POST", "/scanner-supply-chain/rollouts/{id}/pause", "scanner-supply-chain", "Pause rollout at a safe boundary", "operate:scanner-supply-chain", "ScannerReasonRequest", false},
		{"POST", "/scanner-supply-chain/rollouts/{id}/resume", "scanner-supply-chain", "Resume rollout at its persisted boundary", "operate:scanner-supply-chain", "ScannerReasonRequest", false},
		{"POST", "/scanner-supply-chain/rollouts/{id}/rollback", "scanner-supply-chain", "Roll back to the prior verified release", "operate:scanner-supply-chain", "ScannerReasonRequest", false},
		{"GET", "/scanner-supply-chain/registries", "scanner-supply-chain", "List scanner OCI registry targets", "manage:scanner-registries", "", false},
		{"POST", "/scanner-supply-chain/registries", "scanner-supply-chain", "Create scanner OCI registry metadata", "manage:scanner-registries", "ScannerRegistryRequest", false},
		{"GET", "/scanner-supply-chain/registries/{id}", "scanner-supply-chain", "Get scanner OCI registry metadata", "manage:scanner-registries", "", false},
		{"PATCH", "/scanner-supply-chain/registries/{id}", "scanner-supply-chain", "Update scanner OCI registry metadata", "manage:scanner-registries", "ScannerRegistryRequest", false},
		{"DELETE", "/scanner-supply-chain/registries/{id}", "scanner-supply-chain", "Disable scanner OCI registry metadata", "manage:scanner-registries", "", false},
		{"POST", "/scanner-supply-chain/registries/{id}/check", "scanner-supply-chain", "Check registry reachability and authorization", "manage:scanner-registries", "", false},
		{"POST", "/scanner-supply-chain/registries/{id}/reconcile", "scanner-supply-chain", "Compare release digests with registry state", "manage:scanner-registries", "ScannerRegistryReconcileRequest", false},
		{"POST", "/scanner-supply-chain/registries/{id}/jobs", "scanner-supply-chain", "Queue durable registry reconciliation or drift repair", "manage:scanner-registries", "ScannerRegistryJobRequest", false},
		{"POST", "/scanner-supply-chain/registries/{id}/cleanup-jobs", "scanner-supply-chain", "Queue guarded registry quarantine cleanup", "manage:scanner-registries", "ScannerReasonRequest", false},
		{"GET", "/scanner-supply-chain/registry-jobs", "scanner-supply-chain", "List registry reconciliation, repair, and cleanup jobs", "manage:scanner-registries", "", true},
		{"GET", "/scanner-supply-chain/registry-jobs/{id}", "scanner-supply-chain", "Get registry job and exact image evidence readback", "manage:scanner-registries", "", false},
		{"GET", "/scanner-supply-chain/registry-jobs/{id}/events", "scanner-supply-chain", "Stream durable registry job events", "manage:scanner-registries", "", false},
		{"POST", "/scanner-supply-chain/registry-jobs/{id}/retry", "scanner-supply-chain", "Retry a dead-lettered registry job", "manage:scanner-registries", "ScannerReasonRequest", false},
		{"GET", "/scanner-supply-chain/registry-quarantine", "scanner-supply-chain", "List retained and cleanup-eligible registry quarantine objects", "manage:scanner-registries", "", true},
		{"GET", "/scanner-supply-chain/signers", "scanner-supply-chain", "List masked customer signer profiles", "admin:scanner-supply-chain", "", true},
		{"POST", "/scanner-supply-chain/signers", "scanner-supply-chain", "Create a customer signer profile using opaque references", "admin:scanner-supply-chain", "ScannerSignerRequest", false},
		{"GET", "/scanner-supply-chain/signers/{id}", "scanner-supply-chain", "Get a masked customer signer profile", "admin:scanner-supply-chain", "", false},
		{"POST", "/scanner-supply-chain/signers/{id}/rotate", "scanner-supply-chain", "Atomically rotate a customer signer profile", "admin:scanner-supply-chain", "ScannerSignerRequest", false},
		{"POST", "/scanner-supply-chain/signers/{id}/revoke", "scanner-supply-chain", "Revoke a customer signer profile", "admin:scanner-supply-chain", "ScannerReasonRequest", false},
		{"GET", "/scanner-supply-chain/notifications", "scanner-supply-chain", "List scanner release notifications and delivery state", "read:scanner-supply-chain", "", true},
		{"GET", "/scanner-supply-chain/notifications/{id}", "scanner-supply-chain", "Get scanner release notification delivery state", "read:scanner-supply-chain", "", false},
		{"POST", "/scanner-supply-chain/notifications/{id}/retry", "scanner-supply-chain", "Retry a dead-lettered scanner release notification", "admin:scanner-supply-chain", "ScannerReasonRequest", false},
		{"GET", "/scanner-supply-chain/alerts", "scanner-supply-chain", "List current or historical scanner release operational alerts", "read:scanner-supply-chain", "", true},
		{"GET", "/scanner-supply-chain/alerts/{id}", "scanner-supply-chain", "Get a scanner release operational alert", "read:scanner-supply-chain", "", false},
		{"GET", "/scanner-supply-chain/audit", "scanner-supply-chain", "List immutable scanner release domain events", "read:scanner-supply-chain", "", true},
		{"GET", "/scanner-supply-chain/audit/export", "scanner-supply-chain", "Export immutable scanner release events as JSONL", "read:scanner-supply-chain", "", false},

		// Settings.
		{"GET", "/settings", "config", "Get settings", "read:config", "", false},
		{"PUT", "/settings", "config", "Update settings", "write:config", "", false},

		// AI prompts & providers.
		{"GET", "/ai-prompts", "ai", "List AI prompt templates", "read:config", "", true},
		{"PUT", "/ai-prompts", "ai", "Upsert an AI prompt template", "write:config", "", false},
		{"GET", "/ai-prompts/defaults", "ai", "Get default AI prompts", "read:config", "", false},
		{"POST", "/ai-prompts/preview", "ai", "Preview a rendered AI prompt", "read:config", "", false},
		{"DELETE", "/ai-prompts/{id}", "ai", "Delete an AI prompt template", "write:config", "", false},
		{"GET", "/ai-providers", "ai", "List configured AI providers", "read:config", "", true},
	}
}

var pathParamRe = regexp.MustCompile(`\{([^}]+)\}`)

// SpecJSON builds and returns the OpenAPI 3.0 document as JSON bytes.
func SpecJSON() []byte {
	paths := map[string]any{}
	for _, ep := range Endpoints() {
		op := buildOperation(ep)
		key := ep.Path
		entry, ok := paths[key].(map[string]any)
		if !ok {
			entry = map[string]any{}
			if params := pathParameters(key); len(params) > 0 {
				entry["parameters"] = params
			}
			paths[key] = entry
		}
		entry[strings.ToLower(ep.Method)] = op
	}

	doc := map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title":       "wolf API",
			"version":     Version,
			"description": "The Wolf code-scanning platform API (53 scanners). Community is source available, not OSI open source. Every endpoint here is also reachable from the `wolf` CLI. Authenticate with a JWT (interactive login) or a `wolf_`-prefixed API token (Authorization: Bearer).",
			"license":     map[string]any{"name": "Source available (BSL 1.1 intended; LICENSE pending counsel)", "url": "https://github.com/alphabravo-oss/thewolf"},
		},
		"servers": []any{
			map[string]any{"url": "/api/v1"},
		},
		"tags":       buildTags(),
		"paths":      paths,
		"components": buildComponents(),
	}
	b, _ := json.MarshalIndent(doc, "", "  ")
	return b
}

func buildTags() []any {
	order := []string{
		"system", "auth", "tokens", "audit", "users", "repos", "credentials",
		"nodes", "collections", "schedules", "scans", "sources", "fleet", "findings", "sarif",
		"suppressions", "policies", "fixes", "loops", "config", "setup", "notifications",
		"webhooks", "scanners", "scanner-supply-chain", "ai", "admin",
	}
	tags := make([]any, 0, len(order))
	for _, t := range order {
		tags = append(tags, map[string]any{"name": t})
	}
	return tags
}

func pathParameters(path string) []any {
	matches := pathParamRe.FindAllStringSubmatch(path, -1)
	params := make([]any, 0, len(matches))
	for _, m := range matches {
		schema := map[string]any{"type": "string"}
		if m[1] == "kind" && strings.Contains(path, "/diffs/") {
			schema["enum"] = []string{"manifest", "lock"}
		}
		params = append(params, map[string]any{
			"name":     m[1],
			"in":       "path",
			"required": true,
			"schema":   schema,
		})
	}
	return params
}

func buildOperation(ep Endpoint) map[string]any {
	responses := buildResponses(ep)
	if strings.HasPrefix(ep.Path, "/scanner-supply-chain/") {
		addCorrelationResponseHeaders(responses)
	}
	op := map[string]any{
		"tags":        []any{ep.Tag},
		"summary":     ep.Summary,
		"operationId": operationID(ep),
		"responses":   responses,
	}
	if ep.Scope != "" {
		op["security"] = []any{map[string]any{"BearerAuth": []any{}}}
		desc := "Requires authentication."
		if ep.Scope != "self" {
			desc = "Required scope: `" + ep.Scope + "`."
		}
		op["description"] = desc
	}
	if legacyScannerBuildEndpoint(ep) {
		op["deprecated"] = true
		deprecation := "Deprecated compatibility scanner-image build stream. Migrate to durable `POST /scanners/custom-builds` operations. Set `WOLF_SCANNER_LEGACY_BUILD_ENDPOINTS=false` only after callers have migrated; disabled endpoints return 410 Gone."
		if description, ok := op["description"].(string); ok && description != "" {
			op["description"] = description + " " + deprecation
		} else {
			op["description"] = deprecation
		}
	}
	if ep.Body != "" {
		op["requestBody"] = map[string]any{
			"required": true,
			"content": map[string]any{
				"application/json": map[string]any{
					"schema": map[string]any{"$ref": "#/components/schemas/" + ep.Body},
				},
			},
		}
	}
	if ep.Method == "POST" && ep.Path == "/scanner-supply-chain/release-imports" {
		op["requestBody"] = map[string]any{
			"required":    true,
			"description": "A v1 metadata bundle or v2 complete OCI tar.zst transfer. The server streams to bounded staging and verifies the signed manifest, every checksum, OCI descriptor/platform closure, and trust evidence before durable or registry writes.",
			"content": map[string]any{
				"application/vnd.wolf.scanner-release-bundle.v1+tar+zstd": map[string]any{
					"schema": map[string]any{"type": "string", "format": "binary"},
				},
				"application/octet-stream": map[string]any{
					"schema": map[string]any{"type": "string", "format": "binary"},
				},
				"application/vnd.wolf.scanner-release-bundle.v2+tar+zstd": map[string]any{
					"schema": map[string]any{"type": "string", "format": "binary"},
				},
			},
		}
	}
	if ep.Method == "GET" &&
		(ep.Path == "/scanner-supply-chain/alerts" ||
			ep.Path == "/scanner-supply-chain/alerts/{id}") {
		schema := "ScannerAlertResponse"
		if ep.Path == "/scanner-supply-chain/alerts" {
			schema = "ScannerAlertListResponse"
		}
		return map[string]any{
			"200": map[string]any{
				"description": "Durable scanner release operational alert data",
				"content": map[string]any{
					"application/json": map[string]any{
						"schema": map[string]any{"$ref": "#/components/schemas/" + schema},
					},
				},
			},
			"400": errRef("Invalid alert filter or pagination cursor"),
			"401": errRef("Missing or invalid credential"),
			"403": errRef("Insufficient scope"),
			"404": errRef("Alert not found"),
			"429": errRef("Rate limited"),
		}
	}
	if ep.Method == "POST" && ep.Path == "/scans" {
		op["parameters"] = []any{map[string]any{
			"name": "Idempotency-Key", "in": "header", "required": false,
			"description": "Returns the original scan for the same caller and normalized request; conflicting reuse returns 409.",
			"schema":      map[string]any{"type": "string", "maxLength": 255},
		}}
	}
	if ep.Method == "POST" && ep.Path == "/scans/{id}/release-rescans" {
		op["parameters"] = []any{map[string]any{
			"name": "Idempotency-Key", "in": "header", "required": true,
			"description": "Stable identity for this source scan and selected release. Conflicting reuse returns 409.",
			"schema":      map[string]any{"type": "string", "maxLength": 255},
		}}
	}
	if ep.Method == "GET" && ep.Path == "/scans/{id}/stream" {
		op["parameters"] = []any{map[string]any{
			"name": "Last-Event-ID", "in": "header", "required": false,
			"description": "Resume durable scan events after this monotonic event sequence.",
			"schema":      map[string]any{"type": "integer", "format": "int64", "minimum": 0},
		}}
	}
	if strings.HasPrefix(ep.Path, "/scanners/custom-builds") {
		parameters := []any{}
		if ep.Method == "POST" {
			parameters = append(parameters, map[string]any{
				"name": "Idempotency-Key", "in": "header", "required": true,
				"description": "Stable caller-generated command identity. Exact replays return the original operation; conflicting reuse returns 409.",
				"schema":      map[string]any{"type": "string", "maxLength": 200},
			})
		}
		if ep.Method == "POST" &&
			(strings.HasSuffix(ep.Path, "/cancel") ||
				strings.HasSuffix(ep.Path, "/retry")) {
			parameters = append(parameters, map[string]any{
				"name": "If-Match", "in": "header", "required": true,
				"description": "Quoted custom-build resource version obtained from ETag or the response body.",
				"schema":      map[string]any{"type": "string", "example": `"3"`},
			})
		}
		if ep.Method == "GET" && strings.HasSuffix(ep.Path, "/events") {
			parameters = append(parameters, map[string]any{
				"name": "Last-Event-ID", "in": "header", "required": false,
				"description": "Resume persisted logs after this monotonic sequence. Terminal state uses event ID 4001.",
				"schema":      map[string]any{"type": "integer", "format": "int64", "minimum": 0, "maximum": 4001},
			})
		}
		if ep.Method == "GET" && ep.Path == "/scanners/custom-builds" {
			parameters = append(parameters,
				map[string]any{
					"name": "state", "in": "query", "required": false,
					"schema": map[string]any{"type": "string", "enum": []string{
						"queued", "claimed", "running", "completed", "partial", "failed", "cancelled",
					}},
				},
				map[string]any{
					"name": "cursor", "in": "query", "required": false,
					"schema": map[string]any{"type": "string"},
				},
				map[string]any{
					"name": "limit", "in": "query", "required": false,
					"schema": map[string]any{"type": "integer", "minimum": 1, "maximum": 200, "default": 50},
				},
			)
		}
		if len(parameters) != 0 {
			op["parameters"] = parameters
		}
	}
	if strings.HasPrefix(ep.Path, "/scanner-supply-chain/") {
		parameters := []any{
			map[string]any{
				"name": "Traceparent", "in": "header", "required": false,
				"description": "Optional W3C version 00 trace context. Malformed values are ignored and replaced.",
				"schema": map[string]any{
					"type": "string", "maxLength": 55,
					"pattern": `^00-[a-f0-9]{32}-[a-f0-9]{16}-[a-f0-9]{2}$`,
				},
			},
			map[string]any{
				"name": "X-Wolf-Operation-ID", "in": "header", "required": false,
				"description": "Optional bounded caller correlation identity. The effective value is returned and persisted with durable events.",
				"schema": map[string]any{
					"type": "string", "minLength": 8, "maxLength": 128,
					"pattern": `^[A-Za-z0-9][A-Za-z0-9._:/-]{7,127}$`,
				},
			},
		}
		if scannerIdempotentCommand(ep) {
			parameters = append(parameters, map[string]any{
				"name": "Idempotency-Key", "in": "header", "required": true,
				"description": "Stable caller-generated command identity. Conflicting reuse returns 409.",
				"schema":      map[string]any{"type": "string", "maxLength": 200},
			})
		}
		if scannerVersionedCommand(ep) {
			parameters = append(parameters, map[string]any{
				"name": "If-Match", "in": "header", "required": true,
				"description": "Quoted resource version obtained from ETag.",
				"schema":      map[string]any{"type": "string", "example": `"3"`},
			})
		}
		if ep.Method == "GET" && strings.HasSuffix(ep.Path, "/events") {
			parameters = append(parameters, map[string]any{
				"name": "Last-Event-ID", "in": "header", "required": false,
				"description": "Resume after this persisted event sequence.",
				"schema":      map[string]any{"type": "integer", "format": "int64", "minimum": 0},
			})
		}
		if ep.Method == "GET" && ep.Path == "/scanner-supply-chain/notifications" {
			parameters = append(parameters,
				map[string]any{
					"name": "state", "in": "query", "required": false,
					"schema": map[string]any{
						"type": "string", "enum": []string{
							"pending", "delivering", "retry", "delivered", "dead_letter",
						},
					},
				},
				map[string]any{
					"name": "destination_type", "in": "query", "required": false,
					"schema": map[string]any{
						"type": "string", "enum": []string{"ui", "webhook", "email", "siem"},
					},
				},
				map[string]any{
					"name": "notification_type", "in": "query", "required": false,
					"schema": map[string]any{"type": "string", "maxLength": 100},
				},
				map[string]any{
					"name": "cursor", "in": "query", "required": false,
					"schema": map[string]any{"type": "string"},
				},
				map[string]any{
					"name": "limit", "in": "query", "required": false,
					"schema": map[string]any{
						"type": "integer", "minimum": 1, "maximum": 200, "default": 50,
					},
				},
			)
		}
		if ep.Method == "GET" && ep.Path == "/scanner-supply-chain/alerts" {
			parameters = append(parameters,
				map[string]any{
					"name": "state", "in": "query", "required": false,
					"description": "Defaults to open. Use all to include resolved history.",
					"schema": map[string]any{
						"type": "string", "enum": []string{"open", "resolved", "all"},
						"default": "open",
					},
				},
				map[string]any{
					"name": "kind", "in": "query", "required": false,
					"schema": map[string]any{
						"type": "string", "enum": []string{
							"missed_discovery", "stale_stable_release", "queue_backlog",
							"lease_churn", "repeated_gate_failure", "mirror_drift",
							"rollout_failure", "signature_health",
						},
					},
				},
				map[string]any{
					"name": "severity", "in": "query", "required": false,
					"schema": map[string]any{
						"type": "string", "enum": []string{"warning", "critical"},
					},
				},
				map[string]any{
					"name": "cursor", "in": "query", "required": false,
					"schema": map[string]any{"type": "string"},
				},
				map[string]any{
					"name": "limit", "in": "query", "required": false,
					"schema": map[string]any{
						"type": "integer", "minimum": 1, "maximum": 200, "default": 50,
					},
				},
			)
		}
		if ep.Method == "GET" &&
			(ep.Path == "/scanner-supply-chain/audit" ||
				ep.Path == "/scanner-supply-chain/audit/export") {
			parameters = append(parameters,
				map[string]any{
					"name": "trace_id", "in": "query", "required": false,
					"description": "Exact durable 32-character trace identifier.",
					"schema": map[string]any{
						"type": "string", "pattern": `^[a-f0-9]{32}$`,
					},
				},
				map[string]any{
					"name": "operation_id", "in": "query", "required": false,
					"description": "Exact durable Wolf operation identifier.",
					"schema": map[string]any{
						"type": "string", "minLength": 8, "maxLength": 128,
						"pattern": `^[A-Za-z0-9][A-Za-z0-9._:/-]{7,127}$`,
					},
				},
			)
		}
		if ep.Method == "POST" && ep.Path == "/scanner-supply-chain/release-imports" {
			parameters = append(parameters,
				map[string]any{
					"name": "X-Wolf-Import-Reason", "in": "header", "required": true,
					"description": "Auditable operator reason for introducing the offline release.",
					"schema":      map[string]any{"type": "string", "maxLength": 500},
				},
				map[string]any{
					"name": "allow_unverified", "in": "query", "required": false,
					"description": "Administrative break-glass override for an unsigned or untrusted bundle. Archive and content digests are still verified; this never reports the signature as verified.",
					"schema":      map[string]any{"type": "boolean", "default": false},
				},
				map[string]any{
					"name": "registry_target_id", "in": "query", "required": false,
					"description": "Configured private-registry target for digest-idempotent v2 upload and destination readback.",
					"schema":      map[string]any{"type": "string"},
				},
				map[string]any{
					"name": "no_network", "in": "query", "required": false,
					"description": "Require a bundle-only import; incompatible with registry_target_id.",
					"schema":      map[string]any{"type": "boolean", "default": false},
				},
			)
		}
		if ep.Method == "GET" && ep.Path == "/scanner-supply-chain/releases/{id}/export" {
			parameters = append(parameters,
				map[string]any{
					"name": "bundle_version", "in": "query", "required": false,
					"description": "1 preserves metadata-only compatibility; 2 embeds a complete OCI transfer.",
					"schema":      map[string]any{"type": "string", "enum": []string{"1", "2"}, "default": "1"},
				},
				map[string]any{
					"name": "platform", "in": "query", "required": false,
					"description": "OCI platform included in a v2 export. Repeat to select multiple; omit for all.",
					"schema":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"style":       "form", "explode": true,
				},
			)
		}
		if len(parameters) != 0 {
			op["parameters"] = parameters
		}
	}
	return op
}

func addCorrelationResponseHeaders(responses map[string]any) {
	headers := map[string]any{
		"Traceparent": map[string]any{
			"description": "Effective W3C trace context for this response.",
			"schema":      map[string]any{"type": "string"},
		},
		"X-Wolf-Trace-ID": map[string]any{
			"description": "Effective 32-character trace identifier.",
			"schema":      map[string]any{"type": "string", "pattern": `^[a-f0-9]{32}$`},
		},
		"X-Wolf-Operation-ID": map[string]any{
			"description": "Durable operation identifier recorded with scanner release events.",
			"schema":      map[string]any{"type": "string"},
		},
	}
	for _, raw := range responses {
		response, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		existing, _ := response["headers"].(map[string]any)
		if existing == nil {
			existing = map[string]any{}
			response["headers"] = existing
		}
		for name, schema := range headers {
			if _, present := existing[name]; !present {
				existing[name] = schema
			}
		}
	}
}

func scannerIdempotentCommand(ep Endpoint) bool {
	if ep.Method != "POST" {
		return false
	}
	if ep.Path == "/scanner-supply-chain/policy/validate" ||
		ep.Path == "/scanner-supply-chain/policy/dry-run" ||
		strings.HasSuffix(ep.Path, "/restore") ||
		strings.HasSuffix(ep.Path, "/verify") {
		return false
	}
	if ep.Path == "/scanner-supply-chain/registries" ||
		strings.HasPrefix(ep.Path, "/scanner-supply-chain/signers") ||
		strings.HasSuffix(ep.Path, "/check") ||
		strings.HasSuffix(ep.Path, "/reconcile") {
		return false
	}
	return true
}

func scannerVersionedCommand(ep Endpoint) bool {
	if ep.Path == "/scanner-supply-chain/policy" && ep.Method == "PUT" {
		return true
	}
	if strings.HasPrefix(ep.Path, "/scanner-supply-chain/signers/") &&
		(strings.HasSuffix(ep.Path, "/rotate") || strings.HasSuffix(ep.Path, "/revoke")) {
		return true
	}
	if strings.HasSuffix(ep.Path, "/cancel") || strings.HasSuffix(ep.Path, "/retry") ||
		strings.HasSuffix(ep.Path, "/deprecate") || strings.HasSuffix(ep.Path, "/revoke") ||
		strings.HasSuffix(ep.Path, "/pause") || strings.HasSuffix(ep.Path, "/resume") ||
		strings.HasSuffix(ep.Path, "/rollback") {
		return true
	}
	return (ep.Method == "PATCH" || ep.Method == "DELETE") &&
		strings.HasPrefix(ep.Path, "/scanner-supply-chain/registries/")
}

func scannerAcceptedCommand(ep Endpoint) bool {
	if ep.Method == "POST" && ep.Path == "/scanners/custom-builds" {
		return true
	}
	if !strings.HasPrefix(ep.Path, "/scanner-supply-chain/") || ep.Method != "POST" {
		return false
	}
	if scannerSynchronousPost(ep) {
		return false
	}
	if ep.Path == "/scanner-supply-chain/release-imports" {
		return false
	}
	if strings.Contains(ep.Path, "/policy/revisions/") &&
		strings.HasSuffix(ep.Path, "/restore") {
		return false
	}
	return scannerIdempotentCommand(ep)
}

func operationID(ep Endpoint) string {
	id := strings.ToLower(ep.Method) + " " + ep.Path
	id = strings.NewReplacer("/", " ", "{", "", "}", "", "-", " ").Replace(id)
	parts := strings.Fields(id)
	for i := 1; i < len(parts); i++ {
		parts[i] = strings.Title(parts[i]) //nolint:staticcheck // ASCII identifiers only
	}
	return strings.Join(parts, "")
}

func buildResponses(ep Endpoint) map[string]any {
	if legacyScannerBuildEndpoint(ep) {
		gone := errRef("Legacy synchronous scanner image builds are disabled; use durable scanner release candidates")
		gone["headers"] = legacyScannerBuildHeaders()
		return map[string]any{
			"200": map[string]any{
				"description": "Scanner image build event stream",
				"headers":     legacyScannerBuildHeaders(),
				"content": map[string]any{
					"text/event-stream": map[string]any{
						"schema": map[string]any{"type": "string"},
					},
				},
			},
			"401": errRef("Missing or invalid credential"),
			"403": errRef("Insufficient scope"),
			"410": gone,
			"503": errRef("Legacy scanner build endpoint configuration is invalid"),
			"429": errRef("Rate limited"),
		}
	}
	if ep.Method == "GET" &&
		ep.Path == "/scanners/custom-builds/{id}/events" {
		return map[string]any{
			"200": map[string]any{
				"description": "Persisted custom-build log and terminal-state event stream",
				"content": map[string]any{
					"text/event-stream": map[string]any{
						"schema": map[string]any{"type": "string"},
					},
				},
			},
			"400": errRef("Invalid Last-Event-ID"),
			"401": errRef("Missing or invalid credential"),
			"403": errRef("Insufficient scope"),
			"404": errRef("Custom build not found"),
			"429": errRef("Rate limited"),
		}
	}
	if ep.Method == "GET" && ep.Path == "/scanner-supply-chain/overview" {
		return map[string]any{
			"200": map[string]any{
				"description": "Scanner release-management overview and effective capability stage",
				"content": map[string]any{
					"application/json": map[string]any{
						"schema": map[string]any{"$ref": "#/components/schemas/ScannerSupplyChainOverviewResponse"},
					},
				},
			},
			"401": errRef("Missing or invalid credential"),
			"403": errRef("Insufficient scope"),
			"409": errRef("Scanner release management is disabled"),
			"429": errRef("Rate limited"),
		}
	}
	if ep.Method == "GET" && ep.Path == "/scanner-supply-chain/rollouts/{id}" {
		return map[string]any{
			"200": map[string]any{
				"description": "Durable rollout state with combined policy counters and distinct synthetic-corpus and sampled real-scan health evidence.",
				"content": map[string]any{
					"application/json": map[string]any{
						"schema": map[string]any{"$ref": "#/components/schemas/ScannerRolloutDetailResponse"},
					},
				},
			},
			"401": errRef("Missing or invalid credential"),
			"403": errRef("Insufficient scope"),
			"404": errRef("Rollout not found"),
			"409": errRef("Scanner release management is disabled"),
			"429": errRef("Rate limited"),
		}
	}
	if ep.Method == "GET" && ep.Path == "/scanner-supply-chain/releases/{id}/export" {
		return map[string]any{
			"200": map[string]any{
				"description": "Deterministic v1 metadata or v2 complete OCI scanner release tar.zst bundle.",
				"headers": map[string]any{
					"X-Wolf-Manifest-Digest":         map[string]any{"schema": map[string]any{"type": "string"}},
					"X-Wolf-Bundle-Digest":           map[string]any{"schema": map[string]any{"type": "string"}},
					"X-Wolf-Bundle-Signature-Status": map[string]any{"schema": map[string]any{"type": "string"}},
					"X-Wolf-Bundle-Schema":           map[string]any{"schema": map[string]any{"type": "string"}},
					"X-Wolf-Bundle-Platforms":        map[string]any{"schema": map[string]any{"type": "string"}},
				},
				"content": map[string]any{
					"application/vnd.wolf.scanner-release-bundle.v1+tar+zstd": map[string]any{
						"schema": map[string]any{"type": "string", "format": "binary"},
					},
					"application/vnd.wolf.scanner-release-bundle.v2+tar+zstd": map[string]any{
						"schema": map[string]any{"type": "string", "format": "binary"},
					},
				},
			},
			"401": errRef("Missing or invalid credential"),
			"403": errRef("Insufficient scope"),
			"404": errRef("Release not found"),
			"422": errRef("Stored release inventory cannot be represented as a portable bundle"),
			"429": errRef("Rate limited"),
		}
	}
	if ep.Method == "GET" && strings.Contains(ep.Path, "/diffs/{kind}") {
		return map[string]any{
			"200": map[string]any{
				"description": "Bounded UTF-8 unified diff content. available=false represents an expected artifact that has not been persisted; truncated=true means only the safe response prefix is returned.",
				"content": map[string]any{
					"application/json": map[string]any{
						"schema": map[string]any{"$ref": "#/components/schemas/ScannerArtifactDiffResponse"},
					},
				},
			},
			"400": errRef("Diff kind must be manifest or lock"),
			"401": errRef("Missing or invalid credential"),
			"403": errRef("Insufficient scope"),
			"404": errRef("Candidate or release not found"),
			"409": errRef("Stored artifact size or digest no longer matches its immutable record"),
			"413": errRef("Stored diff exceeds the bounded processing limit"),
			"415": errRef("Artifact is not an approved textual diff media type"),
			"422": errRef("Artifact URI, path, or UTF-8 content is invalid"),
			"429": errRef("Rate limited"),
			"503": errRef("Artifact storage is not configured"),
		}
	}
	if ep.Method == "POST" && ep.Path == "/scanner-supply-chain/release-imports" {
		success := map[string]any{
			"description": "Verified import result. integrity_verified covers the bundle index and all content digests; signature_status independently reports cryptographic trust.",
			"content": map[string]any{
				"application/json": map[string]any{
					"schema": map[string]any{"$ref": "#/components/schemas/ScannerReleaseBundleImportResponse"},
				},
			},
		}
		return map[string]any{
			"200": success,
			"201": success,
			"400": errRef("Missing precondition, reason, or invalid option"),
			"401": errRef("Missing or invalid credential"),
			"403": errRef("Insufficient scope"),
			"409": errRef("Release identity, name, or manifest digest conflicts with existing state"),
			"413": errRef("Compressed bundle exceeds the upload limit"),
			"415": errRef("Unsupported bundle media type"),
			"422": errRef("Bundle schema, digest, inventory, or trust-policy verification failed"),
			"428": errRef("Idempotency-Key is missing"),
			"429": errRef("Rate limited"),
		}
	}
	if ep.Method == "POST" && ep.Path == "/scanner-supply-chain/legacy-release-imports" {
		success := map[string]any{
			"description": "Imported legacy release snapshot. runtime_assignments_changed is always false and provenance limitations are explicit.",
			"content": map[string]any{
				"application/json": map[string]any{
					"schema": map[string]any{"$ref": "#/components/schemas/SuccessResponse"},
				},
			},
		}
		return map[string]any{
			"200": success, "201": success,
			"400": errRef("Reason, configured reference, or resolved digest is invalid"),
			"401": errRef("Missing or invalid credential"),
			"403": errRef("Insufficient scope"),
			"409": errRef("Legacy import identity conflicts with existing state"),
			"428": errRef("Idempotency-Key is missing"),
			"429": errRef("Rate limited"),
		}
	}
	okSchema := "SuccessResponse"
	if ep.List {
		okSchema = "ListResponse"
	}
	okCode := "200"
	if ep.Method == "POST" && ep.Body != "" {
		okCode = "201"
	}
	if scannerSynchronousPost(ep) {
		okCode = "200"
	}
	if scannerAcceptedCommand(ep) {
		okCode = "202"
	}
	if ep.Method == "DELETE" {
		okCode = "204"
	}
	resp := map[string]any{}
	if okCode == "204" {
		resp["204"] = map[string]any{"description": "No content"}
	} else {
		resp[okCode] = map[string]any{
			"description": "Success",
			"content": map[string]any{
				"application/json": map[string]any{
					"schema": map[string]any{"$ref": "#/components/schemas/" + okSchema},
				},
			},
		}
	}
	if ep.Body != "" {
		resp["400"] = errRef("Invalid request")
	}
	if ep.Method == "POST" && ep.Path == "/scans" {
		resp["409"] = errRef("Idempotency key was reused for a different request")
	}
	if ep.Scope != "" {
		resp["401"] = errRef("Missing or invalid credential")
		if ep.Scope != "self" {
			resp["403"] = errRef("Insufficient scope")
		}
	}
	if strings.Contains(ep.Path, "{") {
		resp["404"] = errRef("Resource not found")
	}
	if strings.HasPrefix(ep.Path, "/scanner-supply-chain/") {
		resp["409"] = errRef("Invalid state, conflicting idempotency key, or stale revision")
		resp["422"] = errRef("Policy or validation failure")
		if scannerIdempotentCommand(ep) || scannerVersionedCommand(ep) {
			resp["428"] = errRef("Required command precondition header is missing")
		}
	}
	if strings.HasPrefix(ep.Path, "/scanners/custom-builds") {
		resp["409"] = errRef("Invalid state, conflicting idempotency key, or stale revision")
		resp["422"] = errRef("Unsupported variant, platform, distribution, or build-mode combination")
		if ep.Method == "POST" {
			resp["428"] = errRef("Idempotency-Key or If-Match command precondition is missing")
		}
	}
	resp["429"] = errRef("Rate limited")
	return resp
}

func legacyScannerBuildEndpoint(ep Endpoint) bool {
	if ep.Method != "POST" {
		return false
	}
	return ep.Path == "/scanners/images/{variant}/build" ||
		ep.Path == "/scanners/images/build-all"
}

func legacyScannerBuildHeaders() map[string]any {
	return map[string]any{
		"Deprecation": map[string]any{
			"description": "Always true for this deprecated endpoint.",
			"schema":      map[string]any{"type": "string", "enum": []any{"true"}},
		},
		"Link": map[string]any{
			"description": "Successor-version link to /api/v1/scanners/custom-builds.",
			"schema":      map[string]any{"type": "string"},
		},
	}
}

func scannerSynchronousPost(ep Endpoint) bool {
	if ep.Method != "POST" || !strings.HasPrefix(ep.Path, "/scanner-supply-chain/") {
		return false
	}
	return ep.Path == "/scanner-supply-chain/policy/validate" ||
		ep.Path == "/scanner-supply-chain/policy/dry-run" ||
		ep.Path == "/scanner-supply-chain/release-imports" ||
		ep.Path == "/scanner-supply-chain/notifications/{id}/retry" ||
		strings.HasSuffix(ep.Path, "/restore") ||
		strings.HasSuffix(ep.Path, "/verify") ||
		strings.HasSuffix(ep.Path, "/check") ||
		strings.HasSuffix(ep.Path, "/reconcile")
}

func errRef(desc string) map[string]any {
	schema := map[string]any{"$ref": "#/components/schemas/ErrorResponse"}
	return map[string]any{
		"description": desc,
		"headers":     rateLimitHeaders(),
		"content": map[string]any{
			"application/json":         map[string]any{"schema": schema},
			"application/problem+json": map[string]any{"schema": schema},
		},
	}
}

func rateLimitHeaders() map[string]any {
	return map[string]any{
		"X-RateLimit-Limit":     map[string]any{"schema": map[string]any{"type": "integer"}, "description": "Bucket size"},
		"X-RateLimit-Remaining": map[string]any{"schema": map[string]any{"type": "integer"}, "description": "Tokens left"},
		"Retry-After":           map[string]any{"schema": map[string]any{"type": "integer"}, "description": "Seconds to wait when limited"},
		"X-Request-ID":          map[string]any{"schema": map[string]any{"type": "string"}, "description": "Correlation id"},
	}
}

func buildComponents() map[string]any {
	str := map[string]any{"type": "string"}
	strArr := map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	return map[string]any{
		"securitySchemes": map[string]any{
			"BearerAuth": map[string]any{
				"type":        "http",
				"scheme":      "bearer",
				"description": "A JWT from POST /auth/login, or a wolf_ API token from POST /auth/tokens.",
			},
		},
		"schemas": map[string]any{
			"SuccessResponse": map[string]any{
				"type":       "object",
				"properties": map[string]any{"data": map[string]any{"type": "object"}},
			},
			"ScannerPipelineImage": objSchema(map[string]any{
				"key": map[string]any{
					"type": "string", "enum": []string{
						"default", "jvm", "rust", "codeql",
						"fixer-base", "fixer-api", "fixer-claude", "fixer-codex",
					},
				},
				"kind": map[string]any{"type": "string", "enum": []string{"scanner", "fixer"}},
				"platforms": map[string]any{
					"type": "array", "minItems": 1, "maxItems": 2, "uniqueItems": true,
					"items": map[string]any{"type": "string", "enum": []string{"linux/amd64", "linux/arm64"}},
				},
				"depends_on": map[string]any{
					"type": "array", "maxItems": 1, "uniqueItems": true,
					"items": map[string]any{"type": "string", "enum": []string{"fixer-base"}},
				},
			}, "key", "kind", "platforms"),
			"ScannerReleaseImage": objSchema(map[string]any{
				"id":                 str,
				"release_id":         str,
				"image_key":          str,
				"image_kind":         map[string]any{"type": "string", "enum": []string{"scanner", "fixer"}, "default": "scanner"},
				"registry_target_id": str,
				"repository":         str,
				"digest":             map[string]any{"type": "string", "pattern": "^sha256:[a-f0-9]{64}$"},
				"platform_digests": map[string]any{
					"type": "string", "description": "Canonical JSON object mapping linux platform names to sha256 child-manifest digests.",
				},
				"size_bytes":                    map[string]any{"type": "integer", "format": "int64", "minimum": 0},
				"signature_status":              map[string]any{"type": "string", "enum": []string{"verified"}},
				"signature_digest":              map[string]any{"type": "string", "pattern": "^sha256:[a-f0-9]{64}$"},
				"signature_artifact_uri":        str,
				"signature_artifact_digest":     map[string]any{"type": "string", "pattern": "^sha256:[a-f0-9]{64}$"},
				"signature_media_type":          str,
				"signature_artifact_size_bytes": map[string]any{"type": "integer", "format": "int64", "minimum": 1},
				"signature_certificate_digest":  map[string]any{"type": "string", "pattern": "^(?:|sha256:[a-f0-9]{64})$"},
				"signature_identity":            str,
				"signature_issuer":              str,
				"signature_subject":             str,
				"signature_trust_root":          str,
				"signature_operation_id":        map[string]any{"type": "string", "pattern": "^sha256:[a-f0-9]{64}$"},
				"provenance_digest":             map[string]any{"type": "string", "pattern": "^sha256:[a-f0-9]{64}$"},
				"sbom_digest":                   map[string]any{"type": "string", "pattern": "^sha256:[a-f0-9]{64}$"},
				"created_at":                    map[string]any{"type": "string", "format": "date-time"},
			}, "image_key", "registry_target_id", "repository", "digest", "platform_digests", "signature_status",
				"signature_digest", "signature_artifact_uri", "signature_artifact_digest", "signature_media_type",
				"signature_artifact_size_bytes", "signature_identity", "signature_issuer", "signature_subject",
				"signature_trust_root", "signature_operation_id", "provenance_digest", "sbom_digest"),
			"ScannerReleaseBundleRegistryMapping": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required": []string{
					"image_key", "source_reference", "destination_reference",
					"digest", "read_back_verified",
				},
				"properties": map[string]any{
					"image_key":             map[string]any{"type": "string"},
					"source_reference":      map[string]any{"type": "string"},
					"destination_reference": map[string]any{"type": "string"},
					"digest":                map[string]any{"type": "string", "pattern": "^sha256:[a-f0-9]{64}$"},
					"read_back_verified":    map[string]any{"type": "boolean"},
				},
			},
			"ScannerReleaseBundleImportResult": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required": []string{
					"release_id", "manifest_digest", "bundle_digest",
					"bundle_size_bytes", "bundle_uri", "created",
					"integrity_verified", "signature_status",
					"external_signatures_verified", "bundle_schema",
					"oci_closure_verified", "network_mode",
					"destination_read_back_verified",
				},
				"properties": map[string]any{
					"release_id":        map[string]any{"type": "string"},
					"manifest_digest":   map[string]any{"type": "string", "pattern": "^sha256:[a-f0-9]{64}$"},
					"bundle_digest":     map[string]any{"type": "string", "pattern": "^sha256:[a-f0-9]{64}$"},
					"bundle_size_bytes": map[string]any{"type": "integer", "format": "int64", "minimum": 1},
					"bundle_uri":        map[string]any{"type": "string"},
					"created":           map[string]any{"type": "boolean"},
					"integrity_verified": map[string]any{
						"type": "boolean",
					},
					"signature_status":             map[string]any{"type": "string"},
					"signature_key_id":             map[string]any{"type": "string"},
					"external_signatures_verified": map[string]any{"type": "boolean"},
					"bundle_schema":                map[string]any{"type": "string", "enum": []string{"wolf.scanner-release-bundle/v1", "wolf.scanner-release-bundle/v2"}},
					"oci_closure_verified":         map[string]any{"type": "boolean"},
					"network_mode":                 map[string]any{"type": "string", "enum": []string{"no-network", "registry-enabled"}},
					"destination_read_back_verified": map[string]any{
						"type": "boolean",
					},
					"registry_mappings": map[string]any{
						"type":  "array",
						"items": map[string]any{"$ref": "#/components/schemas/ScannerReleaseBundleRegistryMapping"},
					},
				},
			},
			"ScannerReleaseBundleImportResponse": map[string]any{
				"type":     "object",
				"required": []string{"data"},
				"properties": map[string]any{
					"data": map[string]any{"$ref": "#/components/schemas/ScannerReleaseBundleImportResult"},
				},
			},
			"ScannerArtifactDiff": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required": []string{
					"owner_type", "owner_id", "kind", "format", "available",
					"content", "truncated", "total_bytes", "returned_bytes",
					"total_lines", "returned_lines",
				},
				"properties": map[string]any{
					"owner_type": map[string]any{"type": "string", "enum": []string{"candidate", "release"}},
					"owner_id":   map[string]any{"type": "string"},
					"kind":       map[string]any{"type": "string", "enum": []string{"manifest", "lock"}},
					"format":     map[string]any{"type": "string", "enum": []string{"unified"}},
					"available":  map[string]any{"type": "boolean"},
					"content": map[string]any{
						"type":      "string",
						"maxLength": maxScannerArtifactDiffResponseCharacters,
					},
					"truncated":      map[string]any{"type": "boolean"},
					"total_bytes":    map[string]any{"type": "integer", "format": "int64", "minimum": 0},
					"returned_bytes": map[string]any{"type": "integer", "minimum": 0, "maximum": maxScannerArtifactDiffResponseCharacters},
					"total_lines":    map[string]any{"type": "integer", "minimum": 0},
					"returned_lines": map[string]any{"type": "integer", "minimum": 0},
					"digest":         map[string]any{"type": "string"},
					"media_type":     map[string]any{"type": "string"},
				},
			},
			"ScannerArtifactDiffResponse": map[string]any{
				"type":       "object",
				"required":   []string{"data"},
				"properties": map[string]any{"data": map[string]any{"$ref": "#/components/schemas/ScannerArtifactDiff"}},
			},
			"ScannerReleaseCapabilities": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required":             []string{"mode", "read", "candidates", "canary", "stable_control"},
				"description":          "Effective cumulative scanner release-management capabilities. The server enforces the same stage on every route; clients should use these flags to disable unavailable actions.",
				"properties": map[string]any{
					"mode": map[string]any{
						"type": "string",
						"enum": []string{"disabled", "read_only", "candidate", "canary", "stable_control"},
					},
					"read":           map[string]any{"type": "boolean"},
					"candidates":     map[string]any{"type": "boolean"},
					"canary":         map[string]any{"type": "boolean"},
					"stable_control": map[string]any{"type": "boolean"},
				},
				"example": map[string]any{
					"mode": "read_only", "read": true, "candidates": false,
					"canary": false, "stable_control": false,
				},
			},
			"ScannerSupplyChainOverviewResponse": map[string]any{
				"type":     "object",
				"required": []string{"data"},
				"properties": map[string]any{
					"data": map[string]any{
						"type":                 "object",
						"required":             []string{"capabilities"},
						"additionalProperties": true,
						"properties": map[string]any{
							"capabilities": map[string]any{"$ref": "#/components/schemas/ScannerReleaseCapabilities"},
							"active_release": map[string]any{
								"type": "object", "nullable": true, "additionalProperties": true,
							},
							"pending_candidate": map[string]any{
								"type": "object", "nullable": true, "additionalProperties": true,
							},
							"active_rollout": map[string]any{
								"type": "object", "nullable": true, "additionalProperties": true,
							},
							"freshness":       map[string]any{"type": "object", "additionalProperties": true},
							"worker_health":   map[string]any{"type": "object", "additionalProperties": true},
							"registry_health": map[string]any{"type": "object", "additionalProperties": true},
							"alerts": map[string]any{
								"type": "object", "additionalProperties": false,
								"properties": map[string]any{
									"open_warning":  map[string]any{"type": "integer", "minimum": 0},
									"open_critical": map[string]any{"type": "integer", "minimum": 0},
									"resolved":      map[string]any{"type": "integer", "minimum": 0},
								},
							},
							"alert_health": map[string]any{
								"type": "string", "enum": []string{"healthy", "warning", "critical"},
							},
							"generated_at": map[string]any{"type": "string", "format": "date-time"},
						},
					},
				},
				"example": map[string]any{
					"data": map[string]any{
						"capabilities": map[string]any{
							"mode": "candidate", "read": true, "candidates": true,
							"canary": false, "stable_control": false,
						},
						"freshness": map[string]any{
							"status": "current", "current": 31, "updates_available": 2,
						},
						"generated_at": "2026-07-30T12:00:00Z",
					},
				},
			},
			"ScannerSyntheticHealth": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required": []string{
					"corpus_id", "corpus_digest", "current", "state",
					"fixture_total", "fixture_passed", "fixture_failed", "observed_at",
				},
				"properties": map[string]any{
					"corpus_id":      str,
					"corpus_digest":  map[string]any{"type": "string", "pattern": "^sha256:[a-f0-9]{64}$"},
					"current":        map[string]any{"type": "boolean"},
					"state":          map[string]any{"type": "string", "enum": []string{"pending", "passed", "failed"}},
					"fixture_total":  map[string]any{"type": "integer", "minimum": 0},
					"fixture_passed": map[string]any{"type": "integer", "minimum": 0},
					"fixture_failed": map[string]any{"type": "integer", "minimum": 0},
					"failure_class": map[string]any{
						"type": "string", "enum": []string{
							"signature", "manifest", "pull", "parser",
							"finding_loss", "crash_loop", "infrastructure",
						},
					},
					"observed_at": map[string]any{"type": "string", "format": "date-time"},
				},
			},
			"ScannerRealScanHealth": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required": []string{
					"state", "candidate_samples", "stable_samples",
					"candidate_infrastructure_failures", "stable_infrastructure_failures",
					"parser_failures", "expected_finding_losses",
					"candidate_p95_duration_ms", "stable_p95_duration_ms",
					"workers_total", "workers_ready", "workers_failed", "observed_at",
				},
				"properties": map[string]any{
					"state":                             map[string]any{"type": "string", "enum": []string{"pending", "healthy", "degraded"}},
					"candidate_samples":                 map[string]any{"type": "integer", "minimum": 0},
					"stable_samples":                    map[string]any{"type": "integer", "minimum": 0},
					"candidate_infrastructure_failures": map[string]any{"type": "integer", "minimum": 0},
					"stable_infrastructure_failures":    map[string]any{"type": "integer", "minimum": 0},
					"parser_failures":                   map[string]any{"type": "integer", "minimum": 0},
					"expected_finding_losses":           map[string]any{"type": "integer", "minimum": 0},
					"candidate_p95_duration_ms":         map[string]any{"type": "integer", "format": "int64", "minimum": 0},
					"stable_p95_duration_ms":            map[string]any{"type": "integer", "format": "int64", "minimum": 0},
					"workers_total":                     map[string]any{"type": "integer", "minimum": 0},
					"workers_ready":                     map[string]any{"type": "integer", "minimum": 0},
					"workers_failed":                    map[string]any{"type": "integer", "minimum": 0},
					"observed_at":                       map[string]any{"type": "string", "format": "date-time"},
				},
			},
			"ScannerRolloutDetailResponse": map[string]any{
				"type":     "object",
				"required": []string{"data"},
				"properties": map[string]any{
					"data": map[string]any{
						"type":                 "object",
						"additionalProperties": true,
						"required":             []string{"rollout", "cohorts", "events", "affected_workers", "recommendation"},
						"properties": map[string]any{
							"rollout": map[string]any{"type": "object", "additionalProperties": true},
							"cohorts": map[string]any{
								"type": "array", "items": map[string]any{"type": "object", "additionalProperties": true},
							},
							"events": map[string]any{
								"type": "array", "items": map[string]any{"type": "object", "additionalProperties": true},
							},
							"health": map[string]any{
								"type": "object", "nullable": true, "additionalProperties": false,
							},
							"synthetic_health": map[string]any{
								"allOf":    []any{map[string]any{"$ref": "#/components/schemas/ScannerSyntheticHealth"}},
								"nullable": true,
							},
							"real_scan_health": map[string]any{
								"allOf":    []any{map[string]any{"$ref": "#/components/schemas/ScannerRealScanHealth"}},
								"nullable": true,
							},
							"affected_workers": map[string]any{"type": "integer", "minimum": 0},
							"recommendation":   str,
						},
					},
				},
			},
			"ScannerAlert": map[string]any{
				"type": "object",
				"required": []string{
					"id", "fingerprint", "kind", "severity", "state",
					"scope_type", "scope_id", "summary", "evidence",
					"trigger_count", "generation", "version",
					"first_triggered_at", "last_triggered_at",
				},
				"properties": map[string]any{
					"id":          str,
					"fingerprint": str,
					"kind": map[string]any{
						"type": "string", "enum": []string{
							"missed_discovery", "stale_stable_release", "queue_backlog",
							"lease_churn", "repeated_gate_failure", "mirror_drift",
							"rollout_failure", "signature_health",
						},
					},
					"severity": map[string]any{
						"type": "string", "enum": []string{"warning", "critical"},
					},
					"state": map[string]any{
						"type": "string", "enum": []string{"open", "resolved"},
					},
					"scope_type":         str,
					"scope_id":           str,
					"summary":            str,
					"evidence":           map[string]any{"type": "object", "additionalProperties": true},
					"policy_id":          str,
					"policy_scope":       str,
					"policy_revision":    map[string]any{"type": "integer", "format": "int64"},
					"trigger_count":      map[string]any{"type": "integer", "minimum": 1},
					"generation":         map[string]any{"type": "integer", "minimum": 1},
					"version":            map[string]any{"type": "integer", "format": "int64", "minimum": 1},
					"first_triggered_at": map[string]any{"type": "string", "format": "date-time"},
					"last_triggered_at":  map[string]any{"type": "string", "format": "date-time"},
					"resolved_at":        map[string]any{"type": "string", "format": "date-time", "nullable": true},
					"created_at":         map[string]any{"type": "string", "format": "date-time"},
					"updated_at":         map[string]any{"type": "string", "format": "date-time"},
				},
			},
			"ScannerAlertResponse": map[string]any{
				"type":     "object",
				"required": []string{"data"},
				"properties": map[string]any{
					"data": map[string]any{"$ref": "#/components/schemas/ScannerAlert"},
				},
			},
			"ScannerAlertListResponse": map[string]any{
				"type":     "object",
				"required": []string{"data", "meta"},
				"properties": map[string]any{
					"data": map[string]any{
						"type": "array", "items": map[string]any{
							"$ref": "#/components/schemas/ScannerAlert",
						},
					},
					"meta": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"next_cursor": str,
						},
					},
				},
			},
			"ListResponse": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"data": map[string]any{"type": "array", "items": map[string]any{"type": "object"}},
					"meta": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"total":    map[string]any{"type": "integer"},
							"page":     map[string]any{"type": "integer"},
							"per_page": map[string]any{"type": "integer"},
						},
					},
				},
			},
			"ErrorResponse": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"error": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"code":    str,
							"message": str,
						},
					},
					"type":   str,
					"title":  str,
					"status": map[string]any{"type": "integer"},
					"detail": str,
				},
			},
			"RegisterRequest": objSchema(map[string]any{"email": str, "password": str}, "email", "password"),
			"LoginRequest":    objSchema(map[string]any{"email": str, "password": str}, "email", "password"),
			"ChangePasswordRequest": objSchema(map[string]any{
				"current_password": str, "new_password": str,
			}, "current_password", "new_password"),
			"CreateTokenRequest": objSchema(map[string]any{
				"name":            str,
				"scopes":          strArr,
				"expires_in_days": map[string]any{"type": "integer", "description": "Omit for the 90-day default; 0 means never expires."},
			}, "name", "scopes"),
			"CreateUserRequest": objSchema(map[string]any{
				"email":    str,
				"password": str,
				"role": map[string]any{
					"type": "string", "enum": []string{"user", "admin"}, "default": "user",
				},
				"scanner_supply_chain_personas": scannerPersonaArraySchema(),
			}, "email"),
			"UserScannerSupplyChainAccessRequest": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required":             []string{"personas"},
				"properties": map[string]any{
					"personas": scannerPersonaArraySchema(),
				},
			},
			"CreateRepoRequest": objSchema(map[string]any{
				"name": str, "source_type": str, "source_path": str, "remote_node_id": str, "remote_path": str, "default_branch": str,
			}, "name", "source_path"),
			"UpdateRepoRequest": objSchema(map[string]any{"name": str, "branch": str}),
			"RemoteNodeRequest": objSchema(map[string]any{
				"name": str, "host": str, "port": map[string]any{"type": "integer"}, "username": str,
				"auth_type": str, "credential_secret_id": str, "known_hosts": str, "base_path": str,
				"enabled": map[string]any{"type": "boolean"},
			}, "name", "host", "username"),
			"CreateCollectionRequest": objSchema(map[string]any{"name": str}, "name"),
			"CollectionRepoRequest":   objSchema(map[string]any{"repo_id": str}, "repo_id"),
			"CreateScanRequest": objSchema(map[string]any{
				"repo_id": str,
				"source": objSchema(map[string]any{
					"kind": str, "name": str, "url": str, "ref": str, "credential_id": str,
					"node_id": str, "path": str, "host": str, "port": map[string]any{"type": "integer"},
					"username": str, "base_path": str, "known_hosts": str,
				}, "kind"),
				"collection_id": str, "branch": str,
				"profile":    map[string]any{"type": "string", "enum": []string{"standard", "full", "targeted", "fast", "pr", "release"}},
				"categories": strArr, "tools": strArr, "disabled_tools": strArr,
				"include_paths": strArr, "exclude_paths": strArr, "client_reference": str,
				"ai_enabled": map[string]any{"type": "boolean"}, "ai_engine": str, "ai_model": str,
			}),
			"PreflightScanRequest": objSchema(map[string]any{
				"repo_id":    str,
				"profile":    map[string]any{"type": "string", "enum": []string{"standard", "full", "targeted", "fast", "pr", "release"}},
				"categories": strArr, "tools": strArr, "all_scanners": map[string]any{"type": "boolean"},
				"disabled_tools": strArr, "include_paths": strArr, "exclude_paths": strArr,
			}, "repo_id"),
			"ReleaseRescanRequest": objSchema(map[string]any{
				"release_id": str, "reason": str,
			}, "release_id", "reason"),
			"CreateCredentialRequest": objSchema(map[string]any{
				"type": str, "name": str, "secret": str, "username": str,
				"known_hosts": str, "allowed_hosts": strArr,
			}, "type", "name", "secret", "allowed_hosts"),
			"LicenseBlobRequest":   objSchema(map[string]any{"license": str}, "license"),
			"FindingStatusRequest": objSchema(map[string]any{"status": str}, "status"),
			"BulkUpdateFindingsRequest": objSchema(map[string]any{
				"ids":    strArr,
				"status": str,
				"suppress": objSchema(map[string]any{
					"reason": str,
				}),
			}, "ids"),
			"SplitVulnerabilityRequest": objSchema(map[string]any{"finding_ids": strArr}, "finding_ids"),
			"MergeVulnerabilityRequest": objSchema(map[string]any{"vulnerability_id": str}, "vulnerability_id"),
			"InvestigateRequest":        objSchema(map[string]any{"question": str}),
			"VerifyRequest":             objSchema(map[string]any{"environment": str, "class": str}),
			"CreateFixRequest": objSchema(map[string]any{
				"repo_id": str, "scan_id": str, "finding_ids": strArr,
				"target_branch": str, "engine": str, "mode": str,
				"severity_floor":    str,
				"max_attempts":      map[string]any{"type": "integer"},
				"max_loops":         map[string]any{"type": "integer"},
				"human_in_the_loop": map[string]any{"type": "boolean"},
				"model":             str,
				"effort":            str,
				"variant":           str,
			}, "repo_id"),
			"ResumeFixRequest": objSchema(map[string]any{"action": str}),
			"CreateFixerConsoleRequest": objSchema(map[string]any{
				"kind": str, "engine": str,
			}),
			"FixerConsoleInputRequest": objSchema(map[string]any{"data": str}, "data"),
			"CreateSecretRequest": objSchema(map[string]any{
				"key_type": str, "key_name": str, "value": str,
			}, "key_name", "value"),
			"ListOrgReposRequest": objSchema(map[string]any{
				"org": str, "secret_id": str,
			}),
			"DiscoverReposRequest": objSchema(map[string]any{
				"base_path": str,
			}),
			"ScannerPolicySchedule": scannerPolicyScheduleSchema(),
			"ScannerPolicyRequest": objSchema(map[string]any{
				"schedule": map[string]any{"$ref": "#/components/schemas/ScannerPolicySchedule"},
				"rules":    map[string]any{"type": "object", "additionalProperties": true},
				"reason":   str,
			}, "schedule", "rules"),
			"ScannerCustomBuildRequest": objSchema(map[string]any{
				"variants": map[string]any{
					"type":     "array",
					"minItems": 1,
					"maxItems": 4,
					"items": map[string]any{
						"type": "string", "enum": []string{"default", "jvm", "rust", "codeql", "all"},
					},
					"description": "Fixed embedded scanner contexts only. `all` must be the sole value.",
				},
				"push": map[string]any{
					"type":        "boolean",
					"description": "Publish to the configured registry.",
				},
				"platforms": map[string]any{
					"type":     "array",
					"maxItems": 2,
					"items": map[string]any{
						"type": "string", "enum": []string{"linux/amd64", "linux/arm64"},
					},
					"description": "Local load supports at most one platform; multi-platform builds require push.",
				},
				"namespace": map[string]any{
					"type": "string", "maxLength": 255, "pattern": `^[a-z0-9._-]+$`,
				},
				"credential_secret_id": map[string]any{
					"type":        "string",
					"maxLength":   255,
					"description": "Opaque DockerHub-token secret ID. Required for push and never returned by the API.",
				},
				"reason": map[string]any{"type": "string", "minLength": 1, "maxLength": 2048},
			}, "variants", "push", "reason"),
			"ScannerPolicyDryRunRequest": objSchema(map[string]any{
				"candidate_id": str,
				"schedule":     map[string]any{"$ref": "#/components/schemas/ScannerPolicySchedule"},
				"rules":        map[string]any{"type": "object", "additionalProperties": true},
			}, "candidate_id", "schedule", "rules"),
			"ScannerDiscoveryRequest": objSchema(map[string]any{
				"scope": map[string]any{
					"type": "object", "properties": map[string]any{
						"type":  map[string]any{"type": "string", "enum": []string{"all", "selected"}},
						"tools": strArr, "components": strArr,
					},
				},
				"reason": str, "definition_commit": str,
			}, "scope", "reason"),
			"ScannerCandidateRequest": objSchema(map[string]any{
				"discovery_run_id": str, "definition_commit": str, "proposed_commit": str,
				"proposal_url": str, "lock_digest": str, "lock_uri": str,
				"risk_summary":   map[string]any{"type": "object", "additionalProperties": true},
				"selected_items": strArr,
				"reason":         map[string]any{"type": "string", "minLength": 1, "maxLength": 2048},
				"images": map[string]any{
					"type": "array", "minItems": 8, "maxItems": 8, "uniqueItems": true,
					"items":       map[string]any{"$ref": "#/components/schemas/ScannerPipelineImage"},
					"description": "Optional exact complete image set. When omitted, the server uses the canonical four scanner and four fixer artifacts.",
				},
			}, "reason"),
			"ScannerApprovalRequest": objSchema(map[string]any{
				"lock_digest": str, "policy_decision_digest": str, "evidence_digest": str,
				"decision": str, "reason": str,
			}, "reason"),
			"ScannerExceptionRequest": objSchema(map[string]any{
				"gate": str, "owner_id": str, "reason": str,
				"compensating_control": str,
				"evidence_digest": map[string]any{
					"type": "string", "pattern": "^sha256:[a-f0-9]{64}$",
				},
				"expires_at": map[string]any{"type": "string", "format": "date-time"},
			}, "gate", "owner_id", "reason", "compensating_control", "evidence_digest", "expires_at"),
			"ScannerPublicationRequest": objSchema(map[string]any{
				"name": map[string]any{
					"type":        "string",
					"pattern":     `^scanner-set-[0-9]{4}\.[0-9]{2}\.[1-9][0-9]*$`,
					"description": "Optional compatibility override. Omit to reserve the next scanner-set-YYYY.WW.N name transactionally.",
				},
				"receipt_digest": map[string]any{
					"type": "string", "pattern": "^sha256:[a-f0-9]{64}$",
					"description": "Exact server-verified publication receipt from the completed candidate build.",
				},
				"reason": str,
			}, "receipt_digest", "reason"),
			"ScannerPromotionRequest": objSchema(map[string]any{
				"target": str, "strategy": str, "reason": str,
			}, "target", "reason"),
			"ScannerReasonRequest": objSchema(map[string]any{
				"reason": str, "impact_policy": str,
			}, "reason"),
			"ScannerRegistryRequest": objSchema(map[string]any{
				"name": str, "type": map[string]any{
					"type": "string", "enum": []string{"managed", "mirror", "private", "air_gap"},
				},
				"host": str, "namespace": str,
				"secret_reference": map[string]any{
					"type": "string", "writeOnly": true,
					"pattern":     `^secret:[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`,
					"description": "Opaque reference to an existing Wolf secret. Plaintext credentials and credential-bearing URLs are rejected and the reference is never returned.",
				},
				"trust_policy_reference": str,
				"platform_policy":        map[string]any{"type": "object", "additionalProperties": true},
				"enabled":                map[string]any{"type": "boolean"},
			}),
			"ScannerRegistryReconcileRequest": objSchema(map[string]any{
				"release_id": str,
			}, "release_id"),
			"ScannerRegistryJobRequest": objSchema(map[string]any{
				"kind": map[string]any{
					"type": "string", "enum": []string{"reconcile", "repair"},
				},
				"release_id": str, "source_registry_id": str,
				"re_sign_policy": map[string]any{
					"type": "string", "enum": []string{"preserve", "required", "forbidden"},
				},
				"reason":       str,
				"max_attempts": map[string]any{"type": "integer", "minimum": 1, "maximum": 20},
			}, "kind", "release_id", "reason"),
			"ScannerSignerRequest": objSchema(map[string]any{
				"name": str,
				"provider": map[string]any{
					"type": "string",
					"enum": []string{"aws_kms", "gcp_kms", "azure_key_vault", "pkcs11", "keyless", "offline"},
				},
				"algorithm": map[string]any{
					"type": "string",
					"enum": []string{"ed25519", "ecdsa-p256-sha256", "rsa-pss-sha256", "cosign-keyless"},
				},
				"key_reference": str, "secret_reference": str,
				"workload_identity": map[string]any{"type": "boolean"},
				"identity":          str, "issuer": str, "subject": str,
				"trust_root_reference": str,
			}, "name", "provider", "algorithm", "key_reference", "identity", "issuer", "subject", "trust_root_reference"),
			"LegacyReleaseImportRequest": objSchema(map[string]any{
				"reason": str,
				"resolved_digests": map[string]any{
					"type": "object", "additionalProperties": map[string]any{
						"type": "string", "pattern": `^sha256:[a-f0-9]{64}$`,
					},
					"description": "Image-key to immutable digest mapping required only for configured tag references. Keys are default, wolf-<tool>, and upstream-<tool>.",
				},
			}, "reason"),
		},
	}
}

func objSchema(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		req := make([]any, len(required))
		for i, r := range required {
			req[i] = r
		}
		s["required"] = req
	}
	return s
}

func scannerPersonaArraySchema() map[string]any {
	return map[string]any{
		"type":        "array",
		"uniqueItems": true,
		"maxItems":    6,
		"description": "Server-owned scanner access presets. Operator, approver, and registry administrator are composable; viewer and auditor are standalone; an empty list becomes viewer; supply_chain_administrator is exclusive and implies all scanner scopes.",
		"items": map[string]any{
			"type": "string",
			"enum": []string{
				"viewer",
				"scanner_operator",
				"release_approver",
				"registry_administrator",
				"supply_chain_administrator",
				"auditor",
			},
		},
	}
}

func scannerPolicyScheduleSchema() map[string]any {
	periodic := func(frequency string) map[string]any {
		return map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"enabled":   map[string]any{"type": "boolean", "default": true},
				"frequency": map[string]any{"type": "string", "enum": []string{frequency}},
				"at": map[string]any{
					"type": "string", "pattern": `^(?:[01][0-9]|2[0-3]):[0-5][0-9]$`,
				},
				"jitter": map[string]any{
					"type": "string", "minLength": 1, "maxLength": 64,
					"description": "Non-negative Go duration below 24h.",
				},
				"catch_up": map[string]any{
					"type": "string", "minLength": 1, "maxLength": 64,
					"description": "Positive Go duration no greater than 168h.",
				},
			},
		}
	}
	daily := periodic("daily")
	weekly := periodic("weekly")
	weeklyProps, ok := weekly["properties"].(map[string]any)
	if !ok {
		weeklyProps = map[string]any{}
		weekly["properties"] = weeklyProps
	}
	weeklyProps["weekday"] = map[string]any{
		"type": "string",
		"enum": []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"},
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"timezone": map[string]any{
				"type": "string", "minLength": 1, "maxLength": 255,
				"description": "IANA timezone evaluated by the server, for example America/New_York.",
			},
			"daily_discovery":  daily,
			"weekly_candidate": weekly,
			"maximum_stable_image_age": map[string]any{
				"type": "string", "minLength": 1, "maxLength": 64, "default": "168h0m0s",
				"description": "Positive Go duration no greater than 8760h. A candidate is forced when the stable image is older.",
			},
			"force_weekly_rebuild": map[string]any{
				"type": "boolean", "default": false,
				"description": "Build the weekly candidate even when scanner definitions are unchanged.",
			},
			"maintenance_windows": map[string]any{
				"type": "array", "maxItems": 32,
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []string{"name", "cron", "duration"},
					"properties": map[string]any{
						"id":   map[string]any{"type": "string", "maxLength": 255},
						"name": map[string]any{"type": "string", "minLength": 1, "maxLength": 255},
						"cron": map[string]any{
							"type": "string", "minLength": 9, "maxLength": 255,
							"description": "Five-field cron expression evaluated in the policy timezone.",
						},
						"duration": map[string]any{
							"type": "string", "minLength": 1, "maxLength": 64,
							"description": "Positive Go duration no greater than 168h.",
						},
					},
				},
			},
		},
	}
}
