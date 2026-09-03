package api

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/api/middleware"
	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/api/routes"
	"github.com/alphabravocompany/thewolf/internal/api/sse"
	"github.com/alphabravocompany/thewolf/internal/artifacts"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/auth/apikey"
	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/scannerfeature"
	"github.com/alphabravocompany/thewolf/internal/scannerobservability"
	"github.com/alphabravocompany/thewolf/internal/wolflog"
	"github.com/alphabravocompany/thewolf/pkg/edition"
)

// Re-export response types for backward compatibility.
type SuccessResponse = response.SuccessResponse
type ListMeta = response.ListMeta
type ListResponse = response.ListResponse
type ErrorDetail = response.ErrorDetail
type ErrorResponse = response.ErrorResponse

// WriteJSON writes a JSON response.
var WriteJSON = response.WriteJSON

// WriteError writes an error response.
var WriteError = response.WriteError

// Server holds the HTTP server configuration.
type Server struct {
	Router     *chi.Mux
	Store      db.Store
	Addr       string
	httpServer *http.Server
}

// NewServer creates a new API server with all middleware and routes.
func NewServer(store db.Store, addr string) *Server {
	// Initialize route handler with store and plugin registry
	routes.SetHandler(store, plugin.Global)
	scannerobservability.Default.SetDatabaseCheck(store.Ping)
	scannerobservability.Default.SetMaintenanceCheck(func(ctx context.Context) (bool, error) {
		status, err := store.ScannerReleases().GetReleaseMaintenanceStatus(ctx)
		if err != nil {
			return false, err
		}
		return status.RestoreActive(time.Now()), nil
	})

	// Per-request role resolution for RBAC (admin | user). Looked up from the
	// store so role changes apply immediately without re-issuing tokens.
	auth.RoleResolver = func(ctx context.Context, userID string) string {
		if u, err := store.GetUserByID(ctx, userID); err == nil && u != nil {
			return u.Role
		}
		return ""
	}
	auth.HumanAuthorizationResolver = func(ctx context.Context, userID string) (auth.HumanAuthorization, error) {
		u, err := store.GetUserByID(ctx, userID)
		if err != nil || u == nil {
			return auth.HumanAuthorization{}, err
		}
		personas, decodeErr := apikey.DecodeScannerPersonas(u.ScannerSupplyChainPersonas)
		if decodeErr != nil {
			personas = []string{apikey.ScannerPersonaViewer}
		}
		return auth.HumanAuthorization{Role: u.Role, ScannerPersonas: personas}, nil
	}

	// Folder-model invariant: assign any pre-existing orphan repos to their
	// owner's Default collection so none are unreachable now that the nav is
	// browsed via collections. Idempotent; a warning on failure is non-fatal.
	if err := routes.BackfillRepoCollections(context.Background(), store); err != nil {
		wolflog.L().Warn().Err(err).Msg("repo-collection backfill failed")
	}

	// Initialize SSE broker so scan events are broadcast to connected clients.
	routes.SSEBroker = sse.NewBroker()

	// Initialize artifact store at ~/.wolf/artifacts/ for durable scan output storage.
	if artifacts.Global == nil {
		home, _ := os.UserHomeDir()
		_ = artifacts.Init(filepath.Join(home, ".wolf", "artifacts")) // #nosec G104 -- intentional: response/log write errors are not actionable here
	}
	runArtifactRetentionCleanup(store)

	r := chi.NewRouter()

	// Rate limiters
	generalLimiter := middleware.DefaultRateLimiter()
	authLimiter := middleware.StrictRateLimiter()

	// Middleware chain
	r.Use(chimw.RequestID)
	r.Use(middleware.ScannerOperationTrace)
	r.Use(chimw.RealIP)
	r.Use(chimw.Logger)
	r.Use(chimw.Recoverer)
	// 15-minute global timeout. Long enough for the legitimate slow
	// operations — bulk scanner-image pulls (24 images × multi-MB each
	// = several minutes), doctor diagnostics, scan progress streaming
	// (SSE keeps the conn alive). Fast handlers complete in ms either
	// way; this is the safety-net upper bound, not the expected case.
	r.Use(chimw.Timeout(15 * time.Minute))
	r.Use(middleware.MaxBodySizeForRequest(1<<20, func(r *http.Request) int64 {
		if r.Method == http.MethodPost &&
			r.URL.Path == "/api/v1/scanner-supply-chain/release-imports" {
			return routes.ScannerReleaseBundleMaxUploadBytes
		}
		return 0
	})) // 1 MB by default; the streaming bundle import has its own 8 GiB bound.
	r.Use(cors.Handler(cors.Options{
		AllowOriginFunc: allowedCORSOrigin,
		AllowedMethods:  []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders: []string{
			"Accept", "Authorization", "Content-Type", "Idempotency-Key",
			"If-Match", "Last-Event-ID", "Traceparent", "X-Wolf-Import-Reason",
			"X-Wolf-Operation-ID",
		},
		ExposedHeaders: []string{
			"Deprecation", "ETag", "Link", "Retry-After",
			"X-RateLimit-Limit", "X-RateLimit-Remaining", "X-Request-ID",
			"X-Wolf-Release-ID", "X-Wolf-Manifest-Digest", "X-Wolf-Bundle-Digest",
			"X-Wolf-Bundle-Signature-Status", "Traceparent",
			"X-Wolf-Operation-ID", "X-Wolf-Trace-ID",
		},
		AllowCredentials: true,
		MaxAge:           300,
	}))
	// JSON content-type middleware: scoped to /api/* only so the SPA's
	// HTML/CSS/JS assets keep their correct types.
	jsonContentType := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			next.ServeHTTP(w, r)
		})
	}

	// API-token authentication: wire the resolver and the audit recorder.
	auth.SetAPITokenResolver(makeAPITokenResolver(store))
	auth.SetSessionResolver(makeSessionResolver(store))
	tokenLimiter := middleware.TokenRateLimiter()
	auditRecorder := makeAuditRecorder(store)
	// Let handlers emit explicit audit events (e.g. login success/failure on the
	// public auth group, which the mutation middleware never sees). Non-blocking.
	routes.AuditSink = func(e models.AuditLogEntry) {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := store.AppendAuditLog(ctx, &e); err != nil {
				wolflog.Warn().Err(err).Msg("audit log append failed")
			}
		}()
	}

	// Mount the versioned API. All routes live under /api/v1; /api/* is a
	// deprecating redirect alias kept for one release.
	r.Route("/api/v1", func(r chi.Router) {
		r.Use(generalLimiter.Handler)
		r.Use(jsonContentType)

		// Public endpoints — no authentication.
		r.Group(func(r chi.Router) {
			r.Get("/health", routes.Health)
			r.Get("/ready", routes.Ready)
			r.Get("/metrics", routes.Metrics)
			r.Get("/version", routes.Version)
			r.Get("/edition", routes.GetEdition)
			r.Get("/license", routes.GetLicense)
			r.Get("/coverage", routes.GetCoverage)
			r.Get("/scan-profiles", routes.ListScanProfiles)
			r.Get("/capabilities/{name}", routes.GetCapability)
			r.Get("/mcp/status", routes.MCPStatus)
			r.Post("/webhooks/github", routes.GitHubWebhook)
			r.Get("/webhooks/events", routes.ListWebhookEvents)
			r.Get("/scim/v2/Users", routes.SCIMUnavailable)
			r.Post("/scim/v2/Users", routes.SCIMUnavailable)
			r.Get("/scim/v2/Users/{id}", routes.SCIMUnavailable)
			r.Get("/scim/v2/Groups", routes.SCIMUnavailable)
			r.Post("/scim/v2/Groups", routes.SCIMUnavailable)
		})

		// OpenAPI spec + Swagger UI — public by design.
		mountDocs(r)

		// Auth endpoints with stricter rate limiting.
		r.Group(func(r chi.Router) {
			r.Use(authLimiter.Handler)
			r.Get("/auth/settings", routes.AuthSettings)
			r.Get("/auth/providers", routes.AuthProviders)
			r.Get("/auth/sso/{name}/start", routes.StartSSO)
			r.Get("/auth/sso/{name}/callback", routes.SSOCallback)
			r.Post("/auth/register", routes.Register)
			r.Post("/auth/login", routes.Login)
			// Step two of a 2FA login: exchange the challenge token + code for
			// a session. Public — the user has no session yet.
			r.Post("/auth/mfa/login", routes.MFALogin)
		})

		// Protected endpoints. Scope vocabulary is defined in
		// internal/auth/apikey; JWT (UI) sessions hold every scope.
		r.Group(func(r chi.Router) {
			r.Use(auth.Middleware)
			r.Use(tokenLimiter.HandlerForToken)
			r.Use(middleware.Audit(auditRecorder))
			// When the org mandates MFA, confine not-yet-enrolled sessions to
			// the enrollment endpoints. No-op when MFA isn't required.
			r.Use(routes.MFAEnrollmentGuard)

			r.With(auth.RequireScope(apikey.ScopeReadRepos)).Post("/mcp", routes.HandleMCP)

			rRepos := auth.RequireScope(apikey.ScopeReadRepos)
			wRepos := auth.RequireScope(apikey.ScopeWriteRepos)
			rScans := auth.RequireScope(apikey.ScopeReadScans)
			wScans := auth.RequireScope(apikey.ScopeWriteScans)
			rFind := auth.RequireScope(apikey.ScopeReadFindings)
			wFind := auth.RequireScope(apikey.ScopeWriteFindings)
			rFixes := auth.RequireScope(apikey.ScopeReadFixes)
			wFixes := auth.RequireScope(apikey.ScopeWriteFixes)
			rConfig := auth.RequireScope(apikey.ScopeReadConfig)
			wConfig := auth.RequireScope(apikey.ScopeWriteConfig)
			rCredentials := auth.RequireScope(apikey.ScopeReadCredentials)
			wCredentials := auth.RequireScope(apikey.ScopeWriteCredentials)
			readScannerSupplyChain := auth.RequireScope(apikey.ScopeReadScannerSupplyChain)
			operateScannerSupplyChain := auth.RequireScope(apikey.ScopeOperateScannerSupplyChain)
			approveScannerReleases := auth.RequireScope(apikey.ScopeApproveScannerReleases)
			manageScannerRegistries := auth.RequireScope(apikey.ScopeManageScannerRegistries)
			adminScannerSupplyChain := auth.RequireScope(apikey.ScopeAdminScannerSupplyChain)
			readScannerReleaseMode := requireScannerReleaseCapability(scannerfeature.CapabilityRead)
			candidateScannerReleaseMode := requireScannerReleaseCapability(scannerfeature.CapabilityCandidate)
			canaryScannerReleaseMode := requireScannerReleaseCapability(scannerfeature.CapabilityCanary)
			stableScannerReleaseMode := requireScannerReleaseCapability(scannerfeature.CapabilityStable)
			adminScope := auth.RequireScope(apikey.ScopeAdmin)
			// adminOnly gates the human admin surface by ROLE. It is chained
			// WITH the relevant scope (defense in depth): an admin endpoint
			// needs both the admin role AND the write/admin scope, so a
			// limited-scope token can't perform admin ops even for an admin
			// user, and a write-scoped token can't for a non-admin user.
			adminOnly := auth.RequireAdmin

			// Auth/session + API token self-management (no extra scope —
			// any authenticated principal manages its own tokens).
			r.Route("/auth", func(r chi.Router) {
				r.Post("/logout", routes.Logout)
				r.Get("/me", routes.Me)
				r.Put("/profile", routes.UpdateProfile)
				r.Put("/password", routes.ChangePassword)
				r.Get("/tokens", routes.ListAPITokens)
				r.Post("/tokens", routes.CreateAPIToken)
				r.Delete("/tokens/{id}", routes.RevokeAPIToken)
				// Self-service second factor (any authenticated principal).
				r.Get("/mfa/status", routes.MFAStatus)
				r.Post("/mfa/setup", routes.MFASetup)
				r.Post("/mfa/activate", routes.MFAActivate)
				r.Post("/mfa/disable", routes.MFADisable)
			})

			r.With(adminScope).With(adminOnly).Get("/audit-log", routes.ListAuditLog)
			r.With(adminScope).With(adminOnly).Post("/license/validate", routes.ValidateLicense)
			r.With(adminScope).With(adminOnly).Post("/license/install", routes.InstallLicense)

			// Admin oversight: read-only global views across all users.
			r.Route("/admin", func(r chi.Router) {
				r.Use(adminScope)
				r.Use(adminOnly)
				r.Get("/tokens", routes.AdminListTokens)
				r.Get("/secrets", routes.AdminListSecrets)
				r.Get("/disk", routes.AdminDisk)
				r.Post("/workspaces/reap", routes.AdminReapWorkspaces)
			})

			r.Route("/users", func(r chi.Router) {
				r.Use(adminScope)
				r.Use(adminOnly)
				r.Get("/", routes.ListUsers)
				r.Post("/", routes.CreateUserAdmin)
				r.Put("/{id}/role", routes.UpdateUserRole)
				r.Put("/{id}/scanner-supply-chain-access", routes.UpdateUserScannerSupplyChainAccess)
				r.Post("/{id}/mfa/reset", routes.AdminResetUserMFA)
				r.Delete("/{id}", routes.DeleteUser)
			})

			r.Route("/repos", func(r chi.Router) {
				r.With(rRepos).Get("/", routes.ListRepos)
				r.With(wRepos).Post("/", routes.CreateRepo)
				r.With(rRepos).Get("/{id}", routes.GetRepo)
				r.With(wRepos).Put("/{id}", routes.UpdateRepo)
				r.With(wRepos).Post("/{id}/sync", routes.SyncRepo)
				r.With(wRepos).Delete("/{id}", routes.DeleteRepo)
				r.With(rRepos).Get("/{id}/branches", routes.ListRepoBranches)
				r.With(rRepos).Get("/{id}/fixable", routes.GetRepoFixable)
				r.With(rScans).Get("/{id}/baselines", routes.ListRepoBaselines)
				r.With(wScans).Post("/{id}/baselines", routes.CreateRepoBaseline)
			})

			r.Route("/sources", func(r chi.Router) {
				r.With(wRepos).Post("/github/list-org-repos", routes.ListOrgGitHubRepos)
			})

			r.Route("/credentials", func(r chi.Router) {
				r.With(rCredentials).Get("/", routes.ListCredentials)
				r.With(wCredentials).Post("/", routes.CreateCredential)
				r.With(rCredentials).Get("/{id}", routes.GetCredential)
				r.With(wCredentials).Delete("/{id}", routes.DeleteCredential)
			})

			r.Route("/nodes", func(r chi.Router) {
				// Remote SSH nodes are per-user: a user manages their own (the
				// handlers scope to the caller + enforce ownership via
				// loadRemoteNode).
				r.With(rConfig).Get("/", routes.ListRemoteNodes)
				r.With(wConfig).Post("/", routes.CreateRemoteNode)
				r.With(rConfig).Get("/{id}", routes.GetRemoteNode)
				r.With(wConfig).Put("/{id}", routes.UpdateRemoteNode)
				r.With(wConfig).Delete("/{id}", routes.DeleteRemoteNode)
				r.With(wConfig).Post("/{id}/check", routes.CheckRemoteNode)
				r.With(rConfig).Get("/{id}/browse", routes.BrowseRemoteNode)
				r.With(rConfig).Get("/{id}/git-info", routes.RemoteGitInfo)
				r.With(wConfig).Post("/{id}/discover-repos", routes.DiscoverNodeRepos)
			})

			r.Route("/collections", func(r chi.Router) {
				r.With(rRepos).Get("/", routes.ListCollections)
				r.With(wRepos).Post("/", routes.CreateCollection)
				r.With(rRepos).Get("/{id}", routes.GetCollection)
				r.With(wRepos).Put("/{id}", routes.UpdateCollection)
				r.With(wRepos).Delete("/{id}", routes.DeleteCollection)
				r.With(wRepos).Post("/{id}/repos", routes.AddRepoToCollection)
				r.With(wRepos).Delete("/{id}/repos/{repoId}", routes.RemoveRepoFromCollection)
				r.With(rRepos).Get("/{id}/tools", routes.CollectionTools)
				r.With(rRepos).Get("/{id}/metrics", routes.CollectionMetrics)
			})

			r.Route("/schedules", func(r chi.Router) {
				r.With(rScans).Get("/", routes.ListSchedules)
				r.With(wScans).Post("/", routes.CreateSchedule)
				r.With(wScans).Put("/{id}", routes.UpdateSchedule)
				r.With(wScans).Delete("/{id}", routes.DeleteSchedule)
			})

			r.With(rScans).Get("/notifications", routes.ListNotifications)
			r.With(rRepos).Get("/setup/status", routes.GetSetupStatus)
			r.With(wRepos).Post("/setup/sample-repo", routes.CreateSampleRepo)
			r.With(wConfig).With(adminOnly).Post("/webhooks/outbound/test", routes.TestOutboundWebhook)

			r.Route("/scans", func(r chi.Router) {
				r.With(rScans).Get("/", routes.ListScans)
				r.With(rScans).Get("/trends", routes.ScansTrends)
				r.With(rScans).Get("/orphans", routes.ListOrphanScans)
				r.With(wScans).Delete("/orphans", routes.PurgeOrphanScans)
				r.With(wScans).Post("/", routes.CreateScan)
				r.With(wScans, operateScannerSupplyChain, readScannerReleaseMode).Post("/{id}/release-rescans", routes.CreateReleaseRescan)
				// Preflight: which selected scanners are missing their image, so
				// the UI can prompt to pull before starting (read-scope: no scan
				// is created).
				r.With(rScans).Post("/preflight", routes.ScanPreflight)
				r.With(rScans).Get("/{id}", routes.GetScan)
				r.With(rScans).Get("/{id}/lineage", routes.GetScanLineage)
				r.With(rScans).Get("/{id}/result", routes.GetScanResult)
				r.With(rScans).Get("/{id}/findings", routes.GetScanFindings)
				r.With(rScans).Get("/{id}/findings/stats", routes.GetScanFindingStats)
				r.With(rScans).Get("/{id}/stream", routes.StreamScan)
				r.With(rScans).Get("/{id}/report", routes.GetScanReport)
				r.With(rScans).Get("/{id}/manifest", routes.GetScanManifest)
				r.With(rScans).Get("/{id}/sarif", routes.GetScanSARIF)
				r.With(rScans).Get("/{id}/coverage", routes.GetScanCoverage)
				r.With(rScans).Get("/{id}/gate", routes.GetScanGate)
				r.With(rScans).Get("/{id}/diff", routes.GetScanDiff)
				r.With(rScans).Post("/{id}/compare", routes.CompareScanToBaseline)
				r.With(rScans).Get("/{id}/compare/{compareId}", routes.CompareScan)
				r.With(rScans).Get("/{id}/tools", routes.GetScanTools)
				r.With(rScans).Get("/{id}/scanner-runs", routes.GetScannerRunRecords)
				r.With(rScans).Get("/{id}/tools/{toolName}/output", routes.GetToolOutput)
				r.With(rScans).Get("/{id}/artifacts/{artifactId}/download", routes.DownloadArtifact)
				r.With(rScans).Get("/{id}/ai-logs", routes.ListAILogs)
				r.With(rScans).Get("/{id}/tool-summaries", routes.GetToolSummaries)
				r.With(rScans).Get("/{id}/recommendations", routes.GetScanRecommendations)
				r.With(wScans).Post("/{id}/retry", routes.RetryScan)
				r.With(wScans).Delete("/{id}", routes.CancelScan)
				r.With(wScans).Delete("/{id}/tools/{toolName}", routes.CancelScanTool)
			})

			r.Route("/findings", func(r chi.Router) {
				r.With(rFind).Get("/", routes.ListFindings)
				r.With(rFind).Get("/export", routes.ExportFindings)
				r.With(rFind).Get("/trends", routes.FindingTrends)
				r.With(rFind).Get("/trends/export", routes.ExportFindingTrends)
				r.With(rFind).Get("/aggregate", routes.FindingsAggregate)
				r.With(rFind).Get("/by-repo", routes.FindingsByRepo)
				r.With(wFind).Post("/bulk", routes.BulkUpdateFindings)
				r.With(rFind).Get("/{id}", routes.GetFinding)
				r.With(wFind).Put("/{id}/status", routes.UpdateFindingStatus)
			})

			r.With(rFind).Get("/evidence", routes.ListEvidence)

			r.Route("/vulnerabilities", func(r chi.Router) {
				r.With(rFind).Get("/", routes.ListVulnerabilities)
				r.With(rFind).Get("/{id}/attack-path", routes.GetAttackPath)
				r.With(rFind).Get("/{id}/evidence", routes.ListEvidence)
				r.With(rFind).Post("/{id}/investigate", routes.InvestigateVulnerability)
				r.With(wFind).Post("/{id}/verify", routes.VerifyVulnerability)
				r.With(rFind).Get("/{id}", routes.GetVulnerability)
				r.With(wFind).Post("/{id}/split", routes.SplitVulnerability)
				r.With(wFind).Post("/{id}/merge", routes.MergeVulnerability)
			})

			r.Route("/sarif", func(r chi.Router) {
				r.With(wScans).Post("/import", routes.ImportSARIF)
			})

			r.Route("/suppressions", func(r chi.Router) {
				r.With(rFind).Get("/", routes.ListSuppressions)
				r.With(wFind).Post("/", routes.CreateSuppression)
				r.With(wFind).Post("/preview", routes.PreviewSuppression)
				r.With(wFind).Delete("/{id}", routes.RevokeSuppression)
			})

			r.Route("/policies", func(r chi.Router) {
				r.With(rConfig).Get("/", routes.ListPolicies)
				r.With(wConfig).Post("/", routes.CreatePolicy)
				r.With(wConfig).Put("/{id}", routes.UpdatePolicy)
			})

			r.Route("/remediations", func(r chi.Router) {
				r.With(wFixes).Post("/{id}/accept", routes.AcceptRemediation)
			})

			r.Route("/fixes", func(r chi.Router) {
				r.With(rFixes).Get("/", routes.ListFixes)
				r.With(wFixes).Post("/", routes.CreateFix)
				r.With(rFixes).Get("/engines", routes.ListFixEngines)
				r.With(wFixes).Post("/consoles", routes.CreateFixerConsole)
				r.With(rFixes).Get("/consoles/{id}", routes.GetFixerConsole)
				r.With(rFixes).Get("/consoles/{id}/stream", routes.StreamFixerConsole)
				r.With(wFixes).Post("/consoles/{id}/input", routes.InputFixerConsole)
				r.With(wFixes).Delete("/consoles/{id}", routes.CancelFixerConsole)
				r.With(rFixes).Get("/{id}", routes.GetFix)
				r.With(rFixes).Get("/{id}/diff", routes.GetFixDiff)
				r.With(rFixes).Get("/{id}/commits", routes.GetFixCommits)
				r.With(rFixes).Get("/{id}/stream", routes.StreamFix)
				r.With(wFixes).Post("/{id}/resume", routes.ResumeFix)
				r.With(wFixes).Delete("/{id}", routes.CancelFix)
			})

			r.Route("/fleet", func(r chi.Router) {
				r.With(rScans).Get("/posture", routes.FleetPosture)
				r.With(rRepos).Get("/inventory", routes.FleetInventory)
				r.With(rScans).Get("/needs-attention", routes.FleetNeedsAttention)
			})

			r.With(rRepos).Get("/browse", routes.BrowseLocal)
			r.With(rRepos).Get("/git-info", routes.GitInfo)

			r.Route("/config", func(r chi.Router) {
				r.With(rConfig).Get("/secrets", routes.ListSecrets)
				// Secrets are per-user: any authenticated user manages their own
				// (the handlers scope to the caller + enforce ownership).
				r.With(wConfig).Post("/secrets", routes.CreateSecret)
				r.With(wConfig).Delete("/secrets/{id}", routes.DeleteSecret)
				r.With(rConfig).Get("/plugins", routes.ListPlugins)
				r.With(wConfig).With(adminOnly).Post("/plugins/{name}/install", routes.InstallPlugin)
				r.With(rConfig).Get("/setup", routes.SetupStatus)
			})

			// Scanner backend (docs/PLAN-containerized-scanner-execution.md §5).
			r.Route("/scanners", func(r chi.Router) {
				// Scanner-image builds/pulls/updates are admin-only (system
				// maintenance); reads stay open for the scan flow.
				r.With(rConfig).Get("/tools", routes.ScannersTools)
				r.With(rConfig).Get("/tools/{name}", routes.ScannersTool)
				r.With(wConfig).With(adminOnly).Post("/tools/check-updates", routes.ScannersCheckUpdates)
				r.With(wConfig).With(adminOnly).Post("/tools/{name}/check-update", routes.ScannersCheckUpdate)
				r.With(rConfig).Get("/images", routes.ScannersImages)
				r.With(wConfig).With(adminOnly).Post("/images/pull", routes.ScannersPullOne)
				r.With(wConfig).With(adminOnly).With(allowLegacyScannerBuilds).Post("/images/{variant}/build", routes.BuildScannerImage)
				r.With(wConfig).With(adminOnly).With(allowLegacyScannerBuilds).Post("/images/build-all", routes.BuildAllScannerImages)
				r.With(rConfig).With(adminOnly).Get("/custom-builds", routes.ListScannerCustomBuilds)
				r.With(wConfig).With(adminOnly).Post("/custom-builds", routes.CreateScannerCustomBuild)
				r.With(rConfig).With(adminOnly).Get("/custom-builds/{id}", routes.GetScannerCustomBuild)
				r.With(rConfig).With(adminOnly).Get("/custom-builds/{id}/events", routes.StreamScannerCustomBuildEvents)
				r.With(wConfig).With(adminOnly).Post("/custom-builds/{id}/cancel", routes.CancelScannerCustomBuild)
				r.With(wConfig).With(adminOnly).Post("/custom-builds/{id}/retry", routes.RetryScannerCustomBuild)
				r.With(rConfig).Get("/config", routes.ScannersConfig)
				r.With(rConfig).Get("/runtime-capabilities", routes.RuntimeCapabilities)
				r.With(rConfig).Get("/workers", routes.ListScanWorkers)
				r.With(rConfig).Get("/list", routes.ScannersList)
				r.With(rConfig).Post("/plan", routes.ScannersPlan)
				r.With(wConfig).With(adminOnly).Post("/doctor", routes.ScannersDoctor)
				r.With(wConfig).With(adminOnly).Post("/pull", routes.ScannersPull)
			})

			// Durable scanner supply-chain control plane. Existing /scanners
			// endpoints remain available as compatibility/troubleshooting
			// surfaces while these resources own release operations.
			r.Route("/scanner-supply-chain", func(r chi.Router) {
				r.With(readScannerSupplyChain, readScannerReleaseMode).Get("/overview", routes.ScannerSupplyChainOverview)
				r.With(readScannerSupplyChain, readScannerReleaseMode).Get("/updates", routes.ScannerSupplyChainListUpdates)
				r.With(readScannerSupplyChain, readScannerReleaseMode).Get("/policy", routes.ScannerSupplyChainGetPolicy)
				r.With(adminScannerSupplyChain, candidateScannerReleaseMode).Put("/policy", routes.ScannerSupplyChainPutPolicy)
				r.With(readScannerSupplyChain, readScannerReleaseMode).Post("/policy/validate", routes.ScannerSupplyChainValidatePolicy)
				r.With(readScannerSupplyChain, readScannerReleaseMode).Post("/policy/dry-run", routes.ScannerSupplyChainPolicyDryRun)
				r.With(readScannerSupplyChain, readScannerReleaseMode).Get("/policy/revisions", routes.ScannerSupplyChainListPolicyRevisions)
				r.With(adminScannerSupplyChain, candidateScannerReleaseMode).Post("/policy/revisions/{revision}/restore", routes.ScannerSupplyChainRestorePolicy)

				r.With(operateScannerSupplyChain, candidateScannerReleaseMode).Post("/discovery-runs", routes.ScannerSupplyChainCreateDiscovery)
				r.With(readScannerSupplyChain, readScannerReleaseMode).Get("/discovery-runs", routes.ScannerSupplyChainListDiscoveries)
				r.With(readScannerSupplyChain, readScannerReleaseMode).Get("/discovery-runs/{id}", routes.ScannerSupplyChainGetDiscovery)
				r.With(operateScannerSupplyChain, candidateScannerReleaseMode).Post("/discovery-runs/{id}/cancel", routes.ScannerSupplyChainCancelDiscovery)
				r.With(readScannerSupplyChain, readScannerReleaseMode).Get("/discovery-runs/{id}/events", routes.ScannerSupplyChainDiscoveryEvents)

				r.With(operateScannerSupplyChain, candidateScannerReleaseMode).Post("/candidates", routes.ScannerSupplyChainCreateCandidate)
				r.With(readScannerSupplyChain, readScannerReleaseMode).Get("/candidates", routes.ScannerSupplyChainListCandidates)
				r.With(readScannerSupplyChain, readScannerReleaseMode).Get("/candidates/{id}", routes.ScannerSupplyChainGetCandidate)
				r.With(readScannerSupplyChain, readScannerReleaseMode).Get("/candidates/{id}/diffs/{kind}", routes.ScannerSupplyChainGetCandidateDiff)
				r.With(readScannerSupplyChain, readScannerReleaseMode).Get("/candidates/{id}/events", routes.ScannerSupplyChainCandidateEvents)
				r.With(operateScannerSupplyChain, candidateScannerReleaseMode).Post("/candidates/{id}/cancel", routes.ScannerSupplyChainCancelCandidate)
				r.With(operateScannerSupplyChain, candidateScannerReleaseMode).Post("/candidates/{id}/retry", routes.ScannerSupplyChainRetryCandidate)
				r.With(approveScannerReleases, candidateScannerReleaseMode).Post("/candidates/{id}/approve", routes.ScannerSupplyChainApproveCandidate)
				r.With(approveScannerReleases, candidateScannerReleaseMode).Post("/candidates/{id}/reject", routes.ScannerSupplyChainRejectCandidate)
				r.With(approveScannerReleases, candidateScannerReleaseMode).Post("/candidates/{id}/exceptions", routes.ScannerSupplyChainCreateCandidateException)
				r.With(approveScannerReleases, canaryScannerReleaseMode).Post("/candidates/{id}/publish", routes.ScannerSupplyChainPublishCandidate)

				r.With(readScannerSupplyChain, readScannerReleaseMode).Get("/releases", routes.ScannerSupplyChainListReleases)
				r.With(readScannerSupplyChain, readScannerReleaseMode).Get("/releases/compare", routes.ScannerSupplyChainCompareReleases)
				r.With(readScannerSupplyChain, readScannerReleaseMode).Get("/releases/{id}", routes.ScannerSupplyChainGetRelease)
				r.With(readScannerSupplyChain, readScannerReleaseMode).Get("/releases/{id}/diffs/{kind}", routes.ScannerSupplyChainGetReleaseDiff)
				r.With(readScannerSupplyChain, readScannerReleaseMode).Get("/releases/{id}/events", routes.ScannerSupplyChainReleaseEvents)
				r.With(readScannerSupplyChain, readScannerReleaseMode).Get("/releases/{id}/export", routes.ScannerSupplyChainExportReleaseBundle)
				r.With(readScannerSupplyChain, readScannerReleaseMode).Post("/releases/{id}/verify", routes.ScannerSupplyChainVerifyRelease)
				r.With(operateScannerSupplyChain, canaryScannerReleaseMode).Post("/releases/{id}/promote", routes.ScannerSupplyChainPromoteRelease)
				r.With(adminScannerSupplyChain, stableScannerReleaseMode).Post("/releases/{id}/deprecate", routes.ScannerSupplyChainDeprecateRelease)
				r.With(adminScannerSupplyChain, stableScannerReleaseMode).Post("/releases/{id}/revoke", routes.ScannerSupplyChainRevokeRelease)
				r.With(adminScannerSupplyChain, candidateScannerReleaseMode).Post("/release-imports", routes.ScannerSupplyChainImportReleaseBundle)
				r.With(adminScannerSupplyChain, adminOnly, candidateScannerReleaseMode).Post("/legacy-release-imports", routes.ScannerSupplyChainImportLegacyConfig)

				r.With(readScannerSupplyChain, readScannerReleaseMode).Get("/rollouts", routes.ScannerSupplyChainListRollouts)
				r.With(readScannerSupplyChain, readScannerReleaseMode).Get("/rollouts/{id}", routes.ScannerSupplyChainGetRollout)
				r.With(readScannerSupplyChain, readScannerReleaseMode).Get("/rollouts/{id}/events", routes.ScannerSupplyChainRolloutEvents)
				r.With(operateScannerSupplyChain, canaryScannerReleaseMode).Post("/rollouts/{id}/pause", routes.ScannerSupplyChainPauseRollout)
				r.With(operateScannerSupplyChain, canaryScannerReleaseMode).Post("/rollouts/{id}/resume", routes.ScannerSupplyChainResumeRollout)
				r.With(operateScannerSupplyChain, canaryScannerReleaseMode).Post("/rollouts/{id}/rollback", routes.ScannerSupplyChainRollbackRollout)

				r.With(manageScannerRegistries, readScannerReleaseMode).Get("/registries", routes.ScannerSupplyChainListRegistries)
				r.With(manageScannerRegistries, candidateScannerReleaseMode).Post("/registries", routes.ScannerSupplyChainCreateRegistry)
				r.With(manageScannerRegistries, readScannerReleaseMode).Get("/registries/{id}", routes.ScannerSupplyChainGetRegistry)
				r.With(manageScannerRegistries, candidateScannerReleaseMode).Patch("/registries/{id}", routes.ScannerSupplyChainPatchRegistry)
				r.With(manageScannerRegistries, candidateScannerReleaseMode).Delete("/registries/{id}", routes.ScannerSupplyChainDeleteRegistry)
				r.With(manageScannerRegistries, candidateScannerReleaseMode).Post("/registries/{id}/check", routes.ScannerSupplyChainCheckRegistry)
				r.With(manageScannerRegistries, candidateScannerReleaseMode).Post("/registries/{id}/reconcile", routes.ScannerSupplyChainReconcileRegistry)
				r.With(manageScannerRegistries, candidateScannerReleaseMode).Post("/registries/{id}/jobs", routes.ScannerSupplyChainCreateRegistryJob)
				r.With(manageScannerRegistries, candidateScannerReleaseMode).Post("/registries/{id}/cleanup-jobs", routes.ScannerSupplyChainCreateRegistryCleanupJob)
				r.With(manageScannerRegistries, readScannerReleaseMode).Get("/registry-jobs", routes.ScannerSupplyChainListRegistryJobs)
				r.With(manageScannerRegistries, readScannerReleaseMode).Get("/registry-jobs/{id}", routes.ScannerSupplyChainGetRegistryJob)
				r.With(manageScannerRegistries, readScannerReleaseMode).Get("/registry-jobs/{id}/events", routes.ScannerSupplyChainRegistryJobEvents)
				r.With(manageScannerRegistries, candidateScannerReleaseMode).Post("/registry-jobs/{id}/retry", routes.ScannerSupplyChainRetryRegistryJob)
				r.With(manageScannerRegistries, readScannerReleaseMode).Get("/registry-quarantine", routes.ScannerSupplyChainListRegistryQuarantine)

				r.With(adminScannerSupplyChain, readScannerReleaseMode).Get("/signers", routes.ScannerSupplyChainListSigners)
				r.With(adminScannerSupplyChain, candidateScannerReleaseMode).Post("/signers", routes.ScannerSupplyChainCreateSigner)
				r.With(adminScannerSupplyChain, readScannerReleaseMode).Get("/signers/{id}", routes.ScannerSupplyChainGetSigner)
				r.With(adminScannerSupplyChain, candidateScannerReleaseMode).Post("/signers/{id}/rotate", routes.ScannerSupplyChainRotateSigner)
				r.With(adminScannerSupplyChain, candidateScannerReleaseMode).Post("/signers/{id}/revoke", routes.ScannerSupplyChainRevokeSigner)

				r.With(readScannerSupplyChain, readScannerReleaseMode).Get("/notifications", routes.ScannerSupplyChainListNotifications)
				r.With(readScannerSupplyChain, readScannerReleaseMode).Get("/notifications/{id}", routes.ScannerSupplyChainGetNotification)
				r.With(adminScannerSupplyChain, candidateScannerReleaseMode).Post("/notifications/{id}/retry", routes.ScannerSupplyChainRetryNotification)
				r.With(readScannerSupplyChain, readScannerReleaseMode).Get("/alerts", routes.ScannerSupplyChainListAlerts)
				r.With(readScannerSupplyChain, readScannerReleaseMode).Get("/alerts/{id}", routes.ScannerSupplyChainGetAlert)

				r.With(readScannerSupplyChain, readScannerReleaseMode).Get("/audit", routes.ScannerSupplyChainAudit)
				r.With(readScannerSupplyChain, readScannerReleaseMode).Get("/audit/export", routes.ScannerSupplyChainAuditExport)
			})

			// GET stays open (the UI reads feature flags to gate nav); writes
			// are admin-only.
			r.With(rConfig).Get("/settings", routes.ListSettings)
			r.With(wConfig).With(adminOnly).Put("/settings", routes.UpdateSettings)

			r.Route("/ai-prompts", func(r chi.Router) {
				r.With(rConfig).Get("/", routes.ListPromptTemplates)
				r.With(wConfig).Put("/", routes.UpsertPromptTemplate)
				r.With(rConfig).Get("/defaults", routes.GetPromptDefaults)
				r.With(rConfig).Post("/preview", routes.PreviewPrompt)
				r.With(wConfig).Delete("/{id}", routes.DeletePromptTemplate)
			})

			r.With(rConfig).Get("/ai-providers", routes.ListAIProviders)

			// Overlay modules register /enterprise/* here so they inherit
			// session auth. Handlers still 404 without the matching entitlement.
			edition.Default.Mount(r)
		})
	})

	// Deprecating alias: /api/* -> /api/v1/*. 307 preserves method + body.
	r.HandleFunc("/api/*", func(w http.ResponseWriter, req *http.Request) {
		target := "/api/v1" + strings.TrimPrefix(req.URL.Path, "/api")
		if req.URL.RawQuery != "" {
			target += "?" + req.URL.RawQuery
		}
		w.Header().Set("Deprecation", "true")
		w.Header().Set("Link", `</api/v1>; rel="successor-version"`)
		http.Redirect(w, req, target, http.StatusTemporaryRedirect)
	})

	// Static UI (SPA) — mounted AFTER /api so /api/* routes always win.
	// Headless installations can disable only the SPA while retaining the API
	// and OpenAPI documentation.
	if !envBool("WOLF_API_ONLY") {
		MountStaticUI(r, os.Getenv("WOLF_UI_DIR"))
	}

	srv := &Server{
		Router: r,
		Store:  store,
		Addr:   addr,
	}

	srv.httpServer = &http.Server{
		Addr:              addr,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		// WriteTimeout matches the chi router-level Timeout above; the
		// transport-level write deadline must outlast the slowest
		// legitimate handler (bulk scanner pulls, SSE streams).
		WriteTimeout: 15 * time.Minute,
		IdleTimeout:  120 * time.Second,
	}

	return srv
}

