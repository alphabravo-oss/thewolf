package api

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	"github.com/alphabravocompany/thewolf/internal/api/middleware"
	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/api/routes"
	"github.com/alphabravocompany/thewolf/internal/api/sse"
	"github.com/alphabravocompany/thewolf/internal/artifacts"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/db"
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
		artifacts.Init(filepath.Join(home, ".wolf", "artifacts"))
	}

	r := chi.NewRouter()

	// Rate limiters
	generalLimiter := middleware.DefaultRateLimiter()
	authLimiter := middleware.StrictRateLimiter()

	// Middleware chain
	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(chimw.Logger)
	r.Use(chimw.Recoverer)
	r.Use(chimw.Timeout(60 * time.Second))
	r.Use(middleware.MaxBodySize(1 << 20)) // 1 MB body limit
	r.Use(generalLimiter.Handler)
	r.Use(cors.Handler(cors.Options{
		AllowOriginFunc: func(r *http.Request, origin string) bool { return true },
		AllowedMethods:  []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:  []string{"Accept", "Authorization", "Content-Type"},
		AllowCredentials: true,
		MaxAge:           300,
	}))
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			next.ServeHTTP(w, r)
		})
	})

	// Mount routes
	r.Route("/api", func(r chi.Router) {
		// Public endpoints
		r.Group(func(r chi.Router) {
			r.Get("/health", routes.Health)
			r.Get("/ready", routes.Ready)
			r.Get("/version", routes.Version)
		})

		// Auth endpoints with stricter rate limiting
		r.Group(func(r chi.Router) {
			r.Use(authLimiter.Handler)
			r.Post("/auth/register", routes.Register)
			r.Post("/auth/login", routes.Login)
		})

		// Protected endpoints
		r.Group(func(r chi.Router) {
			r.Use(auth.Middleware)

			r.Route("/auth", func(r chi.Router) {
				r.Post("/logout", routes.Logout)
				r.Get("/me", routes.Me)
				r.Put("/password", routes.ChangePassword)
			})

			r.Route("/repos", func(r chi.Router) {
				r.Get("/", routes.ListRepos)
				r.Post("/", routes.CreateRepo)
				r.Get("/{id}", routes.GetRepo)
				r.Put("/{id}", routes.UpdateRepo)
				r.Delete("/{id}", routes.DeleteRepo)
				r.Get("/{id}/branches", routes.ListRepoBranches)
			})

			r.Route("/collections", func(r chi.Router) {
				r.Get("/", routes.ListCollections)
				r.Post("/", routes.CreateCollection)
				r.Get("/{id}", routes.GetCollection)
				r.Put("/{id}", routes.UpdateCollection)
				r.Delete("/{id}", routes.DeleteCollection)
				r.Post("/{id}/repos", routes.AddRepoToCollection)
				r.Delete("/{id}/repos/{repoId}", routes.RemoveRepoFromCollection)
			})

			r.Route("/scans", func(r chi.Router) {
				r.Get("/", routes.ListScans)
				r.Post("/", routes.CreateScan)
				r.Get("/{id}", routes.GetScan)
				r.Get("/{id}/findings", routes.GetScanFindings)
				r.Get("/{id}/stream", routes.StreamScan)
				r.Get("/{id}/report", routes.GetScanReport)
				r.Get("/{id}/sarif", routes.GetScanSARIF)
				r.Get("/{id}/coverage", routes.GetScanCoverage)
				r.Get("/{id}/compare/{compareId}", routes.CompareScan)
				r.Get("/{id}/tools", routes.GetScanTools)
				r.Get("/{id}/tools/{toolName}/output", routes.GetToolOutput)
				r.Get("/{id}/artifacts/{artifactId}/download", routes.DownloadArtifact)
				r.Get("/{id}/ai-logs", routes.ListAILogs)
				r.Get("/{id}/tool-summaries", routes.GetToolSummaries)
				r.Get("/{id}/recommendations", routes.GetScanRecommendations)
				r.Delete("/{id}", routes.CancelScan)
			})

			r.Route("/findings", func(r chi.Router) {
				r.Get("/", routes.ListFindings)
				r.Get("/export", routes.ExportFindings)
				r.Get("/trends", routes.FindingTrends)
				r.Get("/trends/export", routes.ExportFindingTrends)
				r.Get("/{id}", routes.GetFinding)
				r.Put("/{id}/status", routes.UpdateFindingStatus)
			})

			r.Route("/fixes", func(r chi.Router) {
				r.Get("/", routes.ListFixes)
				r.Post("/", routes.CreateFix)
				r.Get("/{id}", routes.GetFix)
				r.Get("/{id}/stream", routes.StreamFix)
				r.Delete("/{id}", routes.CancelFix)
			})

			r.Route("/loops", func(r chi.Router) {
				r.Get("/", routes.ListLoops)
				r.Post("/", routes.CreateLoop)
				r.Get("/{id}", routes.GetLoop)
				r.Get("/{id}/stream", routes.StreamLoop)
				r.Put("/{id}/pause", routes.PauseLoop)
				r.Put("/{id}/resume", routes.ResumeLoop)
				r.Delete("/{id}", routes.StopLoop)
			})

			r.Get("/browse", routes.BrowseLocal)

			r.Route("/config", func(r chi.Router) {
				r.Get("/secrets", routes.ListSecrets)
				r.Post("/secrets", routes.CreateSecret)
				r.Delete("/secrets/{id}", routes.DeleteSecret)
				r.Get("/plugins", routes.ListPlugins)
				r.Post("/plugins/{name}/install", routes.InstallPlugin)
				r.Get("/setup", routes.SetupStatus)
			})

			r.Get("/settings", routes.ListSettings)
			r.Put("/settings", routes.UpdateSettings)

			r.Route("/ai-prompts", func(r chi.Router) {
				r.Get("/", routes.ListPromptTemplates)
				r.Put("/", routes.UpsertPromptTemplate)
				r.Get("/defaults", routes.GetPromptDefaults)
				r.Post("/preview", routes.PreviewPrompt)
				r.Delete("/{id}", routes.DeletePromptTemplate)
			})

			r.Get("/ai-providers", routes.ListAIProviders)

			r.Get("/collections/{id}/tools", routes.CollectionTools)
			r.Get("/collections/{id}/metrics", routes.CollectionMetrics)
		})
	})

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
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	return srv
}

// Start starts the HTTP server (blocking).
func (s *Server) Start() error {
	wolflog.Info().Str("addr", s.Addr).Msg("API server starting")
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
