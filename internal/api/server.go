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
	"github.com/alphabravocompany/thewolf/internal/wolflog"
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

	// Initialize SSE broker so scan events are broadcast to connected clients.
	routes.SSEBroker = sse.NewBroker()

	// Initialize artifact store at ~/.wolf/artifacts/ for durable scan output storage.
	if artifacts.Global == nil {
		home, _ := os.UserHomeDir()
		artifacts.Init(filepath.Join(home, ".wolf", "artifacts")) // #nosec G104 -- intentional: response/log write errors are not actionable here
	}
	runArtifactRetentionCleanup(store)

	r := chi.NewRouter()

	// Rate limiters
	generalLimiter := middleware.DefaultRateLimiter()
	authLimiter := middleware.StrictRateLimiter()

	// Middleware chain
	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(chimw.Logger)
	r.Use(chimw.Recoverer)
	// 15-minute global timeout. Long enough for the legitimate slow
	// operations — bulk scanner-image pulls (24 images × multi-MB each
	// = several minutes), doctor diagnostics, scan progress streaming
	// (SSE keeps the conn alive). Fast handlers complete in ms either
	// way; this is the safety-net upper bound, not the expected case.
	r.Use(chimw.Timeout(15 * time.Minute))
	r.Use(middleware.MaxBodySize(1 << 20)) // 1 MB body limit
	r.Use(generalLimiter.Handler)
	r.Use(cors.Handler(cors.Options{
		AllowOriginFunc:  allowedCORSOrigin,
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
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

	// Mount the versioned API. All routes live under /api/v1; /api/* is a
	// deprecating redirect alias kept for one release.
	r.Route("/api/v1", func(r chi.Router) {
		r.Use(jsonContentType)

		// Public endpoints — no authentication.
		r.Group(func(r chi.Router) {
			r.Get("/health", routes.Health)
			r.Get("/ready", routes.Ready)
			r.Get("/version", routes.Version)
		})

		// OpenAPI spec + Swagger UI — public by design.
		mountDocs(r)

		// Auth endpoints with stricter rate limiting.
		r.Group(func(r chi.Router) {
			r.Use(authLimiter.Handler)
			r.Get("/auth/settings", routes.AuthSettings)
			r.Post("/auth/register", routes.Register)
			r.Post("/auth/login", routes.Login)
		})

		// Protected endpoints. Scope vocabulary is defined in
		// internal/auth/apikey; JWT (UI) sessions hold every scope.
		r.Group(func(r chi.Router) {
			r.Use(auth.Middleware)
			r.Use(tokenLimiter.HandlerForToken)
			r.Use(middleware.Audit(auditRecorder))

			rRepos := auth.RequireScope(apikey.ScopeReadRepos)
			wRepos := auth.RequireScope(apikey.ScopeWriteRepos)
			rScans := auth.RequireScope(apikey.ScopeReadScans)
			wScans := auth.RequireScope(apikey.ScopeWriteScans)
			rFind := auth.RequireScope(apikey.ScopeReadFindings)
			wFind := auth.RequireScope(apikey.ScopeWriteFindings)
			rFixes := auth.RequireScope(apikey.ScopeReadFixes)
			wFixes := auth.RequireScope(apikey.ScopeWriteFixes)
			rLoops := auth.RequireScope(apikey.ScopeReadLoops)
			wLoops := auth.RequireScope(apikey.ScopeWriteLoops)
			rConfig := auth.RequireScope(apikey.ScopeReadConfig)
			wConfig := auth.RequireScope(apikey.ScopeWriteConfig)
			adminOnly := auth.RequireScope(apikey.ScopeAdmin)

			// Auth/session + API token self-management (no extra scope —
			// any authenticated principal manages its own tokens).
			r.Route("/auth", func(r chi.Router) {
				r.Post("/logout", routes.Logout)
				r.Get("/me", routes.Me)
				r.Put("/password", routes.ChangePassword)
				r.Get("/tokens", routes.ListAPITokens)
				r.Post("/tokens", routes.CreateAPIToken)
				r.Delete("/tokens/{id}", routes.RevokeAPIToken)
			})

			r.With(adminOnly).Get("/audit-log", routes.ListAuditLog)

			r.Route("/users", func(r chi.Router) {
				r.Use(adminOnly)
				r.Get("/", routes.ListUsers)
				r.Post("/", routes.CreateUserAdmin)
				r.Delete("/{id}", routes.DeleteUser)
			})

			r.Route("/repos", func(r chi.Router) {
				r.With(rRepos).Get("/", routes.ListRepos)
				r.With(wRepos).Post("/", routes.CreateRepo)
				r.With(rRepos).Get("/{id}", routes.GetRepo)
				r.With(wRepos).Put("/{id}", routes.UpdateRepo)
				r.With(wRepos).Delete("/{id}", routes.DeleteRepo)
				r.With(rRepos).Get("/{id}/branches", routes.ListRepoBranches)
				r.With(rScans).Get("/{id}/baselines", routes.ListRepoBaselines)
				r.With(wScans).Post("/{id}/baselines", routes.CreateRepoBaseline)
			})

			r.Route("/nodes", func(r chi.Router) {
				r.With(rConfig).Get("/", routes.ListRemoteNodes)
				r.With(wConfig).Post("/", routes.CreateRemoteNode)
				r.With(rConfig).Get("/{id}", routes.GetRemoteNode)
				r.With(wConfig).Put("/{id}", routes.UpdateRemoteNode)
				r.With(wConfig).Delete("/{id}", routes.DeleteRemoteNode)
				r.With(wConfig).Post("/{id}/check", routes.CheckRemoteNode)
				r.With(rConfig).Get("/{id}/browse", routes.BrowseRemoteNode)
				r.With(rConfig).Get("/{id}/git-info", routes.RemoteGitInfo)
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

			r.Route("/scans", func(r chi.Router) {
				r.With(rScans).Get("/", routes.ListScans)
				r.With(rScans).Get("/trends", routes.ScansTrends)
				r.With(wScans).Post("/", routes.CreateScan)
				r.With(rScans).Get("/{id}", routes.GetScan)
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
				r.With(wScans).Delete("/{id}", routes.CancelScan)
				r.With(wScans).Delete("/{id}/tools/{toolName}", routes.CancelScanTool)
			})

			r.Route("/findings", func(r chi.Router) {
				r.With(rFind).Get("/", routes.ListFindings)
				r.With(rFind).Get("/export", routes.ExportFindings)
				r.With(rFind).Get("/trends", routes.FindingTrends)
				r.With(rFind).Get("/trends/export", routes.ExportFindingTrends)
				r.With(rFind).Get("/{id}", routes.GetFinding)
				r.With(wFind).Put("/{id}/status", routes.UpdateFindingStatus)
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

			r.Route("/fixes", func(r chi.Router) {
				r.With(rFixes).Get("/", routes.ListFixes)
				r.With(wFixes).Post("/", routes.CreateFix)
				r.With(rFixes).Get("/{id}", routes.GetFix)
				r.With(rFixes).Get("/{id}/stream", routes.StreamFix)
				r.With(wFixes).Delete("/{id}", routes.CancelFix)
			})

			r.Route("/loops", func(r chi.Router) {
				r.With(rLoops).Get("/", routes.ListLoops)
				r.With(wLoops).Post("/", routes.CreateLoop)
				r.With(rLoops).Get("/{id}", routes.GetLoop)
				r.With(rLoops).Get("/{id}/stream", routes.StreamLoop)
				r.With(wLoops).Put("/{id}/pause", routes.PauseLoop)
				r.With(wLoops).Put("/{id}/resume", routes.ResumeLoop)
				r.With(wLoops).Delete("/{id}", routes.StopLoop)
			})

			r.With(rRepos).Get("/browse", routes.BrowseLocal)
			r.With(rRepos).Get("/git-info", routes.GitInfo)

			r.Route("/config", func(r chi.Router) {
				r.With(rConfig).Get("/secrets", routes.ListSecrets)
				r.With(wConfig).Post("/secrets", routes.CreateSecret)
				r.With(wConfig).Delete("/secrets/{id}", routes.DeleteSecret)
				r.With(rConfig).Get("/plugins", routes.ListPlugins)
				r.With(wConfig).Post("/plugins/{name}/install", routes.InstallPlugin)
				r.With(rConfig).Get("/setup", routes.SetupStatus)
			})

			// Scanner backend (PLAN.md §5).
			r.Route("/scanners", func(r chi.Router) {
				r.With(rConfig).Get("/tools", routes.ScannersTools)
				r.With(rConfig).Get("/tools/{name}", routes.ScannersTool)
				r.With(wConfig).Post("/tools/check-updates", routes.ScannersCheckUpdates)
				r.With(wConfig).Post("/tools/{name}/check-update", routes.ScannersCheckUpdate)
				r.With(rConfig).Get("/images", routes.ScannersImages)
				r.With(wConfig).Post("/images/pull", routes.ScannersPullOne)
				r.With(rConfig).Get("/config", routes.ScannersConfig)
				r.With(rConfig).Get("/list", routes.ScannersList)
				r.With(rConfig).Post("/plan", routes.ScannersPlan)
				r.With(wConfig).Post("/doctor", routes.ScannersDoctor)
				r.With(wConfig).Post("/pull", routes.ScannersPull)
			})

			r.With(rConfig).Get("/settings", routes.ListSettings)
			r.With(wConfig).Put("/settings", routes.UpdateSettings)

			r.Route("/ai-prompts", func(r chi.Router) {
				r.With(rConfig).Get("/", routes.ListPromptTemplates)
				r.With(wConfig).Put("/", routes.UpsertPromptTemplate)
				r.With(rConfig).Get("/defaults", routes.GetPromptDefaults)
				r.With(rConfig).Post("/preview", routes.PreviewPrompt)
				r.With(wConfig).Delete("/{id}", routes.DeletePromptTemplate)
			})

			r.With(rConfig).Get("/ai-providers", routes.ListAIProviders)
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
	// Discovery: WOLF_UI_DIR env > /usr/share/wolf/ui/dist > ./ui/dist > ./dist.
	MountStaticUI(r, os.Getenv("WOLF_UI_DIR"))

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
	recoverOrphanScans(s.Store)

	err := s.httpServer.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
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