func allowedCORSOrigin(r *http.Request, origin string) bool {
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return false
	}
	if strings.EqualFold(u.Host, r.Host) {
		return true
	}

	allowed := []string{
		"http://localhost:3000",
		"http://127.0.0.1:3000",
		"http://localhost:5173",
		"http://127.0.0.1:5173",
	}
	if configured := os.Getenv("WOLF_CORS_ORIGINS"); configured != "" {
		allowed = nil
		for _, item := range strings.Split(configured, ",") {
			if trimmed := strings.TrimSpace(item); trimmed != "" {
				allowed = append(allowed, trimmed)
			}
		}
	}
	for _, item := range allowed {
		if strings.EqualFold(origin, item) {
			return true
		}
	}
	return false
}

// Start starts the HTTP server (blocking).
func (s *Server) Start() error {
	wolflog.Info().Str("addr", s.Addr).Msg("API server starting")

	// Orphan recovery: any scans still marked "running" or "pending" must
	// be from a previous server process that crashed or was killed —
	// their executeScan goroutine is gone, so they'll never finish
	// themselves. Mark them as cancelled so the UI doesn't keep showing
	// "1 scan running" forever and operators can re-trigger if needed.
	if !queueExecutionMode() {
		recoverOrphanScans(s.Store)
	}

	err := s.httpServer.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func queueExecutionMode() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("WOLF_SCAN_EXECUTION_MODE"))) {
	case "queue", "worker", "workers":
		return true
	default:
		return false
	}
}

func envBool(name string) bool {
	value := strings.TrimSpace(os.Getenv(name))
	return strings.EqualFold(value, "true") || value == "1"
}

// Shutdown performs a graceful shutdown with the given context deadline.
func (s *Server) Shutdown(ctx context.Context) error {
	wolflog.Info().Msg("API server shutting down")
	return s.httpServer.Shutdown(ctx)
}

// makeAPITokenResolver builds the closure the auth middleware uses to turn a
// plaintext "wolf_" token into a principal. It returns (nil, nil) for any
// token that is unknown, revoked, or expired — the middleware treats all
// three identically (401) so an attacker learns nothing from the response.
func makeAPITokenResolver(store db.Store) auth.APITokenResolver {
	return func(ctx context.Context, plaintext string) (*auth.ResolvedToken, error) {
		tok, err := store.GetAPITokenByHash(ctx, apikey.Hash(plaintext))
		if err != nil {
			return nil, nil // unknown token
		}
		if tok.RevokedAt != nil {
			return nil, nil
		}
		if tok.ExpiresAt != nil && tok.ExpiresAt.Before(time.Now()) {
			return nil, nil
		}
		// Best-effort, non-blocking last-used update.
		go func(id string) {
			tctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = store.TouchAPIToken(tctx, id)
		}(tok.ID)

		email := ""
		if u, uerr := store.GetUserByID(ctx, tok.UserID); uerr == nil && u != nil {
			email = u.Email
		}
		return &auth.ResolvedToken{
			TokenID: tok.ID,
			UserID:  tok.UserID,
			Email:   email,
			Scopes:  tok.ScopeList,
		}, nil
	}
}

func makeSessionResolver(store db.Store) auth.SessionResolver {
	return func(ctx context.Context, plaintext string) (*auth.ResolvedSession, error) {
		session, err := store.GetAuthSessionByHash(ctx, auth.HashSessionToken(plaintext))
		if err != nil {
			return nil, nil
		}
		now := time.Now().UTC()
		if session.RevokedAt != nil || !session.ExpiresAt.After(now) {
			return nil, nil
		}
		user, err := store.GetUserByID(ctx, session.UserID)
		if err != nil {
			return nil, nil
		}
		_ = store.TouchAuthSession(ctx, session.ID)
		return &auth.ResolvedSession{
			SessionID: session.ID,
			UserID:    user.ID,
			Email:     user.Email,
		}, nil
	}
}

// makeAuditRecorder builds the closure the audit middleware calls for every
// mutating request. Errors are swallowed — audit logging must never affect
// the request it describes.
func makeAuditRecorder(store db.Store) func(middleware.AuditEntry) {
	return func(e middleware.AuditEntry) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var tokenID *string
		if e.TokenID != "" {
			id := e.TokenID
			tokenID = &id
		}
		err := store.AppendAuditLog(ctx, &models.AuditLogEntry{
			ID:         uuid.New().String(),
			TokenID:    tokenID,
			UserID:     e.UserID,
			Action:     e.Action,
			Method:     e.Method,
			Path:       e.Path,
			ResourceID: e.ResourceID,
			StatusCode: e.StatusCode,
			EventType:  e.EventType,
			Category:   e.Category,
			Severity:   e.Severity,
			IP:         e.IP,
			UserAgent:  e.UserAgent,
			CreatedAt:  time.Now().UTC(),
		})
		if err != nil {
			wolflog.Warn().Err(err).Msg("audit log append failed")
		}
	}
}

func runArtifactRetentionCleanup(store db.Store) {
	if store == nil || artifacts.Global == nil {
		return
	}
	raw, err := store.GetSetting(context.Background(), "artifact_retention_days")
	if err != nil || strings.TrimSpace(raw) == "" {
		return
	}
	days, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || days <= 0 {
		return
	}
	go func() {
		_, _ = artifacts.Global.CleanupOlderThan(time.Duration(days) * 24 * time.Hour)
	}()
}

// recoverOrphanScans walks the scans table and cancels any rows still in
// the running or pending state — they belong to a previous process that
// exited before writing their terminal status. Logged but errors are
// non-fatal so a transient DB hiccup doesn't block startup.
func recoverOrphanScans(store db.Store) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	scans, err := store.ListScansByUser(ctx, "")
	if err != nil {
		wolflog.Warn().Err(err).Msg("orphan recovery: failed to list scans")
		return
	}
	now := time.Now().UTC()
	recovered := 0
	for i := range scans {
		s := &scans[i]
		if s.Status != models.ScanStatusRunning && s.Status != models.ScanStatusPending {
			continue
		}
		s.Status = models.ScanStatusCancelled
		s.CompletedAt = &now
		if err := store.UpdateScan(ctx, s); err != nil {
			wolflog.Warn().Err(err).Str("scan_id", s.ID).Msg("orphan recovery: update failed")
			continue
		}
		recovered++
	}
	if recovered > 0 {
		wolflog.Info().Int("count", recovered).Msg("orphan recovery: cancelled stuck scans from previous process")
	}
}
